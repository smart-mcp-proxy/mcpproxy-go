package configimport

import (
	"testing"
	"time"
)

func TestMapToServerConfig(t *testing.T) {
	now := time.Date(2026, 1, 17, 12, 0, 0, 0, time.UTC)

	t.Run("basic_stdio_server", func(t *testing.T) {
		parsed := &ParsedServer{
			Name:         "github",
			SourceFormat: FormatClaudeDesktop,
			Fields: map[string]interface{}{
				"command":  "uvx",
				"args":     []string{"mcp-server-github"},
				"env":      map[string]string{"GITHUB_TOKEN": "token"},
				"protocol": "stdio",
			},
		}

		server, skipped, warnings := MapToServerConfig(parsed, now)

		if server.Name != "github" {
			t.Errorf("Name = %s, want github", server.Name)
		}
		if server.Command != "uvx" {
			t.Errorf("Command = %s, want uvx", server.Command)
		}
		if len(server.Args) != 1 || server.Args[0] != "mcp-server-github" {
			t.Errorf("Args = %v, want [mcp-server-github]", server.Args)
		}
		if server.Protocol != "stdio" {
			t.Errorf("Protocol = %s, want stdio", server.Protocol)
		}
		if !server.Quarantined {
			t.Error("server should be quarantined by default")
		}
		if !server.Enabled {
			t.Error("server should be enabled by default")
		}
		if server.Created != now {
			t.Errorf("Created = %v, want %v", server.Created, now)
		}
		if len(skipped) != 0 {
			t.Errorf("skipped = %v, want empty", skipped)
		}
		if len(warnings) != 0 {
			t.Errorf("warnings = %v, want empty", warnings)
		}
	})

	t.Run("http_server_with_headers", func(t *testing.T) {
		parsed := &ParsedServer{
			Name:         "api-server",
			SourceFormat: FormatClaudeCode,
			Fields: map[string]interface{}{
				"url":      "http://localhost:8080/mcp",
				"headers":  map[string]string{"Authorization": "Bearer token"},
				"protocol": "http",
			},
		}

		server, _, _ := MapToServerConfig(parsed, now)

		if server.URL != "http://localhost:8080/mcp" {
			t.Errorf("URL = %s, want http://localhost:8080/mcp", server.URL)
		}
		if server.Headers["Authorization"] != "Bearer token" {
			t.Errorf("Headers[Authorization] = %s, want Bearer token", server.Headers["Authorization"])
		}
		if server.Protocol != "http" {
			t.Errorf("Protocol = %s, want http", server.Protocol)
		}
	})

	t.Run("server_with_cwd", func(t *testing.T) {
		parsed := &ParsedServer{
			Name:         "local",
			SourceFormat: FormatCursor,
			Fields: map[string]interface{}{
				"command":  "python",
				"args":     []string{"-m", "server"},
				"cwd":      "/home/user/project",
				"protocol": "stdio",
			},
		}

		server, _, _ := MapToServerConfig(parsed, now)

		if server.WorkingDir != "/home/user/project" {
			t.Errorf("WorkingDir = %s, want /home/user/project", server.WorkingDir)
		}
	})

	t.Run("disabled_server", func(t *testing.T) {
		parsed := &ParsedServer{
			Name:         "disabled",
			SourceFormat: FormatCodex,
			Fields: map[string]interface{}{
				"command":  "node",
				"args":     []string{"server.js"},
				"protocol": "stdio",
				"enabled":  false,
			},
		}

		server, _, _ := MapToServerConfig(parsed, now)

		if server.Enabled {
			t.Error("server should be disabled")
		}
	})

	t.Run("cursor_oauth", func(t *testing.T) {
		parsed := &ParsedServer{
			Name:         "oauth-server",
			SourceFormat: FormatCursor,
			Fields: map[string]interface{}{
				"url":      "http://localhost:8080",
				"protocol": "sse",
				"auth": &CursorAuthConfig{
					ClientID:     "client-id",
					ClientSecret: "client-secret",
					Scopes:       []string{"read", "write"},
				},
			},
		}

		server, _, warnings := MapToServerConfig(parsed, now)

		if server.OAuth == nil {
			t.Fatal("OAuth should be set")
		}
		if server.OAuth.ClientID != "client-id" {
			t.Errorf("OAuth.ClientID = %s, want client-id", server.OAuth.ClientID)
		}
		if len(server.OAuth.Scopes) != 2 {
			t.Errorf("OAuth.Scopes length = %d, want 2", len(server.OAuth.Scopes))
		}

		// Should have OAuth warning
		hasOAuthWarning := false
		for _, w := range warnings {
			if w == "OAuth credentials imported; you may need to reconfigure" {
				hasOAuthWarning = true
				break
			}
		}
		if !hasOAuthWarning {
			t.Error("should have OAuth warning")
		}
	})

	t.Run("gemini_oauth", func(t *testing.T) {
		parsed := &ParsedServer{
			Name:         "oauth-server",
			SourceFormat: FormatGemini,
			Fields: map[string]interface{}{
				"url":      "http://localhost:8080",
				"protocol": "http",
				"oauth": &GeminiOAuth{
					Enabled:      true,
					ClientID:     "gemini-client",
					ClientSecret: "gemini-secret",
					Scopes:       []string{"scope1"},
					RedirectURI:  "http://localhost:3000/callback",
				},
			},
		}

		server, _, _ := MapToServerConfig(parsed, now)

		if server.OAuth == nil {
			t.Fatal("OAuth should be set")
		}
		if server.OAuth.ClientID != "gemini-client" {
			t.Errorf("OAuth.ClientID = %s, want gemini-client", server.OAuth.ClientID)
		}
		if server.OAuth.RedirectURI != "http://localhost:3000/callback" {
			t.Errorf("OAuth.RedirectURI = %s, want http://localhost:3000/callback", server.OAuth.RedirectURI)
		}
	})

	t.Run("skipped_fields", func(t *testing.T) {
		parsed := &ParsedServer{
			Name:         "test",
			SourceFormat: FormatCursor,
			Fields: map[string]interface{}{
				"command":  "node",
				"args":     []string{"server.js"},
				"protocol": "stdio",
				"envFile":  ".env",
			},
		}

		_, skipped, _ := MapToServerConfig(parsed, now)

		hasEnvFile := false
		for _, s := range skipped {
			if s == "envFile" {
				hasEnvFile = true
				break
			}
		}
		if !hasEnvFile {
			t.Error("envFile should be in skipped list")
		}
	})

	t.Run("propagate_warnings", func(t *testing.T) {
		parsed := &ParsedServer{
			Name:         "test",
			SourceFormat: FormatCodex,
			Fields: map[string]interface{}{
				"command":  "node",
				"protocol": "stdio",
			},
			Warnings: []string{"existing warning"},
		}

		_, _, warnings := MapToServerConfig(parsed, now)

		hasWarning := false
		for _, w := range warnings {
			if w == "existing warning" {
				hasWarning = true
				break
			}
		}
		if !hasWarning {
			t.Error("should propagate existing warnings")
		}
	})
}
