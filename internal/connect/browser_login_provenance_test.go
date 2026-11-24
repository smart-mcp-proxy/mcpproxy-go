//go:build !server

package connect

import (
	"os"
	"strings"
	"testing"
)

// The auth mode of a connect cannot be told from the file by replay: an oauth
// entry edited to the plain endpoint looks exactly like a keyless key entry.
func TestUndo_OAuthEditedToThePlainEndpointIsAConflict(t *testing.T) {
	for _, prior := range []bool{true, false} {
		svc, home := testService(t)
		oauthOn(svc)
		withFakeMinter(svc)
		if prior {
			seedClientConfig(t, home, "claude-code")
		}
		res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth})
		if err != nil || !res.Success {
			t.Fatalf("connect: %v %+v", err, res)
		}
		path := ConfigPath("claude-code", home)
		raw, _ := os.ReadFile(path)
		edit := strings.Replace(string(raw), "127.0.0.1:8080/mcp/oauth/claude-code", "127.0.0.1:8080/mcp", 1)
		if err := os.WriteFile(path, []byte(edit), 0o644); err != nil {
			t.Fatal(err)
		}
		undone, err := svc.Undo("claude-code", "", baseName(res.BackupPath))
		if err != nil || undone.Action != "conflict" {
			t.Fatalf("prior=%v undo: want conflict, got %v %+v", prior, err, undone)
		}
		if after, _ := os.ReadFile(path); string(after) != edit {
			t.Fatalf("prior=%v: the edited file must be left alone", prior)
		}
	}
}
