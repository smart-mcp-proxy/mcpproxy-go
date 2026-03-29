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
	Supported bool   // Whether this client supports HTTP/SSE transport
	Reason    string // Explanation when Supported is false
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
		Supported: false,
		Reason:    "Claude Desktop only supports stdio transport; HTTP/SSE not available",
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

	default:
		return ""
	}
}

// buildServerEntry returns the JSON-serializable map that should be inserted
// into the client's config file for the given mcpproxy URL.
func buildServerEntry(clientID, mcpURL string) map[string]interface{} {
	switch clientID {
	case "claude-code":
		return map[string]interface{}{
			"type": "http",
			"url":  mcpURL,
		}
	case "cursor":
		return map[string]interface{}{
			"url":  mcpURL,
			"type": "sse",
		}
	case "windsurf":
		return map[string]interface{}{
			"serverUrl": mcpURL,
			"type":      "sse",
		}
	case "vscode":
		return map[string]interface{}{
			"type": "http",
			"url":  mcpURL,
		}
	case "gemini":
		return map[string]interface{}{
			"httpUrl": mcpURL,
		}
	default:
		// Fallback: generic HTTP entry
		return map[string]interface{}{
			"type": "http",
			"url":  mcpURL,
		}
	}
}
