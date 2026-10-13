//go:build !server

package server

// Spec 107 T049 (US2, FR-029) — personal-build twin of mcp_auth_forced_test.go.
//
// The personal build carries `server_edition` as an opaque json.RawMessage
// (FR-040) and never interprets it: config.EffectiveRequireMCPAuth (T052 stub)
// returns cfg.RequireMCPAuth, the /mcp middleware keeps the #1148 back-compat
// anonymous administrator, and no boot notice is logged — even when the
// carrier says `"enabled": true`.
//
// Compile-red until T052 lands the `_stub.go` accessor.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// personalEditionCarrier builds the opaque server_edition carrier the personal
// build keeps: the only way to construct one from Go code is through its
// UnmarshalJSON.
func personalEditionCarrier(t *testing.T) *config.ServerEditionConfig {
	t.Helper()
	carrier := &config.ServerEditionConfig{}
	require.NoError(t, json.Unmarshal([]byte(`{"enabled":true,"admin_emails":["admin@example.com"],"oauth":{"provider":"google","client_id":"id","client_secret":"secret"}}`), carrier))
	return carrier
}

func personalForcedAuthConfig(t *testing.T, requireMCPAuth bool) *config.Config {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.APIKey = "test-api-key"
	cfg.RequireMCPAuth = requireMCPAuth
	cfg.ServerEdition = personalEditionCarrier(t)
	return cfg
}

// TestEffectiveRequireMCPAuth_PersonalBuild: the accessor is the configured
// value, whatever the opaque carrier says.
func TestEffectiveRequireMCPAuth_PersonalBuild(t *testing.T) {
	for _, configured := range []bool{false, true} {
		cfg := &config.Config{RequireMCPAuth: configured, ServerEdition: personalEditionCarrier(t)}
		assert.Equal(t, configured, config.EffectiveRequireMCPAuth(cfg), "configured=%v", configured)
	}
	assert.False(t, config.EffectiveRequireMCPAuth(&config.Config{}))
	assert.True(t, config.EffectiveRequireMCPAuth(&config.Config{RequireMCPAuth: true}))
}

// TestMCPAuthMiddleware_PersonalBuildIgnoresCarrier: the personal edition is
// unaffected by the block (FR-040 opaque carrier) — no forced auth, no notice.
func TestMCPAuthMiddleware_PersonalBuildIgnoresCarrier(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	srv, err := NewServer(personalForcedAuthConfig(t, false), zap.New(core))
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown() })

	t.Run("no credential keeps the back-compat anonymous admin", func(t *testing.T) {
		seen, code := captureAuthContext(t, srv, httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody))
		require.Equal(t, http.StatusOK, code)
		require.NotNil(t, seen)
		assert.True(t, seen.IsAdmin())
		assert.True(t, seen.Anonymous)
	})

	t.Run("unrecognised bearer keeps the back-compat anonymous admin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
		req.Header.Set("Authorization", "Bearer not-a-credential")
		seen, code := captureAuthContext(t, srv, req)
		require.Equal(t, http.StatusOK, code)
		require.NotNil(t, seen)
		assert.True(t, seen.Anonymous)
	})

	t.Run("no boot notice is logged", func(t *testing.T) {
		for _, e := range logs.All() {
			assert.False(t, strings.Contains(e.Message, "require_mcp_auth") && strings.Contains(e.Message, "overridden"),
				"personal build must not log the server-edition override notice: %q", e.Message)
		}
	})
}
