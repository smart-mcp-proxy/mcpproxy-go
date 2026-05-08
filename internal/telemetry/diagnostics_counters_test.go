package telemetry

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.etcd.io/bbolt"
)

// newTestDiagDB creates a temporary BBolt DB for use in diagnostics counter tests.
func newTestDiagDB(t *testing.T) (*bbolt.DB, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "diag_test.db")
	db, err := bbolt.Open(path, 0600, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("bbolt.Open: %v", err)
	}
	return db, func() { _ = db.Close() }
}

// H001: empty DB returns zero-value snapshot.
func TestDiagnosticsCounterStore_Empty(t *testing.T) {
	db, cleanup := newTestDiagDB(t)
	defer cleanup()

	var s bboltDiagnosticsCounterStore
	snap, err := s.Snapshot(db)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !snap.isZero() {
		t.Fatalf("expected zero snapshot on empty DB, got %+v", snap)
	}
}

// H002: RecordErrorCode increments per-code counter and unique_codes_ever.
func TestDiagnosticsCounterStore_RecordErrorCode_Basic(t *testing.T) {
	db, cleanup := newTestDiagDB(t)
	defer cleanup()

	var s bboltDiagnosticsCounterStore
	code := "MCPX_HTTP_401"

	for i := 0; i < 3; i++ {
		if err := s.RecordErrorCode(db, code); err != nil {
			t.Fatalf("RecordErrorCode: %v", err)
		}
	}
	// Record a second distinct code once.
	if err := s.RecordErrorCode(db, "MCPX_OAUTH_REFRESH_EXPIRED"); err != nil {
		t.Fatalf("RecordErrorCode second: %v", err)
	}

	snap, err := s.Snapshot(db)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.ErrorCodeCounts24h[code] != 3 {
		t.Errorf("expected count 3 for %s, got %d", code, snap.ErrorCodeCounts24h[code])
	}
	if snap.ErrorCodeCounts24h["MCPX_OAUTH_REFRESH_EXPIRED"] != 1 {
		t.Errorf("expected count 1 for MCPX_OAUTH_REFRESH_EXPIRED, got %d", snap.ErrorCodeCounts24h["MCPX_OAUTH_REFRESH_EXPIRED"])
	}
	if snap.UniqueCodesEver != 2 {
		t.Errorf("expected UniqueCodesEver=2, got %d", snap.UniqueCodesEver)
	}
}

// H003: non-MCPX_ strings are silently dropped (no leakage of free text).
func TestDiagnosticsCounterStore_NonMCPXDropped(t *testing.T) {
	db, cleanup := newTestDiagDB(t)
	defer cleanup()

	var s bboltDiagnosticsCounterStore
	// These should all be silently ignored.
	for _, bad := range []string{"", "mcpx_http_401", "http_401", "random text", "/path/to/file"} {
		if err := s.RecordErrorCode(db, bad); err != nil {
			t.Fatalf("RecordErrorCode(%q): unexpected error %v", bad, err)
		}
	}

	snap, err := s.Snapshot(db)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !snap.isZero() {
		t.Fatalf("expected zero snapshot after non-MCPX_ records, got %+v", snap)
	}
}

// H004: unique_codes_ever is idempotent — same code recorded multiple times
// still counts as 1.
func TestDiagnosticsCounterStore_UniqueCodesIdempotent(t *testing.T) {
	db, cleanup := newTestDiagDB(t)
	defer cleanup()

	var s bboltDiagnosticsCounterStore
	for i := 0; i < 5; i++ {
		_ = s.RecordErrorCode(db, "MCPX_DOCKER_DAEMON_DOWN")
	}
	for i := 0; i < 3; i++ {
		_ = s.RecordErrorCode(db, "MCPX_HTTP_5XX")
	}

	snap, _ := s.Snapshot(db)
	if snap.UniqueCodesEver != 2 {
		t.Errorf("expected UniqueCodesEver=2 (two distinct codes), got %d", snap.UniqueCodesEver)
	}
}

