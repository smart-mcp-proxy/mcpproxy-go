//go:build server

package config

import "fmt"

// Auth-broker modes (spec 074, FR-001/FR-003). Each names the upstream
// credential-acquisition strategy the gateway uses on behalf of the caller.
const (
	// AuthBrokerModeTokenExchange uses RFC 8693 OAuth 2.0 Token Exchange.
	AuthBrokerModeTokenExchange = "token_exchange"
	// AuthBrokerModeEntraOBO uses Microsoft Entra On-Behalf-Of flow.
	AuthBrokerModeEntraOBO = "entra_obo"
	// AuthBrokerModeOAuthConnect uses a per-user OAuth connect/authorize flow.
	AuthBrokerModeOAuthConnect = "oauth_connect"
)

// Default header injection settings (FR-016).
const (
	defaultAuthBrokerHeader       = "Authorization"
	defaultAuthBrokerHeaderFormat = "Bearer {token}"
)

// AuthBrokerConfig is the per-upstream token-brokering block (server edition).
// It is opt-in per server (FR-003); upstreams without it behave exactly as
// today. Brokering applies only to HTTP-family upstreams in this phase
// (FR-002).
type AuthBrokerConfig struct {
	// Mode selects the credential-acquisition strategy: token_exchange,
	// entra_obo, or oauth_connect.
	Mode string `json:"mode" mapstructure:"mode"`
	// TokenEndpoint is the IdP token endpoint used to mint the upstream credential.
	TokenEndpoint string `json:"token_endpoint" mapstructure:"token_endpoint"`
	// AuthorizationEndpoint is the upstream AS authorize URL the user is
	// redirected to for consent. Required for the oauth_connect mode (Path B,
	// spec 074 FR-011); unused by token_exchange/entra_obo.
	AuthorizationEndpoint string `json:"authorization_endpoint,omitempty" mapstructure:"authorization_endpoint"`
	// Resource is the RFC 8707 audience the resulting token is scoped to.
	Resource string `json:"resource,omitempty" mapstructure:"resource"`
	// Scopes requested for the upstream credential.
	Scopes []string `json:"scopes,omitempty" mapstructure:"scopes"`
	// ClientID / ClientSecret authenticate the gateway to the token endpoint.
	ClientID     string `json:"client_id,omitempty" mapstructure:"client_id"`
	ClientSecret string `json:"client_secret,omitempty" mapstructure:"client_secret"`
	// Header is the outbound header name the resolved credential is injected
	// into (FR-016, default "Authorization").
	Header string `json:"header,omitempty" mapstructure:"header"`
	// HeaderFormat is the value template; "{token}" is replaced with the
	// resolved credential (default "Bearer {token}").
	HeaderFormat string `json:"header_format,omitempty" mapstructure:"header_format"`
}

// ApplyDefaults fills the optional header-injection fields when unset (FR-016).
func (a *AuthBrokerConfig) ApplyDefaults() {
	if a == nil {
		return
	}
	if a.Header == "" {
		a.Header = defaultAuthBrokerHeader
	}
	if a.HeaderFormat == "" {
		a.HeaderFormat = defaultAuthBrokerHeaderFormat
	}
}

// Validate checks the broker block's own fields (mode + required endpoint).
// Protocol-family enforcement is handled by validateServerAuthBroker, which has
// the surrounding ServerConfig context.
func (a *AuthBrokerConfig) Validate() error {
	if a == nil {
		return nil
	}
	switch a.Mode {
	case AuthBrokerModeTokenExchange, AuthBrokerModeEntraOBO, AuthBrokerModeOAuthConnect:
		// ok
	case "":
		return fmt.Errorf("auth_broker.mode is required (one of token_exchange, entra_obo, oauth_connect)")
	default:
		return fmt.Errorf("invalid auth_broker.mode: %q (must be token_exchange, entra_obo, or oauth_connect)", a.Mode)
	}
	if a.TokenEndpoint == "" {
		return fmt.Errorf("auth_broker.token_endpoint is required")
	}
	// The connect flow (Path B) additionally needs the upstream authorize URL
	// to redirect the user to for consent.
	if a.Mode == AuthBrokerModeOAuthConnect && a.AuthorizationEndpoint == "" {
		return fmt.Errorf("auth_broker.authorization_endpoint is required for mode %q", AuthBrokerModeOAuthConnect)
	}
	return nil
}

// serverIsHTTPFamily reports whether the server is an HTTP/SSE/streamable-HTTP
// upstream, the only kinds that support brokering in this phase (FR-002). A
// server with an explicit stdio protocol, or a bare Command with no URL, is not
// HTTP-family.
func serverIsHTTPFamily(server *ServerConfig) bool {
	switch server.Protocol {
	case "http", "sse", "streamable-http":
		return true
	case "stdio":
		return false
	case "", "auto":
		// Inferred: an HTTP-family upstream has a URL and no launch command.
		return server.URL != "" && server.Command == ""
	default:
		return false
	}
}

// validateServerAuthBroker applies broker defaults and validates the block in
// the context of its server. It rejects brokering on non-HTTP-family upstreams
// (FR-002) with a clear "unsupported in this phase" message.
func validateServerAuthBroker(server *ServerConfig, fieldPrefix string) []ValidationError {
	if server == nil || server.AuthBroker == nil {
		return nil
	}

	var errs []ValidationError
	if !serverIsHTTPFamily(server) {
		errs = append(errs, ValidationError{
			Field:   fieldPrefix + ".auth_broker",
			Message: "auth_broker is only supported on HTTP-family upstreams (http, sse, streamable-http); brokering for stdio/non-HTTP upstreams is unsupported in this phase",
		})
		// Still apply defaults so a later edition flip surfaces a complete block,
		// but skip field validation — the protocol error is the actionable one.
		server.AuthBroker.ApplyDefaults()
		return errs
	}

	server.AuthBroker.ApplyDefaults()
	if err := server.AuthBroker.Validate(); err != nil {
		errs = append(errs, ValidationError{
			Field:   fieldPrefix + ".auth_broker",
			Message: err.Error(),
		})
	}
	return errs
}
