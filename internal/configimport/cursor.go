package configimport

import (
	"encoding/json"
	"fmt"
)

// CursorMCPConfig represents the Cursor IDE configuration file structure.
type CursorMCPConfig struct {
	MCPServers map[string]CursorServerConfig `json:"mcpServers"`
}

// CursorServerConfig represents a single server in Cursor config.
type CursorServerConfig struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	EnvFile string            `json:"envFile,omitempty"` // Not supported, log warning
	Cwd     string            `json:"cwd,omitempty"`
	URL     string            `json:"url,omitempty"`
	Type    string            `json:"type,omitempty"` // sse, streamable-http, streamableHttp
	Headers map[string]string `json:"headers,omitempty"`
	Auth    *CursorAuthConfig `json:"auth,omitempty"`
}

// CursorAuthConfig represents OAuth configuration in Cursor.
type CursorAuthConfig struct {
	ClientID     string   `json:"CLIENT_ID,omitempty"`
	ClientSecret string   `json:"CLIENT_SECRET,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
}

// CursorParser parses Cursor IDE configuration files.
type CursorParser struct{}

// Format returns the configuration format this parser handles.
func (p *CursorParser) Format() ConfigFormat {
	return FormatCursor
}

// Parse parses Cursor IDE configuration content.
func (p *CursorParser) Parse(content []byte) ([]*ParsedServer, error) {
	var cfg CursorMCPConfig
	if err := json.Unmarshal(content, &cfg); err != nil {
		return nil, &ImportError{
			Type:    "parse_error",
			Message: fmt.Sprintf("invalid JSON: %v", err),
		}
	}

	if len(cfg.MCPServers) == 0 {
		return nil, &ImportError{
			Type:    "no_servers",
			Message: "no MCP servers found in Cursor config",
		}
	}

	servers := make([]*ParsedServer, 0, len(cfg.MCPServers))
	for name, serverCfg := range cfg.MCPServers {
		// Determine protocol
		protocol := serverCfg.Type
		if protocol == "" {
			if serverCfg.URL != "" {
				protocol = "sse" // Default for URL-based servers in Cursor
			} else {
				protocol = "stdio"
			}
		}
		// Normalize streamableHttp to streamable-http
		if protocol == "streamableHttp" {
			protocol = "streamable-http"
		}

		parsed := &ParsedServer{
			Name:         name,
			SourceFormat: FormatCursor,
			Fields: map[string]interface{}{
				"command":     serverCfg.Command,
				"args":        serverCfg.Args,
				"env":         serverCfg.Env,
				"cwd":         serverCfg.Cwd,
				"url":         serverCfg.URL,
				"headers":     serverCfg.Headers,
				"protocol":    protocol,
				"auth":        serverCfg.Auth,
			},
			Warnings: []string{},
		}

		// Log warning for unsupported envFile
		if serverCfg.EnvFile != "" {
			parsed.Warnings = append(parsed.Warnings, "envFile is not supported; use env instead")
			parsed.Fields["envFile"] = serverCfg.EnvFile // Keep for reference
		}

		// Validate required fields based on protocol
		if protocol == "stdio" && serverCfg.Command == "" {
			parsed.Warnings = append(parsed.Warnings, "stdio server missing command field")
		}
		if (protocol == "sse" || protocol == "streamable-http") && serverCfg.URL == "" {
			parsed.Warnings = append(parsed.Warnings, fmt.Sprintf("%s server missing url field", protocol))
		}

		servers = append(servers, parsed)
	}

	return servers, nil
}
