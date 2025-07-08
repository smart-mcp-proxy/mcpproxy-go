package secureenv

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	osWindows = "windows"
	osDarwin  = "darwin"
)

// EnvConfig represents environment configuration for secure filtering
type EnvConfig struct {
	InheritSystemSafe bool              `json:"inherit_system_safe"`
	AllowedSystemVars []string          `json:"allowed_system_vars"`
	CustomVars        map[string]string `json:"custom_vars"`
}

// PathDiscovery contains auto-discovered paths for common tools
type PathDiscovery struct {
	HomePath        string
	BrewPaths       []string
	NodePaths       []string
	PythonPaths     []string
	RustPaths       []string
	GoPaths         []string
	SystemPaths     []string
	DiscoveredPaths []string
	AvailableTools  map[string]string
}

// DefaultEnvConfig returns default environment configuration with safe system variables
func DefaultEnvConfig() *EnvConfig {
	allowedVars := []string{
		"PATH",     // Essential for finding executables
		"HOME",     // User directory path (Unix)
		"TMPDIR",   // Temporary directory (Unix)
		"TEMP",     // Temporary directory (Windows)
		"TMP",      // Temporary directory (Windows)
		"SHELL",    // Default shell
		"TERM",     // Terminal type
		"LANG",     // Language settings
		"USER",     // Current user (Unix)
		"USERNAME", // Current user (Windows)
	}

	// Add Windows-specific variables
	if runtime.GOOS == osWindows {
		allowedVars = append(allowedVars,
			"USERPROFILE",  // User profile directory
			"APPDATA",      // Application data directory
			"LOCALAPPDATA", // Local application data directory
			"PROGRAMFILES", // Program files directory
			"SYSTEMROOT",   // System root directory
			"COMSPEC",      // Command interpreter
		)
	}

	// Add Unix-specific variables
	if runtime.GOOS != osWindows {
		allowedVars = append(allowedVars,
			"XDG_CONFIG_HOME", // XDG config directory
			"XDG_DATA_HOME",   // XDG data directory
			"XDG_CACHE_HOME",  // XDG cache directory
			"XDG_RUNTIME_DIR", // XDG runtime directory
		)
	}

	// Add locale-related variables
	localeVars := []string{
		"LC_ALL", "LC_CTYPE", "LC_NUMERIC", "LC_TIME", "LC_COLLATE",
		"LC_MONETARY", "LC_MESSAGES", "LC_PAPER", "LC_NAME", "LC_ADDRESS",
		"LC_TELEPHONE", "LC_MEASUREMENT", "LC_IDENTIFICATION",
	}
	allowedVars = append(allowedVars, localeVars...)

	return &EnvConfig{
		InheritSystemSafe: true,
		AllowedSystemVars: allowedVars,
		CustomVars:        make(map[string]string),
	}
}

// Manager handles secure environment variable filtering
type Manager struct {
	config        *EnvConfig
	pathDiscovery *PathDiscovery
}

// NewManager creates a new secure environment manager
func NewManager(config *EnvConfig) *Manager {
	if config == nil {
		config = DefaultEnvConfig()
	}

	manager := &Manager{
		config: config,
	}

	// Perform path discovery for robust PATH handling
	manager.pathDiscovery = manager.discoverPaths()

	return manager
}

// discoverPaths automatically discovers common tool installation paths
func (m *Manager) discoverPaths() *PathDiscovery {
	discovery := &PathDiscovery{
		AvailableTools: make(map[string]string),
	}

	// Get home directory
	homeDir, _ := os.UserHomeDir()
	discovery.HomePath = homeDir

	// Discover paths based on operating system
	switch runtime.GOOS {
	case osDarwin:
		discovery = m.discoverMacOSPaths(discovery)
	case osWindows:
		discovery = m.discoverWindowsPaths(discovery)
	default:
		discovery = m.discoverUnixPaths(discovery)
	}

	// Build comprehensive discovered paths list
	discovery.DiscoveredPaths = m.buildDiscoveredPaths(discovery)

	// Discover available tools
	discovery.AvailableTools = m.discoverAvailableTools(discovery.DiscoveredPaths)

	return discovery
}

