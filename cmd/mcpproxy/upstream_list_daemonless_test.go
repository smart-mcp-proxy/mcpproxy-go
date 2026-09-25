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
