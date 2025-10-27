//go:build !nogui && !headless && !linux

package tray

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
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
        <string>/bin/sh</string>
        <string>-c</string>
        <string>%s</string>
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
</dict>
</plist>`
)

const launchAgentLabel = "com.smartmcpproxy.mcpproxy"

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
func (m *AutostartManager) discoverEnvironmentPaths() (discoveredPath string, envVars map[string]string) {
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
	envVars = make(map[string]string)

	// Set Homebrew variables if available
	if brewPrefix := m.getBrewPrefix(); brewPrefix != "" {
		envVars["HOMEBREW_PREFIX"] = brewPrefix
		envVars["HOMEBREW_CELLAR"] = filepath.Join(brewPrefix, "Cellar")
		envVars["HOMEBREW_REPOSITORY"] = brewPrefix
	}

	return strings.Join(validPaths, ":"), envVars
}

// buildLaunchScript constructs the shell script that prepares the environment and
// executes the tray binary. Using a shell wrapper lets us avoid registering a
// second LaunchAgent purely for environment setup.
func (m *AutostartManager) buildLaunchScript(discoveredPath string, envVars map[string]string, homeDir, shell, brewPrefix, brewCellar, brewRepository string) string {
	assignments := map[string]string{
		"PATH":                discoveredPath,
		"HOME":                homeDir,
		"SHELL":               shell,
		"HOMEBREW_PREFIX":     brewPrefix,
		"HOMEBREW_CELLAR":     brewCellar,
		"HOMEBREW_REPOSITORY": brewRepository,
	}

	// Allow discovered env vars (e.g. from brew detection) to override defaults.
	for key, value := range envVars {
		assignments[key] = value
	}

	var keys []string
	for key := range assignments {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var scriptBuilder strings.Builder
	scriptBuilder.WriteString("set -e\n")
	for _, key := range keys {
		value := assignments[key]
		scriptBuilder.WriteString(fmt.Sprintf("export %s=%s\n", key, strconv.Quote(value)))
	}
	scriptBuilder.WriteString(fmt.Sprintf("exec %s --tray=true --log-to-file=true\n", strconv.Quote(m.executablePath)))

	return scriptBuilder.String()
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

// IsEnabled checks if autostart is currently enabled
func (m *AutostartManager) IsEnabled() bool {
	if runtime.GOOS != osDarwin {
		return false
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false
	}

	plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", launchAgentLabel+".plist")
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

	launchScript := m.buildLaunchScript(discoveredPath, envVars, homeDir, shell, brewPrefix, brewCellar, brewRepository)

	plistContent := fmt.Sprintf(launchAgentTemplate,
		launchScript,
		m.logDir,
		m.logDir,
		m.workingDir,
	)

	plistPath := filepath.Join(launchAgentsDir, launchAgentLabel+".plist")
	plistChanged, err := m.ensurePlistContents(plistPath, plistContent)
	if err != nil {
		return err
	}

	if !plistChanged && m.isAgentLoaded(launchAgentLabel) {
		return nil
	}

	if err := m.loadLaunchAgent(plistPath); err != nil {
		return err
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

	plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", launchAgentLabel+".plist")

	if _, err := os.Stat(plistPath); err == nil {
		if err := m.unloadLaunchAgent(plistPath); err != nil {
			return err
		}

		if err := os.Remove(plistPath); err != nil {
			return fmt.Errorf("failed to remove launch agent plist: %w", err)
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

// ensurePlistContents writes the desired plist content only when it changes.
// Returns true when the file was created or updated.
func (m *AutostartManager) ensurePlistContents(path, content string) (bool, error) {
	if existing, err := os.ReadFile(path); err == nil {
		if string(existing) == content {
			return false, nil
		}
	}

	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return false, fmt.Errorf("failed to write plist file: %w", err)
	}

	return true, nil
}

// isAgentLoaded checks whether the LaunchAgent is currently registered with launchd.
func (m *AutostartManager) isAgentLoaded(label string) bool {
	return exec.Command("launchctl", "list", label).Run() == nil
}

// loadLaunchAgent loads the plist with launchctl when required.
func (m *AutostartManager) loadLaunchAgent(plistPath string) error {
	cmd := exec.Command("launchctl", "load", "-w", plistPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		if strings.Contains(string(output), "already loaded") {
			return nil
		}
		return fmt.Errorf("failed to load launch agent: %w, output: %s", err, output)
	}
	return nil
}

// unloadLaunchAgent unloads the plist from launchd.
func (m *AutostartManager) unloadLaunchAgent(plistPath string) error {
	cmd := exec.Command("launchctl", "unload", "-w", plistPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		if strings.Contains(string(output), "not loaded") {
			return nil
		}
		return fmt.Errorf("failed to unload launch agent: %w, output: %s", err, output)
	}
	return nil
}
