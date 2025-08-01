package core

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"

	"mcpproxy-go/internal/config"

	"go.uber.org/zap"
)

// AuthType represents different authentication methods
type AuthType string

const (
	AuthTypeHeaders AuthType = "headers"
	AuthTypeNoAuth  AuthType = "no-auth"
	AuthTypeOAuth   AuthType = "oauth"
)

// OAuthToken represents an OAuth access token
type OAuthToken struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// AuthStrategy implements authentication for different methods
type AuthStrategy struct {
	authType AuthType
	config   *config.ServerConfig
	logger   *zap.Logger
}

// NewAuthStrategy creates a new authentication strategy
func NewAuthStrategy(authType AuthType, config *config.ServerConfig, logger *zap.Logger) *AuthStrategy {
	return &AuthStrategy{
		authType: authType,
		config:   config,
		logger:   logger,
	}
}

// Authenticate attempts authentication using the strategy
func (a *AuthStrategy) Authenticate(ctx context.Context, client interface{}) error {
	switch a.authType {
	case AuthTypeHeaders:
		return a.authenticateWithHeaders(ctx, client)
	case AuthTypeNoAuth:
		return a.authenticateWithoutAuth(ctx, client)
	case AuthTypeOAuth:
		return a.authenticateWithOAuth(ctx, client)
	default:
		return fmt.Errorf("unknown auth type: %s", a.authType)
	}
}

// authenticateWithHeaders attempts authentication using configured headers
func (a *AuthStrategy) authenticateWithHeaders(_ context.Context, _ interface{}) error {
	if len(a.config.Headers) == 0 {
		return fmt.Errorf("no headers configured for headers authentication")
	}

	a.logger.Info("Attempting headers authentication",
		zap.String("server", a.config.Name),
		zap.Int("header_count", len(a.config.Headers)))

	// This would be handled by the transport layer
	// For now, we assume the client is already configured with headers
	return nil
}

// authenticateWithoutAuth attempts connection without authentication
func (a *AuthStrategy) authenticateWithoutAuth(_ context.Context, _ interface{}) error {
	a.logger.Info("Attempting connection without authentication",
		zap.String("server", a.config.Name))

	// No special authentication needed
	return nil
}

// authenticateWithOAuth attempts OAuth authentication
func (a *AuthStrategy) authenticateWithOAuth(_ context.Context, _ interface{}) error {
	a.logger.Info("Attempting OAuth authentication",
		zap.String("server", a.config.Name))

	// TODO: Implement OAuth flow
	// For now, create a placeholder OAuth URL
	authURL := fmt.Sprintf("%s/oauth/authorize", a.config.URL)

	a.logger.Info("OAuth flow started",
		zap.String("server", a.config.Name),
		zap.String("auth_url", authURL))

	// Try to open browser, fallback to console output
	if err := a.openBrowser(authURL); err != nil {
		a.logger.Warn("Could not open browser automatically, please visit the URL manually",
			zap.String("server", a.config.Name),
			zap.String("auth_url", authURL),
			zap.Error(err))

		// Output URL to console for manual access
		fmt.Printf("\n=== OAuth Authentication Required ===\n")
		fmt.Printf("Server: %s\n", a.config.Name)
		fmt.Printf("Please visit this URL to authenticate:\n%s\n", authURL)
		fmt.Printf("=====================================\n\n")
	}

	// TODO: Implement actual OAuth flow
	// For now, return an error indicating OAuth is not implemented
	a.logger.Warn("OAuth authentication not fully implemented, skipping",
		zap.String("server", a.config.Name))

	return fmt.Errorf("OAuth authentication not yet implemented for core client")
}

// openBrowser attempts to open the OAuth URL in the default browser
func (a *AuthStrategy) openBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "linux":
		// Try to detect if we're in a GUI environment
		if !a.hasGUIEnvironment() {
			return fmt.Errorf("no GUI environment detected")
		}
		cmd = "xdg-open"
		args = []string{url}
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	execCmd := exec.Command(cmd, args...)
	return execCmd.Start()
}

// hasGUIEnvironment checks if a GUI environment is available on Linux
func (a *AuthStrategy) hasGUIEnvironment() bool {
	// Check for common environment variables that indicate GUI
	envVars := []string{"DISPLAY", "WAYLAND_DISPLAY", "XDG_SESSION_TYPE"}

	for _, envVar := range envVars {
		if value := getEnvVar(envVar); value != "" {
			return true
		}
	}

	// Check if xdg-open is available
	if _, err := exec.LookPath("xdg-open"); err == nil {
		return true
	}

	return false
}

// IsAuthError checks if an error indicates authentication failure
func (a *AuthStrategy) IsAuthError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	authErrorKeywords := []string{
		"401", "Unauthorized",
		"403", "Forbidden",
		"invalid_token", "token",
		"authentication", "auth",
		"invalid_grant",
		"access_denied",
	}

	for _, keyword := range authErrorKeywords {
		if containsSubstring(errStr, keyword) {
			return true
		}
	}

	return false
}

// Name returns the name of the authentication strategy
func (a *AuthStrategy) Name() string {
	return string(a.authType)
}

// Helper functions

func containsSubstring(str, substr string) bool {
	if substr == "" {
		return true
	}
	if len(str) < len(substr) {
		return false
	}

	for i := 0; i <= len(str)-len(substr); i++ {
		if str[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// getEnvVar is a placeholder for environment variable access
// In practice, this should use os.Getenv or the secure environment manager
func getEnvVar(_ string) string {
	// This is a simplified implementation
	// In the real implementation, this should use the secure environment manager
	return ""
}

// AuthChain manages multiple authentication strategies with fallback
type AuthChain struct {
	strategies []AuthStrategy
	logger     *zap.Logger
}

// NewAuthChain creates a new authentication chain
func NewAuthChain(config *config.ServerConfig, logger *zap.Logger) *AuthChain {
	var strategies []AuthStrategy

	// Add strategies in order of preference
	if len(config.Headers) > 0 {
		strategies = append(strategies, *NewAuthStrategy(AuthTypeHeaders, config, logger))
	}

	// Always try no-auth as fallback, OAuth as final fallback for HTTP/SSE
	strategies = append(strategies,
		*NewAuthStrategy(AuthTypeNoAuth, config, logger),
		*NewAuthStrategy(AuthTypeOAuth, config, logger))

	return &AuthChain{
		strategies: strategies,
		logger:     logger,
	}
}

// Authenticate tries each strategy in order until one succeeds
func (ac *AuthChain) Authenticate(ctx context.Context, client interface{}) error {
	var lastErr error

	for i, strategy := range ac.strategies {
		ac.logger.Debug("Trying authentication strategy",
			zap.Int("strategy_index", i),
			zap.String("strategy", strategy.Name()))

		if err := strategy.Authenticate(ctx, client); err != nil {
			lastErr = err

			// If it's not an auth error, don't try fallback
			if !strategy.IsAuthError(err) {
				ac.logger.Debug("Non-auth error, not trying fallback",
					zap.String("strategy", strategy.Name()),
					zap.Error(err))
				return err
			}

			ac.logger.Debug("Auth strategy failed, trying next",
				zap.String("strategy", strategy.Name()),
				zap.Error(err))
			continue
		}

		ac.logger.Info("Authentication successful",
			zap.String("strategy", strategy.Name()))
		return nil
	}

	return fmt.Errorf("all authentication strategies failed, last error: %w", lastErr)
}
