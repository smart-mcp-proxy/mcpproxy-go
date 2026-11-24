//go:build !server

package connect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func undoFiles(t *testing.T, home string) []string {
	t.Helper()
	m, err := filepath.Glob(ConfigPath("claude-code", home) + ".undo.*")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range m {
		if !strings.HasSuffix(f, ".undo.state") { // the per-config state is not a record
			out = append(out, f)
		}
	}
	return out
}

// With browser login not used, a connect writes exactly the files it always
// wrote: no undo record beside the user's config.
func TestKeyConnect_LeavesNoUndoRecord(t *testing.T) {
	for _, prior := range []bool{true, false} {
		svc, home := testService(t)
		oauthOn(svc)
		withFakeMinter(svc)
		if prior {
			seedClientConfig(t, home, "claude-code")
		}
		if res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{}); err != nil || !res.Success {
			t.Fatalf("connect: %v %+v", err, res)
		}
		if got := undoFiles(t, home); len(got) != 0 {
			t.Fatalf("prior=%v: a key connect must not write undo files, got %v", prior, got)
		}
		dir, _ := os.ReadDir(filepath.Dir(ConfigPath("claude-code", home)))
		for _, e := range dir {
			if filepath.Ext(e.Name()) == ".undo" {
				t.Fatalf("stray %s", e.Name())
			}
		}
	}
}

// An oauth connect writes a record; undoing or disconnecting it leaves none.
func TestOAuthConnect_RecordIsCleanedUp(t *testing.T) {
	for _, how := range []string{"undo", "disconnect"} {
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
			if len(undoFiles(t, home)) != 1 {
				t.Fatalf("an oauth connect writes one record, got %v", undoFiles(t, home))
			}
			if how == "undo" {
				if undone, err := svc.Undo("claude-code", "", baseName(res.BackupPath)); err != nil || !undone.Success {
					t.Fatalf("undo: %v %+v", err, undone)
				}
			} else if out, err := svc.Disconnect("claude-code", ""); err != nil || !out.Success {
				t.Fatalf("disconnect: %v %+v", err, out)
			}
			if got := undoFiles(t, home); len(got) != 0 {
				t.Fatalf("%s prior=%v: stray record files %v", how, prior, got)
			}
		}
	}
}

// A refused Undo keeps its record (the connect can still be undone later).
func TestOAuthConnect_ConflictKeepsTheRecord(t *testing.T) {
	svc, home := testService(t)
	oauthOn(svc)
	withFakeMinter(svc)
	seedClientConfig(t, home, "claude-code")
	res, _ := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth})
	path := ConfigPath("claude-code", home)
	_ = os.WriteFile(path, []byte("{}\n"), 0o644)
	if undone, _ := svc.Undo("claude-code", "", baseName(res.BackupPath)); undone.Action != "conflict" {
		t.Fatalf("want conflict, got %+v", undone)
	}
	if len(undoFiles(t, home)) != 1 {
		t.Fatal("the record must survive a refused undo")
	}
}