// H005: RecordFixAttempt — attempted and succeeded counters are correct.
func TestDiagnosticsCounterStore_FixAttemptSuccessMath(t *testing.T) {
	db, cleanup := newTestDiagDB(t)
	defer cleanup()

	var s bboltDiagnosticsCounterStore
	_ = s.RecordFixAttempt(db, "success")
	_ = s.RecordFixAttempt(db, "success")
	_ = s.RecordFixAttempt(db, "failed")
	_ = s.RecordFixAttempt(db, "blocked")
	_ = s.RecordFixAttempt(db, "unknown_future_outcome")

	snap, err := s.Snapshot(db)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.FixAttempted24h != 5 {
		t.Errorf("expected FixAttempted24h=5, got %d", snap.FixAttempted24h)
	}
	if snap.FixSucceeded24h != 2 {
		t.Errorf("expected FixSucceeded24h=2, got %d", snap.FixSucceeded24h)
	}
}

// H006: 24h decay — counters recorded before the window are zeroed at
// snapshot time.
func TestDiagnosticsCounterStore_24hDecay(t *testing.T) {
	db, cleanup := newTestDiagDB(t)
	defer cleanup()

	code := "MCPX_STDIO_SPAWN_ENOENT"

	// Manually write a stale counter (windowStart > 24h ago).
	pastStart := time.Now().Add(-25 * time.Hour)
	err := db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(DiagnosticsCountersBucketName))
		if err != nil {
			return err
		}
		// per-code counter with stale window
		return b.Put([]byte(diagKeyCodePrefix+code), encodeCounter(42, pastStart.Unix()))
	})
	if err != nil {
		t.Fatalf("seeding stale counter: %v", err)
	}

	var s bboltDiagnosticsCounterStore
	snap, err := s.Snapshot(db)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	// Decayed window should return 0 (key absent from map or zero value).
	if snap.ErrorCodeCounts24h[code] != 0 {
		t.Errorf("expected 0 after 24h decay, got %d", snap.ErrorCodeCounts24h[code])
	}
}

// H007: fix_attempted_24h decays correctly.
func TestDiagnosticsCounterStore_FixAttempt24hDecay(t *testing.T) {
	db, cleanup := newTestDiagDB(t)
	defer cleanup()

	pastStart := time.Now().Add(-25 * time.Hour)
	err := db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(DiagnosticsCountersBucketName))
		if err != nil {
			return err
		}
		return b.Put([]byte(diagKeyFixAttempted24h), encodeCounter(99, pastStart.Unix()))
	})
	if err != nil {
		t.Fatalf("seeding stale fix_attempted: %v", err)
	}

	var s bboltDiagnosticsCounterStore
	snap, err := s.Snapshot(db)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.FixAttempted24h != 0 {
		t.Errorf("expected FixAttempted24h=0 after decay, got %d", snap.FixAttempted24h)
	}
}

