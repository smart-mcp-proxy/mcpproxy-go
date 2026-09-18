//go:build server

package config

import "fmt"

// Auth-broker modes (spec 074, FR-001/FR-003).
//
// Spec 107 FR-032 reduced the accepted set to the one mode that has a live
// implementation. `token_exchange` and `entra_obo` were validated but never
// performed by any code path; a config that still carries one of them loads
// with the whole auth_broker block of that server dropped and a LoadDiagnostic
// recorded (see the server-build normaliser), and the write doors refuse it
// (ValidateRemovedKeys).
const (
	// AuthBrokerModeOAuthConnect uses a per-user OAuth connect/authorize flow.
	AuthBrokerModeOAuthConnect = "oauth_connect"
)

// AuthBrokerConfig is the per-upstream credential-connect block (server
// edition). It is opt-in per server (FR-003); upstreams without it behave
// exactly as today. A user who completes the connect flow gets their
// credential STORED (encrypted) for that upstream — nothing injects it into a
// proxied request (Spec 107 FR-034); the `header`/`header_format` injection
// leaves were removed with the never-wired injector (FR-032).
type AuthBrokerConfig struct {
	// Mode selects the credential-acquisition strategy: oauth_connect.
	Mode string `json:"mode" mapstructure:"mode"`
	// TokenEndpoint is the IdP token endpoint used to mint the upstream credential.
	TokenEndpoint string `json:"token_endpoint" mapstructure:"token_endpoint"`
	// AuthorizationEndpoint is the upstream AS authorize URL the user is
	// redirected to for consent. Required for the oauth_connect mode (Path B,
	// spec 074 FR-011).
	AuthorizationEndpoint string `json:"authorization_endpoint,omitempty" mapstructure:"authorization_endpoint"`
	// Resource is the RFC 8707 audience the resulting token is scoped to.
	Resource string `json:"resource,omitempty" mapstructure:"resource"`
	// Scopes requested for the upstream credential.
	Scopes []string `json:"scopes,omitempty" mapstructure:"scopes"`
	// ClientID / ClientSecret authenticate the gateway to the token endpoint.
	ClientID     string `json:"client_id,omitempty" mapstructure:"client_id"`
	ClientSecret string `json:"client_secret,omitempty" mapstructure:"client_secret"`
}

// Clone returns a deep copy of the block (nil-safe). CopyServerConfig uses it
// so a copied server never shares the Scopes backing array with its source.
func (a *AuthBrokerConfig) Clone() *AuthBrokerConfig {
	if a == nil {
		return nil
	}
	out := *a
	if a.Scopes != nil {
		out.Scopes = append([]string(nil), a.Scopes...)
	}
	return &out
}

// Validate checks the broker block's own fields (mode + required endpoints).
// Protocol-family enforcement is handled by validateServerAuthBroker, which has
// the surrounding ServerConfig context.
func (a *AuthBrokerConfig) Validate() error {
	if a == nil {
		return nil
	}
	switch a.Mode {
	case AuthBrokerModeOAuthConnect:
		// ok
	case "":
		return fmt.Errorf("auth_broker.mode is required (must be %s)", AuthBrokerModeOAuthConnect)
	default:
		return fmt.Errorf("invalid auth_broker.mode: %q (must be %s)", a.Mode, AuthBrokerModeOAuthConnect)
	}
	if a.TokenEndpoint == "" {
		return fmt.Errorf("auth_broker.token_endpoint is required")
	}
	// The connect flow (Path B) additionally needs the upstream authorize URL
	// to redirect the user to for consent.
	if a.AuthorizationEndpoint == "" {
		return fmt.Errorf("auth_broker.authorization_endpoint is required for mode %q", AuthBrokerModeOAuthConnect)
	}
	return nil
}

// serverIsHTTPFamily reports whether the server is an HTTP/SSE/streamable-HTTP
// upstream, the only kinds that support the connect flow (FR-002). A server
// with an explicit stdio protocol, or a bare Command with no URL, is not
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

// validateServerAuthBroker validates the block in the context of its server.
// It rejects the connect flow on non-HTTP-family upstreams (FR-002) with a
// clear "unsupported in this phase" message.
func validateServerAuthBroker(server *ServerConfig, fieldPrefix string) []ValidationError {
	if server == nil || server.AuthBroker == nil {
		return nil
	}

	if !serverIsHTTPFamily(server) {
		// The protocol error is the actionable one; field validation is skipped.
		return []ValidationError{{
			Field:   fieldPrefix + ".auth_broker",
			Message: "auth_broker is only supported on HTTP-family upstreams (http, sse, streamable-http); brokering for stdio/non-HTTP upstreams is unsupported in this phase",
		}}
	}

	if err := server.AuthBroker.Validate(); err != nil {
		return []ValidationError{{
			Field:   fieldPrefix + ".auth_broker",
			Message: err.Error(),
		}}
	}
	return nil
}
