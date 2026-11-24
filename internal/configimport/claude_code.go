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
	Type    string            `json:"type,omitempty"` // stdio, http, sse, websocket
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// Raw so that a malformed block costs only this server its OAuth settings.
	OAuth json.RawMessage `json:"oauth,omitempty"`
}

// ClaudeCodeOAuthConfig is the oauth block in Claude Code configs
// (https://code.claude.com/docs/en/mcp). Scopes is a space-separated string
// there, not a list. The client secret is not part of it: Claude Code keeps it
// in the system keychain.
type ClaudeCodeOAuthConfig struct {
	ClientID              string `json:"clientId,omitempty"`
	Scopes                string `json:"scopes,omitempty"`
	CallbackPort          int    `json:"callbackPort,omitempty"`
	AuthServerMetadataURL string `json:"authServerMetadataUrl,omitempty"`
}

var claudeCodeOAuthKeys = []string{"clientId", "scopes", "callbackPort", "authServerMetadataUrl"}

// parseClaudeCodeOAuth decodes an oauth block. This is a foreign file format,
// so a malformed block or an unknown key is a warning, never a failed import.
func parseClaudeCodeOAuth(raw json.RawMessage) (*ClaudeCodeOAuthConfig, []string) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	var oauth ClaudeCodeOAuthConfig
	var keys map[string]json.RawMessage
	err := json.Unmarshal(raw, &oauth)
	if err == nil {
		err = json.Unmarshal(raw, &keys)
	}
	if err != nil {
		return nil, []string{fmt.Sprintf("oauth block is malformed and was ignored: %v", err)}
	}

	var unknown []string
	for k := range keys {
		// Case-insensitive, like the decoder above.
		if !slices.ContainsFunc(claudeCodeOAuthKeys, func(known string) bool { return strings.EqualFold(k, known) }) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return &oauth, nil
	}
	slices.Sort(unknown)
	return &oauth, []string{fmt.Sprintf("oauth block has unknown keys %s; they were ignored", strings.Join(unknown, ", "))}
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

		oauth, warnings := parseClaudeCodeOAuth(serverCfg.OAuth)

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
				"oauth":    oauth,
			},
			Warnings: append([]string{}, warnings...),
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
