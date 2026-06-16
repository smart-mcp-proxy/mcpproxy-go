//go:build server

package broker

import (
	"context"
	"errors"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

// ErrBrokerStdioUnsupported is returned when brokering is requested for a
// non-HTTP-family (stdio) upstream. Credential injection only works over
// HTTP/SSE/streamable-HTTP transports in this phase (spec 074 FR-002). This is a
// runtime defense-in-depth check; config validation already rejects such blocks
// at load time.
var ErrBrokerStdioUnsupported = errors.New("auth broker: credential injection is only supported on HTTP-family upstreams (http, sse, streamable-http); stdio brokering is unsupported in this phase")

// Fallback header/format used when a brokered server's config has not had its
// defaults applied. These mirror config.AuthBrokerConfig.ApplyDefaults (FR-016);
// config validation normally applies them at load time.
const (
	fallbackBrokerHeader       = "Authorization"
	fallbackBrokerHeaderFormat = "Bearer {token}"
)

// resolver is the subset of *CredentialResolver the injector depends on. It is
// an interface so tests can substitute a fake without a real store/exchanger.
type resolver interface {
	Resolve(ctx context.Context, userID string, server *config.ServerConfig) (*UpstreamCredential, error)
}

// HeaderInjector turns a per-user resolved upstream credential into the
// transport-layer BrokeredAuth injected on a proxied request. It is the bridge
// between the credential broker (server edition) and the edition-neutral
// transport layer: the transport never imports the broker, it only receives the
// resolved credential as plain data.
//
// The injector enforces the spec-074 brokering invariants at the injection
// boundary:
//   - per-user only: an empty userID is rejected (FR-014);
//   - HTTP-family only: stdio brokering is rejected (FR-002);
//   - replacement, not forwarding: the produced BrokeredAuth replaces any
//     configured/inbound auth header (FR-016/FR-017, enforced in transport).
type HeaderInjector struct {
	resolver resolver
}

// NewHeaderInjector builds an injector over a credential resolver. *CredentialResolver
// satisfies the resolver interface.
func NewHeaderInjector(r resolver) *HeaderInjector {
	return &HeaderInjector{resolver: r}
}

// InjectFor resolves the per-user credential for (userID, server) and returns
// the transport.BrokeredAuth to inject. It returns:
//   - ErrUnauthenticated if userID is empty (FR-014);
//   - ErrBrokerNotConfigured if the server has no auth_broker block;
//   - ErrBrokerStdioUnsupported if the server is not HTTP-family (FR-002);
//   - any resolver error (e.g. *NotConnectedError carrying a connect URL).
func (h *HeaderInjector) InjectFor(ctx context.Context, userID string, server *config.ServerConfig) (*transport.BrokeredAuth, error) {
	if userID == "" {
		return nil, ErrUnauthenticated
	}
	if server == nil || server.AuthBroker == nil {
		return nil, ErrBrokerNotConfigured
	}
	// Defense-in-depth: reject brokering on stdio/non-HTTP upstreams (FR-002).
	if transport.DetermineTransportType(server) == transport.TransportStdio {
		return nil, ErrBrokerStdioUnsupported
	}

	cred, err := h.resolver.Resolve(ctx, userID, server)
	if err != nil {
		return nil, err
	}
	if cred == nil || cred.AccessToken == "" {
		return nil, ErrNoCredential
	}

	header := server.AuthBroker.Header
	if header == "" {
		header = fallbackBrokerHeader
	}
	format := server.AuthBroker.HeaderFormat
	if format == "" {
		format = fallbackBrokerHeaderFormat
	}
	return &transport.BrokeredAuth{
		Header: header,
		Format: format,
		Token:  cred.AccessToken,
	}, nil
}

// ConnectionKey derives the pooling key for a brokered upstream connection. It
// binds the connection to a single (user, server) pair so a shared upstream
// brokered per-user never reuses one user's credential/connection for another
// (FR-018). The server component reuses the existing oauth.GenerateServerKey
// scheme (name + URL) so it matches the credential store's keying.
func ConnectionKey(userID string, server *config.ServerConfig) string {
	if server == nil {
		return userID + "\x00"
	}
	return userID + "\x00" + oauth.GenerateServerKey(server.Name, server.URL)
}
