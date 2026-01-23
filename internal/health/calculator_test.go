package health

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
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

// I-002: Test FR-004 - All health status responses must include non-empty summary
func TestCalculateHealth_AlwaysIncludesSummary(t *testing.T) {
	expiresAt := time.Now().Add(30 * time.Minute)

	testCases := []struct {
		name  string
		input HealthCalculatorInput
	}{
		{"disabled server", HealthCalculatorInput{Name: "test", Enabled: false}},
		{"quarantined server", HealthCalculatorInput{Name: "test", Enabled: true, Quarantined: true}},
		{"error state", HealthCalculatorInput{Name: "test", Enabled: true, State: "error", LastError: "connection refused"}},
		{"error state no message", HealthCalculatorInput{Name: "test", Enabled: true, State: "error", LastError: ""}},
		{"disconnected state", HealthCalculatorInput{Name: "test", Enabled: true, State: "disconnected"}},
		{"connecting state", HealthCalculatorInput{Name: "test", Enabled: true, State: "connecting"}},
		{"idle state", HealthCalculatorInput{Name: "test", Enabled: true, State: "idle"}},
		{"connected healthy", HealthCalculatorInput{Name: "test", Enabled: true, State: "connected", Connected: true, ToolCount: 5}},
		{"connected no tools", HealthCalculatorInput{Name: "test", Enabled: true, State: "connected", Connected: true, ToolCount: 0}},
		{"oauth expired", HealthCalculatorInput{Name: "test", Enabled: true, State: "connected", Connected: true, OAuthRequired: true, OAuthStatus: "expired"}},
		{"oauth none", HealthCalculatorInput{Name: "test", Enabled: true, State: "connected", Connected: true, OAuthRequired: true, OAuthStatus: "none"}},
		{"oauth error", HealthCalculatorInput{Name: "test", Enabled: true, State: "connected", Connected: true, OAuthRequired: true, OAuthStatus: "error"}},
		{"user logged out", HealthCalculatorInput{Name: "test", Enabled: true, State: "connected", OAuthRequired: true, OAuthStatus: "authenticated", UserLoggedOut: true}},
		{"token expiring no refresh", HealthCalculatorInput{Name: "test", Enabled: true, State: "connected", Connected: true, OAuthRequired: true, OAuthStatus: "authenticated", TokenExpiresAt: &expiresAt, HasRefreshToken: false}},
		{"token expiring with refresh", HealthCalculatorInput{Name: "test", Enabled: true, State: "connected", Connected: true, OAuthRequired: true, OAuthStatus: "authenticated", TokenExpiresAt: &expiresAt, HasRefreshToken: true, ToolCount: 5}},
		{"unknown state", HealthCalculatorInput{Name: "test", Enabled: true, State: "unknown"}},
		{"empty state", HealthCalculatorInput{Name: "test", Enabled: true, State: ""}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := CalculateHealth(tc.input, nil)
			assert.NotEmpty(t, result.Summary, "FR-004: Summary should never be empty for %s", tc.name)
		})
	}
}

// T008: Test set_secret action
func TestCalculateHealth_MissingSecret(t *testing.T) {
	t.Run("missing secret returns set_secret action", func(t *testing.T) {
		input := HealthCalculatorInput{
			Name:          "test-server",
			Enabled:       true,
			State:         "error",
			LastError:     "environment variable API_KEY not found or empty",
			MissingSecret: "API_KEY",
		}

		result := CalculateHealth(input, nil)

		assert.Equal(t, LevelUnhealthy, result.Level)
		assert.Equal(t, StateEnabled, result.AdminState)
		assert.Equal(t, "Missing secret", result.Summary)
		assert.Equal(t, "API_KEY", result.Detail)
		assert.Equal(t, ActionSetSecret, result.Action)
	})

	t.Run("missing secret takes priority over connection error", func(t *testing.T) {
		input := HealthCalculatorInput{
			Name:          "test-server",
			Enabled:       true,
			State:         "error",
			LastError:     "connection refused",
			MissingSecret: "GITHUB_TOKEN",
		}

		result := CalculateHealth(input, nil)

		// Missing secret should take priority over connection error
		assert.Equal(t, ActionSetSecret, result.Action)
		assert.Equal(t, "GITHUB_TOKEN", result.Detail)
	})

	t.Run("disabled server with missing secret still returns enable action", func(t *testing.T) {
		input := HealthCalculatorInput{
			Name:          "test-server",
			Enabled:       false,
			MissingSecret: "API_KEY",
		}

		result := CalculateHealth(input, nil)

		// Admin state (disabled) takes priority over missing secret
		assert.Equal(t, ActionEnable, result.Action)
	})
}

