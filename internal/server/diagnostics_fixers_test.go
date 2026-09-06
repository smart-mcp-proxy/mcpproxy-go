package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	return newFixerTestServerWithConfig(t, serverName, logDir, nil)
}

// newFixerTestServerWithConfig is newFixerTestServer with a hook to mutate the
// config before construction, for the gate cases.
func newFixerTestServerWithConfig(t *testing.T, serverName, logDir string, tweak func(*config.Config)) *Server {
	t.Helper()

	disabled := false
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.Logging.LogDir = logDir
	// Keep the heartbeat off: this test has no business talking to the network.
	cfg.Telemetry = &config.TelemetryConfig{Enabled: &disabled}
	if tweak != nil {
		tweak(cfg)
	}

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
	//
	// The absence assertions alone would pass VACUOUSLY for an implementation
	// that simply never rendered the two secret-bearing lines (cross-model
	// review, round 1). Pin the non-secret remainder of BOTH of them first, so
	// "the secret is gone" can only mean the scrubber removed it.
	assert.Contains(t, res.Preview, "connect failed",
		"the mcpproxy-written line carrying the URL credential never reached the preview — "+
			"the redaction assertion below would pass for the wrong reason")
	assert.Contains(t, res.Preview, "child said:",
		"the child line carrying the vendor credential never reached the preview — "+
			"the redaction assertion below would pass for the wrong reason")

	assert.NotContains(t, res.Preview, leakySecrets["url"],
		"the preview leaks the URL credential mcpproxy itself logged")
	assert.NotContains(t, res.Preview, "ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		"the preview leaks a vendor credential the child printed")
}

// TestDiagnosticFixer_StdioShowLastLogs_BoundsThePayload covers the byte cap.
//
// diagnosticsLogTailLines bounds the line COUNT only. A child MCP server that
// prints a large blob per line would otherwise produce a multi-megabyte Preview,
// and this Preview is rendered as a Web-UI notification. The cap must drop the
// OLDEST lines — the newest line is the one that explains the failure — and must
// say what it dropped rather than silently swallowing it.
func TestDiagnosticFixer_StdioShowLastLogs_BoundsThePayload(t *testing.T) {
	const serverName = "chatty-stdio"
	logDir := t.TempDir()

	// 40 lines of 1KB each = ~40KB, comfortably over diagnosticsPreviewMaxBytes,
	// while every individual line stays under bufio.Scanner's 64KB token limit.
	const newest = "FINAL: the line that explains the failure"
	var content strings.Builder
	for i := 0; i < 40; i++ {
		content.WriteString(fmt.Sprintf("line-%02d ", i))
		content.WriteString(strings.Repeat("x", 1024))
		content.WriteByte('\n')
	}
	content.WriteString(newest + "\n")
	require.NoError(t, os.WriteFile(
		filepath.Join(logDir, logs.ServerLogFilename(serverName)),
		[]byte(content.String()), 0o600))

	srv := newFixerTestServer(t, serverName, logDir)
	require.NotNil(t, srv)

	res, err := diagnostics.InvokeFixer(context.Background(), "stdio_show_last_logs", diagnostics.FixRequest{
		ServerID: serverName,
		Mode:     diagnostics.ModeExecute,
	})
	require.NoError(t, err)
	require.Equal(t, diagnostics.OutcomeSuccess, res.Outcome, res.FailureMsg)

	assert.Contains(t, res.Preview, newest,
		"the cap dropped the NEWEST line — a tail is read bottom-up")
	assert.NotContains(t, res.Preview, "line-00 ",
		"the oldest line survived, so the payload was not bounded at all")
	assert.Contains(t, res.Preview, "older line(s) omitted",
		"lines were dropped without telling the operator anything was missing")
	// The real bound, not a slack multiple of it. Every retained line here is
	// ~1KB, so the newest-line exemption cannot be what carries this: the
	// assertion fails if the header and the omission notice are left out of
	// the budget, which is exactly the defect a cross-model review found.
	assert.LessOrEqual(t, len(res.Preview), diagnosticsPreviewMaxBytes,
		"the rendered preview exceeded the cap the constant advertises")
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

// TestDiagnosticFixer_OAuthReauth_RespectsWriteGates pins the gate the fixer
// used to skip. POST /api/v1/servers/{id}/login goes through the management
// service, whose checkWriteGates refuses when read_only_mode or
// disable_management is set. The fixer called Runtime.TriggerOAuthLogin
// directly and therefore honoured neither — an authorized diagnostics request
// could start an OAuth flow on an install whose owner had turned management
// off. The diagnostics route's own middleware checks caller authorization,
// not these config gates.
func TestDiagnosticFixer_OAuthReauth_RespectsWriteGates(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(*config.Config)
		want  string
	}{
		{"read_only_mode", func(c *config.Config) { c.ReadOnlyMode = true }, "read_only_mode"},
		{"disable_management", func(c *config.Config) { c.DisableManagement = true }, "disable_management"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newFixerTestServerWithConfig(t, "some-registered-server", t.TempDir(), tc.apply)
			require.NotNil(t, srv)

			res, err := diagnostics.InvokeFixer(context.Background(), "oauth_reauth", diagnostics.FixRequest{
				ServerID: "some-registered-server",
				Mode:     diagnostics.ModeExecute,
			})
			require.NoError(t, err)
			assert.Equal(t, diagnostics.OutcomeFailed, res.Outcome,
				"the fixer started a sign-in while %s was set", tc.want)
			assert.Contains(t, res.FailureMsg, tc.want,
				"the refusal must name the gate that refused, as the REST login route does")
		})
	}
}

