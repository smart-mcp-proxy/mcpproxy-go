package connect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The existing-entry summary is the ONLY channel through which content of a
// user's client config reaches a preview payload. Spec 091 FR-003 requires it to
// be built by CONSTRUCTION from non-secret projections — entry name, transport
// type, endpoint with query AND userinfo stripped, command, and the NAMES (never
// the values) of headers and environment variables. These tests pin the
// whitelist: anything not on it must not appear in the summary.

func TestEntrySummary_ProjectsOnlyNonSecretFields(t *testing.T) {
	entry := map[string]interface{}{
		"type": "http",
		"url":  "http://127.0.0.1:8080/mcp",
		"headers": map[string]interface{}{
			"X-API-Key":     "rotated-secret-value",
			"Authorization": "Bearer sk-live-do-not-leak",
		},
		"env": map[string]interface{}{
			"MCPPROXY_TOKEN": "env-secret-value",
			"OTHER":          "another-secret",
		},
		// Arbitrary user-authored fields must be dropped entirely: the summary is
		// a whitelist, not a blacklist.
		"password": "user-authored-secret",
		"notes":    "internal-only-secret",
	}

	got := buildEntrySummary("old-proxy", entry)
	if got == nil {
		t.Fatal("buildEntrySummary returned nil for a present entry")
	}
	if got.EntryName != "old-proxy" {
		t.Errorf("EntryName = %q, want %q", got.EntryName, "old-proxy")
	}
	if got.Type != "http" {
		t.Errorf("Type = %q, want %q", got.Type, "http")
	}
	if got.Endpoint != "http://127.0.0.1:8080/mcp" {
		t.Errorf("Endpoint = %q, want %q", got.Endpoint, "http://127.0.0.1:8080/mcp")
	}
	if got.Command != "" {
		t.Errorf("Command = %q, want empty", got.Command)
	}
	if strings.Join(got.HeaderNames, ",") != "Authorization,X-API-Key" {
		t.Errorf("HeaderNames = %v, want sorted [Authorization X-API-Key]", got.HeaderNames)
	}
	if strings.Join(got.EnvNames, ",") != "MCPPROXY_TOKEN,OTHER" {
		t.Errorf("EnvNames = %v, want sorted [MCPPROXY_TOKEN OTHER]", got.EnvNames)
	}

	serialized := mustMarshal(t, got)
	for _, secret := range []string{
		"rotated-secret-value", "sk-live-do-not-leak", "env-secret-value",
		"another-secret", "user-authored-secret", "internal-only-secret",
	} {
		if strings.Contains(serialized, secret) {
			t.Errorf("summary leaked %q: %s", secret, serialized)
		}
	}
}

func TestEntrySummary_EndpointStripsQueryAndUserinfo(t *testing.T) {
	cases := []struct {
		name  string
		entry map[string]interface{}
		want  string
	}{
		{
			name:  "query stripped",
			entry: map[string]interface{}{"url": "http://127.0.0.1:8080/mcp?apikey=QUERY-SECRET"},
			want:  "http://127.0.0.1:8080/mcp",
		},
		{
			name:  "userinfo stripped",
			entry: map[string]interface{}{"url": "http://user:USERINFO-SECRET@127.0.0.1:8080/mcp"},
			want:  "http://127.0.0.1:8080/mcp",
		},
		{
			name:  "fragment stripped",
			entry: map[string]interface{}{"url": "https://example.test/mcp#FRAGMENT-SECRET"},
			want:  "https://example.test/mcp",
		},
		{
			name:  "serverUrl carrier (windsurf)",
			entry: map[string]interface{}{"serverUrl": "http://127.0.0.1:8080/mcp?apikey=QUERY-SECRET"},
			want:  "http://127.0.0.1:8080/mcp",
		},
		{
			name:  "httpUrl carrier (gemini)",
			entry: map[string]interface{}{"httpUrl": "http://127.0.0.1:8080/mcp?apikey=QUERY-SECRET"},
			want:  "http://127.0.0.1:8080/mcp",
		},
		{
			name: "non-URL value is not projected at all",
			// A URL field holding arbitrary user content is dropped rather than
			// echoed: only a real scheme://host endpoint is a known-safe shape.
			entry: map[string]interface{}{"url": "NOT-A-URL-SECRET"},
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildEntrySummary("mcpproxy", tc.entry)
			if got.Endpoint != tc.want {
				t.Fatalf("Endpoint = %q, want %q", got.Endpoint, tc.want)
			}
			serialized := mustMarshal(t, got)
			for _, secret := range []string{"QUERY-SECRET", "USERINFO-SECRET", "FRAGMENT-SECRET", "NOT-A-URL-SECRET"} {
				if strings.Contains(serialized, secret) {
					t.Fatalf("summary leaked %q: %s", secret, serialized)
				}
			}
		})
	}
}

func TestEntrySummary_BridgeEntryProjectsCommandAndHeaderNames(t *testing.T) {
	// Claude Desktop's stdio bridge carries the endpoint and the credential in
	// argv. Only the command, the URL-shaped arg (sanitized) and the --header
	// NAMES are projected; no arg VALUE ever is.
	entry := map[string]interface{}{
		"command": "npx",
		"args": []interface{}{
			"-y", "mcp-remote",
			"http://127.0.0.1:8080/mcp?apikey=ARG-QUERY-SECRET",
			"--header", "X-API-Key:ARG-HEADER-SECRET",
		},
	}

	got := buildEntrySummary("mcpproxy", entry)
	if got.Command != "npx" {
		t.Errorf("Command = %q, want npx", got.Command)
	}
	if got.Endpoint != "http://127.0.0.1:8080/mcp" {
		t.Errorf("Endpoint = %q, want http://127.0.0.1:8080/mcp", got.Endpoint)
	}
	if strings.Join(got.HeaderNames, ",") != "X-API-Key" {
		t.Errorf("HeaderNames = %v, want [X-API-Key]", got.HeaderNames)
	}

	serialized := mustMarshal(t, got)
	for _, secret := range []string{"ARG-QUERY-SECRET", "ARG-HEADER-SECRET", "mcp-remote"} {
		if strings.Contains(serialized, secret) {
			t.Errorf("summary leaked %q: %s", secret, serialized)
		}
	}
}

