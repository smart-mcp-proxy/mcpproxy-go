package telemetry

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.etcd.io/bbolt"
	"go.uber.org/zap"
)

// newTestActivationDB creates a temporary BBolt DB for a test and returns it
// alongside a cleanup func.
func newTestActivationDB(t *testing.T) (*bbolt.DB, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "activation_test.db")
	db, err := bbolt.Open(path, 0600, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("bbolt.Open: %v", err)
	}
	return db, func() { _ = db.Close() }
}

// T027(a): empty BBolt returns zero-value struct.
func TestActivationStore_Load_EmptyReturnsZero(t *testing.T) {
	db, cleanup := newTestActivationDB(t)
	defer cleanup()

	var s bboltActivationStore
	st, err := s.Load(db)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if st.FirstConnectedServerEver || st.FirstMCPClientEver || st.FirstRetrieveToolsCallEver {
		t.Fatalf("expected all monotonic flags false, got %+v", st)
	}
	if len(st.MCPClientsSeenEver) != 0 {
		t.Fatalf("expected empty clients, got %v", st.MCPClientsSeenEver)
	}
	if st.RetrieveToolsCalls24h != 0 {
		t.Fatalf("expected 0 retrieve_tools count, got %d", st.RetrieveToolsCalls24h)
	}
	if st.EstimatedTokensSaved24hBucket != "" && st.EstimatedTokensSaved24hBucket != "0" {
		t.Fatalf("expected empty or 0 bucket, got %q", st.EstimatedTokensSaved24hBucket)
	}
}

