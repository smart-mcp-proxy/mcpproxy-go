package core

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/tests/oauthserver"
)

// GH #1922: a server configured with a static Authorization header and no
// oauth block still reaches the OAuth strategy when the upstream rejects the
// header with a real 401 (headers -> no-auth -> OAuth), and is parked awaiting
// sign-in. The manual login the park points at must then be startable: it
// used to be refused by a config-only "headers are configured" veto, so the
// offered Sign in answered HTTP 500 before any OAuth request was made.

func newStaticHeaderClient(t *testing.T, srv *oauthserver.ServerResult, db *storage.BoltDB, name string) *Client {
	t.Helper()
	cfg := &config.ServerConfig{
		Name:     name,
		URL:      srv.MCPURL,
		Protocol: "http",
		Enabled:  true,
		Headers:  map[string]string{"Authorization": "Bearer rejected-static-credential"},
	}
	c, err := NewClient(name, cfg, zap.NewNop(), nil, &config.Config{}, db, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Disconnect() })
	return c
}

func TestStaticHeadersRejected_ParksAwaitingSignIn_ManualLoginStarts(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "")
	t.Setenv("HEADLESS", "1") // never open a browser from a test

	srv := oauthserver.Start(t, oauthserver.Options{})
	t.Cleanup(func() { _ = srv.Shutdown() })
	db, err := storage.NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	const name = "static-header-rejected"
	stopCallbackServerAfterTest(t, name)

	// The connection path: the rejected header falls through to OAuth, which
	// has no token and defers to a user sign-in.
	c := newStaticHeaderClient(t, srv, db, name)
	connectCtx, cancelConnect := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelConnect()
	err = c.Connect(connectCtx)
	require.Error(t, err)
	require.True(t, IsOAuthPending(err),
		"a rejected static header must park the server awaiting sign-in, got: %v", err)

	// The manual login the park offers, on a transient client as the manager
	// builds it.
	login := newStaticHeaderClient(t, srv, db, name)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel) // ends the background callback waiter

	result, err := login.StartOAuthFlowQuick(ctx)
	require.NoError(t, err, "the sign-in offered for a parked static-header server must start")
	require.NotNil(t, result)
	assert.Contains(t, result.AuthURL, srv.AuthorizationEndpoint)
}

// GH #1922, the GitHub shape: the upstream's authorization server offers no
// Dynamic Client Registration and the operator configured no oauth.client_id,
// so the sign-in cannot proceed. The refusal must be the structured
// client_id-required error (HTTP 400 at the REST layer, not a 500), and for a
// server that authenticates with static headers it must name the other way
// out: repairing the rejected credential.
func TestStaticHeadersRejected_NoClientRegistration_ErrorNamesCredentialRepair(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "")
	t.Setenv("HEADLESS", "1")

	srv := oauthserver.Start(t, oauthserver.Options{
		EnableAuthCode: true, // enable something so the defaults do not turn DCR back on
		EnableDCR:      false,
	})
	t.Cleanup(func() { _ = srv.Shutdown() })
	db, err := storage.NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	const name = "static-header-no-dcr"
	stopCallbackServerAfterTest(t, name)

	login := newStaticHeaderClient(t, srv, db, name)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	_, err = login.StartOAuthFlowQuick(ctx)
	require.Error(t, err)
	var flowErr *contracts.OAuthFlowError
	require.ErrorAs(t, err, &flowErr, "the refusal must be the structured OAuth flow error, got: %v", err)
	assert.Equal(t, contracts.OAuthErrorClientIDRequired, flowErr.ErrorType)
	assert.Contains(t, flowErr.Suggestion, "oauth.client_id")
	assert.Contains(t, flowErr.Suggestion, "headers",
		"a static-header server must be pointed at repairing its configured credential")

	// A server with no static headers keeps the plain suggestion.
	plain, err := NewClient(name+"-plain", &config.ServerConfig{
		Name: name + "-plain", URL: srv.MCPURL, Protocol: "http", Enabled: true,
	}, zap.NewNop(), nil, &config.Config{}, db, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = plain.Disconnect() })
	stopCallbackServerAfterTest(t, name+"-plain")
	_, err = plain.StartOAuthFlowQuick(ctx)
	require.ErrorAs(t, err, &flowErr)
	assert.NotContains(t, flowErr.Suggestion, "headers")
}
