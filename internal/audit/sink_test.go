package audit

// Spec 107 PR-D, T098: failing tests for the audit sink (internal/audit/sink.go,
// implemented by T099). This file is intentionally compile-red until T099 lands
// internal/audit/{attempt.go,canonical.go,line.go,sink.go}: it exercises the
// production API those files must expose.
//
// Assumed surface (data-model.md §5 covers only audit.Attempt; the Sink shape
// itself is a T099 implementation detail this test file pins down):
//
//	type Sink interface {
//	    Write(line []byte) error // mutex-guarded, synchronous, write-through
//	    WriteFailures() uint64   // always-on atomic counter (metrics flag irrelevant)
//	    Close() error
//	}
//
//	func NewFileSink(path string, maxSizeMB, maxBackups, maxAgeDays int, compress bool, opts ...Option) (Sink, error)
//	func NewStdoutSink(w io.Writer, opts ...Option) Sink
//
//	type Option func(*sinkOptions)
//	func WithClock(now func() time.Time) Option
//	func WithFailureLogger(fn func(err error)) Option // rate-limited to once/min by the sink itself
//
// research.md D6: the file sink is built on `logs.NewRotatingWriter` (lumberjack);
// the stdout sink writes raw JSON straight to an injected io.Writer, never
// through a zap core. contracts/config-keys.md: an unwritable path is a
// construction-time error (lumberjack opens lazily, so NewFileSink must probe
// the path itself with OpenFile(O_APPEND|O_CREATE|O_WRONLY) before installing
// the rotating writer) — mapping that error to process exit code 4 is the
// caller's job (config/main.go, T109), not this package's.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// test doubles
// ---------------------------------------------------------------------------

// fakeClock is a manually-advanced clock for the once-per-minute
// write-failure log rate limit.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// failingWriter fails every Write from call number failFrom onward
// (1-indexed); it is safe for concurrent use.
type failingWriter struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	calls    int
	failFrom int // 0 = never fail
}

func (w *failingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	if w.failFrom > 0 && w.calls >= w.failFrom {
		return 0, errors.New("simulated write failure")
	}
	return w.buf.Write(p)
}

