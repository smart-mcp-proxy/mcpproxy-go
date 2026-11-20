package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 126 FR-033 (T003 f, T004): server construction records the install
// anchor on a store that was empty at open, with telemetry DISABLED, before
// telemetry is wired; a later start keeps it; and the schema-17 day_two block
// reads it.
func TestServerStartup_RecordsInstallAnchorWithTelemetryDisabled(t *testing.T) {
	restoreScannerWiring(t)
	dir, err := os.MkdirTemp("", "install-anchor-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	off := false
	newCfg := func() (*config.Config, string) {
		cfg := config.DefaultConfig()
		cfg.DataDir = dir
		cfg.Listen = "127.0.0.1:0"
		cfg.APIKey = "install-anchor-key"
		cfg.Telemetry = &config.TelemetryConfig{Enabled: &off}
		path := filepath.Join(dir, "mcp_config.json")
		require.NoError(t, config.SaveConfig(cfg, path))
		return cfg, path
	}

	cfg, path := newCfg()
	before := time.Now().UTC()
	srv, err := NewServerWithConfigPath(cfg, path, zap.NewNop())
	require.NoError(t, err)
	anchor, ok, err := srv.runtime.StorageManager().InstallAnchor()
	require.NoError(t, err)
	require.True(t, ok, "a new data directory records the anchor at startup, telemetry off")
	require.False(t, anchor.Before(before.Add(-time.Second)))

	// The first heartbeat of the first process: day_two and the first
	// counter window (what the first send drains) both say fresh / d0_6.
	p := srv.runtime.TelemetryService().BuildPayload()
	require.NotNil(t, p.DayTwo)
	require.Equal(t, "fresh", p.DayTwo.InstallOrigin)
	require.Equal(t, "d0_6", p.DayTwo.InstallAge)
	window := srv.runtime.TelemetryRegistry().Snapshot().UpstreamOutcomes
	require.Equal(t, "d0_6", window.WindowAge)
	require.Equal(t, "full", window.Coverage)
	require.NoError(t, srv.Shutdown())
	time.Sleep(50 * time.Millisecond)

	cfg2, path2 := newCfg()
	srv2, err := NewServerWithConfigPath(cfg2, path2, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = srv2.Shutdown()
		time.Sleep(50 * time.Millisecond)
	})
	again, ok, err := srv2.runtime.StorageManager().InstallAnchor()
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, again.Equal(anchor), "a later start never changes the anchor")
}
