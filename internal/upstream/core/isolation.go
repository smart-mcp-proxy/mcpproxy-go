package core

import (
	"fmt"
	"path/filepath"
	"strings"

	"mcpproxy-go/internal/config"
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
)

// IsolationManager handles Docker isolation logic for MCP servers
type IsolationManager struct {
	globalConfig *config.DockerIsolationConfig
}

// NewIsolationManager creates a new isolation manager
func NewIsolationManager(globalConfig *config.DockerIsolationConfig) *IsolationManager {
	return &IsolationManager{
		globalConfig: globalConfig,
	}
}

// ShouldIsolate determines if a server should be isolated based on global and server config
func (im *IsolationManager) ShouldIsolate(serverConfig *config.ServerConfig) bool {
	// Check if global isolation is disabled
	if im.globalConfig == nil || !im.globalConfig.Enabled {
		return false
	}

	// Check if server has isolation config and it's explicitly disabled
	if serverConfig.Isolation != nil && !serverConfig.Isolation.Enabled {
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

// DetectRuntimeType detects the runtime type based on the command
func (im *IsolationManager) DetectRuntimeType(command string) string {
	// Extract just the command name without path
	cmdName := filepath.Base(command)

	// Handle common runtime commands
	switch cmdName {
	case cmdPython, cmdPython3, "python3.11", "python3.12":
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
		// For uvx, we need to install it first, then run it
		installCmd := fmt.Sprintf("pip install uv && uvx %s", strings.Join(shellescapeArgs(args), " "))
		return cmdSh, []string{"-c", installCmd}
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

// shellescapeArgs escapes arguments for shell execution
func shellescapeArgs(args []string) []string {
	var escaped []string
	for _, arg := range args {
		if arg == "" {
			escaped = append(escaped, "''")
			continue
		}

		// If string contains no special characters, return as-is
		if !strings.ContainsAny(arg, " \t\n\r\"'\\$`;&|<>(){}[]?*~") {
			escaped = append(escaped, arg)
			continue
		}

		// Use single quotes and escape any single quotes in the string
		escaped = append(escaped, "'"+strings.ReplaceAll(arg, "'", "'\"'\"'")+"'")
	}
	return escaped
}
