package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestConfigServerInfoProvider_UsesLiveConfig is the MCP-2123 regression guard.
//
// configServerInfoProvider used to iterate a config snapshot captured at server
// boot. Servers added at runtime (e.g. from the official registry, whose names
// look like "com.pulsemcp/google-flights") were absent from that snapshot, so
// GetServerInfo returned "not found". The scanner then ran with no ServerInfo:
// source resolution stayed "none", no tools.json was exported, and Pass 2 was
// skipped with "No server info available" — exactly the reported defect.
//
// The provider must resolve server info against the LIVE config.
func TestConfigServerInfoProvider_UsesLiveConfig(t *testing.T) {
	bootSnapshot := &config.Config{
		Servers: []*config.ServerConfig{
			{Name: "boot-server", Protocol: "stdio"},
		},
	}

	// Simulates a server added at runtime after boot — present in the live
	// config but not in the snapshot the provider was constructed with.
	liveConfig := &config.Config{
		Servers: []*config.ServerConfig{
			{Name: "boot-server", Protocol: "stdio"},
			{
				Name:     "com.pulsemcp/google-flights",
				Protocol: "stdio",
				Command:  "npx",
				Args:     []string{"-y", "@pulsemcp/google-flights"},
			},
		},
	}

	p := &configServerInfoProvider{
		cfg:        bootSnapshot,
		liveConfig: func() *config.Config { return liveConfig },
	}

	info, err := p.GetServerInfo("com.pulsemcp/google-flights")
	require.NoError(t, err, "runtime-added server must be resolvable via live config")
	require.NotNil(t, info)
	assert.Equal(t, "com.pulsemcp/google-flights", info.Name)
	assert.Equal(t, "stdio", info.Protocol)
	assert.Equal(t, "npx", info.Command)
}

// TestConfigServerInfoProvider_FallsBackToSnapshot ensures the provider still
// works when no live-config accessor is wired (defensive default).
func TestConfigServerInfoProvider_FallsBackToSnapshot(t *testing.T) {
	p := &configServerInfoProvider{
		cfg: &config.Config{
			Servers: []*config.ServerConfig{{Name: "only-server", Protocol: "stdio"}},
		},
	}

	info, err := p.GetServerInfo("only-server")
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "only-server", info.Name)

	_, err = p.GetServerInfo("missing")
	require.Error(t, err)
}
