package registries

import (
	"encoding/json"
	"testing"
)

// parseGenericFrom is a test helper: unmarshals raw JSON and runs the generic
// parser over it, the way fetchServers does for a custom/json registry.
func parseGenericFrom(t *testing.T, raw string) []ServerEntry {
	t.Helper()
	var data interface{}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatalf("fixture is not valid JSON: %v", err)
	}
	return parseGenericJSON(data)
}

// TestParseGenericJSON_FleurArray is the exact payload from GH discussion #783:
// a bare array of apps whose launch info lives under config{runtime,args}. The
// user's report is that this registry cannot be browsed at all.
func TestParseGenericJSON_FleurArray(t *testing.T) {
	servers := parseGenericFrom(t, `[
	  {
	    "name": "Fetch",
	    "description": "Retrieve and process content from web pages.",
	    "category": "Utilities",
	    "sourceUrl": "https://github.com/modelcontextprotocol/servers/tree/main/src/fetch",
	    "config": {"mcpKey": "fetch", "runtime": "uvx", "args": ["mcp-server-fetch"]}
	  },
	  {
	    "name": "Memory",
	    "description": "Knowledge graph memory.",
	    "sourceUrl": "https://github.com/modelcontextprotocol/servers",
	    "config": {"mcpKey": "memory", "runtime": "npx", "args": ["-y", "@modelcontextprotocol/server-memory"]}
	  }
	]`)

	if len(servers) != 2 {
		t.Fatalf("expected 2 servers, got %d (%+v)", len(servers), servers)
	}
	if servers[0].Name != "Fetch" {
		t.Errorf("name = %q, want Fetch", servers[0].Name)
	}
	if servers[0].InstallCmd != "uvx mcp-server-fetch" {
		t.Errorf("installCmd = %q, want %q", servers[0].InstallCmd, "uvx mcp-server-fetch")
	}
	if servers[0].SourceCodeURL == "" {
		t.Error("sourceUrl was not mapped to SourceCodeURL")
	}
	// A local (stdio) server MUST leave URL empty so the add path builds a stdio
	// transport rather than an http one (issues #483/#567).
	if servers[0].URL != "" {
		t.Errorf("URL = %q, want empty for a local stdio server", servers[0].URL)
	}
	if servers[1].InstallCmd != "npx -y @modelcontextprotocol/server-memory" {
		t.Errorf("installCmd = %q, want %q", servers[1].InstallCmd, "npx -y @modelcontextprotocol/server-memory")
	}
}

// TestParseGenericJSON_MCPServersMap covers the other common hand-written
// registry shape: the Claude-desktop style { "mcpServers": { name: {...} } } map.
func TestParseGenericJSON_MCPServersMap(t *testing.T) {
	servers := parseGenericFrom(t, `{
	  "mcpServers": {
	    "fetch": {"command": "uvx", "args": ["mcp-server-fetch"], "description": "Fetch pages"},
	    "weather": {"url": "https://weather.example/mcp"}
	  }
	}`)

	if len(servers) != 2 {
		t.Fatalf("expected 2 servers, got %d (%+v)", len(servers), servers)
	}
	byName := map[string]ServerEntry{}
	for _, s := range servers {
		byName[s.Name] = s
	}
	if got := byName["fetch"].InstallCmd; got != "uvx mcp-server-fetch" {
		t.Errorf("fetch installCmd = %q", got)
	}
	if got := byName["weather"].URL; got != "https://weather.example/mcp" {
		t.Errorf("weather URL = %q", got)
	}
	if got := byName["weather"].InstallCmd; got != "" {
		t.Errorf("weather installCmd = %q, want empty (remote server)", got)
	}
}

// TestParseGenericJSON_ServersEnvelope covers a static file that wraps a flat
// list under "servers"/"items"/"data".
func TestParseGenericJSON_ServersEnvelope(t *testing.T) {
	for _, key := range []string{"servers", "items", "data", "apps", "results"} {
		raw := `{"` + key + `": [{"id":"acme","name":"Acme","description":"d","url":"https://acme.example/mcp"}]}`
		servers := parseGenericFrom(t, raw)
		if len(servers) != 1 {
			t.Fatalf("%s envelope: expected 1 server, got %d", key, len(servers))
		}
		if servers[0].ID != "acme" || servers[0].URL != "https://acme.example/mcp" {
			t.Errorf("%s envelope: got %+v", key, servers[0])
		}
	}
}

// TestParseGenericJSON_OfficialShapedItems: a static file may also hold official
// server.json objects (packages/remotes). Those must classify exactly like the
// official protocol does — packages => stdio InstallCmd, remotes => URL.
func TestParseGenericJSON_OfficialShapedItems(t *testing.T) {
	servers := parseGenericFrom(t, `[
	  {"name":"io.acme/local","description":"d","packages":[{"registryType":"npm","identifier":"acme-mcp","version":"1.2.3"}]},
	  {"name":"io.acme/remote","description":"d","remotes":[{"type":"streamable-http","url":"https://acme.example/mcp"}]}
	]`)

	if len(servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(servers))
	}
	if servers[0].InstallCmd != "npx acme-mcp@1.2.3" {
		t.Errorf("package entry installCmd = %q", servers[0].InstallCmd)
	}
	if servers[0].URL != "" {
		t.Errorf("package entry URL = %q, want empty", servers[0].URL)
	}
	if servers[1].URL != "https://acme.example/mcp" {
		t.Errorf("remote entry URL = %q", servers[1].URL)
	}
}

// TestParseGenericJSON_SkipsUnusable: entries without a name are dropped, and a
// non-list payload yields no servers rather than panicking.
func TestParseGenericJSON_SkipsUnusable(t *testing.T) {
	if got := parseGenericFrom(t, `[{"description":"no name"},{"name":""}]`); len(got) != 0 {
		t.Errorf("expected nameless entries to be dropped, got %+v", got)
	}
	if got := parseGenericFrom(t, `{"message":"hello"}`); len(got) != 0 {
		t.Errorf("expected no servers from a non-list payload, got %+v", got)
	}
}

// TestParseGenericJSON_CommandArgs covers an explicit command/args pair and a
// pre-rendered installCmd string.
func TestParseGenericJSON_CommandArgs(t *testing.T) {
	servers := parseGenericFrom(t, `[
	  {"name":"a","command":"docker","args":["run","-i","--rm","mcp/git"]},
	  {"name":"b","installCmd":"npx -y some-mcp"}
	]`)
	if len(servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(servers))
	}
	if servers[0].InstallCmd != "docker run -i --rm mcp/git" {
		t.Errorf("command/args installCmd = %q", servers[0].InstallCmd)
	}
	if servers[1].InstallCmd != "npx -y some-mcp" {
		t.Errorf("installCmd passthrough = %q", servers[1].InstallCmd)
	}
}
