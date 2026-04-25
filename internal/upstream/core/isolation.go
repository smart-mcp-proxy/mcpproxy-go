package core

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Command and package manager constants
const (
	cmdPython   = "python"
	cmdPython3  = "python3"
	cmdPip      = "pip"
	cmdPipx     = "pipx"
	cmdNode     = "node"
	cmdNpm      = "npm"
	cmdNpx      = "npx"
	cmdYarn     = "yarn"
	cmdGo       = "go"
	cmdCargo    = "cargo"
	cmdRustc    = "rustc"
	cmdRuby     = "ruby"
	cmdGem      = "gem"
	cmdPhp      = "php"
	cmdComposer = "composer"
	cmdSh       = "sh"
	cmdBash     = "bash"
	cmdUvx      = "uvx"
	cmdRun      = "run"
	cmdDocker   = "docker"

	pathBinBash = "/bin/bash"
	pathBinSh   = "/bin/sh"

	// Docker log driver constants
	logDriverJSONFile = "json-file"
)

// IsolationManager handles Docker isolation logic for MCP servers
type IsolationManager struct {
	globalConfig *config.DockerIsolationConfig
	logger       *zap.Logger

	// warnedServers dedups the "per-server isolation enabled but global is
	// off" warning so we don't spam the log on every ShouldIsolate() call
	// (which runs on each tool dispatch). Keyed by server name.
	warnedServers sync.Map
}

// NewIsolationManager creates a new isolation manager.
func NewIsolationManager(globalConfig *config.DockerIsolationConfig) *IsolationManager {
	return &IsolationManager{
		globalConfig: globalConfig,
	}
}

// NewIsolationManagerWithLogger creates a new isolation manager with a
// structured logger. The logger is optional — when nil, warnings about
// ignored per-server isolation opt-ins are silently dropped (callers that
// care about those warnings should pass a logger).
func NewIsolationManagerWithLogger(globalConfig *config.DockerIsolationConfig, logger *zap.Logger) *IsolationManager {
	return &IsolationManager{
		globalConfig: globalConfig,
		logger:       logger,
	}
}

// SetLogger sets the logger on an existing IsolationManager. Intended for
// call sites that build the manager before a logger is available (e.g. in
// config-time code) but want per-server warnings at runtime.
func (im *IsolationManager) SetLogger(logger *zap.Logger) {
	im.logger = logger
}

// HasLocalFilePath checks if server arguments contain local file paths
func (im *IsolationManager) HasLocalFilePath(serverConfig *config.ServerConfig) bool {
	for _, arg := range serverConfig.Args {
		if isLocalFilePath(arg) {
			return true
		}
	}
	return false
}

// isLocalFilePath checks if a path is a local file path (supports both Unix and Windows paths)
func isLocalFilePath(path string) bool {
	if path == "" {
		return false
	}

	// Unix-style absolute paths: /path/to/file
	if strings.HasPrefix(path, "/") {
		return true
	}

	// Unix-style relative paths: ./file, ../file, ~/file
	if strings.HasPrefix(path, "./") ||
		strings.HasPrefix(path, "../") ||
		strings.HasPrefix(path, "~/") {
		return true
	}

	// Windows-style absolute paths: C:\path, D:\path, etc.
	if len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
		return true
	}

	// Windows-style relative paths: .\file, ..\file
	if strings.HasPrefix(path, ".\\") || strings.HasPrefix(path, "..\\") {
		return true
	}

	// Windows UNC paths: \\server\share
	if strings.HasPrefix(path, "\\\\") {
		return true
	}

	// Check if it looks like a file path with extension
	// (e.g., script.py, index.js, but not git+https://...)
	if !strings.Contains(path, "://") &&
		(strings.HasSuffix(path, ".py") ||
			strings.HasSuffix(path, ".js") ||
			strings.HasSuffix(path, ".ts") ||
			strings.HasSuffix(path, ".sh") ||
			strings.HasSuffix(path, ".rb") ||
			strings.HasSuffix(path, ".php")) {
		return true
	}

	return false
}

