package runtime

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// #1592: the startup telemetry fields are written through the config commit,
// so a concurrent disk-writing commit can neither drop them from the file nor
// write a live config that lacks them.
func TestUpdateTelemetry_SurvivesConcurrentSaveConfiguration(t *testing.T) {
	rt, cfgPath := newSpec116Runtime(t, nil, disabledStdio("a"))

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = rt.SaveConfiguration()
		}()
	}
	require.NoError(t, rt.UpdateTelemetry("startup_outcome", func(tc *config.TelemetryConfig) bool {
		tc.LastStartupOutcome = "success"
		tc.NoticeShown = true
		return true
	}))
	wg.Wait()
	require.NoError(t, rt.SaveConfiguration())

	live, err := rt.GetConfig()
	require.NoError(t, err)
	require.NotNil(t, live.Telemetry)
	assert.Equal(t, "success", live.Telemetry.LastStartupOutcome)
	assert.True(t, live.Telemetry.NoticeShown)

	onDisk, err := config.DecodeConfigFile(cfgPath)
	require.NoError(t, err)
	require.NotNil(t, onDisk.Telemetry)
	assert.Equal(t, "success", onDisk.Telemetry.LastStartupOutcome)
	assert.True(t, onDisk.Telemetry.NoticeShown)
}

func TestUpdateTelemetry_NoChangeIsNoCommit(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil, disabledStdio("a"))
	before := rt.ConfigSnapshot().Version
	require.NoError(t, rt.UpdateTelemetry("noop", func(*config.TelemetryConfig) bool { return false }))
	assert.Equal(t, before, rt.ConfigSnapshot().Version)
}

// #1592: the anonymous id / rotation reach the on-disk config when written by
// the telemetry service through the real commit (the service shares the
// runtime's published config pointer).
func TestTelemetryService_RotationPersistsThroughCommit(t *testing.T) {
	rt, cfgPath := newSpec116Runtime(t, nil, disabledStdio("a"))
	old := time.Now().UTC().Add(-400 * 24 * time.Hour).Format(time.RFC3339)
	require.NoError(t, rt.UpdateTelemetry("seed", func(tc *config.TelemetryConfig) bool {
		tc.AnonymousID = "old-id"
		tc.AnonymousIDCreatedAt = old
		return true
	}))

	rt.SetTelemetry("v1.2.3", "personal")
	_ = rt.telemetryService.BuildPayload()

	live, err := rt.GetConfig()
	require.NoError(t, err)
	require.NotNil(t, live.Telemetry)
	assert.NotEqual(t, "old-id", live.Telemetry.AnonymousID)

	onDisk, err := config.DecodeConfigFile(cfgPath)
	require.NoError(t, err)
	require.NotNil(t, onDisk.Telemetry)
	assert.Equal(t, live.Telemetry.AnonymousID, onDisk.Telemetry.AnonymousID)
}
