package health

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCalculateHealth_DisabledServer(t *testing.T) {
	input := HealthCalculatorInput{
		Name:    "test-server",
		Enabled: false,
	}

	result := CalculateHealth(input, nil)

	assert.Equal(t, LevelHealthy, result.Level)
	assert.Equal(t, StateDisabled, result.AdminState)
	assert.Equal(t, "Disabled", result.Summary)
	assert.Equal(t, ActionEnable, result.Action)
}

func TestCalculateHealth_QuarantinedServer(t *testing.T) {
	input := HealthCalculatorInput{
		Name:        "test-server",
		Enabled:     true,
		Quarantined: true,
	}

	result := CalculateHealth(input, nil)

	assert.Equal(t, LevelHealthy, result.Level)
	assert.Equal(t, StateQuarantined, result.AdminState)
	assert.Equal(t, "Quarantined for review", result.Summary)
	assert.Equal(t, ActionApprove, result.Action)
}

func TestCalculateHealth_ErrorState(t *testing.T) {
	input := HealthCalculatorInput{
		Name:      "test-server",
		Enabled:   true,
		State:     "error",
		LastError: "connection refused",
	}

	result := CalculateHealth(input, nil)

	assert.Equal(t, LevelUnhealthy, result.Level)
	assert.Equal(t, StateEnabled, result.AdminState)
	assert.Equal(t, "Connection refused", result.Summary)
	assert.Equal(t, ActionRestart, result.Action)
}

func TestCalculateHealth_DisconnectedState(t *testing.T) {
	input := HealthCalculatorInput{
		Name:      "test-server",
		Enabled:   true,
		State:     "disconnected",
		LastError: "no such host",
	}

	result := CalculateHealth(input, nil)

	assert.Equal(t, LevelUnhealthy, result.Level)
	assert.Equal(t, StateEnabled, result.AdminState)
	assert.Equal(t, "Host not found", result.Summary)
	assert.Equal(t, ActionRestart, result.Action)
}

func TestCalculateHealth_ConnectingState(t *testing.T) {
	input := HealthCalculatorInput{
		Name:    "test-server",
		Enabled: true,
		State:   "connecting",
	}

	result := CalculateHealth(input, nil)

	assert.Equal(t, LevelDegraded, result.Level)
	assert.Equal(t, StateEnabled, result.AdminState)
	assert.Equal(t, "Connecting...", result.Summary)
	assert.Equal(t, ActionNone, result.Action)
}

func TestCalculateHealth_IdleState(t *testing.T) {
	input := HealthCalculatorInput{
		Name:    "test-server",
		Enabled: true,
		State:   "idle",
	}

	result := CalculateHealth(input, nil)

	assert.Equal(t, LevelDegraded, result.Level)
	assert.Equal(t, StateEnabled, result.AdminState)
	assert.Equal(t, "Connecting...", result.Summary)
	assert.Equal(t, ActionNone, result.Action)
}

func TestCalculateHealth_HealthyConnected(t *testing.T) {
	input := HealthCalculatorInput{
		Name:      "test-server",
		Enabled:   true,
		State:     "connected",
		Connected: true,
		ToolCount: 5,
	}

	result := CalculateHealth(input, nil)

	assert.Equal(t, LevelHealthy, result.Level)
	assert.Equal(t, StateEnabled, result.AdminState)
	assert.Equal(t, "Connected (5 tools)", result.Summary)
	assert.Equal(t, ActionNone, result.Action)
}

func TestCalculateHealth_HealthyConnectedSingleTool(t *testing.T) {
	input := HealthCalculatorInput{
		Name:      "test-server",
		Enabled:   true,
		State:     "connected",
		Connected: true,
		ToolCount: 1,
	}

	result := CalculateHealth(input, nil)

	assert.Equal(t, "Connected (1 tool)", result.Summary)
}

