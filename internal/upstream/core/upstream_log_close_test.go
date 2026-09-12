package core

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// recordingCloser counts Close calls and delegates to the real sink closer, so
// the file Disconnect's own "Disconnecting from server" line opens is released
// before t.TempDir() cleanup (which fails on Windows if it is not).
type recordingCloser struct {
	real   io.Closer
	closed int
}

func (r *recordingCloser) Close() error { r.closed++; return r.real.Close() }

// Disconnect is the one path every teardown reaches (RemoveServer,
// ShutdownAll, reconnect), so it is where the per-server log sink is
// released — issue #1266. lumberjack reopens on the next write, so a
// reconnecting server loses nothing.
func TestDisconnect_ClosesTheUpstreamLogSink(t *testing.T) {
	cfg := &config.ServerConfig{Name: "svc", Protocol: "stdio", Command: "definitely-not-on-path", Enabled: false}
	c, err := NewClient("svc", cfg, zap.NewNop(), &config.LogConfig{LogDir: t.TempDir(), Level: "info", EnableFile: true}, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, c.upstreamLogCloser, "a file-backed upstream logger must come with its closer")

	rec := &recordingCloser{real: c.upstreamLogCloser}
	c.upstreamLogCloser = rec
	t.Cleanup(func() { _ = rec.real.Close() })

	require.NoError(t, c.Disconnect())
	require.Equal(t, 1, rec.closed, "Disconnect must release the per-server log sink")

	require.NoError(t, c.Disconnect())
	require.Equal(t, 2, rec.closed, "every Disconnect releases it again (reopen-on-write makes this idempotent)")
}