// GetDockerIsolationWarning returns a warning message if Docker isolation is enabled with local files
func (im *IsolationManager) GetDockerIsolationWarning(serverConfig *config.ServerConfig) string {
	if !im.ShouldIsolate(serverConfig) {
		return ""
	}

	if im.HasLocalFilePath(serverConfig) {
		return "⚠️  Docker isolation is enabled, but the server uses local file paths. " +
			"The files must be available inside the Docker container, or you can disable " +
			"Docker isolation for this server by setting isolation.enabled=false in the server config."
	}

	return ""
}

// ShouldIsolate determines if a server should be isolated based on global and server config
func (im *IsolationManager) ShouldIsolate(serverConfig *config.ServerConfig) bool {
	// Check if global isolation is disabled
	if im.globalConfig == nil || !im.globalConfig.Enabled {
		// If the user explicitly opted THIS server into isolation (via
		// isolation.enabled: true), warn them once that the per-server
		// setting is being ignored because the global flag is off. Silent
		// short-circuits here are a common cause of confusion — telemetry
		// shows many users configure per-server opt-ins and then wonder why
		// nothing runs in a container.
		if im.hasExplicitPerServerOptIn(serverConfig) {
			im.warnPerServerIgnoredOnce(serverConfig.Name)
		}
		return false
	}

	// Check if server has isolation config and it's explicitly disabled
	// With *bool: nil means "inherit global", explicit false means "disabled"
	if serverConfig.Isolation != nil && serverConfig.Isolation.Enabled != nil && !*serverConfig.Isolation.Enabled {
		return false
	}

	// Only isolate stdio servers (HTTP servers don't need Docker isolation)
	if serverConfig.Command == "" {
		return false
	}

	// Skip isolation for servers that are already using Docker
	// These are typically pre-configured Docker containers that don't need additional isolation
	cmdName := filepath.Base(serverConfig.Command)
	if cmdName == "docker" || strings.Contains(serverConfig.Command, "docker") {
		return false
	}

	return true
}

// hasExplicitPerServerOptIn returns true when the server config explicitly
// sets isolation.enabled = true. Nil / missing means "inherit global" —
// that's NOT an opt-in for our warning purposes.
func (im *IsolationManager) hasExplicitPerServerOptIn(serverConfig *config.ServerConfig) bool {
	if serverConfig == nil || serverConfig.Isolation == nil {
		return false
	}
	return serverConfig.Isolation.Enabled != nil && *serverConfig.Isolation.Enabled
}

// warnPerServerIgnoredOnce emits a one-time warning (deduped by server name)
// when a per-server isolation opt-in is being ignored because the global
// flag is off.
func (im *IsolationManager) warnPerServerIgnoredOnce(serverName string) {
	if im.logger == nil {
		return
	}
	if _, loaded := im.warnedServers.LoadOrStore(serverName, struct{}{}); loaded {
		return
	}
	im.logger.Warn("per-server docker isolation opt-in ignored: global docker_isolation.enabled is false",
		zap.String("server", serverName),
		zap.String("hint", "set docker_isolation.enabled=true in your config (or toggle it in the Web UI Security page) to honor per-server isolation settings"),
	)
}

// DetectRuntimeType detects the runtime type based on the command
func (im *IsolationManager) DetectRuntimeType(command string) string {
	// Extract just the command name without path
	cmdName := filepath.Base(command)

	// Handle common runtime commands
	switch cmdName {
	case cmdPython, cmdPython3, "python3.11", "python3.12", "python3.13":
		return cmdPython
	case cmdUvx:
		return cmdUvx
	case cmdPip, "pip3":
		return cmdPip
	case cmdPipx:
		return cmdPipx
	case cmdNode:
		return cmdNode
	case cmdNpm:
		return cmdNpm
	case cmdNpx:
		return cmdNpx
	case cmdYarn:
		return cmdYarn
	case cmdGo:
		return cmdGo
	case cmdCargo:
		return cmdCargo
	case cmdRustc:
		return cmdRustc
	case cmdRuby:
		return cmdRuby
	case cmdGem:
		return cmdGem
	case cmdPhp:
		return cmdPhp
	case cmdComposer:
		return cmdComposer
	case cmdSh, pathBinSh:
		return cmdSh
	case cmdBash, pathBinBash:
		return cmdBash
	default:
		// Check for common patterns
		if strings.Contains(strings.ToLower(cmdName), "python") {
			return "python"
		}
		if strings.Contains(strings.ToLower(cmdName), "node") {
			return cmdNode
		}

		// Default to binary for unknown commands
		return "binary"
	}
}