// H008: MarshalJSON caps ErrorCodeCounts24h to top-20 by count descending,
// ties broken by code ascending (deterministic order).
func TestDiagnosticsCounters_MarshalJSON_Top20Cap(t *testing.T) {
	counts := make(map[string]int, 25)
	// 25 distinct MCPX_ codes with descending counts 25, 24, ..., 1.
	for i := 1; i <= 25; i++ {
		counts[allTestCodes[i-1]] = 26 - i // code[0] → 25, code[1] → 24, …
	}
	d := DiagnosticsCounters{ErrorCodeCounts24h: counts}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var out struct {
		ErrorCodeCounts24h map[string]int `json:"error_code_counts_24h"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(out.ErrorCodeCounts24h) != 20 {
		t.Errorf("expected 20 entries after cap, got %d", len(out.ErrorCodeCounts24h))
	}
	// The retained codes should have counts 25..6 (the top 20 by value).
	for code, cnt := range out.ErrorCodeCounts24h {
		if cnt < 6 {
			t.Errorf("code %s with count %d should have been dropped (< 6)", code, cnt)
		}
		_ = code
	}
}

// H009: MarshalJSON tie-breaking is deterministic (same output on repeated
// calls with the same input).
func TestDiagnosticsCounters_MarshalJSON_Deterministic(t *testing.T) {
	counts := make(map[string]int, 25)
	for i := 0; i < 25; i++ {
		counts[allTestCodes[i]] = 10 // all same count — pure alphabetic tie-break
	}
	d := DiagnosticsCounters{ErrorCodeCounts24h: counts}

	var results [3][]byte
	for i := range results {
		raw, err := json.Marshal(d)
		if err != nil {
			t.Fatalf("MarshalJSON run %d: %v", i, err)
		}
		results[i] = raw
	}
	for i := 1; i < len(results); i++ {
		if string(results[i]) != string(results[0]) {
			t.Errorf("MarshalJSON is non-deterministic: run 0 != run %d", i)
		}
	}
}

// H010: PII scan — code strings that land in the JSON are all MCPX_* prefix
// (no free text, no paths, no server names).
func TestDiagnosticsCounters_NoLeakPII(t *testing.T) {
	db, cleanup := newTestDiagDB(t)
	defer cleanup()

	var s bboltDiagnosticsCounterStore
	// Record all 29 known MCPX_ codes.
	for _, code := range allTestCodes[:29] {
		_ = s.RecordErrorCode(db, code)
	}
	_ = s.RecordFixAttempt(db, "success")
	_ = s.RecordFixAttempt(db, "failed")

	snap, _ := s.Snapshot(db)
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	js := string(raw)

	// No forbidden patterns.
	forbidden := []string{
		"/home/", "/Users/", "C:\\",
		"localhost", "127.0.0.1",
		"password", "secret", "token",
	}
	for _, f := range forbidden {
		if strings.Contains(js, f) {
			t.Errorf("PII leak: JSON contains forbidden string %q\nJSON: %s", f, js)
		}
	}

	// Every key in error_code_counts_24h must start with MCPX_.
	var out struct {
		ErrorCodeCounts24h map[string]int `json:"error_code_counts_24h"`
	}
	_ = json.Unmarshal(raw, &out)
	for code := range out.ErrorCodeCounts24h {
		if !strings.HasPrefix(code, "MCPX_") {
			t.Errorf("non-MCPX_ code %q leaked into JSON", code)
		}
	}
}

// allTestCodes is the full list of 30 MCPX_ codes from internal/diagnostics/codes.go.
// Used for cardinality tests; we enumerate them directly to avoid a cross-package
// import in this test file (package telemetry).
var allTestCodes = []string{
	"MCPX_STDIO_SPAWN_ENOENT",
	"MCPX_STDIO_SPAWN_EACCES",
	"MCPX_STDIO_EXIT_NONZERO",
	"MCPX_STDIO_HANDSHAKE_TIMEOUT",
	"MCPX_STDIO_HANDSHAKE_INVALID",
	"MCPX_OAUTH_REFRESH_EXPIRED",
	"MCPX_OAUTH_REFRESH_403",
	"MCPX_OAUTH_DISCOVERY_FAILED",
	"MCPX_OAUTH_CALLBACK_TIMEOUT",
	"MCPX_OAUTH_CALLBACK_MISMATCH",
	"MCPX_HTTP_DNS_FAILED",
	"MCPX_HTTP_TLS_FAILED",
	"MCPX_HTTP_401",
	"MCPX_HTTP_403",
	"MCPX_HTTP_404",
	"MCPX_HTTP_5XX",
	"MCPX_HTTP_CONN_REFUSED",
	"MCPX_DOCKER_DAEMON_DOWN",
	"MCPX_DOCKER_IMAGE_PULL_FAILED",
	"MCPX_DOCKER_NO_PERMISSION",
	"MCPX_DOCKER_SNAP_APPARMOR",
	"MCPX_CONFIG_DEPRECATED_FIELD",
	"MCPX_CONFIG_PARSE_ERROR",
	"MCPX_CONFIG_MISSING_SECRET",
	"MCPX_QUARANTINE_PENDING_APPROVAL",
	"MCPX_QUARANTINE_TOOL_CHANGED",
	"MCPX_NETWORK_PROXY_MISCONFIG",
	"MCPX_NETWORK_OFFLINE",
	"MCPX_UNKNOWN_UNCLASSIFIED",
	// 30th entry (one over 29 for cap test)
	"MCPX_HTTP_401", // duplicate deliberately reused for pad; cap test uses index 0-24
}
