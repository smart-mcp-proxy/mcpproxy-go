//go:build !server

package connect

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// A failed commit of the new generation leaves the state pending: the connect
// itself succeeded and says so in cleanup_error, and Undo fails closed.
func TestStateCommitFailure_LeavesTheConnectSucceededAndUndoClosed(t *testing.T) {
	svc, home := testService(t)
	oauthOn(svc)
	withFakeMinter(svc)

	prev := stateWrite
	stateWrite = func(f *os.File, b []byte) error {
		if strings.Contains(string(b), `"pending":false`) {
			return errors.New("no space left on device")
		}
		return prev(f, b)
	}
	res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth})
	stateWrite = prev
	if err != nil || !res.Success {
		t.Fatalf("the config write succeeded, so the connect did: %v %+v", err, res)
	}
	if !strings.Contains(res.CleanupError, "undo state") {
		t.Fatalf("cleanup_error must say the state was not updated: %q", res.CleanupError)
	}
	raw, _ := os.ReadFile(statePathOf(home))
	if !strings.Contains(string(raw), `"pending":true`) {
		t.Fatalf("the state stays pending: %s", raw)
	}
	if undone, _ := svc.Undo("claude-code", "", ""); undone.Action != "conflict" {
		t.Fatalf("Undo must fail closed: %+v", undone)
	}
	// the next successful write sets a fresh generation
	if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth, Force: true}); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(statePathOf(home))
	if !strings.Contains(string(raw), `"pending":false`) {
		t.Fatalf("a successful write ends the pending state: %s", raw)
	}
}

// If the pending state cannot be restored after a failed write, the error says
// so (and Undo then refuses).
func TestFailedWrite_ReportsAStateThatCouldNotBeRestored(t *testing.T) {
	svc, home, cfg, _, _ := reconnectFixture(t)
	prev := stateWrite
	stateWrite = func(f *os.File, b []byte) error {
		if strings.Contains(string(b), `"pending":false`) { // the rollback rewrites the old state
			return errors.New("disk error")
		}
		return prev(f, b)
	}
	recordWrite = func(f *os.File, b []byte) error {
		_, err := f.Write(b)
		_ = os.WriteFile(cfg, []byte(brokenConfig), 0o600)
		return err
	}
	_, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth, Force: true})
	recordWrite = defaultRecordWrite
	stateWrite = prev
	if err == nil || !strings.Contains(err.Error(), "undo state could not be restored") {
		t.Fatalf("want the restore failure in the error, got %v", err)
	}
	_ = os.WriteFile(cfg, []byte(readConfig(t, home, "claude-code")), 0o600)
}