func TestCalculateHealth_HealthyConnectedNoTools(t *testing.T) {
	input := HealthCalculatorInput{
		Name:      "test-server",
		Enabled:   true,
		State:     "connected",
		Connected: true,
		ToolCount: 0,
	}

	result := CalculateHealth(input, nil)

	assert.Equal(t, "Connected", result.Summary)
}

func TestCalculateHealth_OAuthExpired(t *testing.T) {
	input := HealthCalculatorInput{
		Name:          "test-server",
		Enabled:       true,
		State:         "connected",
		Connected:     true,
		OAuthRequired: true,
		OAuthStatus:   "expired",
	}

	result := CalculateHealth(input, nil)

	assert.Equal(t, LevelUnhealthy, result.Level)
	assert.Equal(t, StateEnabled, result.AdminState)
	assert.Equal(t, "Token expired", result.Summary)
	assert.Equal(t, ActionLogin, result.Action)
}

func TestCalculateHealth_OAuthError(t *testing.T) {
	input := HealthCalculatorInput{
		Name:          "test-server",
		Enabled:       true,
		State:         "connected",
		OAuthRequired: true,
		OAuthStatus:   "error",
		LastError:     "invalid_grant",
	}

	result := CalculateHealth(input, nil)

	assert.Equal(t, LevelUnhealthy, result.Level)
	assert.Equal(t, "Authentication error", result.Summary)
	assert.Equal(t, ActionLogin, result.Action)
}

func TestCalculateHealth_OAuthNone(t *testing.T) {
	input := HealthCalculatorInput{
		Name:          "test-server",
		Enabled:       true,
		State:         "connected",
		OAuthRequired: true,
		OAuthStatus:   "none",
	}

	result := CalculateHealth(input, nil)

	assert.Equal(t, LevelUnhealthy, result.Level)
	assert.Equal(t, "Authentication required", result.Summary)
	assert.Equal(t, ActionLogin, result.Action)
}

func TestCalculateHealth_UserLoggedOut(t *testing.T) {
	input := HealthCalculatorInput{
		Name:          "test-server",
		Enabled:       true,
		State:         "connected",
		OAuthRequired: true,
		OAuthStatus:   "authenticated",
		UserLoggedOut: true,
	}

	result := CalculateHealth(input, nil)

	assert.Equal(t, LevelUnhealthy, result.Level)
	assert.Equal(t, "Logged out", result.Summary)
	assert.Equal(t, ActionLogin, result.Action)
}

func TestCalculateHealth_TokenExpiringSoonNoRefresh(t *testing.T) {
	expiresAt := time.Now().Add(30 * time.Minute)
	input := HealthCalculatorInput{
		Name:            "test-server",
		Enabled:         true,
		State:           "connected",
		Connected:       true,
		OAuthRequired:   true,
		OAuthStatus:     "authenticated",
		TokenExpiresAt:  &expiresAt,
		HasRefreshToken: false,
		ToolCount:       5,
	}

	result := CalculateHealth(input, nil)

	assert.Equal(t, LevelDegraded, result.Level)
	assert.Equal(t, StateEnabled, result.AdminState)
	assert.Contains(t, result.Summary, "Token expiring")
	assert.Equal(t, ActionLogin, result.Action)
}

// T039a: Test that token with working auto-refresh returns healthy (FR-016)
func TestCalculateHealth_TokenExpiringSoonWithRefresh(t *testing.T) {
	expiresAt := time.Now().Add(30 * time.Minute)
	input := HealthCalculatorInput{
		Name:            "test-server",
		Enabled:         true,
		State:           "connected",
		Connected:       true,
		OAuthRequired:   true,
		OAuthStatus:     "authenticated",
		TokenExpiresAt:  &expiresAt,
		HasRefreshToken: true, // Has refresh token - will auto-refresh
		ToolCount:       5,
	}

	result := CalculateHealth(input, nil)

	// FR-016: Token with working auto-refresh should return healthy
	assert.Equal(t, LevelHealthy, result.Level, "Server with refresh token should be healthy")
	assert.Equal(t, StateEnabled, result.AdminState)
	assert.Equal(t, "Connected (5 tools)", result.Summary)
	assert.Equal(t, ActionNone, result.Action, "No action needed when auto-refresh is available")
}

