package httpapi

import (
	"encoding/json"
	"net/url"
	"strings"
)

// The delivery helpers of an issued credential (Spec 108-f POST /clients and
// the Spec 115 MCP `credentials` tool): one snippet format, one endpoint, one
// set of UI deep links, in both editions.

// ClientSnippet is the paste-ready config of a custom client. The credential is
// always a header, never a query parameter.
type ClientSnippet struct {
	GenericHTTP string `json:"generic_http"`
	HeaderName  string `json:"header_name"`
}

// clientCredentialHeader is the header a client credential travels in.
const clientCredentialHeader = "X-API-Key"

// mcpEndpointURL is this instance's MCP endpoint for a snippet. The scheme
// follows the listener: with TLS enabled the same listener serves only HTTPS,
// so a delivered snippet must never point a worker (and its credential
// header) at plain HTTP.
func (s *Server) mcpEndpointURL() string {
	addr := "127.0.0.1:8080"
	scheme := "http"
	if cfg, err := s.controller.GetConfig(); err == nil && cfg != nil {
		if cfg.Listen != "" {
			addr = cfg.Listen
		}
		if cfg.TLS != nil && cfg.TLS.Enabled {
			scheme = "https"
		}
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	return scheme + "://" + addr + "/mcp"
}

func (s *Server) clientSnippet(credential string) ClientSnippet {
	type entry struct {
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	}
	doc := struct {
		MCPServers map[string]entry `json:"mcpServers"`
	}{MCPServers: map[string]entry{"mcpproxy": {URL: s.mcpEndpointURL(), Headers: map[string]string{clientCredentialHeader: credential}}}}
	b, _ := json.Marshal(doc)
	return ClientSnippet{GenericHTTP: string(b), HeaderName: clientCredentialHeader}
}

// CredentialSnippet is the paste-ready config for an issued credential: the
// X-API-Key header snippet POST /clients returns (Spec 115 FR-004).
func (s *Server) CredentialSnippet(secret string) ClientSnippet { return s.clientSnippet(secret) }

// MCPEndpoint is this instance's MCP endpoint URL (never carries a key).
func (s *Server) MCPEndpoint() string { return s.mcpEndpointURL() }

// CredentialLinks are the Web UI deep links of an issued credential (Spec 115
// UI-007): absolute URLs built from the listen address plus their ui_path
// forms. No URL carries an API key or a credential.
type CredentialLinks struct {
	Identity       string            `json:"identity"`
	Profile        string            `json:"profile,omitempty"`
	EffectiveTools string            `json:"effective_tools,omitempty"`
	Activity       string            `json:"activity"`
	UIPath         map[string]string `json:"ui_path"`
}

// CredentialUILinks builds the deep links for a client (client != "") or a
// token, bound or pinned to profileName.
func (s *Server) CredentialUILinks(client, token, profileName string) CredentialLinks {
	base := strings.TrimSuffix(s.mcpEndpointURL(), "/mcp") + "/ui"
	paths := map[string]string{}
	if client != "" {
		paths["identity"] = "/clients?client=" + url.QueryEscape(client)
		paths["activity"] = "/activity?client=" + url.QueryEscape(client)
	} else {
		paths["identity"] = "/clients?tab=tokens&token=" + url.QueryEscape(token)
		paths["activity"] = "/activity?token=" + url.QueryEscape(token)
	}
	if profileName != "" {
		p := url.PathEscape(profileName)
		paths["profile"] = "/profiles/" + p
		paths["effective_tools"] = "/profiles/" + p + "?tab=tools&reason=callable"
	}
	return CredentialLinks{
		Identity: base + paths["identity"], Activity: base + paths["activity"],
		Profile: optionalLink(base, paths["profile"]), EffectiveTools: optionalLink(base, paths["effective_tools"]),
		UIPath: paths,
	}
}

func optionalLink(base, path string) string {
	if path == "" {
		return ""
	}
	return base + path
}
