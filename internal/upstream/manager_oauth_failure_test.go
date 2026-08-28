package upstream

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// Issue #975: an OAuth callback that could not be delivered (unknown/expired
// state) used to produce nothing but a log line, so the CLI kept claiming
// "OAuth authentication flow initiated successfully" while the server sat at
// "Sign-in required" forever. The failure must land on the server's status.

func newFailureTestManager(t *testing.T, serverName string) (*Manager, *managed.Client) {
	t.Helper()
	logger := zap.NewNop()

	serverConfig := &config.ServerConfig{
		Name:     serverName,
		URL:      "https://example.test/mcp",
		Protocol: "http",
		Enabled:  true,
		Created:  time.Now(),
	}

	manager := &Manager{
		clients:        make(map[string]*managed.Client),
		logger:         logger,
		secretResolver: secret.NewResolver(),
	}

	client, err := managed.NewClient(serverName, serverConfig, logger, nil, &config.Config{}, nil, secret.NewResolver())
	require.NoError(t, err)
	manager.clients[serverName] = client

	return manager, client
}

func TestRecordOAuthFailure_SurfacesOnServerStatus(t *testing.T) {
	serverName := "oauth-failure-visible"
	manager, client := newFailureTestManager(t, serverName)

	cause := errors.New("OAuth callback for \"oauth-failure-visible\" could not be delivered: state is unknown or expired")
	manager.recordOAuthFailure(serverName, cause)

	info := client.GetConnectionInfo()
	assert.Equal(t, types.StateError, info.State, "the server must not look healthy after a dropped OAuth callback")
	require.Error(t, info.LastError)
	assert.Contains(t, info.LastError.Error(), "could not be delivered")
	assert.True(t, info.IsOAuthError, "the failure must be classified as an OAuth error")
}

func TestRecordOAuthFailure_IgnoresNilAndUnknownServers(t *testing.T) {
	serverName := "oauth-failure-nil"
	manager, client := newFailureTestManager(t, serverName)

	manager.recordOAuthFailure(serverName, nil)
	manager.recordOAuthFailure("no-such-server", errors.New("boom"))

	info := client.GetConnectionInfo()
	assert.NotEqual(t, types.StateError, info.State, "no failure was reported for this server")
}

func TestRecordOAuthFailure_LeavesReadyServerAlone(t *testing.T) {
	serverName := "oauth-failure-ready"
	manager, client := newFailureTestManager(t, serverName)

	client.StateManager.TransitionTo(types.StateConnecting)
	client.StateManager.TransitionTo(types.StateReady)

	manager.recordOAuthFailure(serverName, errors.New("late orphaned callback"))

	assert.Equal(t, types.StateReady, client.GetConnectionInfo().State,
		"a late or orphaned callback must never knock a working connection into Error")
}
