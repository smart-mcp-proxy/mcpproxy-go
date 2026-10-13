//go:build server

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Spec 107 T112: docs/operations/deploying-for-a-team.json is the config
// example quoted (in fragments) by docs/operations/deploying-for-a-team.md.
// This test loads and validates the real file on disk so the doc's example
// can never silently drift from what Config.Validate actually accepts.
func TestDeployGuideExample_LoadsAndValidates(t *testing.T) {
	t.Setenv("MCPPROXY_CRED_KEY", "test-credential-encryption-key-32bytes!")
	t.Setenv("OIDC_CLIENT_SECRET", "test-oidc-client-secret")

	path := deployGuideExamplePath(t)
	cfg, err := LoadFromFile(path)
	require.NoError(t, err, "docs/operations/examples/deploying-for-a-team.json must load")

	require.NoError(t, cfg.Validate(), "docs/operations/examples/deploying-for-a-team.json must pass Config.Validate")

	require.True(t, isServerEditionBuild)
	require.NotNil(t, cfg.ServerEdition)
	require.True(t, cfg.ServerEdition.Enabled)
	require.Equal(t, "https://mcp.example.com", cfg.ServerEdition.PublicURL)
	require.Equal(t, SessionCookieSecureAuto, cfg.ServerEdition.SessionCookieSecure)
	require.NotNil(t, cfg.ServerEdition.OAuth)
	require.Equal(t, "oidc", cfg.ServerEdition.OAuth.Provider)
	require.NotNil(t, cfg.ServerEdition.Access)
	require.Equal(t, []string{"github", "ast-grep"}, cfg.ServerEdition.Access.GroupServers["mcpproxy-engineering"])
	require.Equal(t, []string{"*"}, cfg.ServerEdition.Access.GroupServers["mcpproxy-admins"])

	require.Equal(t, []string{"10.42.0.0/16"}, cfg.TrustedProxies)
	require.True(t, cfg.RequireMCPAuth)

	require.NotNil(t, cfg.AuditLog)
	require.NotNil(t, cfg.AuditLog.Enabled)
	require.True(t, *cfg.AuditLog.Enabled)
	require.NotNil(t, cfg.AuditLog.Stdout)
	require.True(t, *cfg.AuditLog.Stdout)

	resolved, warning, effErr := EffectiveAuditLog(cfg, TransportHTTP)
	require.NoError(t, effErr)
	require.Empty(t, warning)
	require.True(t, resolved.Enabled)
	require.True(t, resolved.Stdout)
}

// deployGuideExamplePath locates docs/operations/examples/deploying-for-a-team.json
// relative to the repo root (two levels up from internal/config).
func deployGuideExamplePath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	path := filepath.Join(wd, "..", "..", "docs", "operations", "examples", "deploying-for-a-team.json")
	_, statErr := os.Stat(path)
	require.NoError(t, statErr, "expected example config at %s", path)
	return path
}
