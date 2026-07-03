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
	// Matrix: every supported client × require_mcp_auth on/off. The previewed
	// entry must equal the written entry's shape for the same configuration
	// (Spec 078 SC-004), with the credential masked in the preview only.
	for _, authOn := range []bool{false, true} {
		authOn := authOn
		for _, clientID := range supportedPreviewClients {
			clientID := clientID
			name := clientID
			if authOn {
				name += "/auth-on"
			} else {
				name += "/auth-off"
			}
			t.Run(name, func(t *testing.T) {
				svc, home := testServiceWithKey(t)
				svc.WithRequireMCPAuth(authOn)
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

				// The write and preview both call buildServerEntry — the single
				// source of truth. Unmasked params == written; masked == preview.
				wantUnmasked := buildServerEntry(clientID, svc.entryParams(false))
				if got, want := marshalEntry(t, written), marshalEntry(t, wantUnmasked); got != want {
					t.Fatalf("written entry != shared constructor output\n got: %s\nwant: %s", got, want)
				}
				wantMasked := buildServerEntry(clientID, svc.entryParams(true))
				if got, want := marshalEntry(t, preview.Entry), marshalEntry(t, wantMasked); got != want {
					t.Fatalf("preview entry != masked constructor output\n got: %s\nwant: %s", got, want)
				}

				// Spec 078 security fix: with auth off, the written config must
				// contain no credential at all.
				if !authOn {
					if strings.Contains(marshalEntry(t, written), "apikey") ||
						strings.Contains(marshalEntry(t, written), "test-key-123") ||
						strings.Contains(marshalEntry(t, written), "headers") {
						t.Fatalf("auth-off write must embed no credential, got: %s", marshalEntry(t, written))
					}
					if preview.ContainsAPIKey {
						t.Fatal("auth-off preview must report contains_api_key=false")
					}
				}
			})
		}
	}
}

