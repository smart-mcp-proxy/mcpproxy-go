package connect

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// serviceWithKey builds a Service with a specific API key and isolated home,
// pinning the Windows env vars the same way testService does.
func serviceWithKey(t *testing.T, key string) (*Service, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	return NewServiceWithHome("127.0.0.1:8080", key, home), home
}

// seedClientConfig writes a minimal, valid, empty config for the client so both
// JSON and TOML Connect paths update an existing file uniformly (and opencode,
// which refuses to create its own file, has one to update).
func seedClientConfig(t *testing.T, home, clientID string) {
	t.Helper()
	cfgPath := ConfigPath(clientID, home)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("{}\n")
	if FindClient(clientID).Format == "toml" {
		content = []byte("")
	}
	if err := os.WriteFile(cfgPath, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

// supportedPreviewClients lists every supported client id so the preview==write
// equivalence is asserted across both JSON and the TOML client (Spec 078 SC-004,
// FR-013). opencode requires a pre-existing config file (Connect refuses to
// create one) and is exercised with a seeded file in the loop.
var supportedPreviewClients = []string{
	"claude-code", "claude-desktop", "cursor", "windsurf",
	"vscode", "codex", "gemini", "opencode",
}

// marshalEntry renders a config entry to canonical JSON (sorted keys) so a
// []string arg slice from buildServerEntry and the []interface{} that survives a
// JSON round-trip compare equal by content rather than Go type.
func marshalEntry(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	return string(b)
}

// readWrittenEntry reads back the entry Connect wrote for serverName under the
// client's server key, for both JSON and TOML formats.
func readWrittenEntry(t *testing.T, svc *Service, clientID, serverName string) map[string]interface{} {
	t.Helper()
	client := FindClient(clientID)
	cfgPath := ConfigPath(clientID, svc.homeDir)
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read written config %s: %v", cfgPath, err)
	}
	var data map[string]interface{}
	if client.Format == "toml" {
		if _, derr := toml.Decode(string(raw), &data); derr != nil {
			t.Fatalf("decode written TOML: %v", derr)
		}
	} else if uerr := json.Unmarshal(raw, &data); uerr != nil {
		t.Fatalf("decode written JSON: %v", uerr)
	}
	servers, ok := data[client.ServerKey].(map[string]interface{})
	if !ok {
		t.Fatalf("no %q section in written %s config", client.ServerKey, clientID)
	}
	entry, ok := servers[serverName].(map[string]interface{})
	if !ok {
		t.Fatalf("no %q entry in written %s config", serverName, clientID)
	}
	return entry
}

// TestPreview_EqualsWrite pins the core US1 guarantee: for every supported
// client, the entry the preview is derived from is byte-for-byte the entry a
// subsequent Connect writes — same key, same shape (Spec 078 FR-002, SC-004).
// It compares against the SHARED constructor with the real (unmasked) URL, then
// separately confirms the preview payload masks that URL.
func TestPreview_EqualsWrite(t *testing.T) {
	for _, clientID := range supportedPreviewClients {
		clientID := clientID
		t.Run(clientID, func(t *testing.T) {
			svc, home := testServiceWithKey(t)
			seedClientConfig(t, home, clientID)

			preview, err := svc.Preview(clientID, "mcpproxy")
			if err != nil {
				t.Fatalf("Preview: %v", err)
			}

			res, err := svc.Connect(clientID, "mcpproxy", true)
			if err != nil {
				t.Fatalf("Connect: %v", err)
			}
			if !res.Success {
				t.Fatalf("Connect not successful: %+v", res)
			}

			written := readWrittenEntry(t, svc, clientID, "mcpproxy")

			// The write uses buildServerEntry(clientID, realURL); the shared
			// constructor is the single source of truth for preview and write.
			wantUnmasked := buildServerEntry(clientID, svc.mcpURL())
			if got, want := marshalEntry(t, written), marshalEntry(t, wantUnmasked); got != want {
				t.Fatalf("written entry != shared constructor output\n got: %s\nwant: %s", got, want)
			}

			// The preview payload masks the credential: its entry equals the
			// shared constructor fed the masked URL, and never the real key.
			wantMasked := buildServerEntry(clientID, maskURLAPIKey(svc.mcpURL()))
			if got, want := marshalEntry(t, preview.Entry), marshalEntry(t, wantMasked); got != want {
				t.Fatalf("preview entry != masked constructor output\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

// TestPreview_MasksAPIKey verifies the real API key never appears anywhere in
// the preview payload while ContainsAPIKey honestly flags that a credential is
// written, and the base URL stays visible (Spec 078 FR-004).
func TestPreview_MasksAPIKey(t *testing.T) {
	const secret = "super-secret-key-1234"
	svc, home := serviceWithKey(t, secret)
	seedClientConfig(t, home, "claude-code")

	preview, err := svc.Preview("claude-code", "mcpproxy")
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}

	payload, err := json.Marshal(preview)
	if err != nil {
		t.Fatalf("marshal preview: %v", err)
	}
	if strings.Contains(string(payload), secret) {
		t.Fatalf("real API key leaked into preview payload: %s", payload)
	}
	if !preview.ContainsAPIKey {
		t.Fatal("ContainsAPIKey must be true when the URL embeds a credential")
	}
	if !strings.Contains(preview.EntryText, apiKeyMask) {
		t.Fatalf("EntryText should show the mask, got: %s", preview.EntryText)
	}
	if !strings.Contains(preview.EntryText, "http://127.0.0.1:8080/mcp") {
		t.Fatalf("EntryText should keep the base URL visible, got: %s", preview.EntryText)
	}
}

// TestPreview_NoAPIKey: with no key configured, ContainsAPIKey is false and no
// apikey param is rendered.
func TestPreview_NoAPIKey(t *testing.T) {
	svc, home := testService(t) // no key
	seedClientConfig(t, home, "cursor")

	preview, err := svc.Preview("cursor", "mcpproxy")
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if preview.ContainsAPIKey {
		t.Fatal("ContainsAPIKey must be false when no key is configured")
	}
	if strings.Contains(preview.EntryText, "apikey") {
		t.Fatalf("no apikey should be present, got: %s", preview.EntryText)
	}
}

// TestPreview_EntryExists distinguishes a create from an overwrite of an
// existing same-named entry (Spec 078 FR-003).
func TestPreview_EntryExists(t *testing.T) {
	svc, home := testService(t)
	cfgPath := ConfigPath("claude-code", home)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// Config with a user-created "mcpproxy" entry — the overwrite case.
	if err := os.WriteFile(cfgPath, []byte(`{"mcpServers":{"mcpproxy":{"type":"http","url":"http://old"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	preview, err := svc.Preview("claude-code", "mcpproxy")
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if !preview.EntryExists {
		t.Fatal("expected EntryExists=true for a pre-existing same-named entry")
	}
	if preview.AccessState != accessAccessible {
		t.Fatalf("expected accessible, got %s", preview.AccessState)
	}

	// A fresh config without the entry is a create.
	if err := os.WriteFile(cfgPath, []byte(`{"mcpServers":{"other":{"url":"http://x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	preview2, err := svc.Preview("claude-code", "mcpproxy")
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if preview2.EntryExists {
		t.Fatal("expected EntryExists=false when no same-named entry present")
	}
}

// TestPreview_NoSideEffects: a preview must not modify the config nor create a
// backup (Spec 078 FR-001, US1 independent test).
func TestPreview_NoSideEffects(t *testing.T) {
	svc, home := testService(t)
	cfgPath := ConfigPath("claude-code", home)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"mcpServers":{"other":{"url":"http://x"}}}`)
	if err := os.WriteFile(cfgPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Preview("claude-code", "mcpproxy"); err != nil {
		t.Fatalf("Preview: %v", err)
	}

	after, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("preview must not modify the config file (mtime changed)")
	}
	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("preview altered config contents:\n%s", got)
	}
	// No backup file must have been created.
	entries, err := os.ReadDir(filepath.Dir(cfgPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak.") {
			t.Fatalf("preview created a backup file: %s", e.Name())
		}
	}
}

// TestPreview_AbsentConfig: previewing a client with no config file is a create
// with access_state=absent and no error (bridge and non-bridge alike).
func TestPreview_AbsentConfig(t *testing.T) {
	svc, _ := testService(t)
	preview, err := svc.Preview("claude-desktop", "mcpproxy")
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if preview.EntryExists {
		t.Fatal("absent config cannot have an existing entry")
	}
	if preview.AccessState != accessAbsent {
		t.Fatalf("expected absent, got %s", preview.AccessState)
	}
	if !preview.Bridge {
		t.Fatal("claude-desktop is a bridge client")
	}
}

// TestPreview_Malformed: an unparseable config degrades to access_state=malformed
// rather than a misleading "create" (Spec 078 FR-012).
func TestPreview_Malformed(t *testing.T) {
	svc, home := testService(t)
	cfgPath := ConfigPath("claude-code", home)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{not valid json at all`), 0o644); err != nil {
		t.Fatal(err)
	}
	preview, err := svc.Preview("claude-code", "mcpproxy")
	if err != nil {
		t.Fatalf("Preview should not hard-error on malformed config: %v", err)
	}
	if preview.AccessState != accessMalformed {
		t.Fatalf("expected malformed, got %s", preview.AccessState)
	}
}

// TestPreview_Denied: a permission-denied config read surfaces the same typed
// AccessError as connect/disconnect (Spec 078 FR-012), so the REST layer maps
// it to 403 + remediation.
func TestPreview_Denied(t *testing.T) {
	home := t.TempDir()
	cfgPath := ConfigPath("claude-code", home)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := NewServiceWithReader("127.0.0.1:8080", "", home, func(string) ([]byte, error) {
		return nil, os.ErrPermission
	})
	_, err := svc.Preview("claude-code", "mcpproxy")
	if err == nil {
		t.Fatal("expected an AccessError for a denied read")
	}
	var accessErr *AccessError
	if !errors.As(err, &accessErr) {
		t.Fatalf("expected *AccessError, got %T: %v", err, err)
	}
}

// TestPreview_UnknownClient errors, matching Connect's contract.
func TestPreview_UnknownClient(t *testing.T) {
	svc, _ := testService(t)
	if _, err := svc.Preview("not-a-client", "mcpproxy"); err == nil {
		t.Fatal("expected error for unknown client")
	}
}
