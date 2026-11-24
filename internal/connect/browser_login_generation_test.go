//go:build !server

package connect

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func statePathOf(home string) string { return ConfigPath("claude-code", home) + ".undo.state" }

func failCreatedRecordRemoval(t *testing.T) {
	t.Helper()
	prev := removeRecordFile
	removeRecordFile = func(p string) error {
		if strings.Contains(filepath.Base(p), ".undo.new.") {
			return errors.New("operation not permitted")
		}
		return prev(p)
	}
	t.Cleanup(func() { removeRecordFile = prev })
}

// An older created-file record that outlives its retirement must never
// authorise an Undo once a later write has replaced the file.
func TestStaleCreatedFileRecord_AfterABrowserLoginReplaceWithABackup(t *testing.T) {
	svc, home := testService(t)
	oauthOn(svc)
	withFakeMinter(svc)
	path := ConfigPath("claude-code", home)

	if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); err != nil {
		t.Fatal(err)
	}
	x := readConfig(t, home, "claude-code")

	svc.listenAddr = "127.0.0.1:9999"
	failCreatedRecordRemoval(t)
	res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth, Force: true})
	if err != nil || !res.Success || res.BackupPath == "" {
		t.Fatalf("force connect: %v %+v", err, res)
	}
	if res.CleanupError == "" {
		t.Fatal("a failed retirement is reported as cleanup_error")
	}
	if err := os.WriteFile(path, []byte(x), 0o600); err != nil {
		t.Fatal(err)
	}
	undone, err := svc.Undo("claude-code", "", "")
	if err != nil || undone.Action != "conflict" {
		t.Fatalf("the stale record must not authorise: %v %+v", err, undone)
	}
	if after, _ := os.ReadFile(path); string(after) != x {
		t.Fatal("the file must be left alone")
	}
}

func TestStaleCreatedFileRecord_AfterAnAPIKeyConnect(t *testing.T) {
	svc, home := testService(t)
	oauthOn(svc)
	withFakeMinter(svc)
	path := ConfigPath("claude-code", home)

	if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); err != nil {
		t.Fatal(err)
	}
	x := readConfig(t, home, "claude-code")
	failCreatedRecordRemoval(t)
	res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Force: true})
	if err != nil || !res.Success {
		t.Fatalf("key connect: %v %+v", err, res)
	}
	if err := os.WriteFile(path, []byte(x), 0o600); err != nil {
		t.Fatal(err)
	}
	undone, err := svc.Undo("claude-code", "", "")
	if err != nil || undone.Action != "conflict" {
		t.Fatalf("the stale record must not authorise: %v %+v", err, undone)
	}
}

// With no time ordering at all, which created-file record counts is decided by
// the generation the state file names.
func TestCreatedFileRecords_AreOrderedByGenerationNotTime(t *testing.T) {
	svc, home := testService(t)
	oauthOn(svc)
	withFakeMinter(svc)
	path := ConfigPath("claude-code", home)
	if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); err != nil {
		t.Fatal(err)
	}
	cur := readConfig(t, home, "claude-code")
	recName := recordFilesIn(t, path)[0]
	recPath := filepath.Join(filepath.Dir(path), recName)
	raw, _ := os.ReadFile(recPath)
	var rec map[string]interface{}
	_ = json.Unmarshal(raw, &rec)
	stateRaw, err := os.ReadFile(statePathOf(home))
	if err != nil {
		t.Fatalf("a browser-login connect writes the per-config state: %v", err)
	}
	var st map[string]interface{}
	_ = json.Unmarshal(stateRaw, &st)
	if rec["gen"] == nil || rec["gen"] != st["current"] || st["pending"] != false {
		t.Fatalf("the record carries the current generation: rec=%v state=%v", rec, st)
	}

	// an older record (same digest as an older file, other generation, equal "time")
	old := map[string]interface{}{}
	for k, v := range rec {
		old[k] = v
	}
	old["gen"] = "00000000000000000000000000000001"
	old["sha256"] = fileDigest([]byte("OLDER"))
	oldRaw, _ := json.Marshal(old)
	if err := os.WriteFile(recPath+"old", oldRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("OLDER"), 0o600); err != nil {
		t.Fatal(err)
	}
	if undone, _ := svc.Undo("claude-code", "", ""); undone.Action != "conflict" {
		t.Fatalf("the older generation must not authorise: %+v", undone)
	}
	if err := os.WriteFile(path, []byte(cur), 0o600); err != nil {
		t.Fatal(err)
	}
	if undone, err := svc.Undo("claude-code", "", ""); err != nil || undone.Action != "deleted" {
		t.Fatalf("the current generation's record authorises: %v %+v", err, undone)
	}
}

