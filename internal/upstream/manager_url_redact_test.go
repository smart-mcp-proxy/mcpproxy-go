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
