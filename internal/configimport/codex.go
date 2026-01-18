package configimport

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// CodexConfig represents the Codex CLI configuration file structure (TOML).
type CodexConfig struct {
	MCPServers map[string]CodexServerConfig `toml:"mcp_servers"`
}

// CodexServerConfig represents a single server in Codex config.
type CodexServerConfig struct {
	// Stdio transport
	Command string            `toml:"command,omitempty"`
	Args    []string          `toml:"args,omitempty"`
	Cwd     string            `toml:"cwd,omitempty"`
	Env     map[string]string `toml:"env,omitempty"`
	EnvVars []string          `toml:"env_vars,omitempty"` // Forward from shell env

	// HTTP transport
	URL               string            `toml:"url,omitempty"`
	BearerToken       string            `toml:"bearer_token,omitempty"`
	BearerTokenEnvVar string            `toml:"bearer_token_env_var,omitempty"`
	HTTPHeaders       map[string]string `toml:"http_headers,omitempty"`
	EnvHTTPHeaders    map[string]string `toml:"env_http_headers,omitempty"`

	// Behavior
	Enabled       *bool    `toml:"enabled,omitempty"`
	EnabledTools  []string `toml:"enabled_tools,omitempty"`  // Not supported
	DisabledTools []string `toml:"disabled_tools,omitempty"` // Not supported

	// Timeouts (not supported)
	StartupTimeoutSec float64 `toml:"startup_timeout_sec,omitempty"`
	StartupTimeoutMs  int64   `toml:"startup_timeout_ms,omitempty"`
	ToolTimeoutSec    float64 `toml:"tool_timeout_sec,omitempty"`
}

// CodexParser parses Codex CLI configuration files (TOML).
type CodexParser struct{}

// Format returns the configuration format this parser handles.
func (p *CodexParser) Format() ConfigFormat {
	return FormatCodex
}

// Parse parses Codex CLI configuration content.
func (p *CodexParser) Parse(content []byte) ([]*ParsedServer, error) {
	var cfg CodexConfig
	if _, err := toml.Decode(string(content), &cfg); err != nil {
		return nil, &ImportError{
			Type:    "parse_error",
			Message: fmt.Sprintf("invalid TOML: %v", err),
		}
	}

	if len(cfg.MCPServers) == 0 {
		return nil, &ImportError{
			Type:    "no_servers",
			Message: "no MCP servers found in Codex config (looking for [mcp_servers.*] sections)",
		}
	}

	servers := make([]*ParsedServer, 0, len(cfg.MCPServers))
	for name, serverCfg := range cfg.MCPServers {
		// Determine protocol
		protocol := "stdio"
		if serverCfg.URL != "" {
			protocol = "streamable-http"
		}

		// Build environment variables
		env := make(map[string]string)
		for k, v := range serverCfg.Env {
			env[k] = v
		}

		// Resolve env_vars from current environment
		for _, envVar := range serverCfg.EnvVars {
			if val := os.Getenv(envVar); val != "" {
				env[envVar] = val
			}
		}

		// Build headers
		headers := make(map[string]string)
		for k, v := range serverCfg.HTTPHeaders {
			headers[k] = v
		}

		// Resolve env_http_headers
		for headerName, envVar := range serverCfg.EnvHTTPHeaders {
			if val := os.Getenv(envVar); val != "" {
				headers[headerName] = val
			}
		}

		// Handle bearer token
		bearerToken := serverCfg.BearerToken
		if bearerToken == "" && serverCfg.BearerTokenEnvVar != "" {
			bearerToken = os.Getenv(serverCfg.BearerTokenEnvVar)
		}
		if bearerToken != "" {
			headers["Authorization"] = "Bearer " + bearerToken
		}

		// Determine enabled state
		enabled := true
		if serverCfg.Enabled != nil {
			enabled = *serverCfg.Enabled
		}

		parsed := &ParsedServer{
			Name:         name,
			SourceFormat: FormatCodex,
			Fields: map[string]interface{}{
				"command":  serverCfg.Command,
				"args":     serverCfg.Args,
				"cwd":      serverCfg.Cwd,
				"env":      env,
				"url":      serverCfg.URL,
				"headers":  headers,
				"protocol": protocol,
				"enabled":  enabled,
			},
			Warnings: []string{},
		}

		// Log warnings for unsupported fields
		if len(serverCfg.EnabledTools) > 0 {
			parsed.Warnings = append(parsed.Warnings, "enabled_tools is not supported; all tools are enabled")
			parsed.Fields["enabled_tools"] = serverCfg.EnabledTools
		}
		if len(serverCfg.DisabledTools) > 0 {
			parsed.Warnings = append(parsed.Warnings, "disabled_tools is not supported; use MCPProxy's tool filtering instead")
			parsed.Fields["disabled_tools"] = serverCfg.DisabledTools
		}
		if serverCfg.StartupTimeoutSec > 0 || serverCfg.StartupTimeoutMs > 0 {
			parsed.Warnings = append(parsed.Warnings, "startup_timeout is not supported")
		}
		if serverCfg.ToolTimeoutSec > 0 {
			parsed.Warnings = append(parsed.Warnings, "tool_timeout_sec is not supported")
		}

		// Validate required fields based on protocol
		if protocol == "stdio" && serverCfg.Command == "" {
			parsed.Warnings = append(parsed.Warnings, "stdio server missing command field")
		}
		if protocol == "streamable-http" && serverCfg.URL == "" {
			parsed.Warnings = append(parsed.Warnings, "HTTP server missing url field")
		}

		servers = append(servers, parsed)
	}

	return servers, nil
}
