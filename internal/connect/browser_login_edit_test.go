//go:build !server

package connect

import (
	"os"
	"strings"
	"testing"
)

// An edit of the written address is somebody's edit, never ours: Undo must
// refuse it and leave the file alone, with or without a prior config.
func TestUndo_RefusesAnEditedOAuthAddress(t *testing.T) {
	const written = "http://127.0.0.1:8080/mcp/oauth/claude-code"
	edits := map[string]string{
		"another client id": "http://127.0.0.1:8080/mcp/oauth/codex",
		"extra path":        written + "/other-server",
		"query":             written + "?tenant=other",
		"fragment":          written + "#x",
		"userinfo":          "http://user:pw@127.0.0.1:8080/mcp/oauth/claude-code",
		"other host":        "http://127.0.0.1:9999/mcp/oauth/claude-code",
	}
	for _, prior := range []bool{true, false} {
		for name, edited := range edits {
			name, edited, prior := name, edited, prior
			t.Run(name, func(t *testing.T) {
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
				edit := strings.Replace(string(raw), written, edited, 1)
				if edit == string(raw) {
					t.Fatal("fixture: the edit did not apply")
				}
				if err := os.WriteFile(path, []byte(edit), 0o644); err != nil {
					t.Fatal(err)
				}
				undone, err := svc.Undo("claude-code", "", baseName(res.BackupPath))
				if err != nil || undone.Action != "conflict" {
					t.Fatalf("undo: want conflict, got %v %+v", err, undone)
				}
				after, rerr := os.ReadFile(path)
				if rerr != nil || string(after) != edit {
					t.Fatalf("the edited file must be left alone: %v\n%s", rerr, after)
				}
				// Status does not claim it as a browser login of ours.
				if st, _ := svc.GetStatus("claude-code"); st.Auth == AuthOAuth {
					t.Fatalf("an edited address is not reported as our oauth entry: %+v", st)
				}
			})
		}
	}
}

// Disconnect works by entry name on both paths, whatever the entry points at.
func TestDisconnect_EditedOAuthEntryIsTreatedLikeAKeyEntry(t *testing.T) {
	run := func(auth string) string {
		svc, home := testService(t)
		oauthOn(svc)
		withFakeMinter(svc)
		seedClientConfig(t, home, "claude-code")
		if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: auth}); err != nil {
			t.Fatal(err)
		}
		path := ConfigPath("claude-code", home)
		raw, _ := os.ReadFile(path)
		edit := strings.Replace(string(raw), "127.0.0.1:8080/mcp", "elsewhere.example:1/mcp", 1)
		_ = os.WriteFile(path, []byte(edit), 0o644)
		res, err := svc.Disconnect("claude-code", "")
		if err != nil {
			t.Fatal(err)
		}
		return res.Action
	}
	if k, o := run(AuthKey), run(AuthOAuth); k != o {
		t.Fatalf("disconnect of an edited entry: key=%q oauth=%q", k, o)
	}
}

// Toggling tls.enabled after Connect must not strand Undo.
func TestUndo_StillRestoresAfterTLSIsToggled(t *testing.T) {
	for _, first := range []bool{false, true} {
		svc, home := listenSvc(t, "127.0.0.1:8080", first)
		before := readConfig(t, home, "claude-code")
		res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth})
		if err != nil || !res.Success {
			t.Fatalf("connect: %v %+v", err, res)
		}
		svc.WithTLSEnabled(func() bool { return !first })
		undone, err := svc.Undo("claude-code", "", baseName(res.BackupPath))
		if err != nil || undone.Action != "restored" || readConfig(t, home, "claude-code") != before {
			t.Fatalf("tls %v->%v: %v %+v", first, !first, err, undone)
		}
	}
}
