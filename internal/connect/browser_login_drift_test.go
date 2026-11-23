//go:build !server

package connect

import (
	"testing"
)

// Pin: what the API-key path does when the listener changes after a connect.
// Its Undo is the original replay, which browser login does not touch.
func TestKeyPath_ListenerChangeAfterConnect(t *testing.T) {
	svc, home := testService(t)
	seedClientConfig(t, home, "claude-code")
	res, err := svc.ConnectWithOptions("claude-code", "my-proxy", ConnectOptions{})
	if err != nil || !res.Success {
		t.Fatalf("connect: %v %+v", err, res)
	}
	svc.listenAddr = "127.0.0.1:9999"
	st, _ := svc.GetStatus("claude-code")
	undone, err := svc.Undo("claude-code", "my-proxy", baseName(res.BackupPath))
	if err != nil {
		t.Fatal(err)
	}
	// Unchanged from the tray branch: Status loses the entry under a custom name
	// and Undo's replay no longer matches, so it reports a conflict.
	if st.Connected || undone.Action != "conflict" {
		t.Fatalf("pin: the key path loses the entry in Status and Undo conflicts; got connected=%v action=%q", st.Connected, undone.Action)
	}
}