// discoverMacOSPaths discovers paths specific to macOS
func (m *Manager) discoverMacOSPaths(discovery *PathDiscovery) *PathDiscovery {
	homeDir := discovery.HomePath

	// System paths (always safe)
	discovery.SystemPaths = []string{
		"/usr/bin",
		"/bin",
		"/usr/sbin",
		"/sbin",
		"/usr/local/bin",
		"/usr/local/sbin",
	}

	// Homebrew paths (Intel and Apple Silicon)
	potentialBrewPaths := []string{
		"/opt/homebrew/bin", // Apple Silicon default
		"/opt/homebrew/sbin",
		"/usr/local/bin", // Intel default (also in system paths)
		"/usr/local/sbin",
	}

	for _, path := range potentialBrewPaths {
		if m.pathExists(path) {
			discovery.BrewPaths = append(discovery.BrewPaths, path)
		}
	}

	// Node.js paths (nvm, brew, system)
	if homeDir != "" {
		potentialNodePaths := []string{
			filepath.Join(homeDir, ".nvm/versions/node/*/bin"), // Will be expanded
			filepath.Join(homeDir, ".volta/bin"),
			filepath.Join(homeDir, ".fnm/versions/*/installation/bin"),
		}

		for _, pathPattern := range potentialNodePaths {
			if strings.Contains(pathPattern, "*") {
				// Expand glob pattern
				expanded := m.expandGlobPath(pathPattern)
				discovery.NodePaths = append(discovery.NodePaths, expanded...)
			} else if m.pathExists(pathPattern) {
				discovery.NodePaths = append(discovery.NodePaths, pathPattern)
			}
		}
	}

	// Python paths (pyenv, pip user, system)
	if homeDir != "" {
		potentialPythonPaths := []string{
			filepath.Join(homeDir, ".pyenv/versions/*/bin"), // Will be expanded
			filepath.Join(homeDir, ".local/bin"),            // pip user installs
			filepath.Join(homeDir, "Library/Python/*/bin"),  // Will be expanded
		}

		for _, pathPattern := range potentialPythonPaths {
			if strings.Contains(pathPattern, "*") {
				expanded := m.expandGlobPath(pathPattern)
				discovery.PythonPaths = append(discovery.PythonPaths, expanded...)
			} else if m.pathExists(pathPattern) {
				discovery.PythonPaths = append(discovery.PythonPaths, pathPattern)
			}
		}
	}

	// Rust paths (cargo)
	if homeDir != "" {
		rustPath := filepath.Join(homeDir, ".cargo/bin")
		if m.pathExists(rustPath) {
			discovery.RustPaths = append(discovery.RustPaths, rustPath)
		}
	}

	// Go paths
	goPaths := []string{
		"/usr/local/go/bin",
	}
	if homeDir != "" {
		goPaths = append(goPaths, filepath.Join(homeDir, "go/bin"))
	}

	for _, path := range goPaths {
		if m.pathExists(path) {
			discovery.GoPaths = append(discovery.GoPaths, path)
		}
	}

	return discovery
}

// discoverWindowsPaths discovers paths specific to Windows
func (m *Manager) discoverWindowsPaths(discovery *PathDiscovery) *PathDiscovery {
	// System paths
	discovery.SystemPaths = []string{
		"C:\\Windows\\System32",
		"C:\\Windows",
		"C:\\Windows\\System32\\Wbem",
		"C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\",
	}

	// Program Files paths
	programFilesPaths := []string{
		"C:\\Program Files\\Git\\bin",
		"C:\\Program Files\\nodejs",
		"C:\\Program Files (x86)\\nodejs",
	}

	for _, path := range programFilesPaths {
		if m.pathExists(path) {
			discovery.NodePaths = append(discovery.NodePaths, path)
		}
	}

	return discovery
}

// discoverUnixPaths discovers paths for generic Unix systems
func (m *Manager) discoverUnixPaths(discovery *PathDiscovery) *PathDiscovery {
	// System paths
	discovery.SystemPaths = []string{
		"/usr/bin",
		"/bin",
		"/usr/sbin",
		"/sbin",
		"/usr/local/bin",
		"/usr/local/sbin",
	}

	return discovery
}

