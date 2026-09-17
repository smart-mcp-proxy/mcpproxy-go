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

// connectorCacheCap bounds the number of distinct (server, base URL)
// connectors held at once. With public_url unset, base comes from r.Host —
// caller-controlled on every direct HTTP/1.1 request, trusted-proxy or not —
// so without a cap a caller could mint one permanently-cached OAuthConnector
// (plus its in-memory PKCE/state map) per distinct Host header value, an
// unbounded memory-growth DoS (cross-review round 7, chunk 3 P2). A single
// deployment normally resolves to one or a handful of origins, so this is
// generous headroom, not a tight budget.
const connectorCacheCap = 256

// connectorProvider builds and caches one broker.OAuthConnector per
// oauth_connect upstream (keyed by serverKey). The same connector instance must
// serve both the connect redirect and the callback because the connector holds
// the in-memory PKCE/state for each pending flow; rebuilding it per request
// would lose that state.
type connectorProvider struct {
	store  broker.CredentialStore
	logger *zap.Logger
	audit  broker.AuditSink // connect-flow audit sink (spec 074 T10); nil = no-op

	// publicURL is server_edition.public_url (restart-pinned): when set it is
	// the sole source of the gateway origin (Spec 107 FR-025).
	publicURL string
	// trustedProxies yields the LIVE trusted_proxies list (FR-027); nil
	// trusts nobody.
	trustedProxies config.TrustedProxiesProvider

	mu    sync.Mutex
	cache map[string]*broker.OAuthConnector // keyed by serverKey + "|" + base URL
	// order is cache's insertion order, oldest first; it bounds cache at
	// connectorCacheCap entries by evicting the oldest on overflow.
	order []string
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

// setFrontDoor installs the public_url and the live trusted-proxy provider.
func (p *connectorProvider) setFrontDoor(publicURL string, trusted config.TrustedProxiesProvider) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.publicURL = strings.TrimSuffix(publicURL, "/")
	p.trustedProxies = trusted
}

// baseURL resolves the gateway's public origin for one request: public_url
// when set, otherwise the scheme and host a trusted proxy forwarded, otherwise
// the listener's own scheme and Host. There is NO first-seen latch (Spec 107
// FR-025): the origin is resolved per request, and the connector cache is
// keyed by it, so the connect redirect and its callback — shaped identically
// by the same ingress — share one connector while a hostile first request
// cannot poison every later one.
func (p *connectorProvider) baseURL(r *http.Request) string {
	p.mu.Lock()
	publicURL, trusted := p.publicURL, p.trustedProxies
	p.mu.Unlock()
	if publicURL != "" {
		return publicURL
	}
	var list []string
	if trusted != nil {
		list = trusted()
	}
	fwd := config.ForwardedHeaders(r, list)
	return fwd.Scheme + "://" + fwd.Host
}

// connector returns the cached connector for an oauth_connect upstream, building
// it on first use. It errors for non-oauth_connect or unbrokered servers.
func (p *connectorProvider) connector(r *http.Request, server *config.ServerConfig) (*broker.OAuthConnector, error) {
	if server == nil || server.AuthBroker == nil {
		return nil, fmt.Errorf("connector provider: server has no auth_broker configuration")
	}
	if server.AuthBroker.Mode != config.AuthBrokerModeOAuthConnect {
		return nil, fmt.Errorf("connector provider: server %q is not an oauth_connect upstream", server.Name)
	}

	base := p.baseURL(r)
	key := oauth.GenerateServerKey(server.Name, server.URL) + "|" + base

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
		RedirectURI:           base + connectCallbackPath(server.Name),
		Resource:              ab.Resource,
	}
	conn, err := broker.NewOAuthConnector(p.store, cfg, p.logger, p.audit)
	if err != nil {
		return nil, err
	}
	if len(p.order) >= connectorCacheCap {
		oldest := p.order[0]
		p.order = p.order[1:]
		delete(p.cache, oldest)
	}
	p.cache[key] = conn
	p.order = append(p.order, key)
	return conn, nil
}

// connectCallbackPath is the relative callback route for a server's connect flow.
func connectCallbackPath(serverName string) string {
	return "/api/v1/user/credentials/" + url.PathEscape(serverName) + "/callback"
}

// connectInitiatePath is the relative connect route for a server.
func connectInitiatePath(serverName string) string {
	return "/api/v1/user/credentials/" + url.PathEscape(serverName) + "/connect"
}