// GetDockerImage returns the appropriate Docker image for a server
func (im *IsolationManager) GetDockerImage(serverConfig *config.ServerConfig, runtimeType string) (string, error) {
	// Check if server has custom image override
	if serverConfig.Isolation != nil && serverConfig.Isolation.Image != "" {
		return im.buildFullImageName(serverConfig.Isolation.Image), nil
	}

	// Use default image from global config
	if image, exists := im.globalConfig.DefaultImages[runtimeType]; exists {
		return im.buildFullImageName(image), nil
	}

	// Fallback to alpine for unknown runtime types
	return im.buildFullImageName("alpine:3.18"), nil
}

// ResolvedIsolationDefaults captures the per-runtime default values that
// would be used for a server when no per-server overrides are set. It is
// used by the REST API to expose contextual placeholders to UI clients
// (notably the macOS tray) so users can see exactly what an "empty"
// override field will resolve to before deciding whether to override it.
type ResolvedIsolationDefaults struct {
	// RuntimeType is the runtime detected from the server command (e.g.
	// "uvx", "npx", "python"). Useful for diagnostic display.
	RuntimeType string

	// Image is the fully-qualified Docker image that would be used,
	// already including registry prefixes via buildFullImageName.
	Image string

	// NetworkMode is the network mode that would be used (typically
	// inherited from the global DockerIsolationConfig).
	NetworkMode string

	// ExtraArgs is the global extra args list that the server would
	// inherit. Per-server extra_args are appended on top, so this
	// communicates the baseline.
	ExtraArgs []string

	// ContainerWorkingDir is the working directory that would be used
	// inside the container. Empty when the global config does not
	// specify one (Docker default applies).
	ContainerWorkingDir string
}

// ResolveDefaults returns the resolved default isolation values for the
// given server, computed from the detected runtime type and global
// DockerIsolationConfig — without applying any per-server overrides.
//
// This intentionally does NOT short-circuit when isolation is disabled
// for the server: the result describes what would be used if isolation
// were active, which is what UI placeholders need to surface.
//
// Returns nil if the global config is missing (degenerate state).
func (im *IsolationManager) ResolveDefaults(serverConfig *config.ServerConfig) *ResolvedIsolationDefaults {
	if im == nil || im.globalConfig == nil || serverConfig == nil {
		return nil
	}

	runtimeType := im.DetectRuntimeType(serverConfig.Command)

	// Compute the default image without consulting per-server overrides.
	// We deliberately avoid calling GetDockerImage(serverConfig, ...)
	// because that prefers the override; here we want the *baseline*.
	var image string
	if img, exists := im.globalConfig.DefaultImages[runtimeType]; exists {
		image = im.buildFullImageName(img)
	} else {
		image = im.buildFullImageName("alpine:3.18")
	}

	defaults := &ResolvedIsolationDefaults{
		RuntimeType:         runtimeType,
		Image:               image,
		NetworkMode:         im.globalConfig.NetworkMode,
		ContainerWorkingDir: "", // No global default for working dir
	}

	if len(im.globalConfig.ExtraArgs) > 0 {
		defaults.ExtraArgs = append([]string(nil), im.globalConfig.ExtraArgs...)
	}

	return defaults
}

// buildFullImageName constructs the full image name with registry if needed
func (im *IsolationManager) buildFullImageName(image string) string {
	// If image already contains a registry (has a slash before the first colon), use as-is
	if strings.Contains(image, "/") && strings.Index(image, "/") < strings.Index(image, ":") {
		return image
	}

	// If no registry specified in config or image, use docker.io
	registry := im.globalConfig.Registry
	if registry == "" {
		registry = "docker.io"
	}

	// Don't prepend registry if image already contains one
	if strings.Contains(image, "/") {
		return image
	}

	// For official images (no slash), prepend library/
	if !strings.Contains(image, "/") {
		return fmt.Sprintf("%s/library/%s", registry, image)
	}

	return fmt.Sprintf("%s/%s", registry, image)
}

