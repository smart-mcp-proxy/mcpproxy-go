package storage_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// The one-shot informational baseline sweep is gated purely by the presence of
// this marker, so absence must read as (nil, nil) and a saved marker must
// survive a reopen of the database.
func TestBaselineSweepMarker_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	mgr, err := storage.NewManager(dir, zap.NewNop().Sugar())
	require.NoError(t, err)

	state, err := mgr.LoadBaselineSweepState()
	require.NoError(t, err)
	assert.Nil(t, state, "absent marker must read as nil, not an error")

	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, mgr.SaveBaselineSweepState(&storage.BaselineSweepState{
		Version:        "v1.2.3",
		CompletedAt:    now,
		ServersScanned: 4,
		Findings:       7,
	}))

	require.NoError(t, mgr.Close())

	reopened, err := storage.NewManager(dir, zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopened.Close() })

	state, err = reopened.LoadBaselineSweepState()
	require.NoError(t, err)
	require.NotNil(t, state)
	assert.Equal(t, "v1.2.3", state.Version)
	assert.True(t, state.CompletedAt.Equal(now))
	assert.Equal(t, 4, state.ServersScanned)
	assert.Equal(t, 7, state.Findings)
}

func TestBaselineSweepMarker_NilStateRejected(t *testing.T) {
	mgr, err := storage.NewManager(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	assert.Error(t, mgr.SaveBaselineSweepState(nil))
}
