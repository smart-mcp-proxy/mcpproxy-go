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
    <string>%s/main.log</string>
    <key>StandardErrorPath</key>
    <string>%s/main-error.log</string>
    <key>WorkingDirectory</key>
    <string>%s</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>%s</string>
        <key>HOME</key>
        <string>%s</string>
        <key>SHELL</key>
        <string>%s</string>
        <key>HOMEBREW_PREFIX</key>
        <string>%s</string>
        <key>HOMEBREW_CELLAR</key>
        <string>%s</string>
        <key>HOMEBREW_REPOSITORY</key>
        <string>%s</string>
    </dict>
</dict>
</plist>`

	// Environment setup launch agent that runs at login to set global environment variables
	envSetupAgentTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.smartmcpproxy.environment</string>
    <key>ProgramArguments</key>
    <array>
        <string>sh</string>
        <string>-c</string>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s/env-setup.log</string>
    <key>StandardErrorPath</key>
    <string>%s/env-setup-error.log</string>
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

// discoverEnvironmentPaths discovers common tool installation paths
func (m *AutostartManager) discoverEnvironmentPaths() (string, map[string]string) {
	homeDir, _ := os.UserHomeDir()

	// Start with essential system paths
	paths := []string{
		"/usr/bin",
		"/bin",
		"/usr/sbin",
		"/sbin",
	}

	// Discover Homebrew paths
	brewPaths := m.discoverBrewPaths()
	paths = append(paths, brewPaths...)

	// Discover Node.js/npm paths
	nodePaths := m.discoverNodePaths()
	paths = append(paths, nodePaths...)

	// Discover Python/uvx/pipx paths
	pythonPaths := m.discoverPythonPaths()
	paths = append(paths, pythonPaths...)

	// Discover other common tool paths
	commonPaths := []string{
		"/usr/local/bin",
		filepath.Join(homeDir, ".local", "bin"),
		filepath.Join(homeDir, ".cargo", "bin"),
		filepath.Join(homeDir, "go", "bin"),
		"/usr/local/go/bin",
	}

	// Filter existing paths
	var validPaths []string
	for _, path := range append(paths, commonPaths...) {
		if m.pathExists(path) && !m.containsPath(validPaths, path) {
			validPaths = append(validPaths, path)
		}
	}

	// Build environment variables
	envVars := make(map[string]string)

	// Set Homebrew variables if available
	if brewPrefix := m.getBrewPrefix(); brewPrefix != "" {
		envVars["HOMEBREW_PREFIX"] = brewPrefix
		envVars["HOMEBREW_CELLAR"] = filepath.Join(brewPrefix, "Cellar")
		envVars["HOMEBREW_REPOSITORY"] = brewPrefix
	}

	return strings.Join(validPaths, ":"), envVars
}

// discoverBrewPaths discovers Homebrew installation paths
func (m *AutostartManager) discoverBrewPaths() []string {
	var paths []string

	// Try common Homebrew locations
	brewPaths := []string{
		"/opt/homebrew/bin", // Apple Silicon
		"/opt/homebrew/sbin",
		"/usr/local/bin", // Intel
		"/usr/local/sbin",
		"/home/linuxbrew/.linuxbrew/bin", // Linux (just in case)
	}

	for _, path := range brewPaths {
		if m.pathExists(path) {
			paths = append(paths, path)
		}
	}

	return paths
}

// discoverNodePaths discovers Node.js/npm/npx paths
func (m *AutostartManager) discoverNodePaths() []string {
	homeDir, _ := os.UserHomeDir()
	var paths []string

	// Check for nvm installations
	nvmDir := filepath.Join(homeDir, ".nvm", "versions", "node")
	if entries, err := os.ReadDir(nvmDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				binPath := filepath.Join(nvmDir, entry.Name(), "bin")
				if m.pathExists(binPath) {
					paths = append(paths, binPath)
				}
			}
		}
	}

	// Check for global npm installations
	npmGlobalPaths := []string{
		filepath.Join(homeDir, ".npm-global", "bin"),
		filepath.Join(homeDir, ".npm-packages", "bin"),
	}

	for _, path := range npmGlobalPaths {
		if m.pathExists(path) {
			paths = append(paths, path)
		}
	}

	return paths
}

// discoverPythonPaths discovers Python/uvx/pipx paths
func (m *AutostartManager) discoverPythonPaths() []string {
	homeDir, _ := os.UserHomeDir()
	var paths []string

	// Check for pipx installations
	pipxPaths := []string{
		filepath.Join(homeDir, ".local", "bin"),
		filepath.Join(homeDir, ".pipx", "bin"),
	}

	// Check for uv installations
	uvPaths := []string{
		filepath.Join(homeDir, ".cargo", "bin"), // uv is often installed via cargo
		filepath.Join(homeDir, ".local", "bin"),
	}

	// Check for pyenv installations
	pyenvPath := filepath.Join(homeDir, ".pyenv", "bin")
	if m.pathExists(pyenvPath) {
		paths = append(paths, pyenvPath)
	}

	for _, path := range append(pipxPaths, uvPaths...) {
		if m.pathExists(path) {
			paths = append(paths, path)
		}
	}

	return paths
}

// getBrewPrefix gets the Homebrew prefix
func (m *AutostartManager) getBrewPrefix() string {
	// Try to get from brew command if available
	if cmd := exec.Command("brew", "--prefix"); cmd.Err == nil {
		if output, err := cmd.Output(); err == nil {
			return strings.TrimSpace(string(output))
		}
	}

	// Fallback to common locations
	commonPrefixes := []string{
		"/opt/homebrew",              // Apple Silicon
		"/usr/local",                 // Intel
		"/home/linuxbrew/.linuxbrew", // Linux
	}

	for _, prefix := range commonPrefixes {
		if m.pathExists(filepath.Join(prefix, "bin", "brew")) {
			return prefix
		}
	}

	return ""
}

// pathExists checks if a path exists
func (m *AutostartManager) pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// containsPath checks if a path is already in the slice
func (m *AutostartManager) containsPath(paths []string, path string) bool {
	for _, p := range paths {
		if p == path {
			return true
		}
	}
	return false
}

// buildEnvironmentSetupScript builds the script for setting up global environment variables
func (m *AutostartManager) buildEnvironmentSetupScript() string {
	discoveredPath, envVars := m.discoverEnvironmentPaths()

	var script strings.Builder

	// Set PATH
	script.WriteString(fmt.Sprintf("launchctl setenv PATH \"%s\";\n", discoveredPath))

	// Set other environment variables
	for key, value := range envVars {
		script.WriteString(fmt.Sprintf("launchctl setenv %s \"%s\";\n", key, value))
	}

	// Set HOME and SHELL
	if homeDir, err := os.UserHomeDir(); err == nil {
		script.WriteString(fmt.Sprintf("launchctl setenv HOME \"%s\";\n", homeDir))
	}

	if shell := os.Getenv("SHELL"); shell != "" {
		script.WriteString(fmt.Sprintf("launchctl setenv SHELL \"%s\";\n", shell))
	}

	return script.String()
}

// IsEnabled checks if autostart is currently enabled
func (m *AutostartManager) IsEnabled() bool {
	if runtime.GOOS != osDarwin {
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
	if runtime.GOOS != osDarwin {
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

	// Discover environment paths and variables
	discoveredPath, envVars := m.discoverEnvironmentPaths()

	// Get environment variable values with defaults
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}

	brewPrefix := envVars["HOMEBREW_PREFIX"]
	if brewPrefix == "" {
		brewPrefix = "/opt/homebrew"
	}

	brewCellar := envVars["HOMEBREW_CELLAR"]
	if brewCellar == "" {
		brewCellar = filepath.Join(brewPrefix, "Cellar")
	}

	brewRepository := envVars["HOMEBREW_REPOSITORY"]
	if brewRepository == "" {
		brewRepository = brewPrefix
	}

	// Create main application plist content
	plistContent := fmt.Sprintf(launchAgentTemplate,
		m.executablePath,
		m.logDir,
		m.logDir,
		m.workingDir,
		discoveredPath,
		homeDir,
		shell,
		brewPrefix,
		brewCellar,
		brewRepository,
	)

	// Write main application plist file
	plistPath := filepath.Join(launchAgentsDir, "com.smartmcpproxy.mcpproxy.plist")
	if err := os.WriteFile(plistPath, []byte(plistContent), 0600); err != nil {
		return fmt.Errorf("failed to write plist file: %w", err)
	}

	// Create environment setup plist for global environment variables
	envScript := m.buildEnvironmentSetupScript()
	envPlistContent := fmt.Sprintf(envSetupAgentTemplate,
		envScript,
		m.logDir,
		m.logDir,
	)

	// Write environment setup plist file
	envPlistPath := filepath.Join(launchAgentsDir, "com.smartmcpproxy.environment.plist")
	if err := os.WriteFile(envPlistPath, []byte(envPlistContent), 0600); err != nil {
		return fmt.Errorf("failed to write environment plist file: %w", err)
	}

	// Load the environment setup agent first
	cmd := exec.Command("launchctl", "load", "-w", envPlistPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		if !strings.Contains(string(output), "already loaded") {
			return fmt.Errorf("failed to load environment setup agent: %w, output: %s", err, output)
		}
	}

	// Load the main application launch agent
	cmd = exec.Command("launchctl", "load", "-w", plistPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		if !strings.Contains(string(output), "already loaded") {
			return fmt.Errorf("failed to load main launch agent: %w, output: %s", err, output)
		}
	}

	return nil
}

// Disable disables autostart functionality
func (m *AutostartManager) Disable() error {
	if runtime.GOOS != osDarwin {
		return fmt.Errorf("autostart is only supported on macOS")
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// Paths to both plist files
	plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", "com.smartmcpproxy.mcpproxy.plist")
	envPlistPath := filepath.Join(homeDir, "Library", "LaunchAgents", "com.smartmcpproxy.environment.plist")

	// Unload and remove main application launch agent
	if _, err := os.Stat(plistPath); err == nil {
		cmd := exec.Command("launchctl", "unload", "-w", plistPath)
		if output, err := cmd.CombinedOutput(); err != nil {
			if !strings.Contains(string(output), "not loaded") {
				return fmt.Errorf("failed to unload main launch agent: %w, output: %s", err, output)
			}
		}

		if err := os.Remove(plistPath); err != nil {
			return fmt.Errorf("failed to remove main plist file: %w", err)
		}
	}

	// Unload and remove environment setup launch agent
	if _, err := os.Stat(envPlistPath); err == nil {
		cmd := exec.Command("launchctl", "unload", "-w", envPlistPath)
		if output, err := cmd.CombinedOutput(); err != nil {
			if !strings.Contains(string(output), "not loaded") {
				return fmt.Errorf("failed to unload environment setup agent: %w, output: %s", err, output)
			}
		}

		if err := os.Remove(envPlistPath); err != nil {
			return fmt.Errorf("failed to remove environment plist file: %w", err)
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