// T009: Test configure action
func TestCalculateHealth_OAuthConfigError(t *testing.T) {
	t.Run("OAuth config error returns configure action", func(t *testing.T) {
		input := HealthCalculatorInput{
			Name:           "test-server",
			Enabled:        true,
			State:          "error",
			LastError:      "requires 'resource' parameter",
			OAuthConfigErr: "requires 'resource' parameter",
		}

		result := CalculateHealth(input, nil)

		assert.Equal(t, LevelUnhealthy, result.Level)
		assert.Equal(t, StateEnabled, result.AdminState)
		assert.Equal(t, "OAuth configuration error", result.Summary)
		assert.Equal(t, "requires 'resource' parameter", result.Detail)
		assert.Equal(t, ActionConfigure, result.Action)
	})

	t.Run("missing secret takes priority over OAuth config error", func(t *testing.T) {
		input := HealthCalculatorInput{
			Name:           "test-server",
			Enabled:        true,
			State:          "error",
			MissingSecret:  "CLIENT_SECRET",
			OAuthConfigErr: "requires 'resource' parameter",
		}

		result := CalculateHealth(input, nil)

		// Missing secret should take priority over OAuth config error
		assert.Equal(t, ActionSetSecret, result.Action)
	})

	t.Run("quarantined server with OAuth config error still returns approve action", func(t *testing.T) {
		input := HealthCalculatorInput{
			Name:           "test-server",
			Enabled:        true,
			Quarantined:    true,
			OAuthConfigErr: "invalid oauth configuration",
		}

		result := CalculateHealth(input, nil)

		// Admin state (quarantined) takes priority
		assert.Equal(t, ActionApprove, result.Action)
	})
}

