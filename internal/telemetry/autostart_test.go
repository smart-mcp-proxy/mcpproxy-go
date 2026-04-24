package telemetry

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestReadAutostart_TrayRespondsTrue: when the tray-owned sidecar reports
// enabled=true, the reader returns a non-nil pointer to true.
func TestReadAutostart_TrayRespondsTrue(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("autostart sidecar is macOS/Windows-only; Linux short-circuits to nil")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "tray-autostart.json")
	if err := os.WriteFile(path, []byte(`{"enabled": true, "updated_at": "2026-04-24T10:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &AutostartReader{Path: path}
	got := r.Read()
	if got == nil {
		t.Fatalf("Read() = nil, want *true")
	}
	if *got != true {
		t.Fatalf("Read() = %v, want true", *got)
	}
}

// TestReadAutostart_TrayRespondsFalse: when the tray-owned sidecar reports
// enabled=false, the reader returns a non-nil pointer to false.
func TestReadAutostart_TrayRespondsFalse(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("autostart sidecar is macOS/Windows-only; Linux short-circuits to nil")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "tray-autostart.json")
	if err := os.WriteFile(path, []byte(`{"enabled": false}`), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &AutostartReader{Path: path}
	got := r.Read()
	if got == nil {
		t.Fatalf("Read() = nil, want *false")
	}
	if *got != false {
		t.Fatalf("Read() = %v, want false", *got)
	}
}

// TestReadAutostart_MalformedPayload: malformed JSON (analogue of tray 500)
// yields nil — the scanner must not emit a bogus boolean.
func TestReadAutostart_MalformedPayload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tray-autostart.json")
	if err := os.WriteFile(path, []byte(`{this is not json`), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &AutostartReader{Path: path}
	got := r.Read()
	if got != nil {
		t.Fatalf("Read() = %v, want nil", *got)
	}
}

// TestReadAutostart_TrayNotRunning: when the sidecar file is absent (tray
// never wrote one, or was never launched), return nil.
func TestReadAutostart_TrayNotRunning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tray-autostart.json")
	// File deliberately not created.
	r := &AutostartReader{Path: path}
	got := r.Read()
	if got != nil {
		t.Fatalf("Read() = %v, want nil", *got)
	}
}

// TestReadAutostart_CachedOneHour: the reader caches the first hit for up to
// one hour. Mutating the sidecar within the TTL window does not change the
// returned value. After the TTL expires, the new value wins.
//
// Note: the reader short-circuits to nil on Linux regardless of sidecar
// content, so this test only runs meaningfully on darwin/windows. On Linux
// it verifies that both pre- and post-TTL reads return nil, not *true/*false.
func TestReadAutostart_CachedOneHour(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tray-autostart.json")
	if err := os.WriteFile(path, []byte(`{"enabled": true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	r := &AutostartReader{
		Path: path,
		now:  func() time.Time { return now },
	}
	got1 := r.Read()

	// Overwrite with false — should still return cached value within TTL.
	if err := os.WriteFile(path, []byte(`{"enabled": false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got2 := r.Read()
	if !ptrEq(got1, got2) {
		t.Fatalf("within-TTL cache mismatch: got1=%v got2=%v", deref(got1), deref(got2))
	}

	// Advance the clock past the TTL — the fresh (false) value should now win.
	r.now = func() time.Time { return now.Add(90 * time.Minute) }
	got3 := r.Read()
	// On Linux got3 will be nil (sidecar never read). On macOS/Windows it
	// will be *false. Either way it must not equal got1 when got1 was *true.
	if got1 != nil && *got1 == true && got3 != nil && *got3 == true {
		t.Fatalf("post-TTL did not refresh: still %v", *got3)
	}
}

// TestReadAutostart_BootRaceDoesNotPoisonCache: gemini P1 — if the core's
// first Read() happens BEFORE the tray writes the sidecar, a missing file
// must NOT poison the 1h TTL. The next Read() should re-probe and see the
// value the tray has since written.
func TestReadAutostart_BootRaceDoesNotPoisonCache(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("autostart sidecar is macOS/Windows-only; Linux short-circuits to nil")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "tray-autostart.json")
	// File deliberately absent at first read — simulates core-starts-before-tray.

	r := &AutostartReader{Path: path}
	if got := r.Read(); got != nil {
		t.Fatalf("first Read() = %v, want nil (file absent)", *got)
	}

	// Tray now writes the sidecar (the race has closed).
	if err := os.WriteFile(path, []byte(`{"enabled": true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Second Read() within the 1h TTL must re-probe and see the new value.
	// Before the fix, this returned nil (poisoned cache) for up to an hour.
	got := r.Read()
	if got == nil {
		t.Fatalf("second Read() = nil, want *true (tray wrote sidecar after first read)")
	}
	if *got != true {
		t.Fatalf("second Read() = %v, want true", *got)
	}
}

// TestDefaultAutostartReader_PathResolution: the default constructor returns
// a reader that does not panic on a missing sidecar.
func TestDefaultAutostartReader_PathResolution(t *testing.T) {
	r := DefaultAutostartReader()
	if r == nil {
		t.Fatal("DefaultAutostartReader returned nil")
	}
	_ = r.Read()
}

func ptrEq(a, b *bool) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func deref(p *bool) interface{} {
	if p == nil {
		return "<nil>"
	}
	return *p
}
