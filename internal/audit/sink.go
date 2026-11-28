package audit

// sink.go: the mutex-guarded, synchronous audit sink (plan.md Complexity
// Tracking; research.md D6). Two backends share one writer implementation:
// NewStdoutSink writes raw JSON straight to an injected io.Writer (never
// through a zap core — Spec 107 stdio-transport rule, FR-014), NewFileSink
// is built on lumberjack for size/age/backup rotation, which never splits
// a line because it rotates before a write that would exceed MaxSize, not
// mid-write.

import (
	"errors"
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

	// torn is true while the underlying writer may end mid-record (a short
	// write, or a file reopened with no trailing newline). The next Write
	// first restores the record boundary with a lone newline, and appends
	// nothing while that repair fails (Spec 133 FR-017, plan D-C). Guarded
	// by mu.
	torn bool
	// openRepair, while set, restores the boundary by appending to the file
	// at its path: a file found torn at open whose first repair failed. The
	// rotating writer has not opened the file yet (it opens lazily and torn
	// blocks every write), so the path still names the torn file, and a
	// rotation cannot run before the repair. Cleared once it succeeds.
	openRepair func() error
	// closeRepair is the held torn-file handle, closed with the sink if the
	// repair never succeeded.
	closeRepair io.Closer

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

	// Restore the record boundary first, as its own write, so a torn tail
	// is repaired in the file that holds it — before a rotation triggered by
	// this record could move on to a new file.
	if s.torn {
		// Through the writer's own handle: the file that received the
		// partial write. (A file reopened torn was already repaired by path
		// before the writer opened it, in NewFileSink.)
		var err error
		if s.openRepair != nil {
			if err = s.openRepair(); err == nil {
				s.torn, s.openRepair, s.closeRepair = false, nil, nil
			}
		} else {
			var n int
			n, err = s.w.Write([]byte{'\n'})
			if err == nil && n != 1 {
				err = io.ErrShortWrite
			}
			if n == 1 {
				s.torn = false
			}
		}
		if s.torn {
			if err == nil {
				err = io.ErrShortWrite
			}
			atomic.AddUint64(&s.failures, 1)
			s.maybeLogFailure(err)
			return fmt.Errorf("audit: record boundary not restored, record not appended: %w", err)
		}
	}

	n, err := s.w.Write(buf)
	if err == nil && n != len(buf) {
		// io.Writer permits a short write with a nil error; treated as a
		// failure here so a partial JSON record never counts as a
		// successfully written line (round-1 cross-review finding, PR-D).
		err = io.ErrShortWrite
	}
	// A partial record (some bytes, not all) leaves the stream mid-record.
	// Nothing written, or everything written (even with an error), leaves it
	// at a boundary.
	if n > 0 && n < len(buf) {
		s.torn = true
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
	if s.closeRepair != nil {
		_ = s.closeRepair.Close()
		s.closeRepair = nil
	}
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
	// One handle checks the tail and repairs it, so an external rotator
	// renaming the file in between cannot redirect the repair. The tail of a
	// file a previous process left mid-record is repaired through that
	// handle before the writer opens the path (the open may rotate a file at
	// its size limit). The writer then opens its own handle (lumberjack opens
	// on its first Write, so an empty one opens it now), and the check is
	// kept only if the path still names the checked file (round 25): a file
	// replaced in between, such as a restored torn copy, is checked again, so
	// the first record never lands on an unchecked fragment.
	var (
		held     *os.File
		torn     bool
		repairOK bool
	)
	for attempt := 0; ; attempt++ {
		var err error
		held, torn, err = openTail(path)
		if err != nil {
			return nil, err
		}
		repairOK = torn && repairAtOpen(held) == nil
		if afterTailCheck != nil {
			afterTailCheck()
		}
		// The open must not rotate (round 26): an over-limit file it would
		// rotate may be a replacement this check has not seen. The size limit
		// is lifted for the open only; the first record rotates a checked file.
		lj.MaxSize = openNoRotateMB
		_, err = lj.Write(nil)
		lj.MaxSize = maxSizeMB
		if err != nil {
			_ = held.Close()
			return nil, fmt.Errorf("audit: cannot open %s: %w", path, err)
		}
		// The identity check holds whether or not the repair worked (round
		// 27): a failed repair keeps its handle for the first Write to retry
		// only while the path still names that file.
		if sameFileAsPath(held, path) {
			break
		}
		if torn && !repairOK {
			// The replaced file is no longer the sink's, but its torn tail
			// still gets one more best-effort repair through the handle.
			_ = writeNewline(held)
		}
		_ = held.Close()
		_ = lj.Close()
		if attempt >= maxOpenAttempts-1 {
			return nil, fmt.Errorf("audit: %s kept changing while it was opened", path)
		}
	}
	s := newWriterSink(lj, opts...)
	if torn && !repairOK {
		// The first Write retries the repair and appends nothing until the
		// boundary is restored.
		s.torn = true
		s.openRepair = heldRepair(held)
		s.closeRepair = held
	} else {
		_ = held.Close()
	}
	return s, nil
}

// openNoRotateMB is the size limit NewFileSink sets while the writer opens
// the file, so the open never rotates (1 TiB).
const openNoRotateMB = 1 << 20

// maxOpenAttempts bounds how often NewFileSink re-checks a file that was
// replaced while it was opened.
const maxOpenAttempts = 3

// repairAtOpen repairs a torn tail found at open; a test replaces it to make
// the repair fail.
var repairAtOpen = writeNewline

// afterTailCheck, when set by a test, runs between the tail check and the
// writer's open.
var afterTailCheck func()

// sameFileAsPath reports whether f is still the file path names.
func sameFileAsPath(f *os.File, path string) bool {
	a, err := f.Stat()
	if err != nil {
		return false
	}
	b, err := os.Stat(path)
	if err != nil {
		return false
	}
	return os.SameFile(a, b)
}

// heldRepair is the retry NewFileSink installs when the repair at open fails:
// it appends the newline through the handle that read the torn tail, so the
// file that was torn is the one repaired even after a rename.
func heldRepair(f *os.File) func() error {
	return func() error {
		if err := writeNewline(f); err != nil {
			return err
		}
		_ = f.Close()
		return nil
	}
}

// appendNewline appends one newline to the file at path.
func appendNewline(path string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	n, werr := f.Write([]byte{'\n'})
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	if n != 1 {
		return io.ErrShortWrite
	}
	return cerr
}

// openTail opens path once for read+append and reports whether it is
// non-empty and does not end in a newline (a previous process was cut off
// mid-record, Spec 133 FR-017); the returned handle is the one to repair
// through. A file the daemon may append to but not read is opened write-only
// and reported torn: its boundary is established unconditionally (one
// newline; an empty line on a clean file, which shippers skip).
func openTail(path string) (*os.File, bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		if !errors.Is(err, os.ErrPermission) {
			return nil, false, fmt.Errorf("audit: cannot open %s: %w", path, err)
		}
		w, werr := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
		if werr != nil {
			return nil, false, fmt.Errorf("audit: cannot open %s: %w", path, werr)
		}
		return w, true, nil
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, false, fmt.Errorf("audit: cannot stat %s: %w", path, err)
	}
	if st.Size() == 0 {
		return f, false, nil
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, st.Size()-1); err != nil {
		_ = f.Close()
		return nil, false, fmt.Errorf("audit: cannot read %s: %w", path, err)
	}
	return f, last[0] != '\n', nil
}

func probeWritable(path string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("audit: cannot open %s for append: %w", path, err)
	}
	return f.Close()
}

// writeNewline writes one newline to f.
func writeNewline(f *os.File) error {
	n, err := f.Write([]byte{'\n'})
	if err != nil {
		return err
	}
	if n != 1 {
		return io.ErrShortWrite
	}
	return nil
}
