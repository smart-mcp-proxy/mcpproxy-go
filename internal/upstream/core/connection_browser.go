package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"go.uber.org/zap"
)

// openBrowser attempts to open the OAuth URL in the default browser
func (c *Client) openBrowser(authURL string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case osWindows:
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", authURL}
	case osDarwin:
		cmd = "open"
		args = []string{authURL}
	case osLinux:
		// Always attempt xdg-open, but warn when no GUI/session indicators are found.
		if !c.hasGUIEnvironment() {
			c.logger.Warn("No GUI session detected - attempting to launch browser anyway. If nothing appears, copy/paste the URL manually.",
				zap.String("server", c.config.Name))
		}

		if _, err := exec.LookPath("xdg-open"); err != nil {
			return fmt.Errorf("xdg-open not found in PATH: %w", err)
		}

		cmd = "xdg-open"
		args = []string{authURL}
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	execCmd := exec.Command(cmd, args...)
	return execCmd.Start()
}

// hasGUIEnvironment checks if a GUI environment is available on Linux
func (c *Client) hasGUIEnvironment() bool {
	// Check for common environment variables that indicate GUI
	envVars := []string{"DISPLAY", "WAYLAND_DISPLAY", "XDG_SESSION_TYPE"}

	for _, envVar := range envVars {
		if value := os.Getenv(envVar); value != "" {
			return true
		}
	}

	return false
}

// isDeferOAuthForTray checks if OAuth should be deferred to prevent tray UI blocking.
// It accepts a context to check if this is a manual OAuth flow triggered by 'auth login' CLI command.
func (c *Client) isDeferOAuthForTray(ctx context.Context) bool {
	// CRITICAL FIX: Never defer manual OAuth flows triggered by 'auth login' CLI command
	// This fixes issue #155 where 'mcpproxy auth login' doesn't open browser windows
	if c.isManualOAuthFlow(ctx) {
		c.logger.Info("🎯 Manual OAuth flow detected (auth login command) - NOT deferring",
			zap.String("server", c.config.Name))
		return false
	}

	// Check if we're in tray mode by looking for tray-specific environment or configuration
	// During initial server startup, we should defer OAuth to prevent blocking the tray UI

	tokenManager := oauth.GetTokenStoreManager()
	if tokenManager == nil {
		return false
	}

	// If OAuth has been recently attempted (within last 5 minutes), don't defer
	// This allows manual retry flows to work
	if tokenManager.HasRecentOAuthCompletion(c.config.Name) {
		c.logger.Debug("OAuth recently attempted - allowing manual flow",
			zap.String("server", c.config.Name))
		return false
	}

	// Check if this is an automatic retry vs manual trigger
	// Defer only during automatic connection attempts to prevent UI blocking
	// Manual OAuth flows (triggered via tray menu) should proceed immediately

	c.logger.Debug("Deferring OAuth during automatic connection attempt",
		zap.String("server", c.config.Name))
	return true
}
