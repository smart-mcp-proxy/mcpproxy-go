package health

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// GH #1929 release QA: operator-facing output must tell a temporary outage
// apart from a genuine sign-in requirement.

func TestCalculateHealth_NetworkFailureReadsAsReconnecting(t *testing.T) {
	next := time.Now().Add(4*time.Minute + 10*time.Second)
	input := HealthCalculatorInput{
		Name: "atlassian", Enabled: true, State: "Error", HasEndpointURL: true,
		LastError:      "failed to connect: MCP initialize failed during no-auth strategy: Post \"https://mcp.atlassian.com/v2/mcp\": dial tcp: lookup mcp.atlassian.com: i/o timeout",
		NetworkFailure: true, NextRetryAt: &next, RetryCount: 9,
	}
	got := CalculateHealth(input, nil)
	assert.Equal(t, LevelDegraded, got.Level, "a network outage is temporary, not red")
	assert.True(t, strings.HasPrefix(got.Summary, "Reconnecting: "), got.Summary)
	assert.Contains(t, got.Detail, "retries automatically")
	assert.Contains(t, got.Detail, "Next scheduled attempt in about 4m")
	assert.Contains(t, got.Detail, "Last error:")
	assert.NotEqual(t, ActionLogin, got.Action, "an outage never offers sign-in")
	assert.NotContains(t, got.Actions, ActionLogin)
	assert.Equal(t, StatusConnecting, got.Status, "still retrying on its own: Connecting, not Error")
	assert.Equal(t, ActionRestart, got.Action, "Restart (retry now) stays offered")

	// Gave up the regular retries: still amber and "Reconnecting", but an
	// attention item again.
	input.NetworkGaveUp = true
	gaveUp := CalculateHealth(input, nil)
	assert.Equal(t, StatusError, gaveUp.Status)
	assert.Equal(t, LevelDegraded, gaveUp.Level)
	assert.Contains(t, gaveUp.Detail, "probed every 30 minutes")
	input.NetworkGaveUp = false

	// Without the network classification the same text keeps the old,
	// red presentation (e.g. a daemon-less CLI rendering from config).
	input.NetworkFailure, input.NextRetryAt = false, nil
	plain := CalculateHealth(input, nil)
	assert.Equal(t, LevelUnhealthy, plain.Level)
	assert.False(t, strings.HasPrefix(plain.Summary, "Reconnecting"))
}

func TestCalculateHealth_ExpiredTokenBeingRefreshedIsNotASignIn(t *testing.T) {
	next := time.Now().Add(90 * time.Second)
	input := HealthCalculatorInput{
		Name: "atlassian", Enabled: true, State: "Ready", Connected: true,
		OAuthRequired: true, OAuthStatus: "expired", HasRefreshToken: true,
		RefreshState: RefreshStateRetrying, RefreshRetryCount: 4, RefreshNextAttempt: &next,
		RefreshLastError: "token endpoint answered 503",
	}
	got := CalculateHealth(input, nil)
	assert.Equal(t, "Token refresh pending", got.Summary)
	assert.Equal(t, LevelDegraded, got.Level)
	assert.Equal(t, StatusConnecting, got.Status, "it resolves on its own")
	assert.Equal(t, ActionViewLogs, got.Action)
	assert.NotContains(t, got.Actions, ActionLogin, "no browser sign-in for a temporary refresh failure")
	assert.Contains(t, got.Detail, "retries automatically")
	assert.Contains(t, got.Detail, next.Format(time.RFC3339), "the next refresh attempt is shown")
	assert.False(t, got.Usable)
}

func TestCalculateHealth_GenuineSignInKeepsTheSignInAction(t *testing.T) {
	cases := map[string]HealthCalculatorInput{
		"refresh failed for good (invalid_grant)": {Name: "s", Enabled: true, State: "Ready", Connected: true,
			OAuthRequired: true, OAuthStatus: "expired", HasRefreshToken: true, RefreshState: RefreshStateFailed},
		"expired without a refresh token": {Name: "s", Enabled: true, State: "Ready", Connected: true,
			OAuthRequired: true, OAuthStatus: "expired"},
		"never signed in": {Name: "s", Enabled: true, State: "Pending Auth",
			LastError: "OAuth authentication required for s: login available via Web UI, system tray menu, or 'mcpproxy auth login' CLI command"},
	}
	for name, input := range cases {
		got := CalculateHealth(input, nil)
		assert.Equal(t, ActionLogin, got.Action, name)
		assert.Equal(t, StatusSignInRequired, got.Status, name)
	}
}

// Review r4: a wrong host keeps "needs config" (Edit URL), not "connecting".
func TestCalculateHealth_EditURLKeepsNeedsConfig(t *testing.T) {
	input := HealthCalculatorInput{
		Name: "typo", Enabled: true, State: "Error", HasEndpointURL: true,
		LastError:      "dial tcp: lookup mcp.exmaple.com: no such host",
		NetworkFailure: true,
	}
	got := CalculateHealth(input, nil)
	assert.Equal(t, ActionEditURL, got.Action)
	assert.NotEqual(t, StatusConnecting, got.Status)
	assert.Equal(t, LevelDegraded, got.Level, "still amber: the network may come back")
}

// GH #1929 (CI): the Windows wording of a refused dial reads as a refused
// connection, not as an OAuth problem.
func TestCalculateHealth_WindowsRefusedDial(t *testing.T) {
	input := HealthCalculatorInput{
		Name: "w", Enabled: true, State: "Error", HasEndpointURL: true, OAuthRequired: true,
		LastError:      `MCP initialize failed during OAuth strategy: dial tcp 127.0.0.1:64438: connectex: No connection could be made because the target machine actively refused it.`,
		NetworkFailure: true,
	}
	got := CalculateHealth(input, nil)
	assert.Equal(t, "Reconnecting: connection refused", got.Summary)
	assert.NotEqual(t, ActionLogin, got.Action)
}
