package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/logs"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// newLogsTestServer builds a minimal Server whose per-server log directory is an
// empty temp dir, and registers `serverName` with the upstream manager WITHOUT
// connecting it, so GetServerLogs passes its existence check while no log file
// has ever been written. Callers must still invoke ensureLogsClient immediately
// before GetServerLogs — see that helper for why registering once is not enough.
//
// The client is registered only once the runtime reports PhaseReady. Background
// initialization runs LoadConfiguredServers, which snapshots the manager's
// clients, diffs them against cfg.Servers and REMOVES the difference in
// goroutines, and only then flips the phase. A client added before that diff is
// on the removal list; a client added after it is not, because the diff never
// sees it and the supervisor's periodic reconcile only ever removes names it
// found in a config snapshot. Registering before the phase flip is what made
// this test flake on a loaded machine: the removal landed between construction
// and the call, and its "Disconnecting from server" line created the very log
// file this test asserts is absent.
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

	require.Eventually(t, func() bool {
		return srv.runtime.CurrentPhase() == runtime.PhaseReady
	}, 10*time.Second, 10*time.Millisecond, "runtime never reached PhaseReady")

	ensureLogsClient(t, srv, serverName)
	return srv, logDir
}

// ensureLogsClient registers the upstream client GetServerLogs resolves, and
// must be called IMMEDIATELY before GetServerLogs.
//
// This mirrors ensureFixerClient in diagnostics_fixers_test.go: a client the
// supervisor never asked for survives only as long as nothing reconciles it
// away, so re-registering at the call site closes the window to the few
// microseconds before GetServerLogs. Re-registering an unchanged config keeps
// the existing client (Manager.AddServerConfig compares first), so this never
// disconnects or logs anything itself.
//
// Putting the server in cfg.Servers instead does not work (verified in
// diagnostics_fixers_test.go): LoadConfiguredServers removes the client for a
// DISABLED entry too, and an ENABLED entry spawns a child and a per-server log
// writer that outlive Shutdown and then race t.TempDir()'s RemoveAll. A disabled
// out-of-band client is the combination with no background goroutine and no
// log writes.
func ensureLogsClient(t *testing.T, srv *Server, serverName string) {
	t.Helper()
	require.NoError(t, srv.runtime.UpstreamManager().AddServerConfig(serverName, &config.ServerConfig{
		Name:     serverName,
		Protocol: "stdio",
		Command:  "definitely-not-on-path",
		Enabled:  false,
	}))
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

	ensureLogsClient(t, srv, serverName)
	entries, err := srv.GetServerLogs(serverName, 10)
	require.NoError(t, err, "an absent per-server log file is 'no entries yet', not an error")
	require.NotNil(t, entries, "must marshal as [] rather than null")
	require.Empty(t, entries)
}

// TestGetServerLogs_UnknownServerStillErrors guards the other half: only the
// ENOENT branch is softened. A name the proxy does not know is still an error,
// so the fix cannot mask a genuine "wrong server" mistake as an empty log.
func TestGetServerLogs_UnknownServerStillErrors(t *testing.T) {
	const serverName = "known"
	srv, _ := newLogsTestServer(t, serverName)

	ensureLogsClient(t, srv, serverName)
	_, err := srv.GetServerLogs("no-such-server", 10)
	require.Error(t, err)
	require.Contains(t, err.Error(), "server not found")
}