// BuildDockerArgs constructs Docker run arguments for isolation
func (im *IsolationManager) BuildDockerArgs(serverConfig *config.ServerConfig, runtimeType string) ([]string, error) {
	image, err := im.GetDockerImage(serverConfig, runtimeType)
	if err != nil {
		return nil, err
	}

	args := []string{"run", "--rm", "-i"}

	// Add container name for easier identification
	containerName := generateContainerName(serverConfig.Name)
	args = append(args, "--name", containerName)

	// Add labels for ownership tracking and cleanup
	labels := formatContainerLabels(serverConfig.Name)
	args = append(args, labels...)

	// Add log driver only if explicitly configured
	logDriver := ""
	if serverConfig.Isolation != nil && serverConfig.Isolation.LogDriver != "" {
		logDriver = serverConfig.Isolation.LogDriver
	} else if im.globalConfig.LogDriver != "" {
		logDriver = im.globalConfig.LogDriver
	}

	if logDriver != "" {
		args = append(args, "--log-driver", logDriver)
	}

	// Always add log size and file limits to prevent disk space issues
	// These options work with Docker's default json-file driver and most other drivers
	logMaxSize := im.globalConfig.LogMaxSize
	if serverConfig.Isolation != nil && serverConfig.Isolation.LogMaxSize != "" {
		logMaxSize = serverConfig.Isolation.LogMaxSize
	}
	if logMaxSize != "" {
		args = append(args, "--log-opt", fmt.Sprintf("max-size=%s", logMaxSize))
	}

	logMaxFiles := im.globalConfig.LogMaxFiles
	if serverConfig.Isolation != nil && serverConfig.Isolation.LogMaxFiles != "" {
		logMaxFiles = serverConfig.Isolation.LogMaxFiles
	}
	if logMaxFiles != "" {
		args = append(args, "--log-opt", fmt.Sprintf("max-file=%s", logMaxFiles))
	}

	// Add network mode
	networkMode := im.globalConfig.NetworkMode
	if serverConfig.Isolation != nil && serverConfig.Isolation.NetworkMode != "" {
		networkMode = serverConfig.Isolation.NetworkMode
	}
	if networkMode != "" {
		args = append(args, "--network", networkMode)
	}

	// Add resource limits
	if im.globalConfig.MemoryLimit != "" {
		args = append(args, "--memory", im.globalConfig.MemoryLimit)
	}
	if im.globalConfig.CPULimit != "" {
		args = append(args, "--cpus", im.globalConfig.CPULimit)
	}

	// Add package cache volume to speed up container restarts
	// This persists downloaded packages so containers don't re-download on every start
	if im.globalConfig.EnableCacheVolume {
		cacheVolume, cachePath := getCacheVolumeForRuntime(runtimeType)
		if cacheVolume != "" {
			args = append(args, "-v", fmt.Sprintf("%s:%s", cacheVolume, cachePath))
		}
	}

	// Add working directory if specified
	if serverConfig.Isolation != nil && serverConfig.Isolation.WorkingDir != "" {
		args = append(args, "--workdir", serverConfig.Isolation.WorkingDir)
	}

	// Add environment variables from server config
	for key, value := range serverConfig.Env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", key, value))
	}

	// Add global extra args
	args = append(args, im.globalConfig.ExtraArgs...)

	// Add server-specific extra args
	if serverConfig.Isolation != nil {
		args = append(args, serverConfig.Isolation.ExtraArgs...)
	}

	// Add the image
	args = append(args, image)

	return args, nil
}