// T027(b): Save then Load round-trips.
func TestActivationStore_SaveLoadRoundTrip(t *testing.T) {
	db, cleanup := newTestActivationDB(t)
	defer cleanup()

	var s bboltActivationStore
	in := ActivationState{
		FirstConnectedServerEver:   true,
		FirstMCPClientEver:         true,
		FirstRetrieveToolsCallEver: true,
		MCPClientsSeenEver:         []string{"claude-code", "cursor"},
		RetrieveToolsCalls24h:      42,
	}
	if err := s.Save(db, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := s.Load(db)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !out.FirstConnectedServerEver || !out.FirstMCPClientEver || !out.FirstRetrieveToolsCallEver {
		t.Fatalf("round-trip lost flags: %+v", out)
	}
	if len(out.MCPClientsSeenEver) != 2 || out.MCPClientsSeenEver[0] != "claude-code" || out.MCPClientsSeenEver[1] != "cursor" {
		t.Fatalf("round-trip lost clients: %v", out.MCPClientsSeenEver)
	}
}

// T027(c): monotonic flag cannot flip true->false through Save.
func TestActivationStore_MonotonicFlagsStickyOnSave(t *testing.T) {
	db, cleanup := newTestActivationDB(t)
	defer cleanup()

	var s bboltActivationStore
	if err := s.Save(db, ActivationState{FirstConnectedServerEver: true}); err != nil {
		t.Fatalf("Save true: %v", err)
	}
	if err := s.Save(db, ActivationState{FirstConnectedServerEver: false}); err != nil {
		t.Fatalf("Save false: %v", err)
	}
	st, err := s.Load(db)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !st.FirstConnectedServerEver {
		t.Fatalf("monotonic flag flipped true->false through Save")
	}
}

// T027(d): MCP clients list deduplicates and caps at 16 (17th dropped).
func TestActivationStore_RecordMCPClient_DedupAndCap(t *testing.T) {
	db, cleanup := newTestActivationDB(t)
	defer cleanup()

	var s bboltActivationStore
	// Insert 16 unique
	for i := 0; i < MaxMCPClientsSeen; i++ {
		name := "client-" + string(rune('a'+i))
		if err := s.RecordMCPClient(db, name); err != nil {
			t.Fatalf("RecordMCPClient[%d]: %v", i, err)
		}
	}
	// Duplicate — no-op.
	if err := s.RecordMCPClient(db, "client-a"); err != nil {
		t.Fatalf("RecordMCPClient dup: %v", err)
	}
	// 17th unique — dropped.
	if err := s.RecordMCPClient(db, "overflow"); err != nil {
		t.Fatalf("RecordMCPClient overflow: %v", err)
	}
	st, err := s.Load(db)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(st.MCPClientsSeenEver) != MaxMCPClientsSeen {
		t.Fatalf("expected cap at %d, got %d (%v)", MaxMCPClientsSeen, len(st.MCPClientsSeenEver), st.MCPClientsSeenEver)
	}
	for _, name := range st.MCPClientsSeenEver {
		if name == "overflow" {
			t.Fatalf("overflow client leaked past cap: %v", st.MCPClientsSeenEver)
		}
	}
}

// T027(e): path-like client name recorded as "unknown".
func TestActivationStore_RecordMCPClient_PathLikeIsUnknown(t *testing.T) {
	db, cleanup := newTestActivationDB(t)
	defer cleanup()

	var s bboltActivationStore
	if err := s.RecordMCPClient(db, "/Users/alice/bin/evil"); err != nil {
		t.Fatalf("RecordMCPClient: %v", err)
	}
	if err := s.RecordMCPClient(db, "C:\\Users\\bob\\tool.exe"); err != nil {
		t.Fatalf("RecordMCPClient: %v", err)
	}
	if err := s.RecordMCPClient(db, "../relative"); err != nil {
		t.Fatalf("RecordMCPClient: %v", err)
	}
	if err := s.RecordMCPClient(db, "user@host"); err != nil {
		t.Fatalf("RecordMCPClient: %v", err)
	}
	st, err := s.Load(db)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(st.MCPClientsSeenEver) != 1 {
		t.Fatalf("expected 1 deduped 'unknown' entry, got %v", st.MCPClientsSeenEver)
	}
	if st.MCPClientsSeenEver[0] != "unknown" {
		t.Fatalf("expected 'unknown', got %q", st.MCPClientsSeenEver[0])
	}
}

// T027(f): 24h window decay resets the counter after simulated 25h elapsed.
func TestActivationStore_RetrieveToolsCalls24hWindowDecay(t *testing.T) {
	db, cleanup := newTestActivationDB(t)
	defer cleanup()

	var s bboltActivationStore
	now := time.Now()
	// Seed window start 25 hours in the past.
	old := now.Add(-25 * time.Hour)
	if err := writeRetrieveCounter(db, 100, old); err != nil {
		t.Fatalf("writeRetrieveCounter: %v", err)
	}

	// Loading counter with read-at-time > 24h should report 0 (decayed).
	count, err := s.LoadRetrieveToolsCalls24hAt(db, now)
	if err != nil {
		t.Fatalf("LoadRetrieveToolsCalls24hAt: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 after decay, got %d", count)
	}

	// Fresh seed within window preserves count.
	fresh := now.Add(-1 * time.Hour)
	if err := writeRetrieveCounter(db, 7, fresh); err != nil {
		t.Fatalf("writeRetrieveCounter: %v", err)
	}
	count, err = s.LoadRetrieveToolsCalls24hAt(db, now)
	if err != nil {
		t.Fatalf("LoadRetrieveToolsCalls24hAt: %v", err)
	}
	if count != 7 {
		t.Fatalf("expected 7 within window, got %d", count)
	}
}

// T028: concurrent IncrementRetrieveToolsCall calls sum to 100, no races.
func TestRetrieveToolsBucket(t *testing.T) {
	db, cleanup := newTestActivationDB(t)
	defer cleanup()

	var s bboltActivationStore
	const N = 100
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			if err := s.IncrementRetrieveToolsCall(db); err != nil {
				t.Errorf("IncrementRetrieveToolsCall: %v", err)
			}
		}()
	}
	wg.Wait()

	st, err := s.Load(db)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if st.RetrieveToolsCalls24h != N {
		t.Fatalf("expected %d, got %d", N, st.RetrieveToolsCalls24h)
	}
}

