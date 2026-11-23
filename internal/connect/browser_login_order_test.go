//go:build !server

package connect

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const brokenConfig = `{"mcpServers":"broken"}`

// reconnectFixture: an oauth connect that created the config, so exactly one
// created-file record exists.
func reconnectFixture(t *testing.T) (svc *Service, home, cfg, good string, recs map[string]string) {
	t.Helper()
	svc, home = testService(t)
	oauthOn(svc)
	withFakeMinter(svc)
	cfg = ConfigPath("claude-code", home)
	if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); err != nil {
		t.Fatal(err)
	}
	good = readConfig(t, home, "claude-code")
	recs = map[string]string{}
	for _, name := range recordFilesIn(t, cfg) {
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(cfg), name))
		if err != nil {
			t.Fatal(err)
		}
		recs[name] = string(raw)
	}
	if len(recs) != 1 {
		t.Fatalf("fixture: one record, got %v", recs)
	}
	return
}

// afterFailedReconnect asserts the failed connect changed nothing: the old
// record is byte-identical, no temp file is left, and Undo still accepts the
// untouched file (restored first if the injection broke it).
func afterFailedReconnect(t *testing.T, label string, err error, svc *Service, home, cfg, good string, recs map[string]string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: the connect must fail", label)
	}
	after := recordFilesIn(t, cfg)
	if len(after) != len(recs) {
		t.Fatalf("%s: records changed: %v", label, after)
	}
	for name, want := range recs {
		got, rerr := os.ReadFile(filepath.Join(filepath.Dir(cfg), name))
		if rerr != nil || string(got) != want {
			t.Fatalf("%s: the old record %s must be untouched: %v", label, name, rerr)
		}
	}
	for _, n := range dirNames(t, filepath.Dir(cfg)) {
		if strings.Contains(n, "undo-tmp") || strings.HasSuffix(n, ".tmp") {
			t.Fatalf("%s: stray temp file %s", label, n)
		}
	}
	if err := os.WriteFile(cfg, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	if undone, uerr := svc.Undo("claude-code", "", ""); uerr != nil || undone.Action != "deleted" {
		t.Fatalf("%s: Undo of the untouched file: %v %+v", label, uerr, undone)
	}
}

func TestFailedOAuthReconnect_RecordWriteFailureKeepsTheOldRecord(t *testing.T) {
	svc, home, cfg, good, recs := reconnectFixture(t)
	recordWrite = func(f *os.File, b []byte) error {
		_, _ = f.Write(b[:len(b)/2])
		return errors.New("no space left on device")
	}
	_, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth, Force: true})
	recordWrite = defaultRecordWrite
	afterFailedReconnect(t, "record write", err, svc, home, cfg, good, recs)
}

func TestFailedOAuthReconnect_ConfigWriteFailureKeepsTheOldRecord(t *testing.T) {
	svc, home, cfg, good, recs := reconnectFixture(t)
	// the record stages fine, then the config's servers section breaks, which
	// the config write's last check refuses
	recordWrite = func(f *os.File, b []byte) error {
		_, err := f.Write(b)
		_ = os.WriteFile(cfg, []byte(brokenConfig), 0o600)
		return err
	}
	_, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth, Force: true})
	recordWrite = defaultRecordWrite
	afterFailedReconnect(t, "config write", err, svc, home, cfg, good, recs)
}

func TestFailedKeyReconnect_LeavesTheOAuthRecordUntouched(t *testing.T) {
	svc, home, cfg, good, recs := reconnectFixture(t)
	prev := backupNow
	broken := false
	backupNow = func() time.Time { // runs while the backup is taken, before the config write
		if !broken {
			broken = true
			_ = os.WriteFile(cfg, []byte(brokenConfig), 0o600)
		}
		return prev()
	}
	_, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Force: true})
	backupNow = prev
	afterFailedReconnect(t, "key reconnect", err, svc, home, cfg, good, recs)
}

// An older record that survives (its retirement failed) can never authorise a
// file the newer connect superseded.
func TestUndo_OnlyTheNewestCreatedFileRecordCounts(t *testing.T) {
	svc, home, cfg, _, _ := reconnectFixture(t)
	x := readConfig(t, home, "claude-code")
	oldName := recordFilesIn(t, cfg)[0]
	oldPath := filepath.Join(filepath.Dir(cfg), oldName)
	oldRaw, _ := os.ReadFile(oldPath)
	_ = os.Remove(cfg)

	svc.listenAddr = "127.0.0.1:9999"
	if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); err != nil {
		t.Fatal(err)
	}
	// the older record survives as if its retirement had failed
	if err := os.WriteFile(oldPath, oldRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	var rec undoRecord
	_ = json.Unmarshal(oldRaw, &rec)
	if rec.Created == 0 {
		t.Fatal("records must carry their creation time")
	}
	if err := os.WriteFile(cfg, []byte(x), 0o600); err != nil {
		t.Fatal(err)
	}
	undone, err := svc.Undo("claude-code", "", "")
	if err != nil || undone.Action != "conflict" {
		t.Fatalf("a superseded record must not authorise: %v %+v", err, undone)
	}
}