// Test ExtractMissingSecret helper
func TestExtractMissingSecret(t *testing.T) {
	tests := []struct {
		name      string
		lastError string
		expected  string
	}{
		{"empty error", "", ""},
		{"unrelated error", "connection refused", ""},
		{"missing env var format", "environment variable API_KEY not found or empty", "API_KEY"},
		{"missing env var with prefix", "failed to start: environment variable GITHUB_TOKEN not found or empty", "GITHUB_TOKEN"},
		{"env ref format", "${env:MY_SECRET} unresolved", "MY_SECRET"},
		{"complex env ref", "failed to resolve ${env:DATABASE_URL}", "DATABASE_URL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractMissingSecret(tt.lastError)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Test ExtractOAuthConfigError helper
func TestExtractOAuthConfigError(t *testing.T) {
	tests := []struct {
		name      string
		lastError string
		expected  string
	}{
		{"empty error", "", ""},
		{"unrelated error", "connection refused", ""},
		{"resource parameter error", "requires 'resource' parameter", "requires 'resource' parameter"},
		{"missing client_id", "missing client_id in oauth config", "missing client_id in oauth config"},
		{"validation failed", "oauth config validation failed: missing required fields", "oauth config validation failed: missing required fields"},
		{"invalid config", "invalid oauth configuration for server", "invalid oauth configuration for server"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractOAuthConfigError(tt.lastError)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// T033: Test health status output per refresh state (Spec 023)
func TestCalculateHealth_RefreshStateRetrying(t *testing.T) {
	nextAttempt := time.Now().Add(30 * time.Second)
	input := HealthCalculatorInput{
		Name:               "test-server",
		Enabled:            true,
		State:              "connected",
		Connected:          true,
		OAuthRequired:      true,
		OAuthStatus:        "authenticated",
		ToolCount:          5,
		RefreshState:       RefreshStateRetrying,
		RefreshRetryCount:  3,
		RefreshLastError:   "connection timeout",
		RefreshNextAttempt: &nextAttempt,
	}

	result := CalculateHealth(input, nil)

	assert.Equal(t, LevelDegraded, result.Level, "RefreshStateRetrying should return degraded")
	assert.Equal(t, StateEnabled, result.AdminState)
	assert.Equal(t, "Token refresh pending", result.Summary)
	assert.Contains(t, result.Detail, "Refresh retry 3")
	assert.Contains(t, result.Detail, "connection timeout")
	assert.Equal(t, ActionViewLogs, result.Action, "Retrying should suggest view_logs action")
}

func TestCalculateHealth_RefreshStateFailed(t *testing.T) {
	input := HealthCalculatorInput{
		Name:             "test-server",
		Enabled:          true,
		State:            "connected",
		Connected:        true,
		OAuthRequired:    true,
		OAuthStatus:      "authenticated",
		ToolCount:        5,
		RefreshState:     RefreshStateFailed,
		RefreshLastError: "invalid_grant: refresh token expired",
	}

	result := CalculateHealth(input, nil)

	assert.Equal(t, LevelUnhealthy, result.Level, "RefreshStateFailed should return unhealthy")
	assert.Equal(t, StateEnabled, result.AdminState)
	assert.Equal(t, "Refresh token expired", result.Summary)
	assert.Contains(t, result.Detail, "Re-authentication required")
	assert.Contains(t, result.Detail, "invalid_grant")
	assert.Equal(t, ActionLogin, result.Action, "Failed should suggest login action")
}

func TestCalculateHealth_RefreshStateFailedNoError(t *testing.T) {
	input := HealthCalculatorInput{
		Name:          "test-server",
		Enabled:       true,
		State:         "connected",
		Connected:     true,
		OAuthRequired: true,
		OAuthStatus:   "authenticated",
		RefreshState:  RefreshStateFailed,
		// No RefreshLastError
	}

	result := CalculateHealth(input, nil)

	assert.Equal(t, LevelUnhealthy, result.Level)
	assert.Equal(t, "Refresh token expired", result.Summary)
	assert.Equal(t, "Re-authentication required", result.Detail)
	assert.Equal(t, ActionLogin, result.Action)
}

func TestCalculateHealth_RefreshStateIdle(t *testing.T) {
	input := HealthCalculatorInput{
		Name:          "test-server",
		Enabled:       true,
		State:         "connected",
		Connected:     true,
		OAuthRequired: true,
		OAuthStatus:   "authenticated",
		ToolCount:     5,
		RefreshState:  RefreshStateIdle, // Idle state
	}

	result := CalculateHealth(input, nil)

	// RefreshStateIdle should not affect health - server should be healthy
	assert.Equal(t, LevelHealthy, result.Level)
	assert.Equal(t, "Connected (5 tools)", result.Summary)
	assert.Equal(t, ActionNone, result.Action)
}

func TestCalculateHealth_RefreshStateScheduled(t *testing.T) {
	input := HealthCalculatorInput{
		Name:          "test-server",
		Enabled:       true,
		State:         "connected",
		Connected:     true,
		OAuthRequired: true,
		OAuthStatus:   "authenticated",
		ToolCount:     5,
		RefreshState:  RefreshStateScheduled, // Scheduled for proactive refresh
	}

	result := CalculateHealth(input, nil)

	// RefreshStateScheduled should not affect health - server should be healthy
	assert.Equal(t, LevelHealthy, result.Level)
	assert.Equal(t, "Connected (5 tools)", result.Summary)
	assert.Equal(t, ActionNone, result.Action)
}

// Test that higher priority issues take precedence over refresh state
func TestCalculateHealth_RefreshStatePriority(t *testing.T) {
	t.Run("disabled takes priority over refresh retrying", func(t *testing.T) {
		input := HealthCalculatorInput{
			Name:         "test-server",
			Enabled:      false,
			RefreshState: RefreshStateRetrying,
		}

		result := CalculateHealth(input, nil)

		assert.Equal(t, StateDisabled, result.AdminState)
		assert.Equal(t, ActionEnable, result.Action)
	})

	t.Run("quarantine takes priority over refresh failed", func(t *testing.T) {
		input := HealthCalculatorInput{
			Name:         "test-server",
			Enabled:      true,
			Quarantined:  true,
			RefreshState: RefreshStateFailed,
		}

		result := CalculateHealth(input, nil)

		assert.Equal(t, StateQuarantined, result.AdminState)
		assert.Equal(t, ActionApprove, result.Action)
	})

	t.Run("connection error takes priority over refresh retrying", func(t *testing.T) {
		input := HealthCalculatorInput{
			Name:         "test-server",
			Enabled:      true,
			State:        "error",
			LastError:    "connection refused",
			RefreshState: RefreshStateRetrying,
		}

		result := CalculateHealth(input, nil)

		assert.Equal(t, "Connection refused", result.Summary)
		assert.Equal(t, ActionRestart, result.Action)
	})

	t.Run("OAuth expired takes priority over refresh retrying", func(t *testing.T) {
		input := HealthCalculatorInput{
			Name:          "test-server",
			Enabled:       true,
			State:         "connected",
			OAuthRequired: true,
			OAuthStatus:   "expired",
			RefreshState:  RefreshStateRetrying,
		}

		result := CalculateHealth(input, nil)

		assert.Equal(t, "Token expired", result.Summary)
		assert.Equal(t, ActionLogin, result.Action)
	})
}

// Test formatRefreshRetryDetail helper function
func TestFormatRefreshRetryDetail(t *testing.T) {
	t.Run("with next attempt time and error", func(t *testing.T) {
		nextAttempt := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		result := formatRefreshRetryDetail(3, &nextAttempt, "network timeout")

		assert.Contains(t, result, "Refresh retry 3")
		assert.Contains(t, result, "2024-01-15T10:30:00Z")
		assert.Contains(t, result, "network timeout")
	})

	t.Run("without next attempt time", func(t *testing.T) {
		result := formatRefreshRetryDetail(5, nil, "connection refused")

		assert.Contains(t, result, "Refresh retry 5 pending")
		assert.Contains(t, result, "connection refused")
	})

	t.Run("without error", func(t *testing.T) {
		nextAttempt := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		result := formatRefreshRetryDetail(1, &nextAttempt, "")

		assert.Contains(t, result, "Refresh retry 1")
		assert.Contains(t, result, "2024-01-15T10:30:00Z")
		assert.NotContains(t, result, ": :")
	})

	t.Run("truncates long error", func(t *testing.T) {
		longError := "This is a very long error message that exceeds 100 characters and should be truncated to prevent overly long detail messages in the health status"
		result := formatRefreshRetryDetail(2, nil, longError)

		assert.Contains(t, result, "...")
		assert.LessOrEqual(t, len(result), 200) // Reasonable max length
	})
}

// TestRefreshStateSync ensures health.RefreshState values stay in sync with oauth.RefreshState.
// The health package mirrors oauth.RefreshState for decoupling, but the values must match
// for proper state mapping when wiring RefreshManager state into health calculation.
func TestRefreshStateSync(t *testing.T) {
	// Verify that the integer values match between health and oauth packages
	// This test will fail if either package changes its constants without updating the other
	assert.Equal(t, int(RefreshStateIdle), int(oauth.RefreshStateIdle),
		"RefreshStateIdle values must match between health and oauth packages")
	assert.Equal(t, int(RefreshStateScheduled), int(oauth.RefreshStateScheduled),
		"RefreshStateScheduled values must match between health and oauth packages")
	assert.Equal(t, int(RefreshStateRetrying), int(oauth.RefreshStateRetrying),
		"RefreshStateRetrying values must match between health and oauth packages")
	assert.Equal(t, int(RefreshStateFailed), int(oauth.RefreshStateFailed),
		"RefreshStateFailed values must match between health and oauth packages")
}
