package configimport

import (
	"encoding/json"
	"fmt"
)

// GeminiConfig represents the Gemini CLI configuration file structure.
type GeminiConfig struct {
	MCP        *GeminiMCPGlobal              `json:"mcp,omitempty"`
	MCPServers map[string]GeminiServerConfig `json:"mcpServers"`
}

// GeminiMCPGlobal represents global MCP settings in Gemini config.
type GeminiMCPGlobal struct {
	Allowed       []string `json:"allowed,omitempty"`       // Server whitelist
	Excluded      []string `json:"excluded,omitempty"`      // Server blacklist
	ServerCommand string   `json:"serverCommand,omitempty"` // Global command
}

// GeminiServerConfig represents a single server in Gemini config.
type GeminiServerConfig struct {
	// Transport (httpUrl > url > command)
	HTTPUrl string `json:"httpUrl,omitempty"` // HTTP streaming (priority)
	URL     string `json:"url,omitempty"`     // SSE endpoint
	Command string `json:"command,omitempty"` // Stdio

	// Common
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Timeout int               `json:"timeout,omitempty"` // Not supported

	// Behavior (not supported)
	Description  string   `json:"description,omitempty"`
	Trust        bool     `json:"trust,omitempty"`        // Security risk, ignore
	IncludeTools []string `json:"includeTools,omitempty"` // Not supported
	ExcludeTools []string `json:"excludeTools,omitempty"` // Not supported

	// OAuth
	AuthProviderType     string       `json:"authProviderType,omitempty"`
	OAuth                *GeminiOAuth `json:"oauth,omitempty"`
	TargetAudience       string       `json:"targetAudience,omitempty"`
	TargetServiceAccount string       `json:"targetServiceAccount,omitempty"`
}

// GeminiOAuth represents OAuth configuration in Gemini config.
type GeminiOAuth struct {
	Enabled      bool     `json:"enabled,omitempty"`
	ClientID     string   `json:"clientId,omitempty"`
	ClientSecret string   `json:"clientSecret,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
	RedirectURI  string   `json:"redirectUri,omitempty"`
}

// GeminiParser parses Gemini CLI configuration files.
type GeminiParser struct{}

// Format returns the configuration format this parser handles.
func (p *GeminiParser) Format() ConfigFormat {
	return FormatGemini
}

// Parse parses Gemini CLI configuration content.
func (p *GeminiParser) Parse(content []byte) ([]*ParsedServer, error) {
	var cfg GeminiConfig
	if err := json.Unmarshal(content, &cfg); err != nil {
		return nil, &ImportError{
			Type:    "parse_error",
			Message: fmt.Sprintf("invalid JSON: %v", err),
		}
	}

	if len(cfg.MCPServers) == 0 {
		return nil, &ImportError{
			Type:    "no_servers",
			Message: "no MCP servers found in Gemini config",
		}
	}

	servers := make([]*ParsedServer, 0, len(cfg.MCPServers))
	for name, serverCfg := range cfg.MCPServers {
		// Determine protocol and URL (httpUrl > url > command)
		var protocol, url string
		if serverCfg.HTTPUrl != "" {
			protocol = "http"
			url = serverCfg.HTTPUrl
		} else if serverCfg.URL != "" {
			protocol = "sse"
			url = serverCfg.URL
		} else {
			protocol = "stdio"
		}

		parsed := &ParsedServer{
			Name:         name,
			SourceFormat: FormatGemini,
			Fields: map[string]interface{}{
				"command":     serverCfg.Command,
				"args":        serverCfg.Args,
				"env":         serverCfg.Env,
				"cwd":         serverCfg.Cwd,
				"url":         url,
				"headers":     serverCfg.Headers,
				"protocol":    protocol,
				"oauth":       serverCfg.OAuth,
				"description": serverCfg.Description,
			},
			Warnings: []string{},
		}

		// Log warnings for unsupported fields
		if serverCfg.Timeout > 0 {
			parsed.Warnings = append(parsed.Warnings, "timeout is not supported")
		}
		if serverCfg.Trust {
			parsed.Warnings = append(parsed.Warnings, "trust field ignored for security reasons")
		}
		if len(serverCfg.IncludeTools) > 0 {
			parsed.Warnings = append(parsed.Warnings, "includeTools is not supported; all tools are available")
			parsed.Fields["includeTools"] = serverCfg.IncludeTools
		}
		if len(serverCfg.ExcludeTools) > 0 {
			parsed.Warnings = append(parsed.Warnings, "excludeTools is not supported; use MCPProxy's tool filtering instead")
			parsed.Fields["excludeTools"] = serverCfg.ExcludeTools
		}
		if serverCfg.TargetAudience != "" {
			parsed.Warnings = append(parsed.Warnings, "targetAudience is not supported")
		}
		if serverCfg.TargetServiceAccount != "" {
			parsed.Warnings = append(parsed.Warnings, "targetServiceAccount is not supported")
		}

		// Validate required fields based on protocol
		if protocol == "stdio" && serverCfg.Command == "" {
			parsed.Warnings = append(parsed.Warnings, "stdio server missing command field")
		}
		if (protocol == "http" || protocol == "sse") && url == "" {
			parsed.Warnings = append(parsed.Warnings, fmt.Sprintf("%s server missing url field", protocol))
		}

		servers = append(servers, parsed)
	}

	return servers, nil
}