// buildDiscoveredPaths builds a comprehensive list of discovered paths in priority order
func (m *Manager) buildDiscoveredPaths(discovery *PathDiscovery) []string {
	var paths []string

	// Priority order: Homebrew, Node.js, Python, Rust, Go, System
	// This ensures user-installed tools take precedence over system ones

	// Homebrew paths (high priority for common tools)
	paths = append(paths, discovery.BrewPaths...)

	// Node.js paths (for npx, npm, etc.)
	paths = append(paths, discovery.NodePaths...)

	// Python paths (for uvx, pip, etc.)
	paths = append(paths, discovery.PythonPaths...)

	// Rust paths (for cargo tools)
	paths = append(paths, discovery.RustPaths...)

	// Go paths
	paths = append(paths, discovery.GoPaths...)

	// System paths (lowest priority but essential)
	paths = append(paths, discovery.SystemPaths...)

	// Remove duplicates while preserving order
	return m.removeDuplicatePaths(paths)
}

// discoverAvailableTools checks which tools are actually available in the discovered paths
func (m *Manager) discoverAvailableTools(paths []string) map[string]string {
	tools := make(map[string]string)

	// Common tools to check for
	commonTools := []string{
		"node", "npm", "npx", "yarn", "pnpm",
		"python", "python3", "pip", "pip3", "uvx",
		"go", "cargo", "rustc",
		"git", "curl", "wget",
	}

	for _, tool := range commonTools {
		if toolPath := m.findToolInPaths(tool, paths); toolPath != "" {
			tools[tool] = toolPath
		}
	}

	return tools
}

// findToolInPaths searches for a tool executable in the given paths
func (m *Manager) findToolInPaths(tool string, paths []string) string {
	for _, path := range paths {
		var toolPath string
		if runtime.GOOS == osWindows {
			toolPath = filepath.Join(path, tool+".exe")
		} else {
			toolPath = filepath.Join(path, tool)
		}

		if m.fileExists(toolPath) && m.isExecutable(toolPath) {
			return toolPath
		}
	}
	return ""
}

// BuildSecureEnvironment builds a secure environment variable list
func (m *Manager) BuildSecureEnvironment() []string {
	var envVars []string

	// Add safe system environment variables if enabled
	if m.config.InheritSystemSafe {
		envVars = append(envVars, m.getFilteredSystemEnv()...)
	}

	// Add custom environment variables from config
	for k, v := range m.config.CustomVars {
		envVars = append(envVars, k+"="+v)
	}

	// Ensure PATH is comprehensive by checking and enhancing it
	envVars = m.ensureComprehensivePath(envVars)

	return envVars
}

// ensureComprehensivePath ensures PATH includes all discovered tool paths
func (m *Manager) ensureComprehensivePath(envVars []string) []string {
	// Find existing PATH in environment
	var existingPath string
	var pathIndex = -1

	for i, envVar := range envVars {
		if strings.HasPrefix(envVar, "PATH=") {
			existingPath = strings.TrimPrefix(envVar, "PATH=")
			pathIndex = i
			break
		}
	}

	// Build enhanced PATH
	enhancedPath := m.buildEnhancedPath(existingPath)

	// Replace or add PATH
	pathVar := "PATH=" + enhancedPath
	if pathIndex >= 0 {
		envVars[pathIndex] = pathVar
	} else {
		envVars = append(envVars, pathVar)
	}

	return envVars
}

// buildEnhancedPath builds a comprehensive PATH by combining existing path with discovered paths
func (m *Manager) buildEnhancedPath(existingPath string) string {
	var pathComponents []string

	// Start with discovered paths (higher priority)
	pathComponents = append(pathComponents, m.pathDiscovery.DiscoveredPaths...)

	// Add existing path components if they exist and aren't duplicates
	if existingPath != "" {
		existingComponents := strings.Split(existingPath, string(os.PathListSeparator))
		for _, component := range existingComponents {
			component = strings.TrimSpace(component)
			if component != "" && !m.containsPath(pathComponents, component) {
				pathComponents = append(pathComponents, component)
			}
		}
	}

	// Remove duplicates and non-existent paths
	validPaths := make([]string, 0, len(pathComponents))
	seen := make(map[string]bool)

	for _, path := range pathComponents {
		if path != "" && !seen[path] && m.pathExists(path) {
			validPaths = append(validPaths, path)
			seen[path] = true
		}
	}

	return strings.Join(validPaths, string(os.PathListSeparator))
}

