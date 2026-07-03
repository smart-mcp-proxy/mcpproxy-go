package connect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Spec 078 US3: one-click undo of a connect. Undo restores the client config
// byte-for-byte from the backup the immediately-preceding connect took, after
// verifying the file has not drifted since (FR-008); when connect created the
// file (no prior file), undo removes it. Every mutation takes its own safety
// backup first.

// readFileT is a test helper that fails the test on read error.
func readFileT(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func TestUndo_RestoresByteIdenticalPreConnectFile(t *testing.T) {
	home := t.TempDir()
	cfgPath := ConfigPath("claude-code", home)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-existing file with a user-owned mcpproxy entry — the overwrite case
	// (FR-008): disconnect could NOT bring this entry back, undo must.
	original := []byte("{\n  \"mcpServers\": {\n    \"mcpproxy\": {\n      \"type\": \"http\",\n      \"url\": \"http://users-own-server/mcp\"\n    },\n    \"other\": {\n      \"url\": \"http://x\"\n    }\n  }\n}\n")
	if err := os.WriteFile(cfgPath, original, 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewServiceWithHome("127.0.0.1:8080", "", home)
	res, err := svc.Connect("claude-code", "mcpproxy", true) // force overwrite
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if !res.Success || res.BackupPath == "" {
		t.Fatalf("connect result unexpected: %+v", res)
	}

	undo, err := svc.Undo("claude-code", "mcpproxy", res.BackupPath)
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if !undo.Success {
		t.Fatalf("undo failed: %+v", undo)
	}
	if undo.Action != "restored" {
		t.Fatalf("action = %q, want restored", undo.Action)
	}
	if undo.BackupPath == "" {
		t.Fatal("undo must take its own safety backup")
	}
	if _, err := os.Stat(undo.BackupPath); err != nil {
		t.Fatalf("safety backup missing: %v", err)
	}

	after := readFileT(t, cfgPath)
	if string(after) != string(original) {
		t.Fatalf("file not byte-identical to pre-connect state:\n got: %q\nwant: %q", after, original)
	}
}

func TestUndo_TOML_RestoresByteIdentical(t *testing.T) {
	home := t.TempDir()
	cfgPath := ConfigPath("codex", home)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("model = \"o4\"\n\n[mcp_servers.other]\nurl = \"http://x\"\n")
	if err := os.WriteFile(cfgPath, original, 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewServiceWithHome("127.0.0.1:8080", "", home)
	res, err := svc.Connect("codex", "mcpproxy", false)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if !res.Success || res.BackupPath == "" {
		t.Fatalf("connect result unexpected: %+v", res)
	}

	undo, err := svc.Undo("codex", "mcpproxy", res.BackupPath)
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if !undo.Success || undo.Action != "restored" {
		t.Fatalf("undo result unexpected: %+v", undo)
	}
	after := readFileT(t, cfgPath)
	if string(after) != string(original) {
		t.Fatalf("TOML file not byte-identical:\n got: %q\nwant: %q", after, original)
	}
}

func TestUndo_RefusesWhenFileDriftedSinceConnect(t *testing.T) {
	home := t.TempDir()
	cfgPath := ConfigPath("claude-code", home)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{"mcpServers":{"other":{"url":"http://x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewServiceWithHome("127.0.0.1:8080", "", home)
	res, err := svc.Connect("claude-code", "mcpproxy", false)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	// The user (or another tool) edits the file after the connect.
	drifted := []byte(`{"mcpServers":{"other":{"url":"http://x"},"mcpproxy":{"type":"http","url":"http://127.0.0.1:8080/mcp"},"added-later":{"url":"http://y"}}}`)
	if err := os.WriteFile(cfgPath, drifted, 0o644); err != nil {
		t.Fatal(err)
	}

	undo, err := svc.Undo("claude-code", "mcpproxy", res.BackupPath)
	if err != nil {
		t.Fatalf("undo returned hard error, want refusal result: %v", err)
	}
	if undo.Success {
		t.Fatalf("undo must refuse on drift: %+v", undo)
	}
	if undo.Action != "conflict" {
		t.Fatalf("action = %q, want conflict", undo.Action)
	}
	// The drifted file must be left untouched (no clobber).
	after := readFileT(t, cfgPath)
	if string(after) != string(drifted) {
		t.Fatalf("drifted file was modified by a refused undo")
	}
}

func TestUndo_NoPriorFile_RemovesCreatedFile(t *testing.T) {
	home := t.TempDir()
	cfgPath := ConfigPath("claude-code", home)

	svc := NewServiceWithHome("127.0.0.1:8080", "", home)
	res, err := svc.Connect("claude-code", "mcpproxy", false)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if res.BackupPath != "" {
		t.Fatalf("expected no backup for a created file, got %q", res.BackupPath)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("connect should have created %s: %v", cfgPath, err)
	}

	undo, err := svc.Undo("claude-code", "mcpproxy", "")
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if !undo.Success {
		t.Fatalf("undo failed: %+v", undo)
	}
	if undo.Action != "deleted" {
		t.Fatalf("action = %q, want deleted", undo.Action)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatalf("config file should be gone (pre-connect state), stat err = %v", err)
	}
	// Even the delete takes a safety backup so the user can recover.
	if undo.BackupPath == "" {
		t.Fatal("undo delete must take a safety backup")
	}
	if _, err := os.Stat(undo.BackupPath); err != nil {
		t.Fatalf("safety backup missing: %v", err)
	}
}

func TestUndo_NoPriorFile_RefusesWhenFileDrifted(t *testing.T) {
	home := t.TempDir()
	cfgPath := ConfigPath("claude-code", home)

	svc := NewServiceWithHome("127.0.0.1:8080", "", home)
	if _, err := svc.Connect("claude-code", "mcpproxy", false); err != nil {
		t.Fatalf("connect: %v", err)
	}
	// User adds their own server to the file mcpproxy created: deleting the
	// whole file would now lose their work — undo must refuse.
	drifted := []byte(`{"mcpServers":{"mcpproxy":{"type":"http","url":"http://127.0.0.1:8080/mcp"},"mine":{"url":"http://y"}}}`)
	if err := os.WriteFile(cfgPath, drifted, 0o644); err != nil {
		t.Fatal(err)
	}

	undo, err := svc.Undo("claude-code", "mcpproxy", "")
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if undo.Success || undo.Action != "conflict" {
		t.Fatalf("undo must refuse: %+v", undo)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("refused undo must not delete the file: %v", err)
	}
}

func TestUndo_BackupMissingReturnsNotFound(t *testing.T) {
	home := t.TempDir()
	svc := NewServiceWithHome("127.0.0.1:8080", "", home)
	res, err := svc.Connect("claude-code", "mcpproxy", false)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	cfgPath := res.ConfigPath
	missing := cfgPath + ".bak.19990101-000000"

	undo, err := svc.Undo("claude-code", "mcpproxy", missing)
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if undo.Success || undo.Action != "not_found" {
		t.Fatalf("undo with a missing backup must report not_found: %+v", undo)
	}
}

func TestUndo_RejectsForeignBackupPath(t *testing.T) {
	home := t.TempDir()
	svc := NewServiceWithHome("127.0.0.1:8080", "", home)
	if _, err := svc.Connect("claude-code", "mcpproxy", false); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// A path that is not a backup of THIS client's config must be rejected —
	// the endpoint must not become an arbitrary-file-restore primitive.
	foreign := filepath.Join(home, "evil.json")
	if err := os.WriteFile(foreign, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Undo("claude-code", "mcpproxy", foreign)
	if err == nil {
		t.Fatal("undo must reject a backup path outside <config>.bak.*")
	}
	if !strings.Contains(err.Error(), "backup") {
		t.Fatalf("error should mention the backup path problem: %v", err)
	}
}

// A path that keeps the "<config>.bak." prefix but traverses out of the config
// directory (e.g. "<config>.bak.x/../../secret.json") must be rejected before
// any read — a prefix-only check would let undo read/restore an arbitrary file.
func TestUndo_RejectsBackupPathTraversal(t *testing.T) {
	home := t.TempDir()
	svc := NewServiceWithHome("127.0.0.1:8080", "", home)
	res, err := svc.Connect("claude-code", "mcpproxy", false)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Craft a traversal path that still starts with "<config>.bak." so a
	// prefix-only guard would admit it, then climbs back out to a foreign file.
	secret := filepath.Join(home, "secret.json")
	if err := os.WriteFile(secret, []byte(`{"secret":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(filepath.Dir(res.ConfigPath), secret)
	if err != nil {
		t.Fatal(err)
	}
	traversal := res.ConfigPath + ".bak.x/../" + rel

	_, err = svc.Undo("claude-code", "mcpproxy", traversal)
	if err == nil {
		t.Fatal("undo must reject a traversal backup path that escapes the config directory")
	}
	if !strings.Contains(err.Error(), "backup") {
		t.Fatalf("error should mention the backup path problem: %v", err)
	}
}

func TestUndo_MissingConfigFileRefuses(t *testing.T) {
	home := t.TempDir()
	svc := NewServiceWithHome("127.0.0.1:8080", "", home)
	res, err := svc.Connect("claude-code", "mcpproxy", false)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := os.Remove(res.ConfigPath); err != nil {
		t.Fatal(err)
	}

	undo, err := svc.Undo("claude-code", "mcpproxy", res.BackupPath)
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if undo.Success || undo.Action != "conflict" {
		t.Fatalf("undo on a since-deleted config must refuse: %+v", undo)
	}
}

// Spec 078 undo reliability: two backups of the same file within the same
// second must yield two distinct backup files (previously the second overwrote
// the first, which could destroy the very backup an undo needs).
func TestBackupFile_SameSecondCollisionYieldsDistinctFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"v":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pin the clock so both backups land in the SAME second deterministically.
	fixed := time.Date(2026, 7, 2, 10, 15, 30, 0, time.UTC)
	orig := backupNow
	backupNow = func() time.Time { return fixed }
	t.Cleanup(func() { backupNow = orig })

	first, err := backupFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Mutate the source so an overwrite would be detectable by content.
	if err := os.WriteFile(path, []byte(`{"v":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := backupFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if first == second {
		t.Fatalf("same-second backups collided: %s", first)
	}
	// Both must exist with their original contents.
	if got := string(readFileT(t, first)); got != `{"v":1}` {
		t.Fatalf("first backup content = %q", got)
	}
	if got := string(readFileT(t, second)); got != `{"v":2}` {
		t.Fatalf("second backup content = %q", got)
	}
	// The collision suffix must keep the timestamped name as a prefix so old
	// backups still sort/glob together.
	base := filepath.Base(path)
	for _, b := range []string{first, second} {
		if !strings.HasPrefix(filepath.Base(b), base+".bak.") {
			t.Fatalf("backup %q must keep the <config>.bak.<ts> shape", b)
		}
	}
}
