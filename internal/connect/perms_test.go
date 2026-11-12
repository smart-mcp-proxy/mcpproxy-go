package connect

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The wizard-created config embeds a minted credential: a NEW file must be
// 0600 and a NEW parent directory 0700; an existing file keeps its mode.
func TestConnect_NewConfigAndDirArePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not meaningful on Windows")
	}
	svc, home := testService(t)
	cfgPath := ConfigPath("cursor", home)
	if _, err := os.Stat(filepath.Dir(cfgPath)); err == nil {
		t.Fatalf("precondition: parent dir %s should not exist", filepath.Dir(cfgPath))
	}
	if _, err := svc.Connect("cursor", "mcpproxy", false); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("new config perm = %o, want 600", got)
	}
	di, err := os.Stat(filepath.Dir(cfgPath))
	if err != nil {
		t.Fatal(err)
	}
	if got := di.Mode().Perm(); got != 0o700 {
		t.Fatalf("new parent dir perm = %o, want 700", got)
	}
}

func TestConnect_ExistingConfigKeepsMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not meaningful on Windows")
	}
	svc, home := testService(t)
	cfgPath := ConfigPath("cursor", home)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfgPath, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Connect("cursor", "mcpproxy", false)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o644 {
		t.Fatalf("existing config perm = %o, want 644 preserved", got)
	}
	di, _ := os.Stat(filepath.Dir(cfgPath))
	if got := di.Mode().Perm(); got != 0o755 {
		t.Fatalf("existing dir perm = %o, want 755 preserved", got)
	}
	if res.BackupPath != "" {
		bi, err := os.Stat(res.BackupPath)
		if err != nil {
			t.Fatal(err)
		}
		if bi.Mode().Perm()&^0o644 != 0 {
			t.Fatalf("backup perm = %o, more permissive than original", bi.Mode().Perm())
		}
	}
}
