package configimport

import (
	"os"
	"testing"
)

func TestClaudeDesktopParser_Parse(t *testing.T) {
	parser := &ClaudeDesktopParser{}

	t.Run("valid_config", func(t *testing.T) {
		content, err := os.ReadFile("testdata/claude_desktop.json")
		if err != nil {
			t.Fatalf("failed to read test file: %v", err)
		}

		servers, err := parser.Parse(content)
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}

		if len(servers) != 3 {
			t.Errorf("Parse() returned %d servers, want 3", len(servers))
		}

		// Check that all servers have correct format
		for _, s := range servers {
			if s.SourceFormat != FormatClaudeDesktop {
				t.Errorf("server %s has format %v, want %v", s.Name, s.SourceFormat, FormatClaudeDesktop)
			}
			if s.Fields["protocol"] != "stdio" {
				t.Errorf("server %s has protocol %v, want stdio", s.Name, s.Fields["protocol"])
			}
		}
	})

	t.Run("invalid_json", func(t *testing.T) {
		content := []byte(`{invalid json}`)
		_, err := parser.Parse(content)
		if err == nil {
			t.Error("Parse() should return error for invalid JSON")
		}
	})

	t.Run("empty_servers", func(t *testing.T) {
		content := []byte(`{"mcpServers": {}}`)
		_, err := parser.Parse(content)
		if err == nil {
			t.Error("Parse() should return error for empty servers")
		}
	})

	t.Run("missing_mcpServers", func(t *testing.T) {
		content := []byte(`{"globalShortcut": "test"}`)
		_, err := parser.Parse(content)
		if err == nil {
			t.Error("Parse() should return error for missing mcpServers")
		}
	})

	t.Run("server_without_command", func(t *testing.T) {
		content := []byte(`{
			"mcpServers": {
				"test": {
					"args": ["arg1"]
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

		// Should have warning about missing command
		if len(servers[0].Warnings) == 0 {
			t.Error("Parse() should add warning for missing command")
		}
	})
}

func TestClaudeDesktopParser_Format(t *testing.T) {
	parser := &ClaudeDesktopParser{}
	if parser.Format() != FormatClaudeDesktop {
		t.Errorf("Format() = %v, want %v", parser.Format(), FormatClaudeDesktop)
	}
}
