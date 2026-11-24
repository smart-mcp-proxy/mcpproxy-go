//go:build !server && !windows

package connect

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Two independent handles on one directory exclude each other (flock is per open
// file description), so this stands in for two processes.
func TestDirLock_ExcludesAnotherHandle(t *testing.T) {
	dir := t.TempDir()
	releaseA, err := acquireDirLock(dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := acquireDirLock(dir, 80*time.Millisecond); err == nil || !strings.Contains(err.Error(), "locked by another") {
		t.Fatalf("B must fail while A holds the lock: %v", err)
	}
	if time.Since(start) < 70*time.Millisecond {
		t.Fatal("B must have waited for its bounded wait")
	}
	releaseA()
	releaseB, err := acquireDirLock(dir, time.Second)
	if err != nil {
		t.Fatalf("B acquires once A released: %v", err)
	}
	releaseB()
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("the lock creates no file: %v", entries)
	}
}

// A connect while another process holds the directory fails before it writes
// anything, with a clear error, in either auth mode.
func TestConnect_FailsBeforeAnyWriteWhileTheDirectoryIsLocked(t *testing.T) {
	prev := dirLockWait
	dirLockWait = 100 * time.Millisecond
	defer func() { dirLockWait = prev }()

	for _, auth := range []string{AuthKey, AuthOAuth} {
		svc, home := testService(t)
		oauthOn(svc)
		withFakeMinter(svc)
		seedClientConfig(t, home, "claude-code")
		cfg := ConfigPath("claude-code", home)
		before := readConfig(t, home, "claude-code")
		names := dirNames(t, filepath.Dir(cfg))

		release, err := acquireDirLock(filepath.Dir(cfg), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_, cerr := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: auth, Force: true})
		release()
		if cerr == nil || !strings.Contains(cerr.Error(), "locked by another") {
			t.Fatalf("%s: want the lock error, got %v", auth, cerr)
		}
		if readConfig(t, home, "claude-code") != before || len(dirNames(t, filepath.Dir(cfg))) != len(names) {
			t.Fatalf("%s: nothing may be written", auth)
		}
	}
}

// The directory lock is held for the whole transaction, so another process can
// never take over, or restore over, a live writer's state.
func TestDirLock_IsHeldAcrossTheWholeTransaction(t *testing.T) {
	svc, home := testService(t)
	oauthOn(svc)
	withFakeMinter(svc)
	cfg := ConfigPath("claude-code", home)

	var contended error
	afterPendingPublished(t, cfg, func() {
		_, contended = acquireDirLock(filepath.Dir(cfg), 50*time.Millisecond)
	})
	if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); err != nil {
		t.Fatal(err)
	}
	if contended == nil {
		t.Fatal("another process must not get the lock while the transaction is pending")
	}
	// and it is released afterwards
	release, err := acquireDirLock(filepath.Dir(cfg), 200*time.Millisecond)
	if err != nil {
		t.Fatalf("the lock must be released when the connect returns: %v", err)
	}
	release()
}

// A pending state found at begin is a dead writer's: the next writer takes it
// over, and if it fails before writing, rolls back to exactly those bytes. That
// leaves the state pending (a true crash residue, which only a successful write
// clears); it can never be a live writer's bytes, because the lock keeps a live
// writer's transaction from being snapshotted.
func TestRollbackOfATakeoverRestoresTheCrashResidueExactly(t *testing.T) {
	svc, home, cfg, good, _ := reconnectFixture(t)
	residue := undoState{Version: 1, Current: gen(3), Pending: true, Owner: gen(0xdead)}
	writeState(t, cfg, residue)
	residueRaw, _ := os.ReadFile(cfg + undoStateSuffix)

	recordWrite = func(f *os.File, b []byte) error {
		_, err := f.Write(b)
		_ = os.WriteFile(cfg, []byte(brokenConfig), 0o600)
		return err
	}
	_, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth, Force: true})
	recordWrite = defaultRecordWrite
	if err == nil {
		t.Fatal("the connect must fail")
	}
	after, _ := os.ReadFile(cfg + undoStateSuffix)
	if string(after) != string(residueRaw) {
		t.Fatalf("the residue is restored exactly: %s", after)
	}
	_ = os.WriteFile(cfg, []byte(good), 0o600)
	// the next successful write clears it
	if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth, Force: true}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(cfg + undoStateSuffix)
	if st, ok := parseUndoState(raw); !ok || st.Pending {
		t.Fatalf("a successful write clears the residue: %s", raw)
	}
	_ = home
}

// Symlinked aliases of one directory share one lock.
func TestLockConfig_ResolvesSymlinkedDirectories(t *testing.T) {
	real := filepath.Join(t.TempDir(), "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(filepath.Dir(real), "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Skip("symlinks unavailable")
	}
	a, b := filepath.Join(real, ".claude.json"), filepath.Join(alias, ".claude.json")
	if lockKey(a) != lockKey(b) {
		t.Fatalf("one key for both: %q vs %q", lockKey(a), lockKey(b))
	}
	// a config that does not exist yet, under a directory that does not either
	c, d := filepath.Join(real, "new", ".x.json"), filepath.Join(alias, "new", ".x.json")
	if lockKey(c) != lockKey(d) {
		t.Fatalf("the unresolved suffix is kept: %q vs %q", lockKey(c), lockKey(d))
	}

	releaseA, err := lockConfig(a)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	acquired := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		prev := dirLockWait
		_ = prev
		release, err := lockConfig(b)
		if err == nil {
			close(acquired)
			release()
		} else {
			close(acquired)
		}
	}()
	select {
	case <-acquired:
		t.Fatal("the alias must wait for the real path's lock")
	case <-time.After(150 * time.Millisecond):
	}
	releaseA()
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("the alias must proceed once the lock is released")
	}
	wg.Wait()
}

// OpenCode's alternate config is written under its own lock and transaction, and
// its undo records and state are cleaned up.
func TestOpenCodeAlternateDisconnect_RunsTheFullCleanup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	svc := NewServiceWithHome("127.0.0.1:8080", "", home)
	cands := opencodeConfigCandidates(home)
	jsonc, plain := cands[0], cands[1]
	if err := os.MkdirAll(filepath.Dir(jsonc), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(jsonc, []byte("{}\n"), 0o644)
	_ = os.WriteFile(plain, []byte(`{"mcp":{"mcpproxy":{"type":"remote","url":"http://127.0.0.1:8080/mcp"}}}`+"\n"), 0o644)
	// browser-login bookkeeping beside the .json (as if left by a state that exists)
	writeState(t, plain, undoState{Version: 1, Current: gen(1)})
	rec := fmt.Sprintf(`{"version":1,"client":"opencode","server":"mcpproxy","backup":"","auth":"oauth","sha256":%q,"gen":%q}`, strings.Repeat("a", 64), gen(1))
	_ = os.WriteFile(plain+".undo.new.aaaa", []byte(rec), 0o600)

	res, err := svc.Disconnect("opencode", "")
	if err != nil || !res.Success {
		t.Fatalf("disconnect: %v %+v", err, res)
	}
	if _, err := os.Stat(plain + ".undo.new.aaaa"); err == nil {
		t.Fatal("the alternate config's records must be cleaned up")
	}
	if _, err := os.Stat(plain + undoStateSuffix); err == nil {
		t.Fatal("and its state removed with the last record")
	}
}
