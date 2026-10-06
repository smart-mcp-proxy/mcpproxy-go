package main

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestRunUpstreamListFromConfig_EnabledServerShowsDaemonNotRunning is a
// round-6 review finding: `mcpproxy upstream list` run with no daemon
// reachable builds each row's health via
// health.CalculateHealth(HealthCalculatorInput{State: "disconnected", ...})
// and then overrides only `summary` to "Daemon not running" for an enabled
// server. But that synthetic input also makes CalculateHealth set
// health.status = StatusError ("error") unconditionally (disconnected with no
// LastError still resolves through connectionErrorStatus(ActionRestart) ==
// StatusError), and upstreamServerRows's STATUS column renders
// health.StatusLabel(health.status) instead of the free-text summary whenever
// health.status is non-empty (Spec 109 FR-015). The net effect: the STATUS
// column regressed from the informative "Daemon not running" to the generic
// "Error" for every enabled server whenever the daemon isn't running.
func TestRunUpstreamListFromConfig_EnabledServerShowsDaemonNotRunning(t *testing.T) {
	cfg := &config.Config{
		Servers: []*config.ServerConfig{
			{Name: "github", Protocol: "http", Enabled: true},
		},
	}

	prevFormat := globalOutputFormat
	globalOutputFormat = "table"
	t.Cleanup(func() { globalOutputFormat = prevFormat })

	stdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	runErr := runUpstreamListFromConfig(cfg)

	w.Close()
	os.Stdout = stdout
	require.NoError(t, runErr)

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	out := buf.String()

	require.Contains(t, out, "github")
	assert.Contains(t, out, "Daemon not running",
		"STATUS must stay the informative daemon-not-running message, not the generic status-vocabulary label")
	assert.NotContains(t, out, "Error",
		"STATUS must not regress to the generic 'Error' status label for a server whose only problem is the daemon being down")
}

// TestRunUpstreamListFromConfig_DisabledAndQuarantinedSurviveTheStatusOverride
// pins that the daemon-not-running override in runUpstreamListFromConfig only
// touches enabled, non-quarantined servers — a disabled server's STATUS must
// still read "Disabled" and a quarantined one "Needs review" (the status
// label for health.CalculateHealth's StatusNeedsReview), exactly as
// health.CalculateHealth reports them regardless of daemon reachability.
func TestRunUpstreamListFromConfig_DisabledAndQuarantinedSurviveTheStatusOverride(t *testing.T) {
	cfg := &config.Config{
		Servers: []*config.ServerConfig{
			{Name: "off-server", Protocol: "stdio", Enabled: false},
			{Name: "held-server", Protocol: "stdio", Enabled: true, Quarantined: true},
		},
	}

	prevFormat := globalOutputFormat
	globalOutputFormat = "table"
	t.Cleanup(func() { globalOutputFormat = prevFormat })

	stdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	runErr := runUpstreamListFromConfig(cfg)

	w.Close()
	os.Stdout = stdout
	require.NoError(t, runErr)

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	out := buf.String()

	assert.Contains(t, out, "Disabled")
	assert.Contains(t, out, "Needs review")
}

// TestRunUpstreamListFromConfig_UnresolvableStatusFilterWarnsInsteadOfSilentlyEmpty
// is this round's review finding: runUpstreamListFromConfig clears
// health.status to "" for every enabled, non-quarantined server (the fix
// above), but filterServersByStatus requires an exact match against
// health.status. `--status error|ready|connecting|sign_in_required|
// needs_secret|needs_config` therefore silently returned an empty
// (headers-only) table with exit 0 for every enabled server whenever the
// daemon is down — exactly the GH #938 "silent empty result indistinguishable
// from no matches" failure mode validateStatusFlag's own doc comment exists
// to prevent. The fix is a stderr notice, not a fabricated status: this
// daemon-less path genuinely cannot classify an enabled server's health, so
// the notice must fire only for a filter value that path can never resolve
// on its own — "disabled" and "needs_review" still work today and must not
// warn.
func TestRunUpstreamListFromConfig_UnresolvableStatusFilterWarnsInsteadOfSilentlyEmpty(t *testing.T) {
	cfg := &config.Config{
		Servers: []*config.ServerConfig{
			{Name: "github", Protocol: "http", Enabled: true},
		},
	}

	prevFormat := globalOutputFormat
	globalOutputFormat = "table"
	prevStatus := upstreamListStatus
	t.Cleanup(func() {
		globalOutputFormat = prevFormat
		upstreamListStatus = prevStatus
	})

	runWithStatus := func(statusFilter []string) (stdout, stderr string) {
		upstreamListStatus = statusFilter

		outR, outW, err := os.Pipe()
		require.NoError(t, err)
		errR, errW, err := os.Pipe()
		require.NoError(t, err)

		prevStdout, prevStderr := os.Stdout, os.Stderr
		os.Stdout, os.Stderr = outW, errW

		runErr := runUpstreamListFromConfig(cfg)

		outW.Close()
		errW.Close()
		os.Stdout, os.Stderr = prevStdout, prevStderr
		require.NoError(t, runErr)

		var outBuf, errBuf bytes.Buffer
		_, _ = io.Copy(&outBuf, outR)
		_, _ = io.Copy(&errBuf, errR)
		return outBuf.String(), errBuf.String()
	}

	t.Run("unresolvable status filter warns on stderr", func(t *testing.T) {
		out, errOut := runWithStatus([]string{"error"})
		assert.NotContains(t, out, "github",
			"an enabled server with no classifiable status must not match --status=error without a daemon")
		assert.Contains(t, errOut, "daemon",
			"stderr must explain why the table came back empty instead of leaving it silent")
	})

	t.Run("resolvable status filter (disabled) does not warn", func(t *testing.T) {
		_, errOut := runWithStatus([]string{"disabled"})
		assert.Empty(t, errOut, "disabled is classifiable without a daemon; no notice is needed")
	})

	t.Run("resolvable status filter (needs_review) does not warn", func(t *testing.T) {
		_, errOut := runWithStatus([]string{"needs_review"})
		assert.Empty(t, errOut, "needs_review is classifiable without a daemon; no notice is needed")
	})

	t.Run("no status filter does not warn", func(t *testing.T) {
		_, errOut := runWithStatus(nil)
		assert.Empty(t, errOut, "omitting --status entirely must stay silent")
	})
}
