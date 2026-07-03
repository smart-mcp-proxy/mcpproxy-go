// Package connect provides functionality to register MCPProxy as an MCP server
// in various client configuration files (Claude Code, Cursor, VS Code, Windsurf, Codex, Gemini).
package connect

import (
	"os"
	"path/filepath"
	"runtime"
)

// ClientDef describes a known MCP client and its configuration file format.
type ClientDef struct {
	ID        string // Unique identifier, e.g. "claude-code"
	Name      string // Human-readable name, e.g. "Claude Code"
	Format    string // File format: "json" or "toml"
	ServerKey string // Top-level key for server entries: "mcpServers" or "servers"
	Supported bool   // Whether this client can be connected (directly or via a bridge)
	Reason    string // Explanation when Supported is false
	Note      string // Optional caveat shown for supported clients (e.g. bridge requirement)
	Bridge    bool   // Connects via a stdio bridge; Connect can create the config when absent
	Icon      string // Icon identifier for frontend use
}

// allClients defines all known MCP client applications.
var allClients = []ClientDef{
	{
		ID:        "claude-code",
		Name:      "Claude Code",
		Format:    "json",
		ServerKey: "mcpServers",
		Supported: true,
		Icon:      "claude-code",
	},
	{
		ID:        "claude-desktop",
		Name:      "Claude Desktop",
		Format:    "json",
		ServerKey: "mcpServers",
		Supported: true,
		Note:      "Connects via an mcp-remote stdio bridge (npx -y mcp-remote). Requires Node.js.",
		Bridge:    true,
		Icon:      "claude-desktop",
	},
	{
		ID:        "cursor",
		Name:      "Cursor",
		Format:    "json",
		ServerKey: "mcpServers",
		Supported: true,
		Icon:      "cursor",
	},
	{
		ID:        "windsurf",
		Name:      "Windsurf",
		Format:    "json",
		ServerKey: "mcpServers",
		Supported: true,
		Icon:      "windsurf",
	},
	{
		ID:        "vscode",
		Name:      "VS Code",
		Format:    "json",
		ServerKey: "servers",
		Supported: true,
		Icon:      "vscode",
	},
	{
		ID:        "codex",
		Name:      "Codex CLI",
		Format:    "toml",
		ServerKey: "mcp_servers",
		Supported: true,
		Icon:      "codex",
	},
	{
		ID:        "gemini",
		Name:      "Gemini CLI",
		Format:    "json",
		ServerKey: "mcpServers",
		Supported: true,
		Icon:      "gemini",
	},
	{
		ID:        "opencode",
		Name:      "OpenCode",
		Format:    "json",
		ServerKey: "mcp",
		Supported: true,
		Icon:      "opencode",
	},
}

// GetAllClients returns the definitions of all known clients.
func GetAllClients() []ClientDef {
	result := make([]ClientDef, len(allClients))
	copy(result, allClients)
	return result
}

// FindClient looks up a client definition by ID. Returns nil if not found.
func FindClient(clientID string) *ClientDef {
	for i := range allClients {
		if allClients[i].ID == clientID {
			c := allClients[i]
			return &c
		}
	}
	return nil
}

// ConfigPath returns the expected configuration file path for the given client
// on the current operating system. homeDir overrides os.UserHomeDir when non-empty
// (useful for testing).
func ConfigPath(clientID, homeDir string) string {
	if homeDir == "" {
		var err error
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return ""
		}
	}

	switch clientID {
	case "claude-code":
		return filepath.Join(homeDir, ".claude.json")

	case "claude-desktop":
		switch runtime.GOOS {
		case "darwin":
			return filepath.Join(homeDir, "Library", "Application Support", "Claude", "claude_desktop_config.json")
		case "windows":
			appData := os.Getenv("APPDATA")
			if appData == "" {
				appData = filepath.Join(homeDir, "AppData", "Roaming")
			}
			return filepath.Join(appData, "Claude", "claude_desktop_config.json")
		default: // linux
			return filepath.Join(homeDir, ".config", "Claude", "claude_desktop_config.json")
		}

	case "cursor":
		return filepath.Join(homeDir, ".cursor", "mcp.json")

	case "windsurf":
		return filepath.Join(homeDir, ".codeium", "windsurf", "mcp_config.json")

	case "vscode":
		switch runtime.GOOS {
		case "darwin":
			return filepath.Join(homeDir, "Library", "Application Support", "Code", "User", "mcp.json")
		case "windows":
			appData := os.Getenv("APPDATA")
			if appData == "" {
				appData = filepath.Join(homeDir, "AppData", "Roaming")
			}
			return filepath.Join(appData, "Code", "User", "mcp.json")
		default: // linux
			return filepath.Join(homeDir, ".config", "Code", "User", "mcp.json")
		}

	case "codex":
		return filepath.Join(homeDir, ".codex", "config.toml")

	case "gemini":
		return filepath.Join(homeDir, ".gemini", "settings.json")

	case "opencode":
		if runtime.GOOS == "windows" {
			localAppData := os.Getenv("LOCALAPPDATA")
			if localAppData == "" {
				localAppData = filepath.Join(homeDir, "AppData", "Local")
			}
			return filepath.Join(localAppData, "opencode", "opencode.json")
		}
		return filepath.Join(homeDir, ".config", "opencode", "opencode.json")

	default:
		return ""
	}
}