func TestEntrySummary_IgnoresNonStringProjections(t *testing.T) {
	entry := map[string]interface{}{
		"type":    map[string]interface{}{"nested": "SECRET"},
		"command": []interface{}{"SECRET"},
		"headers": "SECRET",
		"env":     42,
	}
	got := buildEntrySummary("mcpproxy", entry)
	if got.Type != "" || got.Command != "" {
		t.Fatalf("expected non-string projections dropped, got %+v", got)
	}
	if len(got.HeaderNames) != 0 || len(got.EnvNames) != 0 {
		t.Fatalf("expected no header/env names, got %+v", got)
	}
	if serialized := mustMarshal(t, got); strings.Contains(serialized, "SECRET") {
		t.Fatalf("summary leaked SECRET: %s", serialized)
	}
}

// TestPreview_ExistingEntrySummary_NamesAdoptedEntry pins the adoption-aware
// resolution: the summary must describe the entry the WRITE would replace, even
// when it lives under a different key (OpenCode adoption), so the user is told
// what actually disappears.
func TestPreview_ExistingEntrySummary_NamesAdoptedEntry(t *testing.T) {
	svc, home := serviceWithKey(t, "")
	cfgPath := ConfigPath("opencode", home)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{"mcp":{"proxy-alt":{"type":"remote","url":"http://127.0.0.1:8080/mcp?apikey=ADOPTED-SECRET","headers":{"X-API-Key":"ADOPTED-HEADER-SECRET"}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	preview, err := svc.Preview("opencode", "mcpproxy")
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if !preview.EntryExists {
		t.Fatal("expected EntryExists for the adopted entry")
	}
	if preview.ExistingEntrySummary == nil {
		t.Fatal("expected ExistingEntrySummary for an existing (adopted) entry")
	}
	if preview.ExistingEntrySummary.EntryName != "proxy-alt" {
		t.Errorf("summary EntryName = %q, want the adopted key %q",
			preview.ExistingEntrySummary.EntryName, "proxy-alt")
	}
	if preview.ExistingEntrySummary.Type != "remote" {
		t.Errorf("summary Type = %q, want remote", preview.ExistingEntrySummary.Type)
	}
	if preview.ExistingEntrySummary.Endpoint != "http://127.0.0.1:8080/mcp" {
		t.Errorf("summary Endpoint = %q, want the query-stripped endpoint",
			preview.ExistingEntrySummary.Endpoint)
	}
	if strings.Join(preview.ExistingEntrySummary.HeaderNames, ",") != "X-API-Key" {
		t.Errorf("summary HeaderNames = %v, want [X-API-Key]", preview.ExistingEntrySummary.HeaderNames)
	}
}

// TestPreview_ExistingEntrySummary_AbsentWhenNoEntry keeps the field strictly
// tied to EntryExists (contracts §1: present ONLY when entry_exists=true).
func TestPreview_ExistingEntrySummary_AbsentWhenNoEntry(t *testing.T) {
	svc, home := serviceWithKey(t, "")
	seedClientConfig(t, home, "claude-code")

	preview, err := svc.Preview("claude-code", "mcpproxy")
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if preview.EntryExists {
		t.Fatal("fixture should have no entry")
	}
	if preview.ExistingEntrySummary != nil {
		t.Fatalf("expected no summary without an existing entry, got %+v", preview.ExistingEntrySummary)
	}
}

// TestPreview_ExistingEntrySummary_TOMLClient covers the non-JSON format so the
// summary is not silently JSON-only (Codex writes TOML).
func TestPreview_ExistingEntrySummary_TOMLClient(t *testing.T) {
	svc, home := serviceWithKey(t, "")
	cfgPath := ConfigPath("codex", home)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "[mcp_servers.mcpproxy]\nurl = \"http://127.0.0.1:8080/mcp?apikey=TOML-SECRET\"\n"
	if err := os.WriteFile(cfgPath, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}

	preview, err := svc.Preview("codex", "mcpproxy")
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if preview.ExistingEntrySummary == nil {
		t.Fatal("expected ExistingEntrySummary for the existing TOML entry")
	}
	if preview.ExistingEntrySummary.EntryName != "mcpproxy" {
		t.Errorf("summary EntryName = %q, want mcpproxy", preview.ExistingEntrySummary.EntryName)
	}
	if preview.ExistingEntrySummary.Endpoint != "http://127.0.0.1:8080/mcp" {
		t.Errorf("summary Endpoint = %q, want the query-stripped endpoint",
			preview.ExistingEntrySummary.Endpoint)
	}
	if serialized := mustMarshal(t, preview); strings.Contains(serialized, "TOML-SECRET") {
		t.Errorf("preview leaked TOML-SECRET: %s", serialized)
	}
}

// mustMarshal serializes v the way the HTTP layer would, so assertions run over
// exactly the bytes that would leave the core.
func mustMarshal(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
