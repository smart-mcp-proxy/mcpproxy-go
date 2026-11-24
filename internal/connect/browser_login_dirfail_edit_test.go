//go:build !server

package connect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// With the directory unlistable, an oauth-created file edited to the plain
// keyless form must not be deleted through the key replay.
func TestUndo_UnlistableDirectoryNeverFallsBackToTheKeyReplay(t *testing.T) {
	skipIfPermissionsDoNotBind(t)
	svc, home := testService(t)
	oauthOn(svc)
	withFakeMinter(svc)
	if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); err != nil {
		t.Fatal(err)
	}
	path := ConfigPath("claude-code", home)
	raw, _ := os.ReadFile(path)
	edit := strings.Replace(string(raw), "127.0.0.1:8080/mcp/oauth/claude-code", "127.0.0.1:8080/mcp", 1)
	if err := os.WriteFile(path, []byte(edit), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	unreadable(t, dir)
	undone, err := svc.Undo("claude-code", "", "")
	_ = os.Chmod(dir, 0o700)
	if err != nil || undone.Action != "conflict" {
		t.Fatalf("want conflict, got %v %+v", err, undone)
	}
	if after, _ := os.ReadFile(path); string(after) != edit {
		t.Fatal("the edited file must be left alone")
	}
}
