//go:build !server

package connect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func recordFor(svc *Service, home string, res *ConnectResult) string {
	return undoRecordPath(ConfigPath("claude-code", home), res.BackupPath)
}

// Untouched files are restored whatever the auth mode, with or without a prior
// config, and whatever tls does afterwards (the digest is of the file).
func TestUndo_UntouchedFileIsRestored(t *testing.T) {
	for _, auth := range []string{AuthKey, AuthOAuth} {
		for _, prior := range []bool{true, false} {
			svc, home := testService(t)
			oauthOn(svc)
			withFakeMinter(svc)
			var before string
			if prior {
				seedClientConfig(t, home, "claude-code")
				before = readConfig(t, home, "claude-code")
			}
			res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: auth})
			if err != nil || !res.Success {
				t.Fatalf("connect: %v %+v", err, res)
			}
			svc.WithTLSEnabled(func() bool { return true })
			undone, err := svc.Undo("claude-code", "", baseName(res.BackupPath))
			if err != nil || !undone.Success {
				t.Fatalf("auth=%s prior=%v: %v %+v", auth, prior, err, undone)
			}
			if prior && readConfig(t, home, "claude-code") != before {
				t.Fatalf("auth=%s: not restored", auth)
			}
		}
	}
}

func TestUndo_KeyEntryEditedByTheUserIsAConflict(t *testing.T) {
	svc, home := testService(t)
	withFakeMinter(svc)
	seedClientConfig(t, home, "claude-code")
	res, _ := svc.ConnectWithOptions("claude-code", "", ConnectOptions{})
	path := ConfigPath("claude-code", home)
	raw, _ := os.ReadFile(path)
	edit := strings.Replace(string(raw), "127.0.0.1:8080/mcp", "127.0.0.1:8080/mcp/x", 1)
	_ = os.WriteFile(path, []byte(edit), 0o644)
	if undone, err := svc.Undo("claude-code", "", baseName(res.BackupPath)); err != nil || undone.Action != "conflict" {
		t.Fatalf("%v %+v", err, undone)
	}
}

// A backup made before records existed has no sidecar and is undone by the
// original key-path replay, byte for byte as before.
func TestUndo_LegacyBackupWithoutARecordUsesTheKeyReplay(t *testing.T) {
	svc, home := testService(t)
	seedClientConfig(t, home, "claude-code")
	res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{})
	if err != nil || !res.Success {
		t.Fatal(err)
	}
	if err := os.Remove(recordFor(svc, home, res)); err != nil {
		t.Fatalf("the connect must have written a record: %v", err)
	}
	if undone, err := svc.Undo("claude-code", "", baseName(res.BackupPath)); err != nil || undone.Action != "restored" {
		t.Fatalf("untouched legacy: %v %+v", err, undone)
	}

	res, _ = svc.ConnectWithOptions("claude-code", "", ConnectOptions{})
	_ = os.Remove(recordFor(svc, home, res))
	path := ConfigPath("claude-code", home)
	raw, _ := os.ReadFile(path)
	edit := strings.Replace(string(raw), "127.0.0.1:8080/mcp", "127.0.0.1:8080/mcp/x", 1)
	_ = os.WriteFile(path, []byte(edit), 0o644)
	if undone, err := svc.Undo("claude-code", "", baseName(res.BackupPath)); err != nil || undone.Action != "conflict" {
		t.Fatalf("edited legacy: %v %+v", err, undone)
	}
}

// Without a record, a browser-login entry is never accepted by the key replay.
func TestUndo_OAuthEntryWithoutItsRecordIsAConflict(t *testing.T) {
	svc, home := testService(t)
	oauthOn(svc)
	withFakeMinter(svc)
	seedClientConfig(t, home, "claude-code")
	res, _ := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth})
	_ = os.Remove(recordFor(svc, home, res))
	if undone, err := svc.Undo("claude-code", "", baseName(res.BackupPath)); err != nil || undone.Action != "conflict" {
		t.Fatalf("%v %+v", err, undone)
	}
}

// A record that cannot be trusted fails closed.
func TestUndo_BadRecordsAreAConflict(t *testing.T) {
	good := func(t *testing.T) (*Service, string, *ConnectResult) {
		svc, home := testService(t)
		withFakeMinter(svc)
		seedClientConfig(t, home, "claude-code")
		res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{})
		if err != nil || !res.Success {
			t.Fatal(err)
		}
		return svc, home, res
	}
	mutations := map[string]func(rec string) string{
		"malformed":      func(string) string { return "{not json" },
		"empty":          func(string) string { return "" },
		"tampered":       func(r string) string { return strings.Replace(r, `"sha256":"`, `"sha256":"0`, 1)[:len(r)] },
		"other client":   func(r string) string { return strings.Replace(r, `"claude-code"`, `"cursor"`, 1) },
		"other entry":    func(r string) string { return strings.Replace(r, `"server":"mcpproxy"`, `"server":"other"`, 1) },
		"other backup":   func(r string) string { return strings.Replace(r, `.bak.`, `.bak.x`, 1) },
		"wrong version":  func(r string) string { return strings.Replace(r, `"version":1`, `"version":2`, 1) },
		"short digest":   func(r string) string { return strings.Replace(r, `"sha256":"`, `"sha256":"x`, 1) },
		"unknown backup": func(r string) string { return strings.Replace(r, `"backup":"`, `"backup":"z`, 1) },
	}
	for name, mutate := range mutations {
		svc, home, res := good(t)
		rp := recordFor(svc, home, res)
		raw, err := os.ReadFile(rp)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(rp, []byte(mutate(string(raw))), 0o600); err != nil {
			t.Fatal(err)
		}
		path := ConfigPath("claude-code", home)
		before, _ := os.ReadFile(path)
		undone, err := svc.Undo("claude-code", "", baseName(res.BackupPath))
		if err != nil || undone.Action != "conflict" {
			t.Errorf("%s: want conflict, got %v %+v", name, err, undone)
		}
		if after, _ := os.ReadFile(path); string(after) != string(before) {
			t.Errorf("%s: the file must be left alone", name)
		}
	}
	// an unreadable record (a directory in its place)
	svc, home, res := good(t)
	rp := recordFor(svc, home, res)
	_ = os.Remove(rp)
	_ = os.Mkdir(rp, 0o700)
	if undone, err := svc.Undo("claude-code", "", baseName(res.BackupPath)); err != nil || undone.Action != "conflict" {
		t.Errorf("unreadable: %v %+v", err, undone)
	}
}

// The record sits beside the backup with the config's permissions, and a
// record name can never be passed off as a backup.
func TestUndoRecord_PlacementAndPermissions(t *testing.T) {
	svc, home := testService(t)
	seedClientConfig(t, home, "claude-code")
	_ = os.Chmod(ConfigPath("claude-code", home), 0o640)
	res, _ := svc.ConnectWithOptions("claude-code", "", ConnectOptions{})
	rp := recordFor(svc, home, res)
	if filepath.Dir(rp) != filepath.Dir(res.BackupPath) {
		t.Fatalf("record %s not beside backup %s", rp, res.BackupPath)
	}
	info, err := os.Stat(rp)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("record perms: %v %v", err, info)
	}
	if _, err := svc.Undo("claude-code", "", baseName(rp)); err == nil {
		t.Fatal("a record name must not be accepted as a backup name")
	}
}
