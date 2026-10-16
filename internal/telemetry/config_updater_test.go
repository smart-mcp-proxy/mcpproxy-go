package telemetry

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// #1592: with a ConfigUpdater installed, the anonymous id, rotation and upgrade
// cursor go through the config commit and never call config.SaveConfig.
func TestConfigUpdater_RoutesWritesAndSkipsDirectSave(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "mcp_config.json")
	cfg := &config.Config{Telemetry: &config.TelemetryConfig{}}
	svc := New(cfg, cfgPath, "v1.2.3", "personal", zap.NewNop())

	live := &config.TelemetryConfig{}
	var reasons []string
	svc.SetConfigUpdater(func(reason string, fn func(*config.TelemetryConfig) bool) error {
		// The real commit ends in NotifyConfigChanged, which takes s.mu: calling
		// it here proves the service released s.mu around the call.
		fn(live)
		svc.NotifyConfigChanged(cfg)
		reasons = append(reasons, reason)
		return nil
	})

	svc.ensureAnonymousID()
	require.NotEmpty(t, live.AnonymousID)
	assert.NotEmpty(t, live.AnonymousIDCreatedAt)

	svc.advanceUpgradeFunnel()
	assert.Equal(t, "v1.2.3", live.LastReportedVersion)

	old := time.Now().UTC().Add(-400 * 24 * time.Hour)
	cfg.Telemetry.AnonymousIDCreatedAt = old.Format(time.RFC3339)
	prev := live.AnonymousID
	svc.maybeRotateAnonymousID(cfg, time.Now().UTC())
	assert.NotEqual(t, prev, live.AnonymousID)

	assert.GreaterOrEqual(t, len(reasons), 3)
	_, err := os.Stat(cfgPath)
	assert.True(t, os.IsNotExist(err), "config file must not be written outside the commit")
}
