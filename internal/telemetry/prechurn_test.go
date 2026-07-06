package telemetry

import (
	"path/filepath"
	"testing"
	"time"

	"go.etcd.io/bbolt"
)

// openPreChurnTestDB creates a throwaway BBolt database at a caller-visible
// path so tests can close and reopen it to simulate process restarts.
func openPreChurnTestDB(t *testing.T) (*bbolt.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "prechurn_test.db")
	db := reopenPreChurnTestDB(t, path)
	return db, path
}

// reopenPreChurnTestDB opens (or reopens) the BBolt file at path — the
// "next process instance" in restart-sequence tests.
func reopenPreChurnTestDB(t *testing.T, path string) *bbolt.DB {
	t.Helper()
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatalf("open bbolt: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// rawShutdownMarker reads the persisted marker value directly, bypassing the
// store, so tests can assert on-disk state.
func rawShutdownMarker(t *testing.T, db *bbolt.DB) string {
	t.Helper()
	var val string
	err := db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(PreChurnBucketName))
		if b == nil {
			return nil
		}
		val = string(b.Get([]byte(prechurnKeyShutdownMarker)))
		return nil
	})
	if err != nil {
		t.Fatalf("read raw marker: %v", err)
	}
	return val
}

// --- FR-010 / FR-013: clean vs crash vs first-run detection ---

func TestShutdownMarker_FirstRunIsUnknownNeverCrash(t *testing.T) {
	db, _ := openPreChurnTestDB(t)
	store := NewPreChurnStore()

	prev, err := store.ArmShutdownMarker(db)
	if err != nil {
		t.Fatalf("ArmShutdownMarker: %v", err)
	}
	if prev != PreviousShutdownUnknown {
		t.Fatalf("first run: expected unknown (%q), got %q", PreviousShutdownUnknown, prev)
	}
	if got := rawShutdownMarker(t, db); got != "armed" {
		t.Fatalf("expected marker armed after startup, got %q", got)
	}
}

func TestShutdownMarker_CleanRestartSequence(t *testing.T) {
	db, path := openPreChurnTestDB(t)
	store := NewPreChurnStore()

	// Instance 1: arm, then graceful shutdown resolves the marker.
	if _, err := store.ArmShutdownMarker(db); err != nil {
		t.Fatalf("arm: %v", err)
	}
	if err := store.ResolveCleanShutdown(db); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Instance 2: the prior marker reads clean.
	db2 := reopenPreChurnTestDB(t, path)
	prev, err := store.ArmShutdownMarker(db2)
	if err != nil {
		t.Fatalf("arm 2: %v", err)
	}
	if prev != PreviousShutdownClean {
		t.Fatalf("expected %q after graceful shutdown, got %q", PreviousShutdownClean, prev)
	}
}

func TestShutdownMarker_CrashRestartSequence(t *testing.T) {
	db, path := openPreChurnTestDB(t)
	store := NewPreChurnStore()

	// Instance 1: arm, then die without resolving (SIGKILL/panic/power loss).
	if _, err := store.ArmShutdownMarker(db); err != nil {
		t.Fatalf("arm: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Instance 2: armed-but-unresolved marker reads crash.
	db2 := reopenPreChurnTestDB(t, path)
	prev, err := store.ArmShutdownMarker(db2)
	if err != nil {
		t.Fatalf("arm 2: %v", err)
	}
	if prev != PreviousShutdownCrash {
		t.Fatalf("expected %q after unresolved marker, got %q", PreviousShutdownCrash, prev)
	}
}

// TestShutdownMarker_FullLifecycle walks unknown → clean → crash → clean
// across four simulated instances against the same DB file.
func TestShutdownMarker_FullLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prechurn_lifecycle.db")
	store := NewPreChurnStore()

	type step struct {
		wantPrev string
		resolve  bool // graceful shutdown at instance end
	}
	steps := []step{
		{wantPrev: PreviousShutdownUnknown, resolve: true}, // first run, clean exit
		{wantPrev: PreviousShutdownClean, resolve: false},  // ran, crashed
		{wantPrev: PreviousShutdownCrash, resolve: true},   // saw the crash, clean exit
		{wantPrev: PreviousShutdownClean, resolve: true},   // back to clean
	}
	for i, st := range steps {
		db := reopenPreChurnTestDB(t, path)
		prev, err := store.ArmShutdownMarker(db)
		if err != nil {
			t.Fatalf("step %d arm: %v", i, err)
		}
		if prev != st.wantPrev {
			t.Fatalf("step %d: expected previous_shutdown %q, got %q", i, st.wantPrev, prev)
		}
		if st.resolve {
			if err := store.ResolveCleanShutdown(db); err != nil {
				t.Fatalf("step %d resolve: %v", i, err)
			}
		}
		if err := db.Close(); err != nil {
			t.Fatalf("step %d close: %v", i, err)
		}
	}
}

// TestShutdownMarker_SecondInstanceCannotClobber pins the FR-013 assumption:
// while one process holds the BBolt file lock, a second open fails (this is
// what exit code 3 / DatabaseLockedError is built on), so a second instance
// never reaches the marker at all.
func TestShutdownMarker_SecondInstanceCannotClobber(t *testing.T) {
	db, path := openPreChurnTestDB(t)
	store := NewPreChurnStore()
	if _, err := store.ArmShutdownMarker(db); err != nil {
		t.Fatalf("arm: %v", err)
	}

	// Second "instance": same file, short lock timeout.
	if db2, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: 150 * time.Millisecond}); err == nil {
		_ = db2.Close()
		t.Fatalf("expected second bbolt.Open to fail while the first instance holds the lock")
	}

	if got := rawShutdownMarker(t, db); got != "armed" {
		t.Fatalf("marker clobbered by locked-out second instance: %q", got)
	}
}

