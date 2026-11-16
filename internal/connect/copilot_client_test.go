package connect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Current VS Code versions run agents on the Copilot SDK and read their MCP
// servers from ~/.copilot/mcp-config.json, which the Copilot CLI reads too, so
// connect has to write that file with the mcpServers key (#1896).
func TestConnectCopilotWritesTheCopilotUserConfig(t *testing.T) {
	svc, home := testService(t)
	want := filepath.Join(home, ".copilot", "mcp-config.json")
	if got := ConfigPath("copilot", home); got != want {
		t.Fatalf("ConfigPath = %s, want %s", got, want)
	}

	res, err := svc.Connect("copilot", "", false)
	if err != nil || !res.Success {
		t.Fatalf("Connect: %v %+v", err, res)
	}
	if res.ConfigPath != want {
		t.Fatalf("wrote %s, want %s", res.ConfigPath, want)
	}

	raw, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("config is not the expected JSON: %v (%s)", err, raw)
	}
	servers, ok := data["mcpServers"]
	if !ok {
		t.Fatalf("no mcpServers section: %s", raw)
	}
	entry, ok := servers["mcpproxy"].(map[string]interface{})
	if !ok {
		t.Fatalf("no mcpproxy entry: %s", raw)
	}
	if entry["type"] != "http" || entry["url"] == nil {
		t.Fatalf("entry is not an http one: %v", entry)
	}
}

// Presence ("seen", calls in the last 24h) is matched exactly against the
// clientInfo.name of the MCP initialize request. The Copilot CLI sends
// "copilot-cli" and other Copilot hosts send "github-copilot-developer", so
// both have to be aliases of this client or it never shows as connected.
func TestCopilotClientInfoNamesMatchWhatCopilotSends(t *testing.T) {
	def := FindClient("copilot")
	if def == nil {
		t.Fatal("copilot client is not registered")
	}
	have := make(map[string]bool, len(def.ClientInfoNames))
	for _, name := range def.ClientInfoNames {
		have[name] = true
	}
	for _, want := range []string{"copilot-cli", "github-copilot-developer"} {
		if !have[want] {
			t.Errorf("ClientInfoNames = %v, missing %q", def.ClientInfoNames, want)
		}
	}
}
