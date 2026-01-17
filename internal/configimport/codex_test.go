package configimport

import (
	"os"
	"testing"
)

func TestCodexParser_Parse(t *testing.T) {
	parser := &CodexParser{}

	t.Run("valid_config", func(t *testing.T) {
		content, err := os.ReadFile("testdata/codex.toml")
		if err != nil {
			t.Fatalf("failed to read test file: %v", err)
		}

		servers, err := parser.Parse(content)
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}

		if len(servers) != 5 {
			t.Errorf("Parse() returned %d servers, want 5", len(servers))
		}

		// Check that all servers have correct format
		for _, s := range servers {
			if s.SourceFormat != FormatCodex {
				t.Errorf("server %s has format %v, want %v", s.Name, s.SourceFormat, FormatCodex)
			}
		}
	})

	t.Run("protocol_detection", func(t *testing.T) {
		content, _ := os.ReadFile("testdata/codex.toml")
		servers, _ := parser.Parse(content)

		protocols := make(map[string]string)
		for _, s := range servers {
			protocols[s.Name] = s.Fields["protocol"].(string)
		}

		if protocols["github"] != "stdio" {
			t.Errorf("github should be stdio, got %s", protocols["github"])
		}
		if protocols["http-server"] != "streamable-http" {
			t.Errorf("http-server should be streamable-http, got %s", protocols["http-server"])
		}
	})

	t.Run("enabled_field", func(t *testing.T) {
		content, _ := os.ReadFile("testdata/codex.toml")
		servers, _ := parser.Parse(content)

		var disabledServer *ParsedServer
		for _, s := range servers {
			if s.Name == "disabled-server" {
				disabledServer = s
				break
			}
		}

		if disabledServer == nil {
			t.Fatal("disabled-server not found")
		}

		if disabledServer.Fields["enabled"].(bool) {
			t.Error("disabled-server should have enabled=false")
		}
	})

	t.Run("unsupported_fields_warning", func(t *testing.T) {
		content, _ := os.ReadFile("testdata/codex.toml")
		servers, _ := parser.Parse(content)

		var timeoutServer *ParsedServer
		for _, s := range servers {
			if s.Name == "with-timeouts" {
				timeoutServer = s
				break
			}
		}

		if timeoutServer == nil {
			t.Fatal("with-timeouts not found")
		}

		// Should have warnings about unsupported fields
		hasTimeoutWarning := false
		hasToolsWarning := false
		for _, w := range timeoutServer.Warnings {
			if w == "startup_timeout is not supported" {
				hasTimeoutWarning = true
			}
			if w == "enabled_tools is not supported; all tools are enabled" {
				hasToolsWarning = true
			}
		}

		if !hasTimeoutWarning {
			t.Error("should have warning about startup_timeout")
		}
		if !hasToolsWarning {
			t.Error("should have warning about enabled_tools")
		}
	})

	t.Run("invalid_toml", func(t *testing.T) {
		content := []byte(`{invalid toml`)
		_, err := parser.Parse(content)
		if err == nil {
			t.Error("Parse() should return error for invalid TOML")
		}
	})

	t.Run("empty_servers", func(t *testing.T) {
		content := []byte(`[other_section]
key = "value"`)
		_, err := parser.Parse(content)
		if err == nil {
			t.Error("Parse() should return error for missing mcp_servers")
		}
	})
}

func TestCodexParser_Format(t *testing.T) {
	parser := &CodexParser{}
	if parser.Format() != FormatCodex {
		t.Errorf("Format() = %v, want %v", parser.Format(), FormatCodex)
	}
}