// --- FR-012: last_error_code persistence + enum-only ---

func TestLastErrorCode_AbsentWhenNeverRecorded(t *testing.T) {
	db, _ := openPreChurnTestDB(t)
	store := NewPreChurnStore()

	code, err := store.LastErrorCode(db)
	if err != nil {
		t.Fatalf("LastErrorCode: %v", err)
	}
	if code != "" {
		t.Fatalf("expected empty code on fresh store, got %q", code)
	}
}

func TestLastErrorCode_PersistsAcrossRestart(t *testing.T) {
	db, path := openPreChurnTestDB(t)
	store := NewPreChurnStore()

	if err := store.RecordLastErrorCode(db, "MCPX_UPSTREAM_CONNECT_REFUSED"); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	db2 := reopenPreChurnTestDB(t, path)
	code, err := store.LastErrorCode(db2)
	if err != nil {
		t.Fatalf("LastErrorCode after restart: %v", err)
	}
	if code != "MCPX_UPSTREAM_CONNECT_REFUSED" {
		t.Fatalf("expected code to survive restart, got %q", code)
	}
}

func TestLastErrorCode_MostRecentWins(t *testing.T) {
	db, _ := openPreChurnTestDB(t)
	store := NewPreChurnStore()

	for _, c := range []string{"MCPX_DOCKER_PULL_FAILED", "MCPX_OAUTH_REFRESH_FAILED"} {
		if err := store.RecordLastErrorCode(db, c); err != nil {
			t.Fatalf("record %s: %v", c, err)
		}
	}
	code, err := store.LastErrorCode(db)
	if err != nil {
		t.Fatalf("LastErrorCode: %v", err)
	}
	if code != "MCPX_OAUTH_REFRESH_FAILED" {
		t.Fatalf("expected most recent code, got %q", code)
	}
}

// TestLastErrorCode_EnumOnly asserts that only stable MCPX_* enum shapes are
// ever persisted — free text, paths, server names, and malformed codes are
// silently dropped and never overwrite a previously recorded valid code.
func TestLastErrorCode_EnumOnly(t *testing.T) {
	db, _ := openPreChurnTestDB(t)
	store := NewPreChurnStore()

	if err := store.RecordLastErrorCode(db, "MCPX_CONFIG_INVALID"); err != nil {
		t.Fatalf("record valid: %v", err)
	}

	rejected := []string{
		"",
		"connection refused: dial tcp 10.0.0.5:8080",
		"/Users/bob/.mcpproxy/config.db is locked",
		"mcpx_lowercase_not_a_code",
		"MCPX_",                        // no suffix
		"MCPX_FOO BAR",                 // whitespace
		"MCPX_FOO/../../etc/passwd",    // path chars
		"MCPX_ERR: server github died", // free text after code
		"NOT_MCPX_CODE",
	}
	for _, bad := range rejected {
		if err := store.RecordLastErrorCode(db, bad); err != nil {
			t.Fatalf("RecordLastErrorCode(%q) should silently drop, got error: %v", bad, err)
		}
	}

	code, err := store.LastErrorCode(db)
	if err != nil {
		t.Fatalf("LastErrorCode: %v", err)
	}
	if code != "MCPX_CONFIG_INVALID" {
		t.Fatalf("invalid input overwrote stored code: got %q", code)
	}
}

// TestLastErrorCode_CorruptValueNeverSurfaced: a value that somehow bypassed
// validation on disk is re-validated at read time and never transmitted.
func TestLastErrorCode_CorruptValueNeverSurfaced(t *testing.T) {
	db, _ := openPreChurnTestDB(t)
	store := NewPreChurnStore()

	err := db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(PreChurnBucketName))
		if err != nil {
			return err
		}
		return b.Put([]byte(prechurnKeyLastErrorCode), []byte("server github: dial tcp refused"))
	})
	if err != nil {
		t.Fatalf("inject corrupt value: %v", err)
	}

	code, err := store.LastErrorCode(db)
	if err != nil {
		t.Fatalf("LastErrorCode: %v", err)
	}
	if code != "" {
		t.Fatalf("corrupt on-disk value surfaced: %q", code)
	}
}
