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
	live.AnonymousIDCreatedAt = old.Format(time.RFC3339)
	prev := live.AnonymousID
	svc.maybeRotateAnonymousID(cfg, time.Now().UTC())
	assert.NotEqual(t, prev, live.AnonymousID)

	assert.GreaterOrEqual(t, len(reasons), 3)
	_, err := os.Stat(cfgPath)
	assert.True(t, os.IsNotExist(err), "config file must not be written outside the commit")
}

// commitLikeUpdater mimics runtime.Commit: it clones the PUBLISHED block, runs
// fn on the clone and only "persists" (and publishes) when fn reports a change.
// Mutating the published pointer before the commit therefore makes fn a no-op,
// which is exactly the lost-write bug (#1592).
type commitLikeUpdater struct {
	svc       *Service
	published *config.Config
	disk      config.TelemetryConfig
}

func (c *commitLikeUpdater) update(_ string, fn func(*config.TelemetryConfig) bool) error {
	clone := *c.published
	tc := config.TelemetryConfig{}
	if c.published.Telemetry != nil {
		tc = *c.published.Telemetry
	}
	if !fn(&tc) {
		return nil
	}
	clone.Telemetry = &tc
	c.disk = tc
	c.published = &clone
	c.svc.NotifyConfigChanged(&clone)
	return nil
}

func TestConfigUpdater_PersistsGeneratedIDAndCursorToDisk(t *testing.T) {
	cfg := &config.Config{Telemetry: &config.TelemetryConfig{}}
	svc := New(cfg, "", "v1.2.3", "personal", zap.NewNop())
	c := &commitLikeUpdater{svc: svc, published: cfg}
	svc.SetConfigUpdater(c.update)

	svc.ensureAnonymousID()
	assert.NotEmpty(t, c.disk.AnonymousID, "generated id must reach the commit")
	assert.NotEmpty(t, c.disk.AnonymousIDCreatedAt)
	assert.Empty(t, cfg.Telemetry.AnonymousID, "the previously published config must not be mutated")

	svc.advanceUpgradeFunnel()
	assert.Equal(t, "v1.2.3", c.disk.LastReportedVersion)
}

func TestConfigUpdater_RotationPersistsToDisk(t *testing.T) {
	old := time.Now().UTC().Add(-400 * 24 * time.Hour).Format(time.RFC3339)
	cfg := &config.Config{Telemetry: &config.TelemetryConfig{AnonymousID: "old-id", AnonymousIDCreatedAt: old}}
	svc := New(cfg, "", "v1.2.3", "personal", zap.NewNop())
	c := &commitLikeUpdater{svc: svc, published: cfg}
	svc.SetConfigUpdater(c.update)

	svc.maybeRotateAnonymousID(cfg, time.Now().UTC())
	assert.NotEmpty(t, c.disk.AnonymousID)
	assert.NotEqual(t, "old-id", c.disk.AnonymousID)
	assert.Equal(t, c.disk.AnonymousID, cfg.Telemetry.AnonymousID, "payload snapshot reports the committed id")
}

// A heartbeat holding config A must not overwrite the identity a reload
// installed in config B.
func TestConfigUpdater_StaleRotationDoesNotOverwriteNewerIdentity(t *testing.T) {
	old := time.Now().UTC().Add(-400 * 24 * time.Hour).Format(time.RFC3339)
	fresh := time.Now().UTC().Format(time.RFC3339)
	stale := &config.Config{Telemetry: &config.TelemetryConfig{AnonymousID: "old-id", AnonymousIDCreatedAt: old}}
	live := &config.Config{Telemetry: &config.TelemetryConfig{AnonymousID: "newer-id", AnonymousIDCreatedAt: fresh}}
	svc := New(live, "", "v1.2.3", "personal", zap.NewNop())
	c := &commitLikeUpdater{svc: svc, published: live, disk: *live.Telemetry}
	svc.SetConfigUpdater(c.update)

	svc.maybeRotateAnonymousID(stale, time.Now().UTC())
	assert.Equal(t, "newer-id", c.disk.AnonymousID)
	assert.Equal(t, fresh, c.disk.AnonymousIDCreatedAt)
	assert.Equal(t, "newer-id", c.published.Telemetry.AnonymousID)
}
