package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/logs"
)

// newFixerTestServer builds a real Server through the production constructor —
// the ONLY thing that installs the runtime-backed diagnostics fixers — with a
// scratch data dir and a scratch log dir, plus one upstream client registered
// (without connecting) so GetServerLogs can resolve it.
func newFixerTestServer(t *testing.T, serverName, logDir string) *Server {
	t.Helper()

	disabled := false
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.Logging.LogDir = logDir
	// Keep the heartbeat off: this test has no business talking to the network.
	cfg.Telemetry = &config.TelemetryConfig{Enabled: &disabled}

	srv, err := NewServer(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown() })

	require.NoError(t, srv.runtime.UpstreamManager().AddServerConfig(serverName, &config.ServerConfig{
		Name:     serverName,
		Protocol: "stdio",
		Command:  "definitely-not-on-path",
		Enabled:  false,
	}))
	return srv
}

// TestDiagnosticFixer_StdioShowLastLogs_ReturnsRealTail is the falsifier for the
// placeholder fixer in internal/diagnostics/builtin_fixers.go.
//
// MCPX_STDIO_EXIT_BEFORE_INITIALIZE (270 installs) and
// MCPX_STDIO_HANDSHAKE_TIMEOUT (315 installs) both offer a "Show last server log
// lines" button whose whole value is that the child's stderr usually names the
// missing binary or env var outright. The registered fixer returned a canned
// "log tail unavailable in this build" string with outcome=success, so every
// click was recorded as a fix SUCCESS while showing the user nothing.
func TestDiagnosticFixer_StdioShowLastLogs_ReturnsRealTail(t *testing.T) {
	const serverName = "flaky-stdio"
	logDir := t.TempDir()

	// Seed the per-server log file the way the connection launcher does: one
	// mcpproxy-written structured line carrying the upstream URL credential, and
	// raw lines piped straight from the child's stderr.
	const childStderr = "Error: MISTRAL_API_KEY environment variable is not set"
	require.NoError(t, os.WriteFile(
		filepath.Join(logDir, logs.ServerLogFilename(serverName)),
		[]byte(`{"level":"error","msg":"connect failed","url":"https://host/mcp?token=`+leakySecrets["url"]+`"}`+"\n"+
			childStderr+"\n"+
			"child said: ghp_abcdefghijklmnopqrstuvwxyz0123456789\n"),
		0o600))

	srv := newFixerTestServer(t, serverName, logDir)
	require.NotNil(t, srv)

	res, err := diagnostics.InvokeFixer(context.Background(), "stdio_show_last_logs", diagnostics.FixRequest{
		ServerID: serverName,
		Mode:     diagnostics.ModeExecute,
	})
	require.NoError(t, err)

	assert.Equal(t, diagnostics.OutcomeSuccess, res.Outcome, "reading a log tail must succeed: %s", res.FailureMsg)
	assert.NotContains(t, res.Preview, "unavailable in this build",
		"the placeholder is still registered — no real log tail reached the user")
	assert.Contains(t, res.Preview, childStderr,
		"the child's stderr line — the whole point of the button — must reach the preview")

	// The preview crosses the REST API, so it must carry the same masking
	// GET /api/v1/servers/{id}/logs applies (issue #1148).
	assert.NotContains(t, res.Preview, leakySecrets["url"],
		"the preview leaks the URL credential mcpproxy itself logged")
	assert.NotContains(t, res.Preview, "ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		"the preview leaks a vendor credential the child printed")
}

// TestDiagnosticFixer_StdioShowLastLogs_MissingLogIsAFailure asserts the honest
// outcome for the case the placeholder used to report as a success: there is no
// log file, so nothing was shown and nothing was fixed.
func TestDiagnosticFixer_StdioShowLastLogs_MissingLogIsAFailure(t *testing.T) {
	const serverName = "never-ran"
	logDir := t.TempDir()
	srv := newFixerTestServer(t, serverName, logDir)
	require.NotNil(t, srv)

	res, err := diagnostics.InvokeFixer(context.Background(), "stdio_show_last_logs", diagnostics.FixRequest{
		ServerID: serverName,
		Mode:     diagnostics.ModeExecute,
	})
	require.NoError(t, err)
	assert.Equal(t, diagnostics.OutcomeFailed, res.Outcome,
		"a fixer that showed the user nothing must not report success")
	assert.NotEmpty(t, res.FailureMsg)
}

// TestDiagnosticFixer_OAuthReauth_ReachesTheCoordinator is the falsifier for the
// second placeholder: oauth_reauth returned OutcomeBlocked/"has not been wired to
// the OAuth coordinator" for every execute, which is what MCPX_OAUTH_LOGIN_REQUIRED
// (208 installs) offers as its only Sign in button.
//
// The request names a server that is NOT registered with the upstream manager on
// purpose: the real path then fails inside Manager.StartManualOAuth's own
// existence check and returns immediately, so the assertion proves the call
// reached the coordinator without launching a browser from a unit test.
func TestDiagnosticFixer_OAuthReauth_ReachesTheCoordinator(t *testing.T) {
	srv := newFixerTestServer(t, "some-registered-server", t.TempDir())
	require.NotNil(t, srv)

	res, err := diagnostics.InvokeFixer(context.Background(), "oauth_reauth", diagnostics.FixRequest{
		ServerID: "no-such-server",
		Mode:     diagnostics.ModeExecute,
	})
	require.NoError(t, err)

	assert.NotEqual(t, diagnostics.OutcomeBlocked, res.Outcome,
		"the placeholder is still registered — execute never reached the OAuth coordinator")
	assert.NotContains(t, res.FailureMsg, "has not been wired to the OAuth coordinator")
	assert.Contains(t, res.FailureMsg, "server not found",
		"the failure must come from the upstream manager, proving the real call was made")
}

// TestDiagnosticFixer_OAuthReauth_DryRunDoesNotSignIn keeps the destructive
// fixer's dry-run contract: a preview, no coordinator call. The Web UI renders
// Preview + Execute for a destructive step and gates Execute behind
// window.confirm(), so dry_run must stay side-effect free.
func TestDiagnosticFixer_OAuthReauth_DryRunDoesNotSignIn(t *testing.T) {
	srv := newFixerTestServer(t, "some-registered-server", t.TempDir())
	require.NotNil(t, srv)

	res, err := diagnostics.InvokeFixer(context.Background(), "oauth_reauth", diagnostics.FixRequest{
		ServerID: "no-such-server",
		Mode:     diagnostics.ModeDryRun,
	})
	require.NoError(t, err)
	assert.Equal(t, diagnostics.OutcomeSuccess, res.Outcome)
	assert.NotEmpty(t, res.Preview)
	assert.Empty(t, res.FailureMsg,
		"dry_run must not have called the coordinator (a call would fail: server not found)")
}
