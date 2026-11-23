package configimport

import (
	"os"
	"strings"
	"testing"
)

func TestClaudeCodeParser_Parse(t *testing.T) {
	parser := &ClaudeCodeParser{}

	t.Run("valid_config", func(t *testing.T) {
		content, err := os.ReadFile("testdata/claude_code.json")
		if err != nil {
			t.Fatalf("failed to read test file: %v", err)
		}

		servers, err := parser.Parse(content)
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}

		if len(servers) != 4 {
			t.Errorf("Parse() returned %d servers, want 4", len(servers))
		}

		// Check protocols
		protocols := make(map[string]string)
		for _, s := range servers {
			protocols[s.Name] = s.Fields["protocol"].(string)
		}

		expected := map[string]string{
			"local-server": "stdio",
			"remote-http":  "http",
			"remote-sse":   "sse",
			"remote-ws":    "websocket",
		}

		for name, want := range expected {
			if got, ok := protocols[name]; !ok {
				t.Errorf("server %s not found", name)
			} else if got != want {
				t.Errorf("server %s has protocol %s, want %s", name, got, want)
			}
		}
	})

	t.Run("auto_detect_protocol", func(t *testing.T) {
		content := []byte(`{
			"mcpServers": {
				"with-url": {
					"url": "http://localhost:8080"
				},
				"with-command": {
					"command": "node",
					"args": ["server.js"]
				}
			}
		}`)

		servers, err := parser.Parse(content)
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}

		protocols := make(map[string]string)
		for _, s := range servers {
			protocols[s.Name] = s.Fields["protocol"].(string)
		}

		if protocols["with-url"] != "http" {
			t.Errorf("with-url should auto-detect as http, got %s", protocols["with-url"])
		}
		if protocols["with-command"] != "stdio" {
			t.Errorf("with-command should auto-detect as stdio, got %s", protocols["with-command"])
		}
	})

	t.Run("oauth_block", func(t *testing.T) {
		content := []byte(`{
			"mcpServers": {
				"remote": {
					"type": "http",
					"url": "https://example.com/mcp",
					"oauth": {
						"clientId": "example-client",
						"clientSecret": "example-secret",
						"scopes": "read write",
						"callbackPort": 8080,
						"authServerMetadataUrl": "https://auth.example.com/.well-known/oauth-authorization-server"
					}
				}
			}
		}`)

		servers, err := parser.Parse(content)
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		if len(servers) != 1 {
			t.Fatalf("Parse() returned %d servers, want 1", len(servers))
		}

		oauth, ok := servers[0].Fields["oauth"].(*ClaudeCodeOAuthConfig)
		if !ok || oauth == nil {
			t.Fatal("oauth field should be *ClaudeCodeOAuthConfig")
		}
		if oauth.ClientID != "example-client" {
			t.Errorf("clientId = %s, want example-client", oauth.ClientID)
		}
		if oauth.ClientSecret != "example-secret" {
			t.Errorf("clientSecret = %s, want example-secret", oauth.ClientSecret)
		}
		if oauth.Scopes != "read write" {
			t.Errorf("scopes = %s, want 'read write'", oauth.Scopes)
		}
		if oauth.CallbackPort != 8080 {
			t.Errorf("callbackPort = %d, want 8080", oauth.CallbackPort)
		}
		if oauth.AuthServerMetadataURL != "https://auth.example.com/.well-known/oauth-authorization-server" {
			t.Errorf("authServerMetadataUrl = %s", oauth.AuthServerMetadataURL)
		}
		if len(oauth.UnknownKeys) != 0 {
			t.Errorf("UnknownKeys = %v, want none", oauth.UnknownKeys)
		}
		if len(servers[0].Warnings) != 0 {
			t.Errorf("warnings = %v, want none", servers[0].Warnings)
		}
	})

	t.Run("oauth_unknown_keys_warned", func(t *testing.T) {
		content := []byte(`{
			"mcpServers": {
				"remote": {
					"type": "http",
					"url": "https://example.com/mcp",
					"oauth": {
						"clientId": "example-client",
						"futureField": true
					}
				}
			}
		}`)

		servers, err := parser.Parse(content)
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		oauth, _ := servers[0].Fields["oauth"].(*ClaudeCodeOAuthConfig)
		if oauth == nil {
			t.Fatal("oauth field should be set")
		}
		if len(oauth.UnknownKeys) != 1 || oauth.UnknownKeys[0] != "futureField" {
			t.Errorf("UnknownKeys = %v, want [futureField]", oauth.UnknownKeys)
		}
		found := false
		for _, w := range servers[0].Warnings {
			if strings.Contains(w, "futureField") {
				found = true
			}
		}
		if !found {
			t.Errorf("no warning mentions futureField: %v", servers[0].Warnings)
		}
	})

	t.Run("invalid_json", func(t *testing.T) {
		content := []byte(`{invalid json}`)
		_, err := parser.Parse(content)
		if err == nil {
			t.Error("Parse() should return error for invalid JSON")
		}
	})
}

func TestClaudeCodeParser_Format(t *testing.T) {
	parser := &ClaudeCodeParser{}
	if parser.Format() != FormatClaudeCode {
		t.Errorf("Format() = %v, want %v", parser.Format(), FormatClaudeCode)
	}
}