// TestPreview_MasksCredential verifies the real API key never appears anywhere
// in the preview payload while ContainsAPIKey honestly flags that a credential
// is written, and the base URL stays visible (Spec 078 FR-004). Runs with
// require_mcp_auth on (the only mode that writes a credential).
func TestPreview_MasksCredential(t *testing.T) {
	const secret = "super-secret-key-1234"
	svc, home := serviceWithKey(t, secret)
	svc.WithRequireMCPAuth(true)
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
		t.Fatal("ContainsAPIKey must be true when auth is on with a key")
	}
	if !strings.Contains(preview.EntryText, apiKeyMask) {
		t.Fatalf("EntryText should show the mask, got: %s", preview.EntryText)
	}
	if !strings.Contains(preview.EntryText, "http://127.0.0.1:8080/mcp") {
		t.Fatalf("EntryText should keep the base URL visible, got: %s", preview.EntryText)
	}
	// claude-code carries the credential in a header, so the URL stays clean.
	if strings.Contains(preview.EntryText, "apikey=") {
		t.Fatalf("claude-code should use a header, not an apikey query: %s", preview.EntryText)
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

// TestPreview_LiveConfigProvider pins the Spec 078 security fix under a RUNTIME
// require_mcp_auth toggle: the /mcp middleware honors require_mcp_auth live, so
// the long-lived connect service must too. A stale startup snapshot would keep
// embedding the real API key after auth is turned off — the exact leak this fix
// closes. The provider flips the live value between preview/write calls.
func TestPreview_LiveConfigProvider(t *testing.T) {
	svc, home := serviceWithKey(t, "live-key-xyz")
	seedClientConfig(t, home, "claude-code")

	// Start snapshot as auth-on; the provider overrides it live.
	svc.WithRequireMCPAuth(true)
	requireAuth := false // provider's live value: auth OFF
	svc.WithConfigProvider(func() (string, string, bool) {
		return "127.0.0.1:8080", "live-key-xyz", requireAuth
	})

	// Auth OFF live: preview and write must embed NO credential, even though the
	// startup snapshot said auth-on.
	preview, err := svc.Preview("claude-code", "mcpproxy")
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if preview.ContainsAPIKey {
		t.Fatal("auth-off (live) preview must report contains_api_key=false despite auth-on snapshot")
	}
	if _, err := svc.Connect("claude-code", "mcpproxy", true); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	written := marshalEntry(t, readWrittenEntry(t, svc, "claude-code", "mcpproxy"))
	if strings.Contains(written, "live-key-xyz") || strings.Contains(written, "headers") {
		t.Fatalf("auth-off (live) write must embed no credential, got: %s", written)
	}

	// Flip live to auth ON: now the credential is embedded (as a header for
	// claude-code), without rebuilding the service.
	requireAuth = true
	preview2, err := svc.Preview("claude-code", "mcpproxy")
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if !preview2.ContainsAPIKey {
		t.Fatal("auth-on (live) preview must report contains_api_key=true")
	}
	if _, err := svc.Connect("claude-code", "mcpproxy", true); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	written2 := marshalEntry(t, readWrittenEntry(t, svc, "claude-code", "mcpproxy"))
	if !strings.Contains(written2, "live-key-xyz") {
		t.Fatalf("auth-on (live) write must embed the real credential, got: %s", written2)
	}
}

// TestPreview_OpenCode_EquivalentEntryExists pins preview==write for OpenCode's
// equivalent-entry adoption: an already-installed entry pointing at our MCP URL
// under a DIFFERENT key (here the legacy ?apikey= shape) is adopted/normalized
// by the write path, so preview must report entry_exists=true rather than a
// misleading "create" that diverges from what connect actually does (FR-002).
func TestPreview_OpenCode_EquivalentEntryExists(t *testing.T) {
	svc, home := testService(t)
	cfgPath := ConfigPath("opencode", home)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// Legacy differently-named entry carrying the old ?apikey= URL form.
	if err := os.WriteFile(cfgPath, []byte(`{
	  "mcp": {
	    "proxy-alt": {"type":"remote","url":"http://127.0.0.1:8080/mcp?apikey=old"}
	  }
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	preview, err := svc.Preview("opencode", "mcpproxy")
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if !preview.EntryExists {
		t.Fatal("expected EntryExists=true for an equivalent legacy ?apikey= entry under a different key")
	}
}

// TestConnect_CredentialCarrierMatrix pins the per-client credential carrier
// when require_mcp_auth is on (Spec 078): a header where the client config
// supports one, the mcp-remote --header bridge arg for Claude Desktop, and the
// ?apikey= query only for Codex (whose TOML headers are env-var indirected).
func TestConnect_CredentialCarrierMatrix(t *testing.T) {
	type carrier int
	const (
		header carrier = iota
		bridgeHeaderArg
		query
	)
	want := map[string]carrier{
		"claude-code":    header,
		"vscode":         header,
		"cursor":         header,
		"windsurf":       header,
		"gemini":         header,
		"opencode":       header,
		"claude-desktop": bridgeHeaderArg,
		"codex":          query,
	}

	for clientID, c := range want {
		clientID, c := clientID, c
		t.Run(clientID, func(t *testing.T) {
			svc, home := testServiceWithKey(t)
			svc.WithRequireMCPAuth(true)
			seedClientConfig(t, home, clientID)

			if _, err := svc.Connect(clientID, "mcpproxy", true); err != nil {
				t.Fatalf("Connect: %v", err)
			}
			entry := readWrittenEntry(t, svc, clientID, "mcpproxy")

			switch c {
			case header:
				h, ok := entry["headers"].(map[string]interface{})
				if !ok || h["X-API-Key"] != "test-key-123" {
					t.Fatalf("%s: expected X-API-Key header, got %v", clientID, entry)
				}
				// URL carrier must NOT also carry the key.
				for _, f := range []string{"url", "serverUrl", "httpUrl"} {
					if u, ok := entry[f].(string); ok && strings.Contains(u, "apikey") {
						t.Fatalf("%s: header client must keep URL clean, got %v", clientID, u)
					}
				}
			case bridgeHeaderArg:
				args := entry["args"].([]interface{})
				last, _ := args[len(args)-1].(string)
				if last != "X-API-Key:test-key-123" {
					t.Fatalf("%s: expected --header arg, got %v", clientID, args)
				}
			case query:
				u, _ := entry["url"].(string)
				if !strings.Contains(u, "apikey=test-key-123") {
					t.Fatalf("%s: expected apikey query, got %v", clientID, u)
				}
			}
		})
	}
}

// TestConnect_LegacyEntryMigration proves an upgrade over a legacy entry that
// carried ?apikey= is recognized and updated in place (not duplicated), and the
// upgraded entry drops the leaked key when require_mcp_auth is off (Spec 078).
func TestConnect_LegacyEntryMigration(t *testing.T) {
	svc, home := testService(t) // auth off
	cfgPath := ConfigPath("claude-code", home)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// A legacy mcpproxy entry written by a pre-Spec-078 build: url carries the key.
	legacy := `{"mcpServers":{"mcpproxy":{"type":"http","url":"http://127.0.0.1:8080/mcp?apikey=old-leaked-key"}}}`
	if err := os.WriteFile(cfgPath, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	// The legacy entry is recognized as connected (matching anchors on base URL).
	st, err := svc.GetStatus("claude-code")
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if !st.Connected {
		t.Fatal("legacy ?apikey= entry must be recognized as connected")
	}

	// Reconnect (force) upgrades it in place to the clean, keyless shape.
	if _, err := svc.Connect("claude-code", "mcpproxy", true); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	raw, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(raw), "old-leaked-key") || strings.Contains(string(raw), "apikey") {
		t.Fatalf("upgrade must drop the leaked key, got: %s", raw)
	}
	var data map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	servers := data["mcpServers"].(map[string]interface{})
	if len(servers) != 1 {
		t.Fatalf("expected a single mcpproxy entry (no duplicate), got %d: %v", len(servers), servers)
	}

	// Disconnect must still find and remove the (now clean) entry.
	res, err := svc.Disconnect("claude-code", "mcpproxy")
	if err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	if res.Action != "removed" {
		t.Fatalf("expected removed, got %s", res.Action)
	}
}