// buildServerEntry returns the JSON/TOML-serializable map inserted into the
// client's config for the mcpproxy endpoint. When p.credential is set (only when
// require_mcp_auth is on), it is written via the carrier each client actually
// supports — an X-API-Key header where the config schema allows one, the
// mcp-remote --header bridge arg for Claude Desktop, and the ?apikey= query only
// for clients whose config cannot express a header (Codex). When p.credential is
// empty (the default), a clean, keyless entry is written.
//
// Server-side ExtractToken accepts, in order, the X-API-Key header, an
// Authorization: Bearer token, and the ?apikey= query, so the header carrier and
// the query fallback authenticate identically.
func buildServerEntry(clientID string, p serverEntryParams) map[string]interface{} {
	switch clientID {
	case "claude-code", "vscode":
		// Claude Code (~/.claude.json) and VS Code (mcp.json) "type":"http"
		// entries support a "headers" object.
		return withAPIKeyHeader(map[string]interface{}{
			"type": "http",
			"url":  p.baseURL,
		}, p.credential)
	case "claude-desktop":
		// Claude Desktop only speaks stdio, so bridge to mcpproxy's HTTP
		// endpoint with mcp-remote via npx. mcp-remote forwards the credential
		// with --header. The value carries NO space after the colon: Claude
		// Desktop / Cursor don't escape spaces in args and mangle the header
		// (geelen/mcp-remote README) — mcpproxy's hex/token keys are space-free.
		args := []string{"-y", "mcp-remote", p.baseURL}
		if p.credential != "" {
			args = append(args, "--header", "X-API-Key:"+p.credential)
		}
		return map[string]interface{}{
			"command": "npx",
			"args":    args,
		}
	case "cursor":
		// Cursor mcp.json remote entries support a "headers" object.
		return withAPIKeyHeader(map[string]interface{}{
			"url":  p.baseURL,
			"type": "sse",
		}, p.credential)
	case "windsurf":
		// Windsurf mcp_config.json serverUrl entries support a "headers" object.
		return withAPIKeyHeader(map[string]interface{}{
			"serverUrl": p.baseURL,
			"type":      "sse",
		}, p.credential)
	case "gemini":
		// Gemini CLI settings.json httpUrl entries support a "headers" object.
		return withAPIKeyHeader(map[string]interface{}{
			"httpUrl": p.baseURL,
		}, p.credential)
	case "opencode":
		// OpenCode opencode.json "type":"remote" entries support a "headers" object.
		return withAPIKeyHeader(map[string]interface{}{
			"type": "remote",
			"url":  p.baseURL,
		}, p.credential)
	case "codex":
		// Codex speaks TOML; the entry is a single url key under
		// [mcp_servers.<name>]. Its HTTP transport only accepts header VALUES
		// indirectly via env-var names (http_headers / bearer_token_env_var), so
		// a literal X-API-Key header can't be written — fall back to the ?apikey=
		// query, which mcpproxy's /mcp accepts. Constructed here (not inline in
		// connectTOML) so preview and write share one source of truth (FR-002).
		return map[string]interface{}{
			"url": credentialQuery(p.baseURL, p.credential),
		}
	default:
		// Fallback: generic HTTP entry with the query carrier (no known schema).
		return map[string]interface{}{
			"type": "http",
			"url":  credentialQuery(p.baseURL, p.credential),
		}
	}
}

// withAPIKeyHeader adds an X-API-Key header object to a JSON client entry when a
// credential is present, and returns the entry unchanged otherwise.
func withAPIKeyHeader(entry map[string]interface{}, credential string) map[string]interface{} {
	if credential != "" {
		entry["headers"] = map[string]interface{}{"X-API-Key": credential}
	}
	return entry
}
