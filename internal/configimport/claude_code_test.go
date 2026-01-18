package configimport

import (
	"os"
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