func TestCalculateHealth_TokenNotExpiringSoon(t *testing.T) {
	expiresAt := time.Now().Add(2 * time.Hour) // More than 1 hour
	input := HealthCalculatorInput{
		Name:            "test-server",
		Enabled:         true,
		State:           "connected",
		Connected:       true,
		OAuthRequired:   true,
		OAuthStatus:     "authenticated",
		TokenExpiresAt:  &expiresAt,
		HasRefreshToken: false,
		ToolCount:       5,
	}

	result := CalculateHealth(input, nil)

	assert.Equal(t, LevelHealthy, result.Level)
	assert.Equal(t, "Connected (5 tools)", result.Summary)
	assert.Equal(t, ActionNone, result.Action)
}

func TestCalculateHealth_CustomExpiryWarningDuration(t *testing.T) {
	expiresAt := time.Now().Add(45 * time.Minute)
	cfg := &HealthCalculatorConfig{
		ExpiryWarningDuration: 30 * time.Minute, // Shorter than default 1 hour
	}
	input := HealthCalculatorInput{
		Name:            "test-server",
		Enabled:         true,
		State:           "connected",
		Connected:       true,
		OAuthRequired:   true,
		OAuthStatus:     "authenticated",
		TokenExpiresAt:  &expiresAt,
		HasRefreshToken: false,
		ToolCount:       5,
	}

	result := CalculateHealth(input, cfg)

	// 45 minutes is beyond the 30-minute warning threshold
	assert.Equal(t, LevelHealthy, result.Level)
}

func TestCalculateHealth_ErrorSummaryTruncation(t *testing.T) {
	longError := "This is a very long error message that exceeds the maximum length allowed for the summary field and should be truncated"
	input := HealthCalculatorInput{
		Name:      "test-server",
		Enabled:   true,
		State:     "error",
		LastError: longError,
	}

	result := CalculateHealth(input, nil)

	assert.LessOrEqual(t, len(result.Summary), 50)
	assert.True(t, len(result.Detail) > len(result.Summary))
}

func TestFormatExpiringTokenSummary(t *testing.T) {
	tests := []struct {
		duration time.Duration
		expected string
	}{
		{30 * time.Second, "Token expiring now"},
		{5 * time.Minute, "Token expiring in 5m"},
		{1 * time.Minute, "Token expiring in 1m"},
		{45 * time.Minute, "Token expiring in 45m"},
		{1 * time.Hour, "Token expiring in 1h"},
		{2 * time.Hour, "Token expiring in 2h"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := formatExpiringTokenSummary(tt.duration)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFormatConnectedSummary(t *testing.T) {
	assert.Equal(t, "Connected", formatConnectedSummary(0))
	assert.Equal(t, "Connected (1 tool)", formatConnectedSummary(1))
	assert.Equal(t, "Connected (5 tools)", formatConnectedSummary(5))
	assert.Equal(t, "Connected (100 tools)", formatConnectedSummary(100))
}

func TestFormatErrorSummary(t *testing.T) {
	tests := []struct {
		error    string
		expected string
	}{
		{"", "Connection error"},
		{"connection refused", "Connection refused"},
		{"dial tcp: no such host", "Host not found"},
		{"connection reset by peer", "Connection reset"},
		{"context deadline exceeded (timeout)", "Connection timeout"},
		{"unexpected EOF", "Connection closed"},
		{"oauth: invalid_grant", "OAuth error"},
		{"x509: certificate signed by unknown authority", "Certificate error"},
		{"dial tcp 127.0.0.1:8080", "Cannot connect"},
	}

	for _, tt := range tests {
		t.Run(tt.error, func(t *testing.T) {
			result := formatErrorSummary(tt.error)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDefaultHealthConfig(t *testing.T) {
	cfg := DefaultHealthConfig()

	assert.NotNil(t, cfg)
	assert.Equal(t, time.Hour, cfg.ExpiryWarningDuration)
}
