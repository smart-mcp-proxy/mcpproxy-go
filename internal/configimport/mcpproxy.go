package configimport

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// NativeServerKeys are the top-level server keys of mcpproxy's own JSON
// shape: what `upstream add-json` takes and `upstream import` reads.
var NativeServerKeys = []string{"args", "command", "env", "headers", "oauth", "protocol", "trust_mode", "type", "url", "working_dir"}

// NativeServer is one server in mcpproxy's own JSON shape.
type NativeServer struct {
	URL        string
	Command    string
	Args       []string
	Env        map[string]string
	Headers    map[string]string
	WorkingDir string
	Protocol   string // "type" folded in, auto-detected when both are absent
	TrustMode  string // not validated here: callers own that vocabulary check
	OAuth      *config.OAuthConfig
}

// ParseNativeServer decodes and validates one server in mcpproxy's own JSON
// shape. Top-level keys outside NativeServerKeys are returned sorted (callers
// report them; they are not applied), while an unknown key inside oauth is an
// error. "type" (the Claude Desktop / VS Code spelling) is an alias for
// protocol.
func ParseNativeServer(raw []byte) (*NativeServer, []string, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, nil, fmt.Errorf("invalid JSON: %w", err)
	}
	var unknown []string
	for k := range keys {
		if !slices.Contains(NativeServerKeys, k) {
			unknown = append(unknown, k)
			delete(keys, k) // reported, not decoded: the strict decode below is for oauth
		}
	}
	sort.Strings(unknown)
	known, err := json.Marshal(keys)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid JSON: %w", err)
	}

	var in struct {
		URL        string              `json:"url"`
		Command    string              `json:"command"`
		Args       []string            `json:"args"`
		Env        map[string]string   `json:"env"`
		Headers    map[string]string   `json:"headers"`
		WorkingDir string              `json:"working_dir"`
		Protocol   string              `json:"protocol"`
		Type       string              `json:"type"`
		TrustMode  string              `json:"trust_mode"`
		OAuth      *config.OAuthConfig `json:"oauth"`
	}
	dec := json.NewDecoder(strings.NewReader(string(known)))
	dec.DisallowUnknownFields() // catches a misspelled key inside oauth
	if err := dec.Decode(&in); err != nil {
		return nil, nil, fmt.Errorf("invalid JSON: %w", err)
	}

	protocol := in.Protocol
	if in.Type != "" {
		if protocol != "" && protocol != in.Type {
			return nil, nil, fmt.Errorf("conflicting protocol %q and type %q: set only one", protocol, in.Type)
		}
		protocol = in.Type
	}
	if protocol == "" {
		if in.Command != "" {
			protocol = "stdio"
		} else if in.URL != "" {
			protocol = "streamable-http"
		}
	}

	if in.URL == "" && in.Command == "" {
		return nil, nil, fmt.Errorf("JSON must contain either 'url' or 'command'")
	}
	if err := in.OAuth.Validate(); err != nil {
		return nil, nil, err
	}
	if err := in.OAuth.ValidateRefreshReplay(); err != nil {
		return nil, nil, err
	}

	return &NativeServer{
		URL:        in.URL,
		Command:    in.Command,
		Args:       in.Args,
		Env:        in.Env,
		Headers:    in.Headers,
		WorkingDir: in.WorkingDir,
		Protocol:   protocol,
		TrustMode:  in.TrustMode,
		OAuth:      in.OAuth,
	}, unknown, nil
}

// nativeOAuthKeys are snake_case oauth keys of config.OAuthConfig. No other
// client's dialect uses them (Cursor has auth.CLIENT_ID, Gemini and Claude
// Code are camelCase), so one is enough to call a file mcpproxy's own.
var nativeOAuthKeys = []string{
	"client_id", "client_secret", "redirect_uri", "callback_base_url", "pkce_enabled",
	"refresh_replay_enabled", "extra_params", "authorization_endpoint", "token_endpoint",
	"registration_endpoint", "auth_server_metadata_url",
}

// nativeServerIndicator returns the key that marks server as mcpproxy's own
// shape, or "" when it has none.
func nativeServerIndicator(server map[string]interface{}) string {
	for _, k := range []string{"protocol", "working_dir", "isolation"} {
		if _, ok := server[k]; ok {
			return k
		}
	}
	if oauth, ok := server["oauth"].(map[string]interface{}); ok {
		for _, k := range nativeOAuthKeys {
			if _, ok := oauth[k]; ok {
				return "oauth." + k
			}
		}
	}
	return ""
}

// MCPProxyParser parses an mcpServers map whose entries use mcpproxy's own
// server shape (the `upstream add-json` payload).
type MCPProxyParser struct{}

// Format returns the configuration format this parser handles.
func (p *MCPProxyParser) Format() ConfigFormat {
	return FormatMCPProxy
}

// Parse parses the content. mcpproxy defines this format, so a server that
// add-json would refuse fails the whole file instead of being half-imported.
func (p *MCPProxyParser) Parse(content []byte) ([]*ParsedServer, error) {
	var cfg struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(content, &cfg); err != nil {
		return nil, &ImportError{Type: "parse_error", Message: fmt.Sprintf("invalid JSON: %v", err)}
	}
	if len(cfg.MCPServers) == 0 {
		return nil, &ImportError{Type: ErrTypeNoServers, Message: "no MCP servers found in mcpproxy config"}
	}

	servers := make([]*ParsedServer, 0, len(cfg.MCPServers))
	for name, raw := range cfg.MCPServers {
		s, unknown, err := ParseNativeServer(raw)
		if err != nil {
			return nil, &ImportError{Type: "parse_error", Message: fmt.Sprintf("server %q: %v", name, err)}
		}
		var warnings []string
		if s.TrustMode != "" {
			// An imported file must not choose how much review its own servers get.
			unknown = append(unknown, "trust_mode")
			warnings = append(warnings, fmt.Sprintf("trust_mode %q was not imported; set it after reviewing the server", s.TrustMode))
		}
		fields := map[string]interface{}{
			"command":  s.Command,
			"args":     s.Args,
			"env":      s.Env,
			"url":      s.URL,
			"headers":  s.Headers,
			"protocol": s.Protocol,
			"cwd":      s.WorkingDir,
			"skipped":  unknown,
		}
		if s.OAuth != nil {
			fields["oauth"] = s.OAuth
		}
		servers = append(servers, &ParsedServer{Name: name, SourceFormat: FormatMCPProxy, Fields: fields, Warnings: warnings})
	}
	return servers, nil
}
