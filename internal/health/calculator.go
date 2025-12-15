// Package health provides unified health status calculation for upstream MCP servers.
package health

import (
	"fmt"
	"time"

	"mcpproxy-go/internal/contracts"
)

// HealthCalculatorInput contains all fields needed to calculate health status.
// This struct normalizes data from different sources (StateView, storage, config).
type HealthCalculatorInput struct {
	// Server identification
	Name string

	// Admin state
	Enabled     bool
	Quarantined bool

	// Connection state
	State     string // "connected", "connecting", "error", "idle", "disconnected"
	Connected bool
	LastError string

	// OAuth state (only for OAuth-enabled servers)
	OAuthRequired   bool
	OAuthStatus     string     // "authenticated", "expired", "error", "none"
	TokenExpiresAt  *time.Time // When token expires
	HasRefreshToken bool       // True if refresh token exists
	UserLoggedOut   bool       // True if user explicitly logged out

	// Tool info
	ToolCount int
}

// HealthCalculatorConfig contains configurable thresholds for health calculation.
type HealthCalculatorConfig struct {
	// ExpiryWarningDuration is the duration before token expiry to show degraded status.
	// Default: 1 hour
	ExpiryWarningDuration time.Duration
}

// DefaultHealthConfig returns the default health calculator configuration.
func DefaultHealthConfig() *HealthCalculatorConfig {
	return &HealthCalculatorConfig{
		ExpiryWarningDuration: time.Hour,
	}
}

// CalculateHealth calculates the unified health status for a server.
// The algorithm uses a priority-based approach where admin state is checked first,
// followed by connection state, then OAuth state.
func CalculateHealth(input HealthCalculatorInput, cfg *HealthCalculatorConfig) *contracts.HealthStatus {
	if cfg == nil {
		cfg = DefaultHealthConfig()
	}

	// 1. Admin state checks - these short-circuit health calculation
	if !input.Enabled {
		return &contracts.HealthStatus{
			Level:      LevelHealthy, // Disabled is intentional, not broken
			AdminState: StateDisabled,
			Summary:    "Disabled",
			Action:     ActionEnable,
		}
	}

	if input.Quarantined {
		return &contracts.HealthStatus{
			Level:      LevelHealthy, // Quarantined is intentional, not broken
			AdminState: StateQuarantined,
			Summary:    "Quarantined for review",
			Action:     ActionApprove,
		}
	}

	// 2. Connection state checks
	switch input.State {
	case "error":
		return &contracts.HealthStatus{
			Level:      LevelUnhealthy,
			AdminState: StateEnabled,
			Summary:    formatErrorSummary(input.LastError),
			Detail:     input.LastError,
			Action:     ActionRestart,
		}
	case "disconnected":
		summary := "Disconnected"
		if input.LastError != "" {
			summary = formatErrorSummary(input.LastError)
		}
		return &contracts.HealthStatus{
			Level:      LevelUnhealthy,
			AdminState: StateEnabled,
			Summary:    summary,
			Detail:     input.LastError,
			Action:     ActionRestart,
		}
	case "connecting", "idle":
		return &contracts.HealthStatus{
			Level:      LevelDegraded,
			AdminState: StateEnabled,
			Summary:    "Connecting...",
			Action:     ActionNone, // Will resolve on its own
		}
	}

	// 3. OAuth state checks (only for servers that require OAuth)
	if input.OAuthRequired {
		// User explicitly logged out - needs re-authentication
		if input.UserLoggedOut {
			return &contracts.HealthStatus{
				Level:      LevelUnhealthy,
				AdminState: StateEnabled,
				Summary:    "Logged out",
				Action:     ActionLogin,
			}
		}

		// Token expired
		if input.OAuthStatus == "expired" {
			return &contracts.HealthStatus{
				Level:      LevelUnhealthy,
				AdminState: StateEnabled,
				Summary:    "Token expired",
				Action:     ActionLogin,
			}
		}

		// OAuth error (but not expired)
		if input.OAuthStatus == "error" {
			return &contracts.HealthStatus{
				Level:      LevelUnhealthy,
				AdminState: StateEnabled,
				Summary:    "Authentication error",
				Detail:     input.LastError,
				Action:     ActionLogin,
			}
		}

		// Token expiring soon (only degraded if no refresh token for auto-refresh)
		if input.TokenExpiresAt != nil && !input.TokenExpiresAt.IsZero() {
			timeUntilExpiry := time.Until(*input.TokenExpiresAt)
			if timeUntilExpiry > 0 && timeUntilExpiry <= cfg.ExpiryWarningDuration {
				// If we have a refresh token, the system can auto-refresh - stay healthy
				if input.HasRefreshToken {
					// Token will be auto-refreshed, show healthy with tool count
					return &contracts.HealthStatus{
						Level:      LevelHealthy,
						AdminState: StateEnabled,
						Summary:    formatConnectedSummary(input.ToolCount),
						Action:     ActionNone,
					}
				}
				// No refresh token - user needs to re-authenticate soon
				// M-002: Include exact expiration time in Detail field
				return &contracts.HealthStatus{
					Level:      LevelDegraded,
					AdminState: StateEnabled,
					Summary:    formatExpiringTokenSummary(timeUntilExpiry),
					Detail:     fmt.Sprintf("Token expires at %s", input.TokenExpiresAt.Format(time.RFC3339)),
					Action:     ActionLogin,
				}
			}
		}

		// Token is not authenticated yet (none status)
		if input.OAuthStatus == "none" || input.OAuthStatus == "" {
			// Server requires OAuth but no token - needs login
			return &contracts.HealthStatus{
				Level:      LevelUnhealthy,
				AdminState: StateEnabled,
				Summary:    "Authentication required",
				Action:     ActionLogin,
			}
		}
	}

	// 4. Healthy state - connected with valid authentication (if required)
	return &contracts.HealthStatus{
		Level:      LevelHealthy,
		AdminState: StateEnabled,
		Summary:    formatConnectedSummary(input.ToolCount),
		Action:     ActionNone,
	}
}

