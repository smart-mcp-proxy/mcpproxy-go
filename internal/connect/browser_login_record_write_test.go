//go:build !server

package connect

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// A record whose write fails half way leaves no record and no temp file, and a
// failed connect leaves the config as it was.
func TestOAuthConnect_FailedRecordWriteLeavesNothingBehind(t *testing.T) {
	for _, prior := range []bool{true, false} {
		svc, home := testService(t)
		oauthOn(svc)
		withFakeMinter(svc)
		if prior {
			seedClientConfig(t, home, "claude-code")
		}
		dir := filepath.Dir(ConfigPath("claude-code", home))
		before := dirNames(t, dir)
		cfgBefore := ""
		if prior {
			cfgBefore = readConfig(t, home, "claude-code")
		}

		recordWrite = func(f *os.File, b []byte) error {
			_, _ = f.Write(b[:len(b)/2]) // a partial write, then the disk is full
			return errors.New("no space left on device")
		}
		_, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth})
		recordWrite = defaultRecordWrite
		if err == nil {
			t.Fatalf("prior=%v: a failed record write must fail the connect", prior)
		}
		after := dirNames(t, dir)
		// A backup of the prior config is the one thing a refused write may leave.
		for _, n := range after {
			if filepath.Ext(n) != "" && (contains(n, ".undo") || contains(n, "tmp")) {
				t.Fatalf("prior=%v: stray file %s (dir %v)", prior, n, after)
			}
		}
		if !prior && len(after) != len(before) {
			t.Fatalf("no config may be created: before %v after %v", before, after)
		}
		if prior && readConfig(t, home, "claude-code") != cfgBefore {
			t.Fatal("the config must be untouched")
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Two connects that both create the file (the racing case) keep one record
// each, under distinct names; Undo accepts the file that matches one of them
// and refuses one that matches none.
func TestOAuthConnect_CreatedFileRecordsAreUniquePerConnect(t *testing.T) {
	svc, home := testService(t)
	oauthOn(svc)
	withFakeMinter(svc)
	path := ConfigPath("claude-code", home)

	if _, err := svc.ConnectWithOptions("claude-code", "one", ConnectOptions{Auth: AuthOAuth}); err != nil {
		t.Fatal(err)
	}
	first := readConfig(t, home, "claude-code")
	_ = os.Remove(path) // the second connect also finds no file
	if _, err := svc.ConnectWithOptions("claude-code", "two", ConnectOptions{Auth: AuthOAuth}); err != nil {
		t.Fatal(err)
	}
	if got := undoFiles(t, home); len(got) != 2 {
		t.Fatalf("each connect keeps its own record, got %v", got)
	}

	// the file is the first connect's, as if it had won the race to write last
	if err := os.WriteFile(path, []byte(first), 0o600); err != nil {
		t.Fatal(err)
	}
	if undone, err := svc.Undo("claude-code", "one", ""); err != nil || undone.Action != "deleted" {
		t.Fatalf("a file matching one of the records is untouched: %v %+v", err, undone)
	}

	// a file matching none is a conflict
	_ = os.WriteFile(path, []byte("{}\n"), 0o600)
	if undone, err := svc.Undo("claude-code", "two", ""); err != nil || undone.Action != "conflict" {
		t.Fatalf("want conflict, got %v %+v", err, undone)
	}
}