// TestDiagnosticFixer_StdioShowLastLogs_CapCountsItsOwnFraming pins the second
// half of the byte cap, which a cross-model review showed was missing: the line
// loop measured only the log lines, while the rendered payload also carries a
// header and an "older lines omitted" notice, both interpolating the server
// name. A tail whose lines filled the cap therefore shipped a payload LARGER
// than the cap the constant advertises.
//
// The fixture is sized so the two behaviours differ by exactly one line: every
// line costs 180 bytes, so an unbudgeted loop keeps 45 of them (8100 bytes,
// under 8192) and the framing pushes the result past the cap, while a budgeted
// loop keeps 44 and lands inside it. The newest-line exemption cannot mask this
// — no single line here is anywhere near the cap.
func TestDiagnosticFixer_StdioShowLastLogs_CapCountsItsOwnFraming(t *testing.T) {
	const serverName = "framed-stdio"
	logDir := t.TempDir()

	var content strings.Builder
	for i := 0; i < 60; i++ {
		// 8 + 171 = 179 bytes of message, 180 with the newline the loop adds.
		content.WriteString(fmt.Sprintf("line-%02d ", i))
		content.WriteString(strings.Repeat("y", 171))
		content.WriteByte('\n')
	}
	require.NoError(t, os.WriteFile(
		filepath.Join(logDir, logs.ServerLogFilename(serverName)),
		[]byte(content.String()), 0o600))

	srv := newFixerTestServer(t, serverName, logDir)
	require.NotNil(t, srv)

	res, err := diagnostics.InvokeFixer(context.Background(), "stdio_show_last_logs", diagnostics.FixRequest{
		ServerID: serverName,
		Mode:     diagnostics.ModeExecute,
	})
	require.NoError(t, err)
	require.Equal(t, diagnostics.OutcomeSuccess, res.Outcome, res.FailureMsg)
	require.Contains(t, res.Preview, "older line(s) omitted",
		"fixture no longer exceeds the cap, so this test proves nothing")

	assert.LessOrEqual(t, len(res.Preview), diagnosticsPreviewMaxBytes,
		"header and omission notice are not counted against the cap (got %d bytes)", len(res.Preview))
}