func (w *failingWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// ---------------------------------------------------------------------------
// always-on write-failure counter
// ---------------------------------------------------------------------------

func TestSink_WriteFailuresCounterAlwaysOn(t *testing.T) {
	// No metrics flag, no opt-in: the counter exists and reads zero from the
	// moment the sink is constructed, independent of whether Prometheus
	// mirroring (internal/observability, T109) is enabled anywhere.
	var buf bytes.Buffer
	s := NewStdoutSink(&buf)
	t.Cleanup(func() { _ = s.Close() })

	if got := s.WriteFailures(); got != 0 {
		t.Fatalf("WriteFailures() before any write = %d, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// defence-in-depth whole-line sanitizer pass, wired into the production
// write path (contracts/audit-line-events.md "Redaction (FR-015)")
// ---------------------------------------------------------------------------

// TestSink_WriteRunsLinesThroughSanitizerAndCountsHits proves the sink's
// Write itself — not just the standalone SanitizeLine helper — applies the
// defence-in-depth whole-line pass before a line reaches the underlying
// writer, and that a hit is counted on Sink.SanitizerHits(). This is the
// production wiring the contract requires as a safety net against a future
// builder bug that lets a credential-shaped string past per-field masking;
// without it, such a string would reach disk/stdout in clear.
func TestSink_WriteRunsLinesThroughSanitizerAndCountsHits(t *testing.T) {
	var buf bytes.Buffer
	s := NewStdoutSink(&buf)
	t.Cleanup(func() { _ = s.Close() })

	if got := s.SanitizerHits(); got != 0 {
		t.Fatalf("SanitizerHits() before any write = %d, want 0", got)
	}

	clean := []byte(`{"server":"jira"}`)
	if err := s.Write(clean); err != nil {
		t.Fatalf("Write(clean): %v", err)
	}
	if got := s.SanitizerHits(); got != 0 {
		t.Fatalf("SanitizerHits() after a clean write = %d, want 0", got)
	}
	if !strings.Contains(buf.String(), `"server":"jira"`) {
		t.Fatalf("clean line must be written verbatim, got: %s", buf.String())
	}

	// Simulated builder bug: a fixed-prefix credential reaches Write directly
	// (standing in for a future field that skips per-field masking).
	const sinkTestAkiaSentinel = "AKIASINKTEST7SENTINEL0"
	buf.Reset()
	leaked := []byte(`{"client":{"name":"` + sinkTestAkiaSentinel + `"}}`)
	if err := s.Write(leaked); err != nil {
		t.Fatalf("Write(leaked): %v", err)
	}
	if got := s.SanitizerHits(); got != 1 {
		t.Fatalf("SanitizerHits() after a credential-shaped write = %d, want 1", got)
	}
	if strings.Contains(buf.String(), sinkTestAkiaSentinel) {
		t.Fatalf("credential must be masked before it reaches the writer, got: %s", buf.String())
	}
}

func TestSink_RuntimeWriteFailureIncrementsCounterAndCallProceeds(t *testing.T) {
	fw := &failingWriter{failFrom: 2} // first write ok, every write after fails
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	var loggedMu sync.Mutex
	var logged []time.Time
	s := NewStdoutSink(fw,
		WithClock(clock.Now),
		WithFailureLogger(func(err error) {
			if err == nil {
				t.Error("WithFailureLogger called with nil error")
			}
			loggedMu.Lock()
			logged = append(logged, clock.Now())
			loggedMu.Unlock()
		}),
	)
	t.Cleanup(func() { _ = s.Close() })

	// First write succeeds.
	if err := s.Write([]byte(`{"seq":0}` + "\n")); err != nil {
		t.Fatalf("first Write() unexpected error: %v", err)
	}
	if got := s.WriteFailures(); got != 0 {
		t.Fatalf("WriteFailures() after ok write = %d, want 0", got)
	}

	// Subsequent writes fail at the underlying writer; the call must not
	// panic and control must return to the caller (the funnel proceeds).
	for i := 1; i <= 5; i++ {
		err := s.Write([]byte(fmt.Sprintf(`{"seq":%d}`+"\n", i)))
		if err == nil {
			t.Fatalf("Write() #%d: want error from failing writer, got nil", i)
		}
	}

	if got := s.WriteFailures(); got != 5 {
		t.Fatalf("WriteFailures() after 5 failing writes = %d, want 5", got)
	}

	// All five failures happened inside the same minute: the rate limit
	// must have logged at most once.
	loggedMu.Lock()
	firstWindowLogs := len(logged)
	loggedMu.Unlock()
	if firstWindowLogs != 1 {
		t.Fatalf("failure-logger calls within one minute = %d, want 1", firstWindowLogs)
	}

	// Advance the fake clock past the one-minute window and fail again: a
	// second log call is now permitted.
	clock.Advance(61 * time.Second)
	if err := s.Write([]byte(`{"seq":6}` + "\n")); err == nil {
		t.Fatal("Write() after clock advance: want error from failing writer, got nil")
	}
	if got := s.WriteFailures(); got != 6 {
		t.Fatalf("WriteFailures() after 6th failure = %d, want 6", got)
	}

	loggedMu.Lock()
	secondWindowLogs := len(logged)
	loggedMu.Unlock()
	if secondWindowLogs != 2 {
		t.Fatalf("failure-logger calls after clock advance = %d, want 2 total", secondWindowLogs)
	}
}

// ---------------------------------------------------------------------------
// stdout sink
// ---------------------------------------------------------------------------

func TestNewStdoutSink_WritesRawJSONToInjectedWriter(t *testing.T) {
	var buf bytes.Buffer
	s := NewStdoutSink(&buf)
	t.Cleanup(func() { _ = s.Close() })

	line := []byte(`{"schema_version":1,"event":"authz"}`)
	if err := s.Write(line); err != nil {
		t.Fatalf("Write() unexpected error: %v", err)
	}

	got := buf.String()
	// Raw JSON line plus exactly one trailing newline: no zap console
	// encoder timestamp/level prefix, no ANSI color codes, nothing else on
	// the line.
	want := string(line) + "\n"
	if got != want {
		t.Fatalf("stdout sink wrote %q, want %q (must never go through a zap core)", got, want)
	}
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("stdout sink output contains ANSI escape codes: %q", got)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSuffix(got, "\n")), &decoded); err != nil {
		t.Fatalf("stdout sink output is not valid single-line JSON: %v", err)
	}
}

func TestNewStdoutSink_MultipleWritesEachOwnLine(t *testing.T) {
	var buf bytes.Buffer
	s := NewStdoutSink(&buf)
	t.Cleanup(func() { _ = s.Close() })

	for i := 0; i < 3; i++ {
		if err := s.Write([]byte(fmt.Sprintf(`{"seq":%d}`, i))); err != nil {
			t.Fatalf("Write() #%d unexpected error: %v", i, err)
		}
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3: %q", len(lines), buf.String())
	}
	for i, l := range lines {
		var decoded struct {
			Seq int `json:"seq"`
		}
		if err := json.Unmarshal([]byte(l), &decoded); err != nil {
			t.Fatalf("line %d not valid JSON: %v (%q)", i, err, l)
		}
		if decoded.Seq != i {
			t.Fatalf("line %d seq = %d, want %d", i, decoded.Seq, i)
		}
	}
}

// ---------------------------------------------------------------------------
// file sink: append-only, write-through, rotation params, unwritable path
// ---------------------------------------------------------------------------

func TestNewFileSink_AppendOnlyWriteThrough(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	if err := os.WriteFile(path, []byte(`{"seq":-1}`+"\n"), 0o600); err != nil {
		t.Fatalf("seeding pre-existing file: %v", err)
	}

	s, err := NewFileSink(path, 50, 10, 90, true)
	if err != nil {
		t.Fatalf("NewFileSink() unexpected error: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.Write([]byte(`{"seq":0}` + "\n")); err != nil {
		t.Fatalf("Write() unexpected error: %v", err)
	}

	// Write-through: the line must be on disk immediately after Write
	// returns, with no separate Flush/Sync/Close required.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading sink file: %v", err)
	}
	want := "{\"seq\":-1}\n{\"seq\":0}\n"
	if string(got) != want {
		t.Fatalf("file sink content = %q, want %q (append-only, write-through)", string(got), want)
	}
}

func TestNewFileSink_UnwritablePath_ParentDirMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist", "audit.jsonl")

	s, err := NewFileSink(path, 50, 10, 90, true)
	if err == nil {
		_ = s.Close()
		t.Fatal("NewFileSink() with a missing parent directory: want error, got nil")
	}
	if s != nil {
		t.Fatalf("NewFileSink() returned a non-nil sink alongside an error: %v", s)
	}
}

func TestNewFileSink_UnwritablePath_PermissionDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits don't apply on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores permission bits")
	}

	dir := t.TempDir()
	roDir := filepath.Join(dir, "readonly")
	if err := os.Mkdir(roDir, 0o555); err != nil {
		t.Fatalf("creating read-only dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(roDir, 0o755) }) // let TempDir clean up

	path := filepath.Join(roDir, "audit.jsonl")
	s, err := NewFileSink(path, 50, 10, 90, true)
	if err == nil {
		_ = s.Close()
		t.Fatal("NewFileSink() on an unwritable directory: want error, got nil")
	}

	// The probe (OpenFile(O_APPEND|O_CREATE|O_WRONLY) then close) must run
	// at construction time, not lazily on the first Write — lumberjack
	// itself only opens the file lazily, so a naive implementation that
	// merely hands the path to logs.NewRotatingWriter would return nil
	// here and fail only later.
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatal("NewFileSink() left a file behind on the failed construction path")
	}
}

