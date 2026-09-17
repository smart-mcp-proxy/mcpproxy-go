//go:build server

package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func connectTestServer(name string) *config.ServerConfig {
	return &config.ServerConfig{
		Name: name,
		URL:  "https://upstream.example/" + name,
		AuthBroker: &config.AuthBrokerConfig{
			Mode:                  config.AuthBrokerModeOAuthConnect,
			TokenEndpoint:         "https://idp.example/token",
			AuthorizationEndpoint: "https://idp.example/authorize",
			ClientID:              "client-id",
			ClientSecret:          "client-secret",
		},
	}
}

// With public_url unset, base comes from r.Host — caller-controlled on every
// direct request. Without a bound, a caller varying its own Host header could
// grow the connector cache (and its in-memory PKCE/state) without limit
// (cross-review round 7, chunk 3 P2). The cache must stay bounded, evicting
// the oldest entry rather than growing forever.
func TestConnectorProvider_CacheIsBounded(t *testing.T) {
	store := credTestStore(t)
	p := newConnectorProvider(store, nil, nil)
	server := connectTestServer("s")

	for i := 0; i < connectorCacheCap+10; i++ {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/user/credentials/s/connect", nil)
		r.Host = fmt.Sprintf("host-%d.attacker.example", i)
		_, err := p.connector(r, server)
		require.NoError(t, err)
	}

	p.mu.Lock()
	size := len(p.cache)
	orderLen := len(p.order)
	p.mu.Unlock()

	assert.LessOrEqual(t, size, connectorCacheCap, "cache must never exceed its cap")
	assert.Equal(t, size, orderLen, "cache and its eviction order must stay in sync")
}

// The most recently used bases must survive eviction: a caller cycling
// through more distinct Host values than the cap must not evict the base it
// is actively using between its /connect and its /callback.
func TestConnectorProvider_CacheKeepsRecentEntries(t *testing.T) {
	store := credTestStore(t)
	p := newConnectorProvider(store, nil, nil)
	server := connectTestServer("s")

	last := fmt.Sprintf("host-%d.example", connectorCacheCap+9)
	for i := 0; i < connectorCacheCap+10; i++ {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/user/credentials/s/connect", nil)
		r.Host = fmt.Sprintf("host-%d.example", i)
		_, err := p.connector(r, server)
		require.NoError(t, err)
	}

	r := httptest.NewRequest(http.MethodGet, "/api/v1/user/credentials/s/connect", nil)
	r.Host = last
	conn1, err := p.connector(r, server)
	require.NoError(t, err)

	conn2, err := p.connector(r, server)
	require.NoError(t, err)
	assert.Same(t, conn1, conn2, "the just-used base's connector is still cached, not rebuilt")
}
