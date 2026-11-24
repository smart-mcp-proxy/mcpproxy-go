//go:build !server

package connect

import (
	"os"
	"strings"
	"testing"
)

// A client that does not support browser login never has its entry read as one:
// an edit of its address to a lookalike oauth path is a conflict for Undo.
func TestUndo_UnsupportedClientsNeverMatchAnOAuthReplay(t *testing.T) {
	for _, id := range []string{"claude-desktop", "cursor"} {
		for _, prior := range []bool{true, false} {
			id, prior := id, prior
			t.Run(id, func(t *testing.T) {
				svc, home := testService(t)
				oauthOn(svc) // the feature is on; the client still does not support it
				if prior {
					seedClientConfig(t, home, id)
				}
				res, err := svc.ConnectWithOptions(id, "", ConnectOptions{})
				if err != nil || !res.Success {
					t.Fatalf("connect: %v %+v", err, res)
				}
				path := ConfigPath(id, home)
				raw, _ := os.ReadFile(path)
				edit := strings.Replace(string(raw), "127.0.0.1:8080/mcp", "127.0.0.1:8080/mcp/oauth/"+id, 1)
				if edit == string(raw) {
					t.Fatal("fixture: the edit did not apply")
				}
				if err := os.WriteFile(path, []byte(edit), 0o644); err != nil {
					t.Fatal(err)
				}
				undone, err := svc.Undo(id, "", baseName(res.BackupPath))
				if err != nil || undone.Action != "conflict" {
					t.Fatalf("undo: want conflict, got %v %+v", err, undone)
				}
				if after, _ := os.ReadFile(path); string(after) != edit {
					t.Fatalf("the edited file must be left alone:\n%s", after)
				}
				if st, _ := svc.GetStatus(id); st.Auth == AuthOAuth {
					t.Fatalf("status must not read it as a browser login: %+v", st)
				}
			})
		}
	}
}
