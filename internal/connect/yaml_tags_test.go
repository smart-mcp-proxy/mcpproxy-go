package connect

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestConnectResult_YAMLTagsMatchJSON guards `mcpproxy connect <client> -o
// yaml` (and `-o json`): gopkg.in/yaml.v3 ignores Go's `json` struct tags
// entirely and falls back to lowercased field names with no separators when
// a field carries no `yaml` tag of its own. Without matching `yaml` tags,
// the documented snake_case keys (including this PR's DisplayPath/
// ReloadHint fields) never appear in `-o yaml` output, unlike
// output.StructuredError, which carries `yaml` tags for exactly this reason.
func TestConnectResult_YAMLTagsMatchJSON(t *testing.T) {
	result := ConnectResult{
		Success:     true,
		Client:      "cursor",
		ConfigPath:  "/home/alice/.cursor/mcp.json",
		BackupPath:  "/home/alice/.cursor/mcp.json.bak",
		ServerName:  "mcpproxy",
		Action:      "updated",
		Message:     "Successfully connected mcpproxy to cursor",
		DisplayPath: "~/.cursor/mcp.json",
		ReloadHint:  "Restart Cursor to load MCPProxy",
	}
	out, err := yaml.Marshal(result)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	got := string(out)

	wantKeys := []string{
		"success:", "client:", "config_path:", "backup_path:", "server_name:",
		"action:", "message:", "display_path:", "reload_hint:",
	}
	for _, key := range wantKeys {
		if !strings.Contains(got, key) {
			t.Errorf("ConnectResult YAML output missing snake_case key %q, got:\n%s", key, got)
		}
	}

	collapsedKeys := []string{"configpath:", "backuppath:", "servername:", "displaypath:", "reloadhint:"}
	for _, wrong := range collapsedKeys {
		if strings.Contains(got, wrong) {
			t.Errorf("ConnectResult YAML output must not use yaml.v3's default collapsed key %q, got:\n%s", wrong, got)
		}
	}
}

// TestClientStatus_YAMLTagsMatchJSON is TestConnectResult_YAMLTagsMatchJSON's
// counterpart for ClientStatus (`connect --list`/`--all -o yaml`).
func TestClientStatus_YAMLTagsMatchJSON(t *testing.T) {
	status := ClientStatus{
		ID:            "cursor",
		Name:          "Cursor",
		ConfigPath:    "/home/alice/.cursor/mcp.json",
		Exists:        true,
		Connected:     true,
		Supported:     true,
		Reason:        "",
		Note:          "",
		Bridge:        false,
		Icon:          "cursor",
		ServerName:    "mcpproxy",
		DisplayPath:   "~/.cursor/mcp.json",
		ReloadHint:    "Restart Cursor to load MCPProxy",
		AccessState:   "accessible",
		CheckedPaths:  []string{"/home/alice/.cursor/mcp.json"},
		Remediation:   "",
		ProxyURL:      "http://127.0.0.1:8080/mcp",
		RegisteredURL: "http://127.0.0.1:8080/mcp",
		EndpointMatch: "this",
	}
	out, err := yaml.Marshal(status)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	got := string(out)

	wantKeys := []string{
		"id:", "name:", "config_path:", "exists:", "connected:", "supported:",
		"icon:", "server_name:", "display_path:", "reload_hint:",
		"access_state:", "checked_paths:", "proxy_url:", "registered_url:",
		"endpoint_match:",
	}
	for _, key := range wantKeys {
		if !strings.Contains(got, key) {
			t.Errorf("ClientStatus YAML output missing snake_case key %q, got:\n%s", key, got)
		}
	}

	collapsedKeys := []string{
		"configpath:", "servername:", "displaypath:", "reloadhint:",
		"accessstate:", "checkedpaths:", "proxyurl:", "registeredurl:",
		"endpointmatch:",
	}
	for _, wrong := range collapsedKeys {
		if strings.Contains(got, wrong) {
			t.Errorf("ClientStatus YAML output must not use yaml.v3's default collapsed key %q, got:\n%s", wrong, got)
		}
	}
}
