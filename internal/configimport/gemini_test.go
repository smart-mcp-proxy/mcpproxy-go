package configimport

import (
	"os"
	"testing"
)

func TestGeminiParser_Parse(t *testing.T) {
	parser := &GeminiParser{}

	t.Run("valid_config", func(t *testing.T) {
		content, err := os.ReadFile("testdata/gemini.json")
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
			if s.SourceFormat != FormatGemini {
				t.Errorf("server %s has format %v, want %v", s.Name, s.SourceFormat, FormatGemini)
			}
		}
	})

	t.Run("protocol_detection", func(t *testing.T) {
		content, _ := os.ReadFile("testdata/gemini.json")
		servers, _ := parser.Parse(content)

		protocols := make(map[string]string)
		for _, s := range servers {
			protocols[s.Name] = s.Fields["protocol"].(string)
		}

		if protocols["github"] != "stdio" {
			t.Errorf("github should be stdio, got %s", protocols["github"])
		}
		if protocols["http-api"] != "http" {
			t.Errorf("http-api should be http (from httpUrl), got %s", protocols["http-api"])
		}
		if protocols["sse-server"] != "sse" {
			t.Errorf("sse-server should be sse (from url), got %s", protocols["sse-server"])
		}
	})

	t.Run("httpUrl_priority", func(t *testing.T) {
		content := []byte(`{
			"mcpServers": {
				"test": {
					"httpUrl": "http://http.example.com",
					"url": "http://sse.example.com"
				}
			}
		}`)

		servers, err := parser.Parse(content)
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}

		if servers[0].Fields["url"] != "http://http.example.com" {
			t.Errorf("httpUrl should take priority, got url=%s", servers[0].Fields["url"])
		}
		if servers[0].Fields["protocol"] != "http" {
			t.Errorf("protocol should be http when httpUrl is present, got %s", servers[0].Fields["protocol"])
		}
	})

	t.Run("oauth_mapping", func(t *testing.T) {
		content, _ := os.ReadFile("testdata/gemini.json")
		servers, _ := parser.Parse(content)

		var oauthServer *ParsedServer
		for _, s := range servers {
			if s.Name == "with-oauth" {
				oauthServer = s
				break
			}
		}

		if oauthServer == nil {
			t.Fatal("with-oauth not found")
		}

		oauth, ok := oauthServer.Fields["oauth"].(*GeminiOAuth)
		if !ok {
			t.Fatal("oauth field not found or wrong type")
		}

		if oauth.ClientID != "gemini-client" {
			t.Errorf("ClientID = %s, want gemini-client", oauth.ClientID)
		}
		if len(oauth.Scopes) != 2 {
			t.Errorf("Scopes length = %d, want 2", len(oauth.Scopes))
		}
	})

	t.Run("unsupported_fields_warning", func(t *testing.T) {
		content, _ := os.ReadFile("testdata/gemini.json")
		servers, _ := parser.Parse(content)

		var sseServer *ParsedServer
		for _, s := range servers {
			if s.Name == "sse-server" {
				sseServer = s
				break
			}
		}

		if sseServer == nil {
			t.Fatal("sse-server not found")
		}

		// Should have warnings about trust and timeout
		hasTrustWarning := false
		hasTimeoutWarning := false
		for _, w := range sseServer.Warnings {
			if w == "trust field ignored for security reasons" {
				hasTrustWarning = true
			}
			if w == "timeout is not supported" {
				hasTimeoutWarning = true
			}
		}

		if !hasTrustWarning {
			t.Error("should have warning about trust field")
		}
		if !hasTimeoutWarning {
			t.Error("should have warning about timeout")
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

func TestGeminiParser_Format(t *testing.T) {
	parser := &GeminiParser{}
	if parser.Format() != FormatGemini {
		t.Errorf("Format() = %v, want %v", parser.Format(), FormatGemini)
	}
}
