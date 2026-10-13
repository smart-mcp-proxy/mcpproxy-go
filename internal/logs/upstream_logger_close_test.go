package logs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// The per-server log sink is a lumberjack writer that nothing ever closed:
// every server a core had logged for kept an open file handle until the
// process exited. On Windows that also pinned the file (issue #1266: CI's
// t.TempDir() cleanup failed on server-*.log). The logger now comes with a
// closer, and — because lumberjack reopens lazily on the next Write — closing
// it while a server is disconnected costs nothing when it reconnects.
func TestNewUpstreamServerLogger_CloserReleasesTheFileAndWritesReopen(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.LogConfig{LogDir: dir, Level: "info", EnableFile: true, MaxSize: 1, MaxBackups: 1, MaxAge: 1}

	logger, closer, err := NewUpstreamServerLogger(cfg, "svc")
	require.NoError(t, err)
	require.NotNil(t, closer)

	logger.Info("first line")
	require.NoError(t, logger.Sync())
	path := filepath.Join(dir, ServerLogFilename("svc"))
	require.FileExists(t, path)

	require.NoError(t, closer.Close())
	require.NoError(t, closer.Close(), "closing twice must be harmless")

	// Renaming an open file fails on Windows; after Close it must succeed on
	// every platform. (On Unix this is a weaker check, but the Windows CI leg
	// runs this test too.)
	moved := path + ".moved"
	require.NoError(t, os.Rename(path, moved))

	// A write after Close reopens the sink at the configured path.
	logger.Info("second line")
	require.NoError(t, logger.Sync())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.True(t, strings.Contains(string(data), "second line"), "post-close write must land in a reopened file: %q", data)

	require.NoError(t, closer.Close())
}

// CreateUpstreamServerLogger keeps its old shape for callers that cannot own a
// closer, and must still hand back a usable logger. It deliberately never
// writes: lumberjack opens the file on the first Write, and with no closer in
// hand that handle would outlive the test — on Windows, failing t.TempDir()'s
// cleanup with exactly the leak this change fixes.
func TestCreateUpstreamServerLogger_StillWorksWithoutACloser(t *testing.T) {
	cfg := &config.LogConfig{LogDir: t.TempDir(), Level: "info", EnableFile: true}
	logger, err := CreateUpstreamServerLogger(cfg, "svc")
	require.NoError(t, err)
	require.NotNil(t, logger)
	require.True(t, logger.Core().Enabled(zap.InfoLevel))
}
