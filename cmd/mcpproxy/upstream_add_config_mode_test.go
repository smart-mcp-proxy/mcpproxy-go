package main

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUpstreamAddConfigModeQuarantineFollowsTrustMode: `upstream add` must land
// in the same state with and without a running daemon. The REST path derives the
// add-time quarantine default from the trust tier
// (Config.QuarantineDefaultForServer — auto is admitted unquarantined); the
// no-daemon config path hardcoded quarantined=true, so the identical command
// produced a different server depending on whether the daemon happened to be up.
func TestUpstreamAddConfigModeQuarantineFollowsTrustMode(t *testing.T) {
	addServer := func(t *testing.T, trustMode string, explicit *bool) *config.ServerConfig {
		t.Helper()
		cfg := config.DefaultConfig()
		cfg.DataDir = t.TempDir()
		req := &cliclient.AddServerRequest{
			Name:        "srv",
			URL:         "https://example.com/mcp",
			Protocol:    "streamable-http",
			TrustMode:   trustMode,
			Quarantined: explicit,
		}
		require.NoError(t, runUpstreamAddConfigMode(req, cfg))
		require.Len(t, cfg.Servers, 1)
		return cfg.Servers[0]
	}

	t.Run("auto is admitted unquarantined", func(t *testing.T) {
		srv := addServer(t, "auto", nil)
		assert.Equal(t, "auto", srv.TrustMode)
		assert.False(t, srv.Quarantined, "trust_mode auto must not be quarantined on add (matches the daemon path)")
	})

	for _, mode := range []string{"scan", "manual", ""} {
		t.Run("quarantined for trust_mode "+mode, func(t *testing.T) {
			assert.True(t, addServer(t, mode, nil).Quarantined)
		})
	}

	t.Run("explicit --no-quarantine still wins", func(t *testing.T) {
		no := false
		assert.False(t, addServer(t, "manual", &no).Quarantined)
	})

	t.Run("explicit quarantine wins over auto", func(t *testing.T) {
		yes := true
		assert.True(t, addServer(t, "auto", &yes).Quarantined)
	})
}

// TestParseAddJSONTrustMode: `upstream add-json` silently dropped trust_mode —
// the command reported success and persisted the fail-closed default while the
// operator believed the requested tier had been applied.
func TestParseAddJSONTrustMode(t *testing.T) {
	req, err := parseAddJSONRequest("srv", `{"url":"https://example.com/mcp","trust_mode":"scan"}`)
	require.NoError(t, err)
	assert.Equal(t, "scan", req.TrustMode, "add-json must carry trust_mode through")
	assert.Equal(t, "streamable-http", req.Protocol)

	t.Run("bogus value refused like every other write seam", func(t *testing.T) {
		_, err := parseAddJSONRequest("srv", `{"url":"https://example.com/mcp","trust_mode":"yolo"}`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "auto, scan, manual")
	})

	t.Run("omitted means inherit", func(t *testing.T) {
		req, err := parseAddJSONRequest("srv", `{"command":"echo"}`)
		require.NoError(t, err)
		assert.Equal(t, "", req.TrustMode)
		assert.Equal(t, "stdio", req.Protocol)
	})
}
