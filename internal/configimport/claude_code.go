package configimport

import (
	"encoding/json"
	"fmt"
)

// ClaudeCodeConfig represents the Claude Code configuration file structure.
type ClaudeCodeConfig struct {
	MCPServers map[string]ClaudeCodeServerConfig `json:"mcpServers"`
}

// ClaudeCodeServerConfig represents a single server in Claude Code config.
type ClaudeCodeServerConfig struct {
	Type    string            `json:"type,omitempty"` // stdio, http, sse, websocket
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// ClaudeCodeParser parses Claude Code configuration files.
type ClaudeCodeParser struct{}

// Format returns the configuration format this parser handles.
func (p *ClaudeCodeParser) Format() ConfigFormat {
	return FormatClaudeCode
}

// Parse parses Claude Code configuration content.
func (p *ClaudeCodeParser) Parse(content []byte) ([]*ParsedServer, error) {
	var cfg ClaudeCodeConfig
	if err := json.Unmarshal(content, &cfg); err != nil {
		return nil, &ImportError{
			Type:    "parse_error",
			Message: fmt.Sprintf("invalid JSON: %v", err),
		}
	}

	if cfg.MCPServers == nil || len(cfg.MCPServers) == 0 {
		return nil, &ImportError{
			Type:    "no_servers",
			Message: "no MCP servers found in Claude Code config",
		}
	}

	servers := make([]*ParsedServer, 0, len(cfg.MCPServers))
	for name, serverCfg := range cfg.MCPServers {
		// Determine protocol
		protocol := serverCfg.Type
		if protocol == "" {
			if serverCfg.URL != "" {
				protocol = "http"
			} else {
				protocol = "stdio"
			}
		}

		parsed := &ParsedServer{
			Name:         name,
			SourceFormat: FormatClaudeCode,
			Fields: map[string]interface{}{
				"command":  serverCfg.Command,
				"args":     serverCfg.Args,
				"env":      serverCfg.Env,
				"url":      serverCfg.URL,
				"headers":  serverCfg.Headers,
				"protocol": protocol,
			},
			Warnings: []string{},
		}

		// Validate required fields based on protocol
		if protocol == "stdio" && serverCfg.Command == "" {
			parsed.Warnings = append(parsed.Warnings, "stdio server missing command field")
		}
		if (protocol == "http" || protocol == "sse" || protocol == "websocket") && serverCfg.URL == "" {
			parsed.Warnings = append(parsed.Warnings, fmt.Sprintf("%s server missing url field", protocol))
		}

		servers = append(servers, parsed)
	}

	return servers, nil
}
