package upstream

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

func launchMarkerConfig(t *testing.T, name, marker string) *config.ServerConfig {
	t.Helper()
	return &config.ServerConfig{
		Name: name, Command: "/bin/sh", Protocol: "stdio", Enabled: true,
		Args: []string{"-c", "touch " + marker + "; exec cat"},
	}
}

// A goroutine that fetched the client before RemoveServer must not be able to
// dial it afterwards: Disconnect resets the client to a connectable state, so
// removal has to retire it permanently.
func TestRetiredClient_RemovedBetweenLookupAndConnect_NeverDials(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "launched")
	m := NewManager(zap.NewNop(), &config.Config{}, nil, secret.NewResolver(), nil)
	sc := launchMarkerConfig(t, "rm", marker)
	require.NoError(t, m.AddServerConfig("rm", sc))

	stale, ok := m.GetClient("rm") // lookup done, dial not yet started
	require.True(t, ok)

	m.RemoveServer("rm") // removal + disconnect complete

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.ErrorIs(t, stale.Connect(ctx), managed.ErrClientRetired)
	require.NoFileExists(t, marker, "a removed upstream's process must never launch")
	require.NotEqual(t, types.StateReady, stale.GetState())
	require.NotEqual(t, types.StateConnecting, stale.GetState())

	// The manager-level connect path treats it as a quiet no-op.
	require.NoError(t, m.ConnectServer("rm", sc))
	require.NoFileExists(t, marker)
	_, exists := m.GetClient("rm")
	require.False(t, exists)
}

func TestRetiredClient_ReplacedBetweenLookupAndConnect_NeverDials(t *testing.T) {
	dir := t.TempDir()
	oldMarker := filepath.Join(dir, "old")
	newMarker := filepath.Join(dir, "new")
	m := NewManager(zap.NewNop(), &config.Config{}, nil, secret.NewResolver(), nil)
	require.NoError(t, m.AddServerConfig("rp", launchMarkerConfig(t, "rp", oldMarker)))
	stale, _ := m.GetClient("rp")

	newCfg := launchMarkerConfig(t, "rp", newMarker) // different args => replaced
	require.NoError(t, m.AddServerConfig("rp", newCfg))
	cur, _ := m.GetClient("rp")
	require.NotSame(t, stale, cur)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.ErrorIs(t, stale.Connect(ctx), managed.ErrClientRetired)
	require.NoFileExists(t, oldMarker)
	require.False(t, cur.IsRetired(), "the current client is untouched")
	_ = os.Remove(newMarker)
}
