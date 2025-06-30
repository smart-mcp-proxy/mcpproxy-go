//go:build !nogui && !headless && !linux

package tray

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	launchAgentTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.smartmcpproxy.mcpproxy</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>--tray=true</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <false/>
    <key>StandardOutPath</key>
    <string>%s/mcpproxy.log</string>
    <key>StandardErrorPath</key>
    <string>%s/mcpproxy-error.log</string>
    <key>WorkingDirectory</key>
    <string>%s</string>
</dict>
</plist>`
)

// AutostartManager handles autostart functionality across platforms
type AutostartManager struct {
	executablePath string
	logDir         string
	workingDir     string
}

// NewAutostartManager creates a new autostart manager
func NewAutostartManager() (*AutostartManager, error) {
	execPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("failed to get executable path: %w", err)
	}

	// Resolve symlinks to get the actual executable path
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve executable path: %w", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	logDir := filepath.Join(homeDir, "Library", "Logs", "mcpproxy")
	workingDir := filepath.Dir(execPath)

	return &AutostartManager{
		executablePath: execPath,
		logDir:         logDir,
		workingDir:     workingDir,
	}, nil
}

// IsEnabled checks if autostart is currently enabled
func (m *AutostartManager) IsEnabled() bool {
	if runtime.GOOS != "darwin" {
		return false
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false
	}

	plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", "com.smartmcpproxy.mcpproxy.plist")
	_, err = os.Stat(plistPath)
	return err == nil
}

// Enable enables autostart functionality
func (m *AutostartManager) Enable() error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("autostart is only supported on macOS")
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// Create LaunchAgents directory if it doesn't exist
	launchAgentsDir := filepath.Join(homeDir, "Library", "LaunchAgents")
	if err := os.MkdirAll(launchAgentsDir, 0755); err != nil {
		return fmt.Errorf("failed to create LaunchAgents directory: %w", err)
	}

	// Create log directory if it doesn't exist
	if err := os.MkdirAll(m.logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	// Create plist content
	plistContent := fmt.Sprintf(launchAgentTemplate,
		m.executablePath,
		m.logDir,
		m.logDir,
		m.workingDir)

	// Write plist file
	plistPath := filepath.Join(launchAgentsDir, "com.smartmcpproxy.mcpproxy.plist")
	if err := os.WriteFile(plistPath, []byte(plistContent), 0644); err != nil {
		return fmt.Errorf("failed to write plist file: %w", err)
	}

	// Load the launch agent
	cmd := exec.Command("launchctl", "load", "-w", plistPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		// If already loaded, that's okay
		if !strings.Contains(string(output), "already loaded") {
			return fmt.Errorf("failed to load launch agent: %w, output: %s", err, output)
		}
	}

	return nil
}

// Disable disables autostart functionality
func (m *AutostartManager) Disable() error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("autostart is only supported on macOS")
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", "com.smartmcpproxy.mcpproxy.plist")

	// Unload the launch agent if it exists
	if _, err := os.Stat(plistPath); err == nil {
		cmd := exec.Command("launchctl", "unload", "-w", plistPath)
		if output, err := cmd.CombinedOutput(); err != nil {
			// If not loaded, that's okay
			if !strings.Contains(string(output), "not loaded") {
				return fmt.Errorf("failed to unload launch agent: %w, output: %s", err, output)
			}
		}

		// Remove the plist file
		if err := os.Remove(plistPath); err != nil {
			return fmt.Errorf("failed to remove plist file: %w", err)
		}
	}

	return nil
}

// Toggle toggles autostart functionality
func (m *AutostartManager) Toggle() error {
	if m.IsEnabled() {
		return m.Disable()
	}
	return m.Enable()
}
