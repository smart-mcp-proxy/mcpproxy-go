//go:build !server

package connect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func skipIfPermissionsDoNotBind(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not bind here")
	}
}

// unreadable makes dir searchable and writable but not listable.
func unreadable(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
}

func TestOAuthConnect_RefusesWhenTheDirectoryCannotBeListed(t *testing.T) {
	skipIfPermissionsDoNotBind(t)
	svc, home := testService(t)
	oauthOn(svc)
	withFakeMinter(svc)
	seedClientConfig(t, home, "claude-code")
	dir := filepath.Dir(ConfigPath("claude-code", home))
	before := readConfig(t, home, "claude-code")
	listing := dirNames(t, dir)

	unreadable(t, dir)
	if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); err == nil {
		t.Fatal("an oauth connect must refuse when it cannot list the directory")
	}
	_ = os.Chmod(dir, 0o700)
	if got := dirNames(t, dir); len(got) != len(listing) {
		t.Fatalf("nothing may be written: before %v after %v", listing, got)
	}
	if readConfig(t, home, "claude-code") != before {
		t.Fatal("the config must be untouched")
	}
}

func TestUndo_IsAConflictWhenTheDirectoryCannotBeListed(t *testing.T) {
	skipIfPermissionsDoNotBind(t)
	svc, home := testService(t)
	oauthOn(svc)
	withFakeMinter(svc)
	if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(ConfigPath("claude-code", home))
	before := readConfig(t, home, "claude-code")
	unreadable(t, dir)
	undone, err := svc.Undo("claude-code", "", "")
	if err != nil || undone.Action != "conflict" {
		t.Fatalf("want conflict, got %v %+v", err, undone)
	}
	_ = os.Chmod(dir, 0o700)
	if readConfig(t, home, "claude-code") != before {
		t.Fatal("the file must be left alone")
	}
}

func TestDisconnect_ReportsACleanupFailure(t *testing.T) {
	skipIfPermissionsDoNotBind(t)
	svc, home := testService(t)
	oauthOn(svc)
	withFakeMinter(svc)
	seedClientConfig(t, home, "claude-code")
	if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); err != nil {
		t.Fatal(err)
	}
	unreadable(t, filepath.Dir(ConfigPath("claude-code", home)))
	out, err := svc.Disconnect("claude-code", "")
	if err != nil || !out.Success || out.CleanupError == "" {
		t.Fatalf("the entry is removed and the failed cleanup is reported: %v %+v", err, out)
	}
}

// A key connect must not newly fail on an install with no records.
func TestKeyConnect_ProceedsWhenTheDirectoryCannotBeListed(t *testing.T) {
	skipIfPermissionsDoNotBind(t)
	svc, home := testService(t)
	oauthOn(svc)
	withFakeMinter(svc)
	seedClientConfig(t, home, "claude-code")
	unreadable(t, filepath.Dir(ConfigPath("claude-code", home)))
	res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{})
	if err != nil || !res.Success {
		t.Fatalf("a key connect must proceed: %v %+v", err, res)
	}
}

// A record at the name a new backup would take that belongs to another entry or
// client is a name collision: the connect takes another backup name and leaves it.
func TestBackupNameCollisionWithAForeignRecord(t *testing.T) {
	fixed := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	prev := backupNow
	backupNow = func() time.Time { return fixed }
	defer func() { backupNow = prev }()

	for _, foreign := range []string{"entry", "client"} {
		svc, home := testService(t)
		oauthOn(svc)
		withFakeMinter(svc)
		seedClientConfig(t, home, "claude-code")
		cfg := ConfigPath("claude-code", home)

		first, err := svc.ConnectWithOptions("claude-code", "one", ConnectOptions{Auth: AuthOAuth})
		if err != nil || !first.Success {
			t.Fatal(err)
		}
		recPath := undoRecordPath(cfg, first.BackupPath)
		if foreign == "client" {
			raw, _ := os.ReadFile(recPath)
			var rec undoRecord
			_ = json.Unmarshal(raw, &rec)
			rec.Client = "cursor"
			raw, _ = json.Marshal(rec)
			_ = os.WriteFile(recPath, raw, 0o600)
		}
		recBefore, _ := os.ReadFile(recPath)
		_ = os.Remove(first.BackupPath) // its name is free again

		name := "two"
		if foreign == "client" {
			name = "one"
		}
		second, err := svc.ConnectWithOptions("claude-code", name, ConnectOptions{Auth: AuthOAuth, Force: true})
		if err != nil || !second.Success {
			t.Fatalf("%s: %v %+v", foreign, err, second)
		}
		if second.BackupPath == first.BackupPath {
			t.Fatalf("%s: the colliding backup name must not be reused", foreign)
		}
		if after, err := os.ReadFile(recPath); err != nil || string(after) != string(recBefore) {
			t.Fatalf("%s: the other record must survive untouched: %v", foreign, err)
		}
		if _, err := os.Stat(undoRecordPath(cfg, second.BackupPath)); err != nil {
			t.Fatalf("%s: the new connect keeps its own record: %v", foreign, err)
		}
		if undone, err := svc.Undo("claude-code", name, baseName(second.BackupPath)); err != nil || undone.Action != "restored" {
			t.Fatalf("%s: undo of the new connect: %v %+v", foreign, err, undone)
		}
	}
}
