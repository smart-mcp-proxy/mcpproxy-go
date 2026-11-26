package audit

// Spec 133 FR-017 / plan D-C: after a short or errored write the sink resumes
// at a record boundary, so a partial record never glues onto the next one,
// and it appends nothing while a boundary cannot be established.

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scriptedWriter writes into buf; each call consumes the next step: n<0 =
// write everything, otherwise write only the first n bytes; err is returned
// alongside.
type scriptedWriter struct {
	buf   bytes.Buffer
	steps []scriptStep
}

type scriptStep struct {
	n   int
	err error
}

func (w *scriptedWriter) Write(p []byte) (int, error) {
	if len(w.steps) == 0 {
		return w.buf.Write(p)
	}
	st := w.steps[0]
	w.steps = w.steps[1:]
	n := len(p)
	if st.n >= 0 && st.n < n {
		n = st.n
	}
	w.buf.Write(p[:n])
	return n, st.err
}

// assertWellDelimited fails unless every newline-separated record parses,
// except that a final unterminated tail is reported as torn.
func assertWellDelimited(t *testing.T, raw []byte) []string {
	t.Helper()
	var recs []string
	for _, l := range strings.Split(string(raw), "\n") {
		if l == "" {
			continue
		}
		var v map[string]interface{}
		if err := json.Unmarshal([]byte(l), &v); err != nil {
			// A torn record is allowed only if it stands alone on its line.
			if strings.Contains(l, "}{") {
				t.Fatalf("a partial record glued onto the next one: %q", l)
			}
			continue
		}
		recs = append(recs, l)
	}
	return recs
}

func TestSink_TornJSONIsFollowedByARecordBoundary(t *testing.T) {
	w := &scriptedWriter{steps: []scriptStep{{n: 5, err: errors.New("disk full")}}}
	s := newWriterSink(w)
	if err := s.Write([]byte(`{"seq":1}`)); err == nil {
		t.Fatal("short write must report an error")
	}
	if err := s.Write([]byte(`{"seq":2}`)); err != nil {
		t.Fatalf("next write: %v", err)
	}
	recs := assertWellDelimited(t, w.buf.Bytes())
	if len(recs) != 1 || recs[0] != `{"seq":2}` {
		t.Fatalf("want the second record intact on its own line, got %q (raw %q)", recs, w.buf.String())
	}
	if s.WriteFailures() != 1 {
		t.Fatalf("one failure, got %d", s.WriteFailures())
	}
}

func TestSink_LostNewlineIsRepaired(t *testing.T) {
	rec := `{"seq":1}`
	w := &scriptedWriter{steps: []scriptStep{{n: len(rec), err: nil}}}
	s := newWriterSink(w)
	_ = s.Write([]byte(rec))
	if err := s.Write([]byte(`{"seq":2}`)); err != nil {
		t.Fatalf("next write: %v", err)
	}
	recs := assertWellDelimited(t, w.buf.Bytes())
	if len(recs) != 2 {
		t.Fatalf("both records must parse on their own lines, got %q", w.buf.String())
	}
}

func TestSink_FullLengthWriteWithErrorNeedsNoRepair(t *testing.T) {
	w := &scriptedWriter{steps: []scriptStep{{n: -1, err: errors.New("fsync failed")}}}
	s := newWriterSink(w)
	_ = s.Write([]byte(`{"seq":1}`))
	_ = s.Write([]byte(`{"seq":2}`))
	if got := w.buf.String(); got != "{\"seq\":1}\n{\"seq\":2}\n" {
		t.Fatalf("a full-length errored write left a complete record; no extra newline expected, got %q", got)
	}
}

func TestSink_RefusesToAppendWhileBoundaryCannotBeRestored(t *testing.T) {
	w := &scriptedWriter{steps: []scriptStep{
		{n: 3, err: errors.New("disk full")}, // torn record
		{n: 0, err: errors.New("disk full")}, // repair newline fails
	}}
	s := newWriterSink(w)
	_ = s.Write([]byte(`{"seq":1}`))
	if err := s.Write([]byte(`{"seq":2}`)); err == nil {
		t.Fatal("a write must fail while the boundary cannot be restored")
	}
	if strings.Contains(w.buf.String(), `"seq":2`) {
		t.Fatalf("nothing may be appended after an unrepaired torn tail: %q", w.buf.String())
	}
	if err := s.Write([]byte(`{"seq":3}`)); err != nil {
		t.Fatalf("once the repair succeeds the record is written: %v", err)
	}
	recs := assertWellDelimited(t, w.buf.Bytes())
	if len(recs) != 1 || recs[0] != `{"seq":3}` {
		t.Fatalf("got %q", w.buf.String())
	}
}

func TestNewFileSink_ReopenOfTornFileRepairsTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	if err := os.WriteFile(path, []byte("{\"seq\":1}\n{\"se"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewFileSink(path, 10, 1, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write([]byte(`{"seq":2}`)); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	raw, _ := os.ReadFile(path)
	recs := assertWellDelimited(t, raw)
	if len(recs) != 2 || recs[1] != `{"seq":2}` {
		t.Fatalf("reopen must start at a record boundary, got %q", raw)
	}
}

func TestNewFileSink_ReopenOfCleanFileAddsNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	if err := os.WriteFile(path, []byte("{\"seq\":1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewFileSink(path, 10, 1, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Write([]byte(`{"seq":2}`))
	_ = s.Close()
	raw, _ := os.ReadFile(path)
	if string(raw) != "{\"seq\":1}\n{\"seq\":2}\n" {
		t.Fatalf("got %q", raw)
	}
}

func TestNewFileSink_TornFileAtSizeLimitIsRepairedInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	// 1 MB is lumberjack's smallest unit: fill to exactly the limit, torn.
	body := strings.Repeat("x", 1024*1024-3) + "{\"s"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewFileSink(path, 1, 5, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write([]byte(`{"seq":2}`)); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		raw, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if len(raw) > 0 && raw[len(raw)-1] != '\n' {
			t.Fatalf("%s ends mid-record after the repair", e.Name())
		}
	}
}

func TestNewFileSink_WriteOnlyFileGetsABoundaryAtOpen(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can read a write-only file")
	}
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	if err := os.WriteFile(path, []byte("{\"seq\":1}\n{\"se"), 0o200); err != nil {
		t.Fatal(err)
	}
	s, err := NewFileSink(path, 10, 1, 1, false)
	if err != nil {
		t.Fatalf("a write-only file keeps working: %v", err)
	}
	if err := s.Write([]byte(`{"seq":2}`)); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	_ = os.Chmod(path, 0o600)
	raw, _ := os.ReadFile(path)
	recs := assertWellDelimited(t, raw)
	if len(recs) != 2 || recs[1] != `{"seq":2}` {
		t.Fatalf("the new record must start on its own line: %q", raw)
	}
}

func TestSink_FailedOpenRepairIsRetriedByPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	if err := os.WriteFile(path, []byte("{\"se"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewFileSink(path, 1, 5, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	ws := s.(*writerSink)
	// Simulate the open-time repair having failed (disk full at open).
	ws.mu.Lock()
	ws.torn = true
	calls := 0
	ws.openRepair = func() error { calls++; return appendNewline(path) }
	ws.mu.Unlock()
	if err := s.Write([]byte(`{"seq":2}`)); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if calls != 1 {
		t.Fatalf("the retry must go through the path repair, got %d calls", calls)
	}
	raw, _ := os.ReadFile(path)
	if !strings.HasSuffix(string(raw), "\n{\"seq\":2}\n") {
		t.Fatalf("got %q", raw)
	}
}
