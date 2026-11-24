//go:build !server

package connect

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// afterPendingPublished runs fn once, right after the writer's pending state has
// been published (and before its config write), by hooking the directory sync.
func afterPendingPublished(t *testing.T, cfg string, fn func()) {
	t.Helper()
	prev := dirSync
	done := false
	dirSync = func(dir string) error {
		err := prev(dir)
		if !done {
			if raw, e := os.ReadFile(cfg + undoStateSuffix); e == nil && strings.Contains(string(raw), `"pending":true`) {
				done = true
				fn()
			}
		}
		return err
	}
	t.Cleanup(func() { dirSync = prev })
}

func gen(n int) string { return fmt.Sprintf("%032x", n) }

func writeState(t *testing.T, cfg string, st undoState) {
	t.Helper()
	raw, _ := json.Marshal(st)
	if err := os.WriteFile(cfg+undoStateSuffix, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A writer that fails before its config write must not roll the state back over
// another writer's committed generation.
func TestRollbackAfterAnotherWriterCommitted_ChangesNothing(t *testing.T) {
	svc, home, cfg, x, _ := reconnectFixture(t)
	recName := recordFilesIn(t, cfg)[0]
	recRaw, _ := os.ReadFile(filepath.Join(filepath.Dir(cfg), recName))

	y := `{"mcpServers":{"mcpproxy":{"type":"http","url":"http://127.0.0.1:7777/mcp/oauth/claude-code"}}}` + "\n"
	afterPendingPublished(t, cfg, func() {
		// B commits generation 1 with its own created-file record for Y ...
		var rec map[string]interface{}
		_ = json.Unmarshal(recRaw, &rec)
		rec["gen"] = gen(1)
		rec["sha256"] = fileDigest([]byte(y))
		b, _ := json.Marshal(rec)
		_ = os.WriteFile(filepath.Join(filepath.Dir(cfg), filepath.Base(cfg)+".undo.new.bbbbbbbb"), b, 0o600)
		writeState(t, cfg, undoState{Version: 1, Current: gen(1)})
		// ... while A's own write is refused
		_ = os.WriteFile(cfg, []byte(brokenConfig), 0o600)
	})
	_, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth, Force: true})
	if err == nil || !strings.Contains(err.Error(), "could not be restored") {
		t.Fatalf("A's refused rollback is reported in its error: %v", err)
	}
	raw, _ := os.ReadFile(cfg + undoStateSuffix)
	if st, ok := parseUndoState(raw); !ok || st.Pending || st.Current != gen(1) {
		t.Fatalf("the state must still be B's: %s", raw)
	}
	// the stale record plus reverted bytes does not authorise ...
	if err := os.WriteFile(cfg, []byte(x), 0o600); err != nil {
		t.Fatal(err)
	}
	if undone, _ := svc.Undo("claude-code", "", ""); undone.Action != "conflict" {
		t.Fatalf("stale record + reverted bytes: want conflict, got %+v", undone)
	}
	// ... and B's untouched file is still undoable
	if err := os.WriteFile(cfg, []byte(y), 0o600); err != nil {
		t.Fatal(err)
	}
	if undone, err := svc.Undo("claude-code", "", ""); err != nil || undone.Action != "deleted" {
		t.Fatalf("B's untouched file: %v %+v", err, undone)
	}
	_ = home
}

// A writer whose commit is refused after it wrote the config leaves the state
// pending: its record never authorises anything and neither does an older one.
func TestCommitAfterAnotherWriterTookOver_LeavesTheStatePending(t *testing.T) {
	svc, home, cfg, _, _ := reconnectFixture(t)
	afterPendingPublished(t, cfg, func() {
		writeState(t, cfg, undoState{Version: 1, Current: gen(5), Pending: true, Owner: gen(9)})
	})
	res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth, Force: true})
	if err != nil || !res.Success {
		t.Fatalf("the config write succeeded, so the connect did: %v %+v", err, res)
	}
	if !strings.Contains(res.CleanupError, "owned by another writer") {
		t.Fatalf("cleanup_error must say so: %q", res.CleanupError)
	}
	raw, _ := os.ReadFile(cfg + undoStateSuffix)
	if st, ok := parseUndoState(raw); !ok || !st.Pending {
		t.Fatalf("the state stays pending: %s", raw)
	}
	for _, name := range recordFilesIn(t, cfg) {
		_ = name
	}
	if undone, _ := svc.Undo("claude-code", "", ""); undone.Action != "conflict" {
		t.Fatalf("no record may authorise: %+v", undone)
	}
	_ = home
}

// A writer that finds the state pending under another owner (a crash) proceeds
// and takes the ownership over; the old owner's later commit and rollback are
// refused.
func TestTakeoverOfAStalePendingState(t *testing.T) {
	svc, home, cfg, _, _ := reconnectFixture(t)
	oldOwner := gen(0xdead)
	writeState(t, cfg, undoState{Version: 1, Current: gen(3), Pending: true, Owner: oldOwner})

	res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth, Force: true})
	if err != nil || !res.Success || res.CleanupError != "" {
		t.Fatalf("crash recovery must work: %v %+v", err, res)
	}
	raw, _ := os.ReadFile(cfg + undoStateSuffix)
	st, ok := parseUndoState(raw)
	if !ok || st.Pending || st.Current == gen(3) {
		t.Fatalf("a successful write ends with a fresh settled state: %s", raw)
	}
	// the old owner wakes up
	old := &configTx{cfgPath: cfg, owner: oldOwner, perm: 0o600, hadState: true, prevRaw: []byte(`{}`)}
	if err := old.rollback(); !errors.Is(err, errLostOwnership) {
		t.Fatalf("the old owner's rollback is refused: %v", err)
	}
	after, _ := os.ReadFile(cfg + undoStateSuffix)
	if string(after) != string(raw) {
		t.Fatal("a refused rollback changes nothing")
	}
	if err := old.commit(gen(7)); !errors.Is(err, errLostOwnership) {
		t.Fatalf("the old owner's commit is refused: %v", err)
	}
	_ = home
}

