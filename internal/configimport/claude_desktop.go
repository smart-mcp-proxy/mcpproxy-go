package configimport

import (
	"encoding/json"
	"fmt"
)

// ClaudeDesktopConfig represents the Claude Desktop configuration file structure.
type ClaudeDesktopConfig struct {
	GlobalShortcut string                                `json:"globalShortcut,omitempty"`
	MCPServers     map[string]ClaudeDesktopServerConfig `json:"mcpServers"`
}

// ClaudeDesktopServerConfig represents a single server in Claude Desktop config.
type ClaudeDesktopServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
}

// ClaudeDesktopParser parses Claude Desktop configuration files.
type ClaudeDesktopParser struct{}

// Format returns the configuration format this parser handles.
func (p *ClaudeDesktopParser) Format() ConfigFormat {
	return FormatClaudeDesktop
}

// Parse parses Claude Desktop configuration content.
func (p *ClaudeDesktopParser) Parse(content []byte) ([]*ParsedServer, error) {
	var cfg ClaudeDesktopConfig
	if err := json.Unmarshal(content, &cfg); err != nil {
		return nil, &ImportError{
			Type:    "parse_error",
			Message: fmt.Sprintf("invalid JSON: %v", err),
		}
	}

	if cfg.MCPServers == nil || len(cfg.MCPServers) == 0 {
		return nil, &ImportError{
			Type:    "no_servers",
			Message: "no MCP servers found in Claude Desktop config",
		}
	}

	servers := make([]*ParsedServer, 0, len(cfg.MCPServers))
	for name, serverCfg := range cfg.MCPServers {
		parsed := &ParsedServer{
			Name:         name,
			SourceFormat: FormatClaudeDesktop,
			Fields: map[string]interface{}{
				"command":  serverCfg.Command,
				"args":     serverCfg.Args,
				"env":      serverCfg.Env,
				"protocol": "stdio", // Claude Desktop only supports stdio
			},
			Warnings: []string{},
		}

		// Validate required fields
		if serverCfg.Command == "" {
			parsed.Warnings = append(parsed.Warnings, "missing command field")
		}

		servers = append(servers, parsed)
	}

	return servers, nil
}
