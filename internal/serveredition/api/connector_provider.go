//go:build server

package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/broker"
)

// connectorProvider builds and caches one broker.OAuthConnector per
// oauth_connect upstream (keyed by serverKey). The same connector instance must
// serve both the connect redirect and the callback because the connector holds
// the in-memory PKCE/state for each pending flow; rebuilding it per request
// would lose that state. It satisfies broker.ConnectorProvider so the T6
// CredentialResolver can reuse the same connectors when it needs to produce a
// connect URL for an unconnected user.
type connectorProvider struct {
	store  broker.CredentialStore
	logger *zap.Logger
	audit  broker.AuditSink // connect-flow audit sink (spec 074 T10); nil = no-op

	mu      sync.Mutex
	baseURL string // gateway public origin, e.g. "https://gw.example.com"
	cache   map[string]*broker.OAuthConnector
}

// newConnectorProvider constructs an empty provider. A nil audit sink disables
// connect-flow audit emission.
func newConnectorProvider(store broker.CredentialStore, logger *zap.Logger, audit broker.AuditSink) *connectorProvider {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &connectorProvider{
		store:  store,
		logger: logger,
		audit:  audit,
		cache:  make(map[string]*broker.OAuthConnector),
	}
}

// observeBaseURL records the gateway's public origin the first time it is seen
// (from an incoming request). The connect callback URL registered with the
// upstream authorization server is derived from it, and OAuth requires the
// redirect_uri to be byte-identical between the authorize request and the token
// exchange — so it is fixed once and reused for the lifetime of a connector.
func (p *connectorProvider) observeBaseURL(r *http.Request) {
	base := baseURLFromRequest(r)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.baseURL == "" {
		p.baseURL = base
	}
}

// connector returns the cached connector for an oauth_connect upstream, building
// it on first use. It errors for non-oauth_connect or unbrokered servers.
func (p *connectorProvider) connector(server *config.ServerConfig) (*broker.OAuthConnector, error) {
	if server == nil || server.AuthBroker == nil {
		return nil, fmt.Errorf("connector provider: server has no auth_broker configuration")
	}
	if server.AuthBroker.Mode != config.AuthBrokerModeOAuthConnect {
		return nil, fmt.Errorf("connector provider: server %q is not an oauth_connect upstream", server.Name)
	}

	key := oauth.GenerateServerKey(server.Name, server.URL)

	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.cache[key]; ok {
		return c, nil
	}

	ab := server.AuthBroker
	cfg := broker.ConnectorConfig{
		ServerName:            server.Name,
		ServerURL:             server.URL,
		AuthorizationEndpoint: ab.AuthorizationEndpoint,
		TokenEndpoint:         ab.TokenEndpoint,
		ClientID:              ab.ClientID,
		ClientSecret:          ab.ClientSecret,
		Scopes:                ab.Scopes,
		RedirectURI:           p.callbackURLLocked(server.Name),
		Resource:              ab.Resource,
	}
	conn, err := broker.NewOAuthConnector(p.store, cfg, p.logger, p.audit)
	if err != nil {
		return nil, err
	}
	p.cache[key] = conn
	return conn, nil
}

// ConnectorFor satisfies broker.ConnectorProvider for the credential resolver.
func (p *connectorProvider) ConnectorFor(server *config.ServerConfig) (broker.Connector, error) {
	return p.connector(server)
}

// callbackURLLocked builds the gateway callback URL for a server. Caller holds p.mu.
func (p *connectorProvider) callbackURLLocked(serverName string) string {
	base := strings.TrimSuffix(p.baseURL, "/")
	return base + connectCallbackPath(serverName)
}

// connectCallbackPath is the relative callback route for a server's connect flow.
func connectCallbackPath(serverName string) string {
	return "/api/v1/user/credentials/" + url.PathEscape(serverName) + "/callback"
}

// connectInitiatePath is the relative connect route for a server.
func connectInitiatePath(serverName string) string {
	return "/api/v1/user/credentials/" + url.PathEscape(serverName) + "/connect"
}

// baseURLFromRequest derives the gateway's public origin (scheme://host),
// honoring X-Forwarded-Proto for reverse-proxy deployments. Mirrors the OAuth
// login handler's buildCallbackURL scheme detection.
func baseURLFromRequest(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	return scheme + "://" + r.Host
}

// Compile-time assertion that the provider satisfies the resolver's interface.
var _ broker.ConnectorProvider = (*connectorProvider)(nil)
