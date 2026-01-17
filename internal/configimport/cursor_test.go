package configimport

import (
	"os"
	"testing"
)

func TestCursorParser_Parse(t *testing.T) {
	parser := &CursorParser{}

	t.Run("valid_config", func(t *testing.T) {
		content, err := os.ReadFile("testdata/cursor.json")
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

		if protocols["github"] != "stdio" {
			t.Errorf("github should be stdio, got %s", protocols["github"])
		}
		if protocols["sse-server"] != "sse" {
			t.Errorf("sse-server should be sse, got %s", protocols["sse-server"])
		}
		if protocols["streamable-server"] != "streamable-http" {
			t.Errorf("streamable-server should be streamable-http, got %s", protocols["streamable-server"])
		}
	})

	t.Run("streamableHttp_normalized", func(t *testing.T) {
		content := []byte(`{
			"mcpServers": {
				"test": {
					"type": "streamableHttp",
					"url": "http://localhost:8080"
				}
			}
		}`)

		servers, err := parser.Parse(content)
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}

		if servers[0].Fields["protocol"] != "streamable-http" {
			t.Errorf("streamableHttp should be normalized to streamable-http, got %s", servers[0].Fields["protocol"])
		}
	})

	t.Run("auth_config", func(t *testing.T) {
		content, _ := os.ReadFile("testdata/cursor.json")
		servers, _ := parser.Parse(content)

		var streamableServer *ParsedServer
		for _, s := range servers {
			if s.Name == "streamable-server" {
				streamableServer = s
				break
			}
		}

		if streamableServer == nil {
			t.Fatal("streamable-server not found")
		}

		auth, ok := streamableServer.Fields["auth"].(*CursorAuthConfig)
		if !ok {
			t.Fatal("auth field not found or wrong type")
		}

		if auth.ClientID != "my-client-id" {
			t.Errorf("ClientID = %s, want my-client-id", auth.ClientID)
		}
		if len(auth.Scopes) != 2 {
			t.Errorf("Scopes length = %d, want 2", len(auth.Scopes))
		}
	})

	t.Run("cwd_mapping", func(t *testing.T) {
		content, _ := os.ReadFile("testdata/cursor.json")
		servers, _ := parser.Parse(content)

		var cwdServer *ParsedServer
		for _, s := range servers {
			if s.Name == "with-cwd" {
				cwdServer = s
				break
			}
		}

		if cwdServer == nil {
			t.Fatal("with-cwd not found")
		}

		if cwdServer.Fields["cwd"] != "/home/user/project" {
			t.Errorf("cwd = %s, want /home/user/project", cwdServer.Fields["cwd"])
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

func TestCursorParser_Format(t *testing.T) {
	parser := &CursorParser{}
	if parser.Format() != FormatCursor {
		t.Errorf("Format() = %v, want %v", parser.Format(), FormatCursor)
	}
}
