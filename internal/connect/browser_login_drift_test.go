//go:build !server

package connect

import (
	"testing"
)

// Parity pin: what the API-key path does when the listener changes after a
// connect, so the oauth path can be held to the same behaviour.
func TestKeyPath_ListenerChangeAfterConnect(t *testing.T) {
	svc, home := testService(t)
	seedClientConfig(t, home, "claude-code")
	res, err := svc.ConnectWithOptions("claude-code", "my-proxy", ConnectOptions{})
	if err != nil || !res.Success {
		t.Fatalf("connect: %v %+v", err, res)
	}
	svc.listenAddr = "127.0.0.1:9999"
	st, _ := svc.GetStatus("claude-code")
	t.Logf("key path after a port change: connected=%v match=%q server=%q", st.Connected, st.EndpointMatch, st.ServerName)
	undone, err := svc.Undo("claude-code", "my-proxy", baseName(res.BackupPath))
	t.Logf("key path undo: err=%v action=%q", err, undone.Action)
	if st.Connected || undone.Action != "conflict" {
		t.Fatalf("pin: the key path loses the entry (not connected) and Undo conflicts; got connected=%v action=%q", st.Connected, undone.Action)
	}
}