// TransformCommandForContainer transforms the original command to run inside the container
func (im *IsolationManager) TransformCommandForContainer(command string, args []string, runtimeType string) (containerCommand string, containerArgs []string) {
	switch runtimeType {
	case cmdPython, cmdPython3:
		// For Python commands, use python directly in container
		return cmdPython, args
	case cmdUvx:
		// With ghcr.io/astral-sh/uv image, uvx is pre-installed — run directly
		return cmdUvx, args
	case cmdPip, cmdPipx:
		// Use pip directly
		return cmdPip, args
	case cmdNode:
		return "node", args
	case cmdNpm:
		return "npm", args
	case cmdNpx:
		return "npx", args
	case cmdYarn:
		return "yarn", args
	case cmdGo:
		return "go", args
	case cmdCargo:
		return "cargo", args
	case cmdRustc:
		return "rustc", args
	case cmdRuby:
		return "ruby", args
	case cmdGem:
		return "gem", args
	case cmdPhp:
		return "php", args
	case cmdComposer:
		return "composer", args
	case "sh", "bash":
		// For shell commands, use the shell directly
		return command, args
	default:
		// For binary/unknown, try to run the original command
		// This assumes the binary is available in the container
		return command, args
	}
}

// generateContainerName creates a Docker container name from server name with random suffix
func generateContainerName(serverName string) string {
	// Sanitize server name for Docker container naming
	sanitized := sanitizeServerNameForContainer(serverName)

	// Generate 4-character random suffix
	suffix := generateRandomSuffix()

	return fmt.Sprintf("mcpproxy-%s-%s", sanitized, suffix)
}

// sanitizeServerNameForContainer converts server name to valid Docker container name
func sanitizeServerNameForContainer(name string) string {
	// Replace invalid characters with hyphens
	// Docker container names can contain: [a-zA-Z0-9][a-zA-Z0-9_.-]*
	reg := regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)
	sanitized := reg.ReplaceAllString(name, "-")

	// Remove multiple consecutive hyphens
	for strings.Contains(sanitized, "--") {
		sanitized = strings.ReplaceAll(sanitized, "--", "-")
	}

	// Ensure it starts with alphanumeric character
	if sanitized != "" && !regexp.MustCompile(`^[a-zA-Z0-9]`).MatchString(sanitized) {
		sanitized = "server-" + sanitized
		// Remove consecutive hyphens that might have been created by the prefix addition
		for strings.Contains(sanitized, "--") {
			sanitized = strings.ReplaceAll(sanitized, "--", "-")
		}
	}

	// Remove trailing hyphens/dots
	sanitized = strings.TrimRight(sanitized, "-.")

	// Ensure minimum length
	if sanitized == "" {
		sanitized = "server"
	}

	// Truncate if too long (Docker limit is 253 chars, leave room for prefix and suffix)
	maxLen := 200 // mcpproxy- (9) + sanitized (200) + - (1) + suffix (4) = 214 chars
	if len(sanitized) > maxLen {
		sanitized = sanitized[:maxLen]
		sanitized = strings.TrimRight(sanitized, "-.")
	}

	return sanitized
}

// generateRandomSuffix generates a 4-character random alphanumeric suffix
func generateRandomSuffix() string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	const suffixLength = 4

	result := make([]byte, suffixLength)
	charsetLen := big.NewInt(int64(len(charset)))

	for i := range result {
		randomIndex, err := rand.Int(rand.Reader, charsetLen)
		if err != nil {
			// Fallback to a simple method if crypto/rand fails
			result[i] = charset[i%len(charset)]
		} else {
			result[i] = charset[randomIndex.Int64()]
		}
	}

	return string(result)
}

// getCacheVolumeForRuntime returns a (volume_name, container_path) pair for package caching.
// Returns empty strings for runtimes that don't benefit from caching.
func getCacheVolumeForRuntime(runtimeType string) (string, string) {
	switch runtimeType {
	case cmdUvx, cmdPip, cmdPipx, cmdPython, cmdPython3:
		return "mcpproxy-uv-cache", "/root/.cache/uv"
	case cmdNpm, cmdNpx, cmdYarn, cmdNode:
		return "mcpproxy-npm-cache", "/root/.npm"
	case cmdGo:
		return "mcpproxy-go-cache", "/go/pkg/mod"
	default:
		return "", ""
	}
}
