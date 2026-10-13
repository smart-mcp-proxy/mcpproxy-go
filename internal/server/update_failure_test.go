package server

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// openUpdateFailureDB creates a standalone BBolt DB backing the real
// diagnostics counter store, so these tests assert durable persistence rather
// than a mock's call log (FR-011: 204 promises durability).
func openUpdateFailureDB(t *testing.T) *bbolt.DB {
	t.Helper()
	db, err := bbolt.Open(filepath.Join(t.TempDir(), "diag.db"), 0o600, &bbolt.Options{Timeout: 2 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, telemetry.EnsureDiagnosticsCountersBucket(db))
	return db
}

// clearTelemetryEnv neutralizes every env override the gate reads. CI is
// exported in this repo's CI runs, so tests that expect an ACTIVE gate must
// pin it empty or they pass locally and no-op in CI.
func clearTelemetryEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CI", "")
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")
}

func telemetryEnabledConfig(enabled bool) *config.Config {
	return &config.Config{Telemetry: &config.TelemetryConfig{Enabled: &enabled}}
}

func snapshotCount(t *testing.T, db *bbolt.DB, code string) int {
	t.Helper()
	snap, err := telemetry.NewDiagnosticsCounterStore().Snapshot(db)
	require.NoError(t, err)
	return snap.ErrorCodeCounts24h[code]
}

// TestRecordUpdateFailure_StageMapping — every stage lands on its own code and
// nothing else is touched (SC-003 on the core side).
func TestRecordUpdateFailure_StageMapping(t *testing.T) {
	cases := map[string]string{
		"appcast":  "MCPX_UPDATE_APPCAST_FAILED",
		"download": "MCPX_UPDATE_DOWNLOAD_FAILED",
		"install":  "MCPX_UPDATE_INSTALL_FAILED",
		"other":    "MCPX_UPDATE_OTHER_FAILED",
	}
	for stage, code := range cases {
		t.Run(stage, func(t *testing.T) {
			clearTelemetryEnv(t)
			db := openUpdateFailureDB(t)
			store := telemetry.NewDiagnosticsCounterStore()

			recorded, err := recordUpdateFailure(telemetryEnabledConfig(true), "v1.2.3", store, db, stage)

			require.NoError(t, err)
			assert.True(t, recorded)
			assert.Equal(t, 1, snapshotCount(t, db, code))

			snap, err := store.Snapshot(db)
			require.NoError(t, err)
			assert.Len(t, snap.ErrorCodeCounts24h, 1, "only the mapped code may be incremented")
		})
	}
}

// TestRecordUpdateFailure_UnknownStage — the handler validates first, but the
// seam refuses an out-of-enum stage too rather than recording a stray code.
func TestRecordUpdateFailure_UnknownStage(t *testing.T) {
	clearTelemetryEnv(t)
	db := openUpdateFailureDB(t)

	recorded, err := recordUpdateFailure(telemetryEnabledConfig(true), "v1.2.3",
		telemetry.NewDiagnosticsCounterStore(), db, "network")

	require.Error(t, err)
	assert.False(t, recorded)
	snap, err := telemetry.NewDiagnosticsCounterStore().Snapshot(db)
	require.NoError(t, err)
	assert.Empty(t, snap.ErrorCodeCounts24h)
}

