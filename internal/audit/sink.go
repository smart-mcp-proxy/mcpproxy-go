package audit

// sink.go: the mutex-guarded, synchronous audit sink (plan.md Complexity
// Tracking; research.md D6). Two backends share one writer implementation:
// NewStdoutSink writes raw JSON straight to an injected io.Writer (never
// through a zap core — Spec 107 stdio-transport rule, FR-014), NewFileSink
// is built on lumberjack for size/age/backup rotation, which never splits
// a line because it rotates before a write that would exceed MaxSize, not
// mid-write.

import (
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

// failureLogRateLimit bounds how often a write-failure logger callback is
// invoked, independent of how often writes actually fail.
const failureLogRateLimit = time.Minute

// Sink is the audit line writer: mutex-guarded and synchronous (the caller
// blocks until the line is on disk/stdout), with an always-on write-failure
// counter (mirrored to metrics, surfaced by doctor — wiring is T109, not
// this package) and an always-on defence-in-depth sanitizer-hit counter
// (contracts/audit-line-events.md "Redaction (FR-015)": Write runs every
// line through SanitizeLine before it reaches the underlying writer, and a
// hit — which can only happen on a builder bug, since every well-formed
// constructor output is untouched by the pass — is counted here).
type Sink interface {
	Write(line []byte) error
	WriteFailures() uint64
	SanitizerHits() uint64
	Close() error
}

type sinkOptions struct {
	clock         func() time.Time
	failureLogger func(err error)
}

// Option configures a Sink at construction.
type Option func(*sinkOptions)

// WithClock overrides the clock used for the write-failure log rate limit
// (tests only; production sinks use time.Now).
func WithClock(now func() time.Time) Option {
	return func(o *sinkOptions) { o.clock = now }
}

// WithFailureLogger installs a callback invoked at most once per minute
// when a write fails, receiving the most recent error.
func WithFailureLogger(fn func(err error)) Option {
	return func(o *sinkOptions) { o.failureLogger = fn }
}

// writerSink is the shared Sink implementation behind both backends.
type writerSink struct {
	mu            sync.Mutex
	w             io.Writer
	closer        io.Closer
	failures      uint64 // atomic
	sanitizerHits uint64 // atomic

	clock         func() time.Time
	failureLogger func(error)
	logMu         sync.Mutex
	lastLoggedAt  time.Time
}

func newWriterSink(w io.Writer, opts ...Option) *writerSink {
	o := &sinkOptions{clock: time.Now}
	for _, opt := range opts {
		opt(o)
	}
	closer, _ := w.(io.Closer)
	return &writerSink{
		w:             w,
		closer:        closer,
		clock:         o.clock,
		failureLogger: o.failureLogger,
	}
}

// Write appends line (adding exactly one trailing newline if the caller did
// not already include one) under the sink's mutex, so concurrent callers
// never interleave partial lines. Before anything else, line passes through
// the defence-in-depth whole-line sanitizer (SanitizeLine, FR-015): the
// identity on every well-formed constructor output, but a safety net
// against a future builder bug that lets a credential-shaped string past
// the per-field masking. A hit increments the always-on SanitizerHits
// counter and the (possibly masked) line is still written — the sink never
// drops a line. A write failure increments the always-on write-failure
// counter and, rate-limited, invokes the failure logger; it never panics
// and control always returns to the caller.
func (s *writerSink) Write(line []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	buf, hit := SanitizeLine(line)
	if hit {
		atomic.AddUint64(&s.sanitizerHits, 1)
	}
	if len(buf) == 0 || buf[len(buf)-1] != '\n' {
		buf = append(buf, '\n')
	}

	n, err := s.w.Write(buf)
	if err == nil && n != len(buf) {
		// io.Writer permits a short write with a nil error; treated as a
		// failure here so a partial JSON record never counts as a
		// successfully written line (round-1 cross-review finding, PR-D).
		err = io.ErrShortWrite
	}
	if err != nil {
		atomic.AddUint64(&s.failures, 1)
		s.maybeLogFailure(err)
	}
	return err
}

func (s *writerSink) maybeLogFailure(err error) {
	if s.failureLogger == nil {
		return
	}
	s.logMu.Lock()
	now := s.clock()
	if !s.lastLoggedAt.IsZero() && now.Sub(s.lastLoggedAt) < failureLogRateLimit {
		s.logMu.Unlock()
		return
	}
	s.lastLoggedAt = now
	s.logMu.Unlock()
	s.failureLogger(err)
}

// WriteFailures returns the always-on write-failure count. It reads zero
// from construction, independent of whether metrics mirroring is enabled
// anywhere.
func (s *writerSink) WriteFailures() uint64 {
	return atomic.LoadUint64(&s.failures)
}

// SanitizerHits returns the always-on count of lines whose defence-in-depth
// whole-line sanitizer pass fired. It reads zero from construction and
// stays zero for the lifetime of the process unless a builder bug lets a
// credential-shaped string past the per-field masking.
func (s *writerSink) SanitizerHits() uint64 {
	return atomic.LoadUint64(&s.sanitizerHits)
}

// Close releases the underlying writer's handle, if it has one (the stdout
// sink's injected io.Writer typically does not).
func (s *writerSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closer != nil {
		return s.closer.Close()
	}
	return nil
}

// NewStdoutSink writes raw JSON lines straight to w (one call per line, no
// zap core, no ANSI, no encoder prefix) — the stdio-transport rule of
// FR-014: stdout must stay pure JSON-RPC on that transport, so this sink is
// never installed there.
func NewStdoutSink(w io.Writer, opts ...Option) Sink {
	return newWriterSink(w, opts...)
}

// NewFileSink opens (creating if absent) an append-only rotating file at
// path. It probes writability at construction time — lumberjack itself
// opens lazily, so a naive wrapper would only fail on the first Write —
// and returns a non-nil error (never a partially-usable Sink) when the
// path cannot be opened for append.
func NewFileSink(path string, maxSizeMB, maxBackups, maxAgeDays int, compress bool, opts ...Option) (Sink, error) {
	if err := probeWritable(path); err != nil {
		return nil, err
	}
	lj := &lumberjack.Logger{
		Filename:   path,
		MaxSize:    maxSizeMB,
		MaxBackups: maxBackups,
		MaxAge:     maxAgeDays,
		Compress:   compress,
	}
	return newWriterSink(lj, opts...), nil
}

func probeWritable(path string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("audit: cannot open %s for append: %w", path, err)
	}
	return f.Close()
}
