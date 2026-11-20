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

// Each clientInfo.name a host sends must be an alias of the client whose config it reads (#1959).
func TestClientInfoNamesMatchWhatEachHostSends(t *testing.T) {
	for id, wants := range map[string][]string{
		"copilot": {"copilot-cli", "github-copilot-developer", "vscode-agent-host", "vscode"},
		"vscode":  {"Visual Studio Code", "Visual Studio Code - Insiders"},
	} {
		def := FindClient(id)
		if def == nil {
			t.Fatalf("%s client is not registered", id)
		}
		have := make(map[string]bool, len(def.ClientInfoNames))
		for _, name := range def.ClientInfoNames {
			have[name] = true
		}
		for _, want := range wants {
			if !have[want] {
				t.Errorf("%s: ClientInfoNames = %v, missing %q", id, def.ClientInfoNames, want)
			}
		}
	}
}

// Copilot CLI and VS Code read $COPILOT_HOME/mcp-config.json when COPILOT_HOME
// is set and do not look at ~/.copilot as well, so connect has to follow it or
// the entry lands in a file nothing reads.
func TestConfigPathCopilotFollowsCopilotHome(t *testing.T) {
	copilotHome := t.TempDir()
	t.Setenv("COPILOT_HOME", copilotHome)

	want := filepath.Join(copilotHome, "mcp-config.json")
	if got := ConfigPath("copilot", ""); got != want {
		t.Fatalf("ConfigPath = %s, want %s", got, want)
	}
}

// An explicit home directory is the isolation seam every test and embedder
// relies on: COPILOT_HOME from the developer's shell must not redirect a
// write that was pointed at a temp home.
func TestConfigPathCopilotExplicitHomeIgnoresCopilotHome(t *testing.T) {
	t.Setenv("COPILOT_HOME", t.TempDir())
	home := t.TempDir()

	want := filepath.Join(home, ".copilot", "mcp-config.json")
	if got := ConfigPath("copilot", home); got != want {
		t.Fatalf("ConfigPath = %s, want %s", got, want)
	}
}

// A relative COPILOT_HOME resolves against the working directory of whoever
// reads it. The core does not share the client's working directory, so a
// relative value falls back to the default instead of naming a wrong file.
func TestConfigPathCopilotIgnoresRelativeCopilotHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COPILOT_HOME", filepath.Join("relative", "copilot"))

	want := filepath.Join(home, ".copilot", "mcp-config.json")
	if got := ConfigPath("copilot", ""); got != want {
		t.Fatalf("ConfigPath = %s, want %s", got, want)
	}
}

// Disconnect removes only the mcpproxy entry: servers the user added to the
// shared Copilot config by hand, and their "tools" lists, stay as written.
func TestDisconnectCopilotKeepsOtherServers(t *testing.T) {
	svc, home := testService(t)
	cfgPath := ConfigPath("copilot", home)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"mcpServers":{"context7":{"type":"http","url":"https://mcp.context7.com/mcp","tools":["*"]}}}`
	if err := os.WriteFile(cfgPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	if res, err := svc.Connect("copilot", "", false); err != nil || !res.Success {
		t.Fatalf("Connect: %v %+v", err, res)
	}
	if res, err := svc.Disconnect("copilot", ""); err != nil || !res.Success {
		t.Fatalf("Disconnect: %v %+v", err, res)
	}

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]map[string]map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("config is not the expected JSON: %v (%s)", err, raw)
	}
	servers := data["mcpServers"]
	if _, still := servers["mcpproxy"]; still {
		t.Fatalf("mcpproxy entry was not removed: %s", raw)
	}
	other, ok := servers["context7"]
	if !ok {
		t.Fatalf("the user's own server was removed: %s", raw)
	}
	tools, _ := other["tools"].([]interface{})
	if other["url"] != "https://mcp.context7.com/mcp" || len(tools) != 1 || tools[0] != "*" {
		t.Fatalf("the user's own server was rewritten: %v", other)
	}
}