// A state left pending (a crash between the write and its commit) fails closed.
func TestCreatedFileRecord_PendingStateIsAConflict(t *testing.T) {
	svc, home := testService(t)
	oauthOn(svc)
	withFakeMinter(svc)
	if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); err != nil {
		t.Fatal(err)
	}
	stateRaw, err := os.ReadFile(statePathOf(home))
	if err != nil {
		t.Fatal(err)
	}
	pending := strings.Replace(string(stateRaw), `"pending":false`, `"pending":true`, 1)
	if pending == string(stateRaw) {
		t.Fatalf("fixture: state %s", stateRaw)
	}
	for name, content := range map[string]string{"pending": pending, "malformed": "{nope", "empty": ""} {
		if err := os.WriteFile(statePathOf(home), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if undone, _ := svc.Undo("claude-code", "", ""); undone.Action != "conflict" {
			t.Fatalf("%s state: want conflict, got %+v", name, undone)
		}
	}
	_ = os.Remove(statePathOf(home))
	if undone, _ := svc.Undo("claude-code", "", ""); undone.Action != "conflict" {
		t.Fatalf("missing state with a record: want conflict, got %+v", undone)
	}
}

// A never-used install gets no state file from a key connect.
func TestKeyConnect_WritesNoStateFileOnAnInstallThatNeverUsedBrowserLogin(t *testing.T) {
	for _, prior := range []bool{true, false} {
		svc, home := testService(t)
		oauthOn(svc)
		withFakeMinter(svc)
		if prior {
			seedClientConfig(t, home, "claude-code")
		}
		if res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{}); err != nil || !res.Success {
			t.Fatal(err)
		}
		if _, err := os.Stat(statePathOf(home)); err == nil {
			t.Fatalf("prior=%v: a key connect must not create the state file", prior)
		}
		if _, err := svc.Disconnect("claude-code", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(statePathOf(home)); err == nil {
			t.Fatalf("prior=%v: nor may a disconnect", prior)
		}
	}
}

// The state follows the records: Undo or Disconnect that removes the last one
// removes it; one that leaves a record keeps it.
func TestState_IsRemovedWithTheLastRecord(t *testing.T) {
	for _, how := range []string{"undo", "disconnect"} {
		svc, home := testService(t)
		oauthOn(svc)
		withFakeMinter(svc)
		if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(statePathOf(home)); err != nil {
			t.Fatal(err)
		}
		if how == "undo" {
			if undone, err := svc.Undo("claude-code", "", ""); err != nil || undone.Action != "deleted" {
				t.Fatalf("%v %+v", err, undone)
			}
		} else if _, err := svc.Disconnect("claude-code", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(statePathOf(home)); err == nil {
			t.Fatalf("%s: the state file must go with the last record", how)
		}
	}

	// another entry's record remains: the state stays, in a new generation
	svc, home := testService(t)
	oauthOn(svc)
	withFakeMinter(svc)
	if _, err := svc.ConnectWithOptions("claude-code", "one", ConnectOptions{Auth: AuthOAuth}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(statePathOf(home))
	_ = os.Remove(ConfigPath("claude-code", home))
	if _, err := svc.ConnectWithOptions("claude-code", "two", ConnectOptions{Auth: AuthOAuth}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Disconnect("claude-code", "two"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(statePathOf(home))
	if err != nil || string(after) == string(before) {
		t.Fatalf("the state must stay (the other entry's record remains) in a new generation: %v", err)
	}
}

// A post-write verification failure keeps what the connect learned.
func TestVerifyFailure_CarriesTheRecordNoteAndTheBackupPath(t *testing.T) {
	svc, home := testService(t)
	oauthOn(svc)
	withFakeMinter(svc)
	seedClientConfig(t, home, "claude-code")
	cfg := ConfigPath("claude-code", home)
	if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); err != nil {
		t.Fatal(err)
	}
	// a later forced connect: its retirement fails and its verify read fails
	failCreatedRecordRemoval(t)
	svc.listenAddr = "127.0.0.1:9999"
	svc.setReadFile(func(p string) ([]byte, error) {
		raw, err := os.ReadFile(p)
		if p == cfg && strings.Contains(string(raw), ":9999") { // only the re-read after the write
			return nil, errors.New("transient read error")
		}
		return raw, err
	})
	_, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth, Force: true})
	if err == nil {
		t.Fatal("a verification failure still fails the connect")
	}
	if !strings.Contains(err.Error(), "verification failed") || !strings.Contains(err.Error(), ".bak.") {
		t.Fatalf("the error must carry the backup path: %v", err)
	}
}
