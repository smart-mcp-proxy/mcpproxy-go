package audit

// Spec 133 FR-017 / plan D-C: after a short or errored write the sink resumes
// at a record boundary, so a partial record never glues onto the next one,
// and it appends nothing while a boundary cannot be established.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
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
	found := 0
	for _, e := range entries {
		raw, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if len(raw) > 0 && raw[len(raw)-1] != '\n' {
			t.Fatalf("%s ends mid-record after the repair", e.Name())
		}
		for _, l := range strings.Split(string(raw), "\n") {
			if l == `{"seq":2}` {
				found++
			}
		}
	}
	if found != 1 {
		t.Fatalf("the record written after the repair must be present once, found %d", found)
	}
}

// An active sink: a short write leaves the file torn, the next write repairs
// it through the writer's own handle and the record after it is intact, even
// when that record makes the file rotate.
func TestNewFileSink_ShortWriteThenRotationKeepsRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	s, err := NewFileSink(path, 1, 5, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	ws := s.(*writerSink)
	// First record: only part of it reaches the file (the lumberjack writer
	// is wrapped so its first call is short).
	ws.mu.Lock()
	ws.w = &shortOnceWriter{w: ws.w, n: 5}
	ws.mu.Unlock()
	_ = s.Write([]byte(`{"seq":1}`))
	// Fill close to the 1 MB limit, then a record that forces a rotation.
	pad := strings.Repeat("x", 1024*1024-20)
	if err := s.Write([]byte(`{"pad":"` + pad + `"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.Write([]byte(`{"seq":3}`)); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	entries, _ := os.ReadDir(dir)
	seen := map[string]bool{}
	for _, e := range entries {
		raw, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if len(raw) > 0 && raw[len(raw)-1] != '\n' {
			t.Fatalf("%s ends mid-record", e.Name())
		}
		for _, l := range assertWellDelimited(t, raw) {
			if strings.HasPrefix(l, `{"seq":3}`) || strings.HasPrefix(l, `{"pad":`) {
				seen[l[:8]] = true
			}
		}
	}
	if len(entries) < 2 {
		t.Fatalf("the fixture must reach the rotation threshold, got %d file(s)", len(entries))
	}
	if !seen[`{"seq":3`] || !seen[`{"pad":"`] {
		t.Fatalf("records after the repair must be intact across the rotation: %v", seen)
	}
}

// shortOnceWriter writes only n bytes of its first call, then passes through.
type shortOnceWriter struct {
	w    io.Writer
	n    int
	done bool
}

func (s *shortOnceWriter) Write(p []byte) (int, error) {
	if !s.done {
		s.done = true
		n, _ := s.w.Write(p[:s.n])
		return n, errors.New("disk full")
	}
	return s.w.Write(p)
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

// D-C at the rotation threshold: a record that lost only its newline, and a
// full-length write that still returned an error, each leave every file
// well-delimited and the next record readable on its own line.
func TestNewFileSink_LostNewlineAndErroredWriteAtRotation(t *testing.T) {
	for name, wrap := range map[string]func(io.Writer) io.Writer{
		"lost newline":              func(w io.Writer) io.Writer { return &dropNewlineOnceWriter{w: w} },
		"full-length errored write": func(w io.Writer) io.Writer { return &errAfterFullWriteOnceWriter{w: w} },
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "audit.jsonl")
			s, err := NewFileSink(path, 1, 5, 1, false)
			if err != nil {
				t.Fatal(err)
			}
			ws := s.(*writerSink)
			// Sized so the damaged record lands just below 1 MiB and the next
			// record crosses it: pad line 1 MiB-17, seq:1 (≤10), repair (≤1),
			// seq:2 (10) > 1 MiB.
			pad := strings.Repeat("x", 1024*1024-28)
			if err := s.Write([]byte(`{"pad":"` + pad + `"}`)); err != nil {
				t.Fatal(err)
			}
			ws.mu.Lock()
			ws.w = wrap(ws.w)
			ws.mu.Unlock()
			_ = s.Write([]byte(`{"seq":1}`)) // near the threshold, damaged
			if err := s.Write([]byte(`{"seq":2}`)); err != nil {
				t.Fatal(err)
			}
			_ = s.Close()
			entries, _ := os.ReadDir(dir)
			if len(entries) < 2 {
				t.Fatalf("the fixture must cross the rotation threshold, got %d file(s)", len(entries))
			}
			found := 0
			for _, e := range entries {
				raw, _ := os.ReadFile(filepath.Join(dir, e.Name()))
				if len(raw) > 0 && raw[len(raw)-1] != '\n' {
					t.Fatalf("%s ends mid-record", e.Name())
				}
				for _, l := range assertWellDelimited(t, raw) {
					if l == `{"seq":2}` {
						found++
					}
				}
			}
			if found != 1 {
				t.Fatalf("the next record must be readable once, found %d", found)
			}
		})
	}
}

// Reopen at the size limit of a file whose last record lost its newline.
func TestNewFileSink_ReopenLostNewlineAtSizeLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	rec := `{"pad":"` + strings.Repeat("x", 1024*1024-10) + `"}`
	if err := os.WriteFile(path, []byte(rec), 0o600); err != nil {
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
	parsed := 0
	for _, e := range entries {
		raw, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if len(raw) > 0 && raw[len(raw)-1] != '\n' {
			t.Fatalf("%s ends mid-record", e.Name())
		}
		parsed += len(assertWellDelimited(t, raw))
	}
	if parsed != 2 {
		t.Fatalf("the complete old record and the new one must both parse, got %d", parsed)
	}
}

type dropNewlineOnceWriter struct {
	w    io.Writer
	done bool
}

func (d *dropNewlineOnceWriter) Write(p []byte) (int, error) {
	if !d.done && len(p) > 1 && p[len(p)-1] == '\n' {
		d.done = true
		n, err := d.w.Write(p[:len(p)-1])
		if err == nil {
			err = errors.New("newline lost")
		}
		return n, err
	}
	return d.w.Write(p)
}

type errAfterFullWriteOnceWriter struct {
	w    io.Writer
	done bool
}

func (e *errAfterFullWriteOnceWriter) Write(p []byte) (int, error) {
	n, err := e.w.Write(p)
	if !e.done {
		e.done = true
		if err == nil {
			err = errors.New("fsync failed")
		}
	}
	return n, err
}

// The write that crosses the rotation threshold is itself torn: lumberjack
// rotates first, so the partial record and its repair are in the new file,
// and the old file stays complete.
func TestNewFileSink_TornWriteThatTriggersRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	s, err := NewFileSink(path, 1, 5, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	ws := s.(*writerSink)
	if err := s.Write([]byte(`{"pad":"` + strings.Repeat("x", 1024*1024-20) + `"}`)); err != nil {
		t.Fatal(err)
	}
	// The pad leaves 9 bytes before 1 MiB; forwarding 15 bytes of the next
	// record makes lumberjack rotate on that very write, which then tears in
	// the new file.
	ws.mu.Lock()
	ws.w = &shortOnceWriter{w: ws.w, n: 15}
	ws.mu.Unlock()
	_ = s.Write([]byte(`{"seq":1,"crosses":"the threshold"}`))
	if err := s.Write([]byte(`{"seq":2}`)); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	entries, _ := os.ReadDir(dir)
	if len(entries) < 2 {
		t.Fatalf("the torn write must have rotated, got %d file(s)", len(entries))
	}
	found := 0
	for _, e := range entries {
		raw, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if len(raw) > 0 && raw[len(raw)-1] != '\n' {
			t.Fatalf("%s ends mid-record", e.Name())
		}
		for _, l := range assertWellDelimited(t, raw) {
			if l == `{"seq":2}` {
				found++
			}
		}
	}
	if found != 1 {
		t.Fatalf("the record after the torn write must be readable once, found %d", found)
	}
	cur, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(cur), `{"seq":1,"cross`+"\n") {
		t.Fatalf("the torn fragment and its repair are in the new file: %q", cur)
	}
}

// Round 25: a file replaced between the tail check and the writer's open is
// checked again, so the first record never glues onto a restored fragment.
func TestNewFileSink_ReplacedBeforeTheWriterOpensIsRechecked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not rename a file another handle holds open")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	if err := os.WriteFile(path, []byte("{\"seq\":0}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	once := false
	afterTailCheck = func() {
		if once {
			return
		}
		once = true
		if err := os.Rename(path, path+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"seq":"torn`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { afterTailCheck = nil }()
	s, err := NewFileSink(path, 10, 1, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Write([]byte(`{"seq":1}`)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	recs := assertWellDelimited(t, raw)
	if len(recs) != 1 || recs[0] != `{"seq":1}` {
		t.Fatalf("want the record on its own line after the fragment, got %q", raw)
	}
}

// Round 26: an over-limit torn copy restored before the writer opens is not
// rotated away unchecked: every file the sink leaves ends at a boundary.
func TestNewFileSink_OverLimitReplacementIsNotRotatedUnchecked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not rename a file another handle holds open")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	if err := os.WriteFile(path, []byte("{\"seq\":0}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	once := false
	afterTailCheck = func() {
		if once {
			return
		}
		once = true
		if err := os.Rename(path, path+".old"); err != nil {
			t.Fatal(err)
		}
		big := append(bytes.Repeat([]byte("{\"pad\":\"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\"}\n"), 30000), []byte(`{"seq":"torn`)...)
		if err := os.WriteFile(path, big, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { afterTailCheck = nil }()
	s, err := NewFileSink(path, 1, 5, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write([]byte(`{"seq":1}`)); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, en := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, en.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) > 0 && raw[len(raw)-1] != '\n' {
			t.Fatalf("%s ends mid-record", en.Name())
		}
	}
}
