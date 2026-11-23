package configimport

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// ClaudeCodeConfig represents the Claude Code configuration file structure.
type ClaudeCodeConfig struct {
	MCPServers map[string]ClaudeCodeServerConfig `json:"mcpServers"`
}

// ClaudeCodeServerConfig represents a single server in Claude Code config.
type ClaudeCodeServerConfig struct {
	Type    string                 `json:"type,omitempty"` // stdio, http, sse, websocket
	Command string                 `json:"command,omitempty"`
	Args    []string               `json:"args,omitempty"`
	Env     map[string]string      `json:"env,omitempty"`
	URL     string                 `json:"url,omitempty"`
	Headers map[string]string      `json:"headers,omitempty"`
	OAuth   *ClaudeCodeOAuthConfig `json:"oauth,omitempty"`
}

// ClaudeCodeOAuthConfig is the oauth block in Claude Code configs
// (https://code.claude.com/docs/en/mcp). Scopes is a space-separated string
// there, not a list. Unknown keys are collected for a warning instead of a
// refusal: this is a foreign file format, and new Claude Code fields should
// surface as "ignored", not break the import.
type ClaudeCodeOAuthConfig struct {
	ClientID              string   `json:"clientId,omitempty"`
	ClientSecret          string   `json:"clientSecret,omitempty"`
	Scopes                string   `json:"scopes,omitempty"`
	CallbackPort          int      `json:"callbackPort,omitempty"`
	AuthServerMetadataURL string   `json:"authServerMetadataUrl,omitempty"`
	UnknownKeys           []string `json:"-"`
}

// UnmarshalJSON decodes the known fields and records everything else.
func (c *ClaudeCodeOAuthConfig) UnmarshalJSON(data []byte) error {
	type plain ClaudeCodeOAuthConfig
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*c = ClaudeCodeOAuthConfig(p)

	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return err
	}
	for k := range keys {
		switch k {
		case "clientId", "clientSecret", "scopes", "callbackPort", "authServerMetadataUrl":
		default:
			c.UnknownKeys = append(c.UnknownKeys, k)
		}
	}
	return nil
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

	if len(cfg.MCPServers) == 0 {
		return nil, &ImportError{
			Type:    ErrTypeNoServers,
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
				"oauth":    serverCfg.OAuth,
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
		if serverCfg.OAuth != nil && len(serverCfg.OAuth.UnknownKeys) > 0 {
			slices.Sort(serverCfg.OAuth.UnknownKeys)
			parsed.Warnings = append(parsed.Warnings, fmt.Sprintf("oauth block has unknown keys %s; they were ignored", strings.Join(serverCfg.OAuth.UnknownKeys, ", ")))
		}

		servers = append(servers, parsed)
	}

	return servers, nil
}
