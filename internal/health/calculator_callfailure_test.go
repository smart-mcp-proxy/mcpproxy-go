package health

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// connectedInput is a server that would be plain healthy but for the call
// failure rate; tests mutate it to prove each existing outcome keeps precedence.
func connectedInput() HealthCalculatorInput {
	return HealthCalculatorInput{
		Name:      "srv",
		Enabled:   true,
		State:     "connected",
		Connected: true,
		ToolCount: 3,
	}
}

func TestCalculateHealth_CallFailureRate_Boundaries(t *testing.T) {
	tests := []struct {
		name            string
		calls, failures int
		degraded        bool
	}{
		{"no calls", 0, 0, false},
		{"4 of 4 failed is below the sample floor", 4, 4, false},
		{"5 calls at 40% stays healthy", 5, 2, false},
		{"6 calls at exactly 50% stays healthy", 6, 3, false},
		{"10 calls at exactly 50% stays healthy", 10, 5, false},
		{"5 calls at 60% degrades", 5, 3, true},
		{"6 of 10 degrades", 10, 6, true},
		{"all failed over the floor degrades", 5, 5, true},
		{"all succeeded", 20, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := connectedInput()
			in.CallsInWindow = tt.calls
			in.CallFailuresInWindow = tt.failures
			in.DominantCallFailureKind = "timeout"
			got := CalculateHealth(in, nil)
			if !tt.degraded {
				assert.Equal(t, LevelHealthy, got.Level)
				assert.Equal(t, StatusReady, got.Status)
				return
			}
			assert.Equal(t, LevelDegraded, got.Level)
			assert.Equal(t, StateEnabled, got.AdminState)
			// Still connected and callable: an advisory, not an outage.
			assert.Equal(t, StatusReady, got.Status)
			assert.True(t, got.Usable)
			assert.Equal(t, ActionViewLogs, got.Action)
			assert.Equal(t, []string{ActionViewLogs}, got.Actions)
		})
	}
}

func TestCalculateHealth_CallFailureRate_Text(t *testing.T) {
	in := connectedInput()
	in.CallsInWindow = 10
	in.CallFailuresInWindow = 6
	in.DominantCallFailureKind = "timeout"
	got := CalculateHealth(in, nil)
	assert.Equal(t, "6 of 10 tool calls failed in the last 5 min", got.Summary)
	assert.Contains(t, got.Detail, "timeout")

	in.DominantCallFailureKind = ""
	got = CalculateHealth(in, nil)
	assert.Equal(t, "6 of 10 tool calls failed in the last 5 min", got.Summary)
	assert.NotEmpty(t, got.Detail)
}

func TestCalculateHealth_CallFailureRate_Precedence(t *testing.T) {
	soon := time.Now().Add(10 * time.Minute)
	tests := []struct {
		name   string
		mutate func(*HealthCalculatorInput)
		level  string
		admin  string
	}{
		{"disabled", func(i *HealthCalculatorInput) { i.Enabled = false }, LevelHealthy, StateDisabled},
		{"quarantined", func(i *HealthCalculatorInput) { i.Quarantined = true }, LevelHealthy, StateQuarantined},
		{"disconnected", func(i *HealthCalculatorInput) { i.State = "disconnected"; i.Connected = false }, LevelUnhealthy, StateEnabled},
		{"error", func(i *HealthCalculatorInput) { i.State = "error"; i.LastError = "boom" }, LevelUnhealthy, StateEnabled},
		{"connecting", func(i *HealthCalculatorInput) { i.State = "connecting" }, LevelHealthy, StateEnabled},
		{"missing secret", func(i *HealthCalculatorInput) { i.MissingSecret = "TOKEN" }, LevelUnhealthy, StateEnabled},
		{"retry stopped", func(i *HealthCalculatorInput) { i.RetryStopped = true }, LevelUnhealthy, StateEnabled},
		{"oauth expired", func(i *HealthCalculatorInput) { i.OAuthRequired = true; i.OAuthStatus = "expired" }, LevelUnhealthy, StateEnabled},
		{"refresh failed", func(i *HealthCalculatorInput) { i.RefreshState = RefreshStateFailed }, LevelUnhealthy, StateEnabled},
		{"refresh retrying", func(i *HealthCalculatorInput) { i.RefreshState = RefreshStateRetrying }, LevelDegraded, StateEnabled},
		{"token expiring without refresh token", func(i *HealthCalculatorInput) {
			i.OAuthRequired, i.OAuthStatus, i.TokenExpiresAt = true, "authenticated", &soon
		}, LevelDegraded, StateEnabled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := connectedInput()
			in.CallsInWindow, in.CallFailuresInWindow = 10, 10
			tt.mutate(&in)
			got := CalculateHealth(in, nil)
			assert.Equal(t, tt.level, got.Level)
			assert.Equal(t, tt.admin, got.AdminState)
			assert.NotContains(t, got.Summary, "tool calls failed")
		})
	}
}

// The early healthy return for an OAuth token inside the expiry-warning window
// (auto-refreshable) must also be subject to the failure rate.
func TestCalculateHealth_CallFailureRate_OAuthExpiryWarningHealthy(t *testing.T) {
	soon := time.Now().Add(10 * time.Minute)
	in := connectedInput()
	in.OAuthRequired, in.OAuthStatus = true, "authenticated"
	in.TokenExpiresAt, in.HasRefreshToken = &soon, true

	assert.Equal(t, LevelHealthy, CalculateHealth(in, nil).Level)

	in.CallsInWindow, in.CallFailuresInWindow = 10, 8
	got := CalculateHealth(in, nil)
	assert.Equal(t, LevelDegraded, got.Level)
	assert.Equal(t, "8 of 10 tool calls failed in the last 5 min", got.Summary)
}
