package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/logs"
)

// newLogsTestServer builds a minimal Server whose per-server log directory is an
// empty temp dir, and registers `serverName` with the upstream manager WITHOUT
// connecting it (AddServerConfig), so GetServerLogs passes its existence check
// while no log file has ever been written.
func newLogsTestServer(t *testing.T, serverName string) (*Server, string) {
	t.Helper()

	logDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.Logging.LogDir = logDir

	srv, err := NewServer(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown() })

	require.NoError(t, srv.runtime.UpstreamManager().AddServerConfig(serverName, &config.ServerConfig{
		Name:     serverName,
		Protocol: "http",
		URL:      "https://example.invalid/mcp",
		Enabled:  true,
	}))

	return srv, logDir
}

// TestGetServerLogs_MissingFileReturnsEmptyNotError pins the UX-audit F12 fix.
//
// The per-server log file is created lazily by lumberjack and is level-gated
// (internal/logs/logger.go), so a healthy, connected server that never logged
// anything at or above the configured level has NO file on disk. Reporting that
// as an error made the Logs tab claim "server may not have run yet" seconds
// after a verified successful tool call through that same server. An absent
// file means "no entries yet", not a failure.
func TestGetServerLogs_MissingFileReturnsEmptyNotError(t *testing.T) {
	const serverName = "never-logged"
	srv, logDir := newLogsTestServer(t, serverName)

	logFile := filepath.Join(logDir, logs.ServerLogFilename(serverName))
	_, statErr := os.Stat(logFile)
	require.True(t, os.IsNotExist(statErr), "precondition: %s must not exist", logFile)

	entries, err := srv.GetServerLogs(serverName, 10)
	require.NoError(t, err, "an absent per-server log file is 'no entries yet', not an error")
	require.NotNil(t, entries, "must marshal as [] rather than null")
	require.Empty(t, entries)
}

// TestGetServerLogs_UnknownServerStillErrors guards the other half: only the
// ENOENT branch is softened. A name the proxy does not know is still an error,
// so the fix cannot mask a genuine "wrong server" mistake as an empty log.
func TestGetServerLogs_UnknownServerStillErrors(t *testing.T) {
	srv, _ := newLogsTestServer(t, "known")

	_, err := srv.GetServerLogs("no-such-server", 10)
	require.Error(t, err)
	require.Contains(t, err.Error(), "server not found")
}