// T029: token-saved bucketing.
func TestTokensSavedBucketing(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{50, "1_100"},
		{100, "1_100"},
		{500, "100_1k"},
		{1000, "100_1k"},
		{5000, "1k_10k"},
		{10000, "1k_10k"},
		{50000, "10k_100k"},
		{100000, "10k_100k"},
		{500000, "100k_plus"},
	}
	for _, tc := range cases {
		got := BucketTokens(tc.in)
		if got != tc.want {
			t.Errorf("BucketTokens(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Sanitizer unit tests supporting T035.
func TestSanitizeClientName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"claude-code", "claude-code"},
		{"cursor", "cursor"},
		{"Windsurf", "windsurf"},                // lowercased
		{"", "unknown"},                         // empty
		{"/Users/alice/bin", "unknown"},         // path
		{"C:\\Users\\bob", "unknown"},           // windows path
		{"../relative", "unknown"},              // relative traversal
		{"user@host", "unknown"},                // email-like
		{"a.b.c_d-2", "a.b.c_d-2"},              // allowed chars
		{"toolong" + longString(64), "unknown"}, // too long (>64)
	}
	for _, tc := range cases {
		got := sanitizeClientName(tc.in)
		if got != tc.want {
			t.Errorf("sanitizeClientName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func longString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}

// Installer-pending round-trip (supports US3 but harmless here).
func TestInstallerPendingFlag(t *testing.T) {
	db, cleanup := newTestActivationDB(t)
	defer cleanup()

	var s bboltActivationStore
	pending, err := s.IsInstallerPending(db)
	if err != nil {
		t.Fatalf("IsInstallerPending: %v", err)
	}
	if pending {
		t.Fatalf("expected false on empty db")
	}
	if err := s.SetInstallerPending(db, true); err != nil {
		t.Fatalf("SetInstallerPending: %v", err)
	}
	pending, err = s.IsInstallerPending(db)
	if err != nil {
		t.Fatalf("IsInstallerPending: %v", err)
	}
	if !pending {
		t.Fatalf("expected true after set")
	}
}

// TestInstallerPending_ClearedAfterFirstHeartbeat (T046) verifies the
// one-shot lifecycle via a thin heartbeat-layer simulator. We don't
// exercise the real HTTP Service here — instead we hit the same helper
// the payload builder uses (resolveLaunchSource), which is the public
// seam between the activation store and the LaunchSource emission.
//
// Contract: first resolveLaunchSource call after SetInstallerPending(true)
// returns "installer" and clears the flag; subsequent calls return
// whatever DetectLaunchSourceOnce() yields in the test environment
// (typically "unknown" / "cli" with no TTY attached).
func TestInstallerPending_ClearedAfterFirstHeartbeat(t *testing.T) {
	db, cleanup := newTestActivationDB(t)
	defer cleanup()

	var store bboltActivationStore

	// Simulate startup wire-up: env var present → SetInstallerPending(true).
	if err := store.SetInstallerPending(db, true); err != nil {
		t.Fatalf("SetInstallerPending: %v", err)
	}

	// Build a minimal Service with just the activation wiring. We skip the
	// HTTP loop — resolveLaunchSource is the unit under test.
	s := &Service{
		logger:          zap.NewNop(),
		activationStore: &store,
		activationDB:    db,
	}

	// First call → "installer" and the pending flag must be cleared.
	ls1 := s.resolveLaunchSource()
	if ls1 != LaunchSourceInstaller {
		t.Fatalf("first resolveLaunchSource = %q, want %q", ls1, LaunchSourceInstaller)
	}
	pending, err := store.IsInstallerPending(db)
	if err != nil {
		t.Fatalf("IsInstallerPending after first call: %v", err)
	}
	if pending {
		t.Fatalf("installer_heartbeat_pending should be cleared after first heartbeat")
	}

	// Second call → no longer installer; should return the runtime
	// detector's value (whatever it is in the test env), not installer.
	ls2 := s.resolveLaunchSource()
	if ls2 == LaunchSourceInstaller {
		t.Fatalf("second resolveLaunchSource still returned installer — one-shot broken")
	}
}