// Concurrent writers of one config in one process end in one consistent state.
func TestConcurrentWriters_EndWithOneConsistentState(t *testing.T) {
	svc, home := testService(t)
	oauthOn(svc)
	withFakeMinter(svc)
	cfg := ConfigPath("claude-code", home)

	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			opts := ConnectOptions{Force: true}
			if i%2 == 0 {
				opts.Auth = AuthOAuth
			}
			if _, err := svc.ConnectWithOptions("claude-code", "", opts); err != nil {
				t.Errorf("writer %d: %v", i, err)
			}
		}()
	}
	wg.Wait()

	raw, err := os.ReadFile(cfg + undoStateSuffix)
	if err != nil {
		t.Fatal(err)
	}
	if st, ok := parseUndoState(raw); !ok || st.Pending {
		t.Fatalf("one settled, well-formed state: %s", raw)
	}
	for _, n := range dirNames(t, filepath.Dir(cfg)) {
		if strings.Contains(n, "undo-tmp") {
			t.Fatalf("stray temp %s", n)
		}
	}
}

// Only a strictly well-formed state lets a record authorise.
func TestStateParsing_IsStrict(t *testing.T) {
	svc, home, cfg, _, _ := reconnectFixture(t)
	good, _ := os.ReadFile(cfg + undoStateSuffix)
	var cur string
	{
		st, _ := parseUndoState(good)
		cur = st.Current
	}
	bad := map[string]string{
		"missing pending":     fmt.Sprintf(`{"version":1,"current":%q}`, cur),
		"null pending":        fmt.Sprintf(`{"version":1,"current":%q,"pending":null}`, cur),
		"missing current":     `{"version":1,"pending":false}`,
		"null current":        `{"version":1,"current":null,"pending":false}`,
		"wrong type current":  `{"version":1,"current":5,"pending":false}`,
		"wrong version":       fmt.Sprintf(`{"version":2,"current":%q,"pending":false}`, cur),
		"missing version":     fmt.Sprintf(`{"current":%q,"pending":false}`, cur),
		"unknown field":       fmt.Sprintf(`{"version":1,"current":%q,"pending":false,"extra":1}`, cur),
		"short current":       `{"version":1,"current":"abc","pending":false}`,
		"non-hex current":     fmt.Sprintf(`{"version":1,"current":%q,"pending":false}`, strings.Repeat("z", 32)),
		"empty current":       `{"version":1,"current":"","pending":false}`,
		"settled with owner":  fmt.Sprintf(`{"version":1,"current":%q,"pending":false,"pending_owner":%q}`, cur, gen(1)),
		"pending without own": fmt.Sprintf(`{"version":1,"current":%q,"pending":true}`, cur),
		"trailing garbage":    string(good) + `{}`,
		"not an object":       `[]`,
	}
	for name, content := range bad {
		if err := os.WriteFile(cfg+undoStateSuffix, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if undone, _ := svc.Undo("claude-code", "", ""); undone.Action != "conflict" {
			t.Errorf("%s: want conflict, got %+v", name, undone)
		}
	}
	if err := os.WriteFile(cfg+undoStateSuffix, good, 0o600); err != nil {
		t.Fatal(err)
	}
	if undone, err := svc.Undo("claude-code", "", ""); err != nil || undone.Action != "deleted" {
		t.Fatalf("the well-formed state authorises: %v %+v", err, undone)
	}
	_ = home
}

// Durability: a failed fsync of the pending state stops the connect before the
// config is written; a failed fsync after a successful write is a note.
func TestPendingStepSyncFailure_StopsTheConnectBeforeTheConfigWrite(t *testing.T) {
	for _, which := range []string{"file", "directory"} {
		svc, home := testService(t)
		oauthOn(svc)
		withFakeMinter(svc)
		seedClientConfig(t, home, "claude-code")
		cfg := ConfigPath("claude-code", home)
		before := readConfig(t, home, "claude-code")

		if which == "file" {
			prev := stateSync
			stateSync = func(*os.File) error { return errors.New("fsync failed") }
			defer func() { stateSync = prev }()
		} else {
			prev := dirSync
			dirSync = func(string) error { return errors.New("dir fsync failed") }
			defer func() { dirSync = prev }()
		}
		_, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth})
		if err == nil {
			t.Fatalf("%s: a sync failure on the pending step must stop the connect", which)
		}
		if readConfig(t, home, "claude-code") != before {
			t.Fatalf("%s: the config must not have been written", which)
		}
		if _, err := os.Stat(cfg + undoStateSuffix); err == nil {
			t.Fatalf("%s: a failed pending step leaves no state behind", which)
		}
		for _, n := range dirNames(t, filepath.Dir(cfg)) {
			if strings.Contains(n, "undo-tmp") {
				t.Fatalf("%s: stray temp %s", which, n)
			}
		}
		stateSync, dirSync = syncFile, syncDirectory
	}
}