// formatConnectedSummary formats the summary for a healthy connected server.
func formatConnectedSummary(toolCount int) string {
	if toolCount == 0 {
		return "Connected"
	}
	if toolCount == 1 {
		return "Connected (1 tool)"
	}
	return fmt.Sprintf("Connected (%d tools)", toolCount)
}

// formatErrorSummary formats an error message for the summary field.
// It truncates long errors and makes them more user-friendly.
func formatErrorSummary(lastError string) string {
	if lastError == "" {
		return "Connection error"
	}

	// Common error patterns to friendly messages
	errorMappings := map[string]string{
		"connection refused":     "Connection refused",
		"no such host":           "Host not found",
		"connection reset":       "Connection reset",
		"timeout":                "Connection timeout",
		"EOF":                    "Connection closed",
		"authentication failed":  "Authentication failed",
		"unauthorized":           "Unauthorized",
		"forbidden":              "Access forbidden",
		"oauth":                  "OAuth error",
		"certificate":            "Certificate error",
		"dial tcp":               "Cannot connect",
	}

	// Check for known patterns
	for pattern, friendly := range errorMappings {
		if containsIgnoreCase(lastError, pattern) {
			return friendly
		}
	}

	// Truncate if too long (max 50 chars for summary)
	if len(lastError) > 50 {
		return lastError[:47] + "..."
	}
	return lastError
}

// formatExpiringTokenSummary formats the summary for an expiring token.
func formatExpiringTokenSummary(timeUntilExpiry time.Duration) string {
	if timeUntilExpiry < time.Minute {
		return "Token expiring now"
	}
	if timeUntilExpiry < time.Hour {
		minutes := int(timeUntilExpiry.Minutes())
		if minutes == 1 {
			return "Token expiring in 1m"
		}
		return fmt.Sprintf("Token expiring in %dm", minutes)
	}
	hours := int(timeUntilExpiry.Hours())
	if hours == 1 {
		return "Token expiring in 1h"
	}
	return fmt.Sprintf("Token expiring in %dh", hours)
}

// containsIgnoreCase checks if s contains substr, ignoring case.
func containsIgnoreCase(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr ||
		 containsLower(toLower(s), toLower(substr)))
}

// toLower is a simple ASCII lowercase conversion.
func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

// containsLower checks if s contains substr (both should be lowercase).
func containsLower(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
