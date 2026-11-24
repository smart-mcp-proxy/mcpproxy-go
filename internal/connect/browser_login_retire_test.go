//go:build !server

package connect

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// A record describes one file. Once that file is gone, any connect that creates
// the config again, key connects included, retires it.
func TestKeyConnectCreatingTheFile_RetiresAnOAuthRecord(t *testing.T) {
	for _, edited := range []bool{false, true} {
		svc, home := testService(t)
		oauthOn(svc)
		withFakeMinter(svc)
		path := ConfigPath("claude-code", home)

		if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); err != nil {
			t.Fatal(err)
		}
		x := readConfig(t, home, "claude-code")
		_ = os.Remove(path)

		res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{})
		if err != nil || !res.Success {
			t.Fatalf("key connect: %v %+v", err, res)
		}
		if got := undoFiles(t, home); len(got) != 0 {
			t.Fatalf("the stale oauth record must be retired, got %v", got)
		}
		if edited {
			if err := os.WriteFile(path, []byte(x), 0o600); err != nil {
				t.Fatal(err)
			}
			undone, err := svc.Undo("claude-code", "", "")
			if err != nil || undone.Action != "conflict" {
				t.Fatalf("edited back to X: want conflict, got %v %+v", err, undone)
			}
			if after, _ := os.ReadFile(path); string(after) != x {
				t.Fatal("the changed file must be left alone")
			}
		} else if undone, err := svc.Undo("claude-code", "", ""); err != nil || undone.Action != "deleted" {
			t.Fatalf("untouched key file: want deleted, got %v %+v", err, undone)
		}
	}
}

// The backup case: an oauth connect's record never decides a later key connect's
// undo, whether the backups differ by second or collide, or the old backup is gone.
func TestKeyConnectWithABackup_IsNotJudgedByAnOAuthRecord(t *testing.T) {
	fixed := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	prev := backupNow
	backupNow = func() time.Time { return fixed }
	defer func() { backupNow = prev }()

	for _, deleteOldBackup := range []bool{false, true} {
		svc, home := testService(t)
		oauthOn(svc)
		withFakeMinter(svc)
		seedClientConfig(t, home, "claude-code")
		path := ConfigPath("claude-code", home)

		first, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth})
		if err != nil || !first.Success {
			t.Fatal(err)
		}
		x := readConfig(t, home, "claude-code")
		if deleteOldBackup {
			_ = os.Remove(first.BackupPath) // its name is free to be reused
		}
		second, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Force: true})
		if err != nil || !second.Success {
			t.Fatalf("key connect: %v %+v", err, second)
		}
		if !deleteOldBackup && second.BackupPath == first.BackupPath {
			t.Fatal("fixture: same-second backups must not share a name")
		}
		if deleteOldBackup {
			if _, err := os.Stat(undoRecordPath(path, second.BackupPath)); err == nil {
				t.Fatal("the key connect must retire a stale record at its backup's record name")
			}
		}
		// edited back to the oauth content: not what the key connect wrote
		if err := os.WriteFile(path, []byte(x), 0o600); err != nil {
			t.Fatal(err)
		}
		if undone, _ := svc.Undo("claude-code", "", baseName(second.BackupPath)); undone.Action != "conflict" {
			t.Fatalf("delete=%v: edited back to the oauth content: want conflict, got %+v", deleteOldBackup, undone)
		}
	}
}

// A home directory with glob metacharacters must not hide records.
func TestRecords_AreFoundInADirectoryWithGlobCharacters(t *testing.T) {
	name := "user[1]*?"
	if runtime.GOOS == "windows" {
		name = "user[1]"
	}
	home := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	svc := NewServiceWithHome("127.0.0.1:8080", "", home)
	oauthOn(svc)
	withFakeMinter(svc)

	// created-file connect, untouched, Undo deletes it
	if res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); err != nil || !res.Success {
		t.Fatalf("connect: %v %+v", err, res)
	}
	if got := recordFilesIn(t, ConfigPath("claude-code", home)); len(got) != 1 {
		t.Fatalf("one record, got %v", got)
	}
	if undone, err := svc.Undo("claude-code", "", ""); err != nil || undone.Action != "deleted" {
		t.Fatalf("untouched created file: %v %+v", err, undone)
	}
	if got := recordFilesIn(t, ConfigPath("claude-code", home)); len(got) != 0 {
		t.Fatalf("stray records %v", got)
	}

	// Disconnect cleans up, and a re-create retires a leftover
	if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); err != nil {
		t.Fatal(err)
	}
	if out, err := svc.Disconnect("claude-code", ""); err != nil || !out.Success {
		t.Fatalf("disconnect: %v %+v", err, out)
	}
	if got := recordFilesIn(t, ConfigPath("claude-code", home)); len(got) != 0 {
		t.Fatalf("disconnect left %v", got)
	}
	_ = os.Remove(ConfigPath("claude-code", home))
	if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(ConfigPath("claude-code", home))
	if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); err != nil {
		t.Fatal(err)
	}
	if got := recordFilesIn(t, ConfigPath("claude-code", home)); len(got) != 1 {
		t.Fatalf("the older record must be retired, got %v", got)
	}
}

// recordFilesIn lists record files by reading the literal directory.
func recordFilesIn(t *testing.T, cfgPath string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(cfgPath))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if n := e.Name(); len(n) > len(filepath.Base(cfgPath)) && n[:len(filepath.Base(cfgPath))] == filepath.Base(cfgPath) && contains(n[len(filepath.Base(cfgPath)):], ".undo.") && !contains(n, ".undo.state") {
			out = append(out, n)
		}
	}
	return out
}
