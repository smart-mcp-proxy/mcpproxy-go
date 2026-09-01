package upstream

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// TestConnectAll_DoesNotLogTheConfiguredURLCredential covers issue #1148 at the
// one raw-URL log site the earlier rounds left behind.
//
// internal/upstream/core and internal/transport each grew a logSafeURL helper,
// but the manager's own "Attempting to connect client" line — which runs on
// every connect attempt, for every server, at Info — still wrote
// client.GetConfig().URL verbatim. A URL-embedded credential (`?token=…`,
// `user:pass@`) therefore kept landing in main.log on a loop, where `tail_log`
// and any process with disk access can read it.
//
// (Pre-existing: this line is identical on origin/main.)
func TestConnectAll_DoesNotLogTheConfiguredURLCredential(t *testing.T) {
	const secret = "urlsecret999"

	core, logs := observer.New(zap.DebugLevel)
	logger := zap.New(core)

	tempDir := t.TempDir()
	db, err := storage.NewBoltDB(tempDir, logger.Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	serverConfig := &config.ServerConfig{
		Name:     "leaky",
		Protocol: "http",
		// Port 1 refuses immediately, so the attempt fails fast — the log line
		// under test is emitted before the dial either way.
		URL:     "http://127.0.0.1:1/mcp?token=" + secret,
		Enabled: true,
	}

	manager := &Manager{
		clients:        make(map[string]*managed.Client),
		logger:         logger,
		storage:        db,
		secretResolver: secret2Resolver(),
	}
	client, err := managed.NewClient(serverConfig.Name, serverConfig, logger, nil, &config.Config{}, db, secret2Resolver())
	require.NoError(t, err)
	manager.clients[serverConfig.Name] = client

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = manager.ConnectAll(ctx)

	entries := logs.All()
	require.NotEmpty(t, entries, "precondition: ConnectAll must log the attempt")
	for _, e := range entries {
		rendered := e.Message + " " + fmt.Sprint(e.ContextMap())
		assert.NotContains(t, rendered, secret,
			"the URL credential must never reach the log (entry: %s)", rendered)
	}
	assert.True(t, hasEntryContaining(entries, "Attempting to connect client"),
		"precondition: the site under test must have run")
}

func secret2Resolver() *secret.Resolver { return secret.NewResolver() }

func hasEntryContaining(entries []observer.LoggedEntry, msg string) bool {
	for _, e := range entries {
		if strings.Contains(e.Message, msg) {
			return true
		}
	}
	return false
}

// TestGetStats_MasksURLAndErrorCredentials covers issue #1148 round 4.
//
// GetStats builds the per-server status map that /api/v1/status, /api/v1/servers
// and the SSE `status` event all serve verbatim as `upstream_stats`, and it put
// cfg.URL in raw. The sibling surfaces on the very same responses —
// httpapi.redactServerSecrets and Runtime.redactServerSecrets — already mask
// exactly these fields, so half of one payload was masked and half was not.
func TestGetStats_MasksURLAndErrorCredentials(t *testing.T) {
	const urlSecret = "urlsecret999"

	logger := zap.NewNop()
	tempDir := t.TempDir()
	db, err := storage.NewBoltDB(tempDir, logger.Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	serverConfig := &config.ServerConfig{
		Name:     "leaky",
		Protocol: "http",
		URL:      "https://alice:hunter2phrase@host/mcp?token=" + urlSecret + "&debug=1",
		Enabled:  true,
	}

	manager := &Manager{
		clients:        make(map[string]*managed.Client),
		logger:         logger,
		storage:        db,
		secretResolver: secret2Resolver(),
	}
	manager.globalConfig.Store(&config.Config{})
	client, err := managed.NewClient(serverConfig.Name, serverConfig, logger, nil, &config.Config{}, db, secret2Resolver())
	require.NoError(t, err)
	manager.clients[serverConfig.Name] = client

	stats := manager.GetStats()
	rendered := fmt.Sprint(stats)

	assert.Contains(t, rendered, "host/mcp", "precondition: the status map carries the URL")
	assert.NotContains(t, rendered, urlSecret, "the query credential must not reach the status map")
	assert.NotContains(t, rendered, "hunter2phrase", "the userinfo password must not reach the status map")
	assert.Contains(t, rendered, "debug=1", "non-sensitive parameters must stay readable")
}

// TestGetStats_RevealSecretHeadersOptOut mirrors the escape hatch the two
// sibling redactors honour, so a user who has deliberately opted in still sees
// the real URL on every surface rather than only some of them.
func TestGetStats_RevealSecretHeadersOptOut(t *testing.T) {
	const urlSecret = "urlsecret999"

	logger := zap.NewNop()
	tempDir := t.TempDir()
	db, err := storage.NewBoltDB(tempDir, logger.Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	serverConfig := &config.ServerConfig{
		Name:     "leaky",
		Protocol: "http",
		URL:      "https://host/mcp?token=" + urlSecret,
		Enabled:  true,
	}

	manager := &Manager{
		clients:        make(map[string]*managed.Client),
		logger:         logger,
		storage:        db,
		secretResolver: secret2Resolver(),
	}
	manager.globalConfig.Store(&config.Config{RevealSecretHeaders: true})
	client, err := managed.NewClient(serverConfig.Name, serverConfig, logger, nil, &config.Config{}, db, secret2Resolver())
	require.NoError(t, err)
	manager.clients[serverConfig.Name] = client

	assert.Contains(t, fmt.Sprint(manager.GetStats()), urlSecret,
		"reveal_secret_headers:true must expose the real URL, as it does on /api/v1/servers")
}