func TestNewFileSink_ConstructionProbesBeforeFirstWrite(t *testing.T) {
	// A successful construction must not silently defer the writability
	// check to the first Write call: probing at construction time means a
	// caller that only constructs (e.g. a --check-config dry run) already
	// knows the path is usable.
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	s, err := NewFileSink(path, 50, 10, 90, true)
	if err != nil {
		t.Fatalf("NewFileSink() unexpected error: %v", err)
	}
	defer func() { _ = s.Close() }()

	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("expected the probe to create %s at construction time: %v", path, statErr)
	}
}

func TestNewFileSink_RotationNeverSplitsALine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	// Smallest lumberjack rotation unit is 1 MB; write enough ~250-byte
	// lines to force several rotations inside one test.
	s, err := NewFileSink(path, 1 /* MB */, 10, 90, false)
	if err != nil {
		t.Fatalf("NewFileSink() unexpected error: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	const total = 6000
	padding := strings.Repeat("x", 180)
	for i := 0; i < total; i++ {
		line := fmt.Sprintf(`{"seq":%d,"pad":%q}`+"\n", i, padding)
		if err := s.Write([]byte(line)); err != nil {
			t.Fatalf("Write() #%d unexpected error: %v", i, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() unexpected error: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading sink dir: %v", err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.Contains(e.Name(), "audit") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	if len(files) < 2 {
		t.Fatalf("expected rotation to produce at least 2 files with MaxSizeMB=1, got %d: %v", len(files), files)
	}
	sort.Strings(files)

	seen := make(map[int]bool, total)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		for _, l := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
			if l == "" {
				continue
			}
			var decoded struct {
				Seq int `json:"seq"`
			}
			if err := json.Unmarshal([]byte(l), &decoded); err != nil {
				t.Fatalf("line in %s is not valid whole JSON (a rotation split it): %v\nline: %q", f, err, l)
			}
			if seen[decoded.Seq] {
				t.Fatalf("seq %d appears more than once across rotated files", decoded.Seq)
			}
			seen[decoded.Seq] = true
		}
	}
	if len(seen) != total {
		t.Fatalf("recovered %d distinct lines across %d files, want %d (a split or dropped line would show here)", len(seen), len(files), total)
	}
}

// ---------------------------------------------------------------------------
// concurrency: whole lines, no interleaving
// ---------------------------------------------------------------------------

func TestSink_ConcurrentWritesProduceWholeLinesNoInterleaving(t *testing.T) {
	const n = 2000
	var buf bytes.Buffer // deliberately not synchronized: the Sink's own
	// mutex must be what makes this safe.
	s := NewStdoutSink(&buf)
	t.Cleanup(func() { _ = s.Close() })

	var wg sync.WaitGroup
	var writeErrs int64
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(seq int) {
			defer wg.Done()
			line := fmt.Sprintf(`{"seq":%d,"pad":"%s"}`, seq, strings.Repeat("y", seq%37))
			if err := s.Write([]byte(line)); err != nil {
				atomic.AddInt64(&writeErrs, 1)
			}
		}(i)
	}
	wg.Wait()

	if writeErrs != 0 {
		t.Fatalf("%d concurrent writes returned an error against a healthy writer", writeErrs)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != n {
		t.Fatalf("got %d lines from %d concurrent writes, want %d (interleaving would corrupt the count)", len(lines), n, n)
	}

	seen := make(map[int]bool, n)
	for _, l := range lines {
		var decoded struct {
			Seq int `json:"seq"`
		}
		if err := json.Unmarshal([]byte(l), &decoded); err != nil {
			t.Fatalf("line is not valid whole JSON (interleaved writes): %v\nline: %q", err, l)
		}
		if seen[decoded.Seq] {
			t.Fatalf("seq %d observed twice: a write's bytes were duplicated/torn", decoded.Seq)
		}
		seen[decoded.Seq] = true
	}
	if len(seen) != n {
		t.Fatalf("recovered %d distinct seqs, want %d", len(seen), n)
	}
}

func TestSink_ConcurrentWritesUnderRuntimeFailuresStillCountEveryCall(t *testing.T) {
	// A saturated/failing sink must never panic or deadlock under
	// concurrent load, and every call must be reflected exactly once
	// either as a successful line or as a counted failure.
	const n = 2000
	fw := &failingWriter{failFrom: 1001} // roughly half fail
	s := NewStdoutSink(fw)
	t.Cleanup(func() { _ = s.Close() })

	var wg sync.WaitGroup
	var errs int64
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(seq int) {
			defer wg.Done()
			if err := s.Write([]byte(fmt.Sprintf(`{"seq":%d}`, seq))); err != nil {
				atomic.AddInt64(&errs, 1)
			}
		}(i)
	}
	wg.Wait()

	if got := s.WriteFailures(); got != uint64(errs) {
		t.Fatalf("WriteFailures() = %d, want %d (must match the errors actually returned)", got, errs)
	}
	if errs == 0 {
		t.Fatal("expected at least one write to hit the failing writer given failFrom=1001 over 2000 writers")
	}

	successLines := strings.Split(strings.TrimRight(fw.String(), "\n"), "\n")
	// Every successfully-written line must still be whole JSON — a runtime
	// failure on one goroutine must not corrupt bytes already committed by
	// another.
	for _, l := range successLines {
		if l == "" {
			continue
		}
		var decoded struct {
			Seq int `json:"seq"`
		}
		if err := json.Unmarshal([]byte(l), &decoded); err != nil {
			t.Fatalf("successful line is not valid whole JSON: %v\nline: %q", err, l)
		}
	}
}

// ---------------------------------------------------------------------------
// interface satisfaction (compile-time documentation of the expected shape)
// ---------------------------------------------------------------------------

var (
	_ Sink = (*sinkStub)(nil) // ensures Sink stays a small, mockable interface
)

// sinkStub is not used by any test above; it exists only so a change that
// widens the Sink interface fails this file to compile, which is the whole
// point of a compile-red test file.
type sinkStub struct{}

func (sinkStub) Write(_ []byte) error  { return nil }
func (sinkStub) WriteFailures() uint64 { return 0 }
func (sinkStub) SanitizerHits() uint64 { return 0 }
func (sinkStub) Close() error          { return nil }

var _ io.Closer = sinkStub{}