// getFilteredSystemEnv returns filtered system environment variables
func (m *Manager) getFilteredSystemEnv() []string {
	systemEnv := os.Environ()
	var filtered []string

	for _, envVar := range systemEnv {
		if m.isEnvVarAllowed(envVar) {
			filtered = append(filtered, envVar)
		}
	}

	return filtered
}

// isEnvVarAllowed checks if an environment variable is allowed based on the allow-list
func (m *Manager) isEnvVarAllowed(envVar string) bool {
	parts := strings.SplitN(envVar, "=", 2)
	if len(parts) != 2 {
		return false
	}

	key := parts[0]

	// Check against allow-list
	for _, allowedVar := range m.config.AllowedSystemVars {
		if key == allowedVar {
			return true
		}

		// Support wildcard matching for locale variables (LC_*)
		if strings.HasSuffix(allowedVar, "*") {
			prefix := strings.TrimSuffix(allowedVar, "*")
			if strings.HasPrefix(key, prefix) {
				return true
			}
		}
	}

	return false
}

// GetSystemEnvVar gets a specific system environment variable if allowed
func (m *Manager) GetSystemEnvVar(key string) (string, bool) {
	if !m.isKeyAllowed(key) {
		return "", false
	}

	value := os.Getenv(key)
	return value, value != ""
}

// isKeyAllowed checks if a key is in the allow-list
func (m *Manager) isKeyAllowed(key string) bool {
	for _, allowedVar := range m.config.AllowedSystemVars {
		if key == allowedVar {
			return true
		}

		// Support wildcard matching
		if strings.HasSuffix(allowedVar, "*") {
			prefix := strings.TrimSuffix(allowedVar, "*")
			if strings.HasPrefix(key, prefix) {
				return true
			}
		}
	}

	return false
}

// ValidateConfig validates the environment configuration
func (m *Manager) ValidateConfig() error {
	// Environment configuration is always valid with our allow-list approach
	// The worst case is an empty environment, which is still secure
	return nil
}

// GetFilteredEnvCount returns the number of filtered environment variables
func (m *Manager) GetFilteredEnvCount() (filteredCount, totalCount int) {
	systemEnv := os.Environ()
	filteredEnv := m.getFilteredSystemEnv()
	return len(filteredEnv), len(systemEnv)
}

// GetPathDiscovery returns the path discovery information for debugging
func (m *Manager) GetPathDiscovery() *PathDiscovery {
	return m.pathDiscovery
}

// Utility functions

// pathExists checks if a directory exists
func (m *Manager) pathExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// fileExists checks if a file exists
func (m *Manager) fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// isExecutable checks if a file is executable (Unix systems)
func (m *Manager) isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}

	if runtime.GOOS == osWindows {
		// On Windows, .exe files are generally executable
		return strings.HasSuffix(strings.ToLower(path), ".exe")
	}

	// On Unix systems, check execute permission
	return info.Mode()&0111 != 0
}

// expandGlobPath expands a glob pattern to actual paths
func (m *Manager) expandGlobPath(pattern string) []string {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}

	var validPaths []string
	for _, match := range matches {
		if m.pathExists(match) {
			validPaths = append(validPaths, match)
		}
	}

	return validPaths
}

// removeDuplicatePaths removes duplicate paths while preserving order
func (m *Manager) removeDuplicatePaths(paths []string) []string {
	seen := make(map[string]bool)
	var unique []string

	for _, path := range paths {
		if path != "" && !seen[path] {
			unique = append(unique, path)
			seen[path] = true
		}
	}

	return unique
}

// containsPath checks if a path is already in the list
func (m *Manager) containsPath(paths []string, target string) bool {
	for _, path := range paths {
		if path == target {
			return true
		}
	}
	return false
}
