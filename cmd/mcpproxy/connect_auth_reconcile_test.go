//go:build !server

package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
)

// A browser-login connect, refused or not, never finalises a staged key
// rotation in the offline path either.
func TestConnectAuthFlag_OfflineOAuthDoesNotReconcileAKeyRotation(t *testing.T) {
	for _, featureOn := range []bool{false, true} {
		home := connectAuthEnv(t, featureOn)
		path := seedClaudeCode(t, home)
		cfg, err := loadConnectConfig()
		require.NoError(t, err)
		backend, err := newOfflineConnectBackend(cfg)
		require.NoError(t, err)

		first, err := backend.connect("claude-code", "", false, connect.CredentialIntent{})
		require.NoError(t, err)
		require.True(t, first.Success)
		m := backend.clients.ConnectMinter()
		issued, err := m.Issue("claude-code", connect.CredentialIntent{})
		require.NoError(t, err)
		m.Release("claude-code", issued)
		require.NoError(t, os.WriteFile(path, []byte(`{"mcpServers":{"mcpproxy":{"type":"http","url":"http://127.0.0.1:1/mcp","headers":{"X-API-Key":"`+issued.Secret+`"}}}}`), 0o644))
		view, err := backend.clients.Get("claude-code")
		require.NoError(t, err)
		require.True(t, view.RotationPending)

		_, _ = backend.connect("claude-code", "", true, connect.CredentialIntent{Auth: connect.AuthOAuth})
		view, err = backend.clients.Get("claude-code")
		require.NoError(t, err)
		require.True(t, view.RotationPending, "featureOn=%v: the staged rotation must survive an oauth connect", featureOn)
		backend.close()
	}
}
