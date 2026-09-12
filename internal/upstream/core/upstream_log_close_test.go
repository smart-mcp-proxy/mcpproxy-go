package core

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

type recordingCloser struct{ closed int }

func (r *recordingCloser) Close() error { r.closed++; return nil }

// Disconnect is the one path every teardown reaches (RemoveServer,
// ShutdownAll, reconnect), so it is where the per-server log sink is
// released — issue #1266. lumberjack reopens on the next write, so a
// reconnecting server loses nothing.
func TestDisconnect_ClosesTheUpstreamLogSink(t *testing.T) {
	cfg := &config.ServerConfig{Name: "svc", Protocol: "stdio", Command: "definitely-not-on-path", Enabled: false}
	c, err := NewClient("svc", cfg, zap.NewNop(), &config.LogConfig{LogDir: t.TempDir(), Level: "info", EnableFile: true}, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, c.upstreamLogCloser, "a file-backed upstream logger must come with its closer")

	rec := &recordingCloser{}
	c.upstreamLogCloser = rec

	require.NoError(t, c.Disconnect())
	require.Equal(t, 1, rec.closed, "Disconnect must release the per-server log sink")

	require.NoError(t, c.Disconnect())
	require.Equal(t, 2, rec.closed, "every Disconnect releases it again (reopen-on-write makes this idempotent)")
}
