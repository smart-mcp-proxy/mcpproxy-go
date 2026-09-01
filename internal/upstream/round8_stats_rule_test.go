package upstream

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// Issue #1148, round 8 finding 1.
//
// GetStats builds `upstream_stats`, which is served verbatim on
// GET /api/v1/status, on GET /api/v1/servers and on the initial SSE `status`
// event — the SAME responses that carry the redacted server list. It masked its
// `url` with the NAME-rule-only oauth.RedactURLQueryParams and its `last_error`
// with the NAME-rule-only oauth.RedactSensitiveData, while the sibling list in
// the same response ran the name rule PLUS the value-shaped detector. So a
// credential under a query parameter no matcher recognises was masked in one
// half of a single response and published in the clear in the other.

const round8GHPToken = "ghp_1234567890abcdefghijABCDEFGHIJ123456"

func newStatsManager(t *testing.T, serverConfig *config.ServerConfig, global *config.Config) (*Manager, *managed.Client) {
	t.Helper()
	logger := zap.NewNop()
	db, err := storage.NewBoltDB(t.TempDir(), logger.Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	manager := &Manager{
		clients:        make(map[string]*managed.Client),
		logger:         logger,
		storage:        db,
		secretResolver: secret2Resolver(),
	}
	manager.globalConfig.Store(global)
	client, err := managed.NewClient(serverConfig.Name, serverConfig, logger, nil, &config.Config{}, db, secret2Resolver())
	require.NoError(t, err)
	manager.clients[serverConfig.Name] = client
	return manager, client
}

func TestGetStats_UsesTheSharedURLRuleAsTheServerList(t *testing.T) {
	rawURL := "https://h/mcp?opaque=" + round8GHPToken
	manager, _ := newStatsManager(t, &config.ServerConfig{
		Name: "leaky", Protocol: "http", URL: rawURL, Enabled: true,
	}, &config.Config{})

	rendered := fmt.Sprint(manager.GetStats())
	assert.NotContains(t, rendered, round8GHPToken,
		"upstream_stats published a credential the sibling server list in the SAME response masks")
	assert.Equal(t, oauth.LiveRedaction.URLValue(rawURL), statsServerField(t, manager, "leaky", "url"),
		"upstream_stats must render `url` with the one shared live rule")
}

func TestGetStats_UsesTheSharedFreeTextRuleForLastError(t *testing.T) {
	msg := `Post "https://h/mcp?opaque=` + round8GHPToken + `": dial tcp: refused`
	manager, client := newStatsManager(t, &config.ServerConfig{
		Name: "leaky", Protocol: "http", URL: "https://h/mcp", Enabled: true,
	}, &config.Config{})
	client.StateManager.SetError(errors.New(msg))

	rendered := fmt.Sprint(manager.GetStats())
	assert.NotContains(t, rendered, round8GHPToken,
		"upstream_stats published a credential carried by last_error")
	assert.Equal(t, oauth.ScrubUpstreamText(msg), statsServerField(t, manager, "leaky", "last_error"),
		"upstream_stats must scrub `last_error` with the one shared free-text rule")
}

func TestGetStats_RevealSecretHeadersStillOptsOut(t *testing.T) {
	rawURL := "https://h/mcp?opaque=" + round8GHPToken
	manager, client := newStatsManager(t, &config.ServerConfig{
		Name: "leaky", Protocol: "http", URL: rawURL, Enabled: true,
	}, &config.Config{RevealSecretHeaders: true})
	client.StateManager.SetError(errors.New("boom " + round8GHPToken))

	rendered := fmt.Sprint(manager.GetStats())
	assert.Contains(t, rendered, rawURL, "the opt-out must still expose the real URL")
	assert.Contains(t, rendered, "boom "+round8GHPToken, "the opt-out must still expose the real error")
}

func statsServerField(t *testing.T, m *Manager, server, field string) string {
	t.Helper()
	stats := m.GetStats()
	servers, ok := stats["servers"].(map[string]interface{})
	require.True(t, ok, "upstream_stats must carry a servers map: %#v", stats)
	entry, ok := servers[server].(map[string]interface{})
	require.True(t, ok, "no entry for %q: %#v", server, servers)
	value, ok := entry[field].(string)
	require.True(t, ok, "%q is not a string: %#v", field, entry)
	return value
}