// TestRecordUpdateFailure_GatesClosed — FR-013: each gate independently makes
// the call a silent no-op that persists nothing.
func TestRecordUpdateFailure_GatesClosed(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		cfg     *config.Config
		version string
	}{
		{name: "config opt-out", cfg: telemetryEnabledConfig(false), version: "v1.2.3"},
		{name: "nil config", cfg: nil, version: "v1.2.3"},
		{name: "DO_NOT_TRACK", env: map[string]string{"DO_NOT_TRACK": "1"}, cfg: telemetryEnabledConfig(true), version: "v1.2.3"},
		{name: "CI", env: map[string]string{"CI": "true"}, cfg: telemetryEnabledConfig(true), version: "v1.2.3"},
		{name: "MCPPROXY_TELEMETRY=false", env: map[string]string{"MCPPROXY_TELEMETRY": "false"}, cfg: telemetryEnabledConfig(true), version: "v1.2.3"},
		{name: "dev build (non-semver)", cfg: telemetryEnabledConfig(true), version: "dev"},
		{name: "empty version", cfg: telemetryEnabledConfig(true), version: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearTelemetryEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			db := openUpdateFailureDB(t)

			recorded, err := recordUpdateFailure(tt.cfg, tt.version,
				telemetry.NewDiagnosticsCounterStore(), db, "download")

			require.NoError(t, err, "a closed gate is a no-op, never an error")
			assert.False(t, recorded)
			snap, err := telemetry.NewDiagnosticsCounterStore().Snapshot(db)
			require.NoError(t, err)
			assert.Empty(t, snap.ErrorCodeCounts24h, "nothing may be persisted while the gate is closed")
		})
	}
}

// TestRecordUpdateFailure_NoStoreWired — short-lived CLI processes have no
// counter store; recording is a no-op, not an error.
func TestRecordUpdateFailure_NoStoreWired(t *testing.T) {
	clearTelemetryEnv(t)
	db := openUpdateFailureDB(t)

	recorded, err := recordUpdateFailure(telemetryEnabledConfig(true), "v1.2.3", nil, db, "download")
	require.NoError(t, err)
	assert.False(t, recorded)

	recorded, err = recordUpdateFailure(telemetryEnabledConfig(true), "v1.2.3",
		telemetry.NewDiagnosticsCounterStore(), nil, "download")
	require.NoError(t, err)
	assert.False(t, recorded)
}

// TestRecordUpdateFailure_OptOutIsNotDeferred — SC-005: an occurrence during
// opt-out can never become transmissible later, including after re-enabling.
func TestRecordUpdateFailure_OptOutIsNotDeferred(t *testing.T) {
	clearTelemetryEnv(t)
	db := openUpdateFailureDB(t)
	store := telemetry.NewDiagnosticsCounterStore()

	recorded, err := recordUpdateFailure(telemetryEnabledConfig(false), "v1.2.3", store, db, "install")
	require.NoError(t, err)
	require.False(t, recorded)

	// Re-enable and take the snapshot the heartbeat would take: the
	// opted-out occurrence must not reappear.
	snap, err := store.Snapshot(db)
	require.NoError(t, err)
	assert.Empty(t, snap.ErrorCodeCounts24h)

	// A later occurrence, with the gate open, counts exactly once.
	recorded, err = recordUpdateFailure(telemetryEnabledConfig(true), "v1.2.3", store, db, "install")
	require.NoError(t, err)
	require.True(t, recorded)
	assert.Equal(t, 1, snapshotCount(t, db, "MCPX_UPDATE_INSTALL_FAILED"))
}

// TestRecordUpdateFailure_PersistenceErrorPropagates — a failed write must
// surface so the handler can answer 500 instead of a durability-promising 204.
func TestRecordUpdateFailure_PersistenceErrorPropagates(t *testing.T) {
	clearTelemetryEnv(t)
	db := openUpdateFailureDB(t)
	require.NoError(t, db.Close())

	recorded, err := recordUpdateFailure(telemetryEnabledConfig(true), "v1.2.3",
		telemetry.NewDiagnosticsCounterStore(), db, "other")

	require.Error(t, err)
	assert.False(t, recorded)
}

// TestRecordUpdateFailure_CountsAccumulate — occurrences are counts, not flags.
func TestRecordUpdateFailure_CountsAccumulate(t *testing.T) {
	clearTelemetryEnv(t)
	db := openUpdateFailureDB(t)
	store := telemetry.NewDiagnosticsCounterStore()

	for i := 0; i < 3; i++ {
		recorded, err := recordUpdateFailure(telemetryEnabledConfig(true), "v1.2.3", store, db, "download")
		require.NoError(t, err)
		require.True(t, recorded)
	}
	assert.Equal(t, 3, snapshotCount(t, db, "MCPX_UPDATE_DOWNLOAD_FAILED"))
}
