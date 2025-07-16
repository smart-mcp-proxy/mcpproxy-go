package transport

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/secureenv"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
)

const (
	osWindows = "windows"
)

// StdioTransportConfig holds configuration for stdio transport
type StdioTransportConfig struct {
	Command    string
	Args       []string
	Env        map[string]string
	EnvManager *secureenv.Manager
}

// StdioClientResult holds the result of creating a stdio client with stderr access
type StdioClientResult struct {
	Client *client.Client
	Stderr io.Reader
}

// CreateStdioClient creates a new MCP client using stdio transport and returns stderr access
func CreateStdioClient(cfg *StdioTransportConfig) (*StdioClientResult, error) {
	if cfg.Command == "" {
		return nil, fmt.Errorf("no command specified for stdio transport")
	}

	// Use secure environment manager to build filtered environment variables
	envVars := cfg.EnvManager.BuildSecureEnvironment()

	// Wrap command in a shell to ensure user's PATH is respected, especially in GUI apps
	command, cmdArgs := wrapCommandInShell(cfg.Command, cfg.Args)

	stdioTransport := transport.NewStdio(command, envVars, cmdArgs...)
	mcpClient := client.NewClient(stdioTransport)

	// Note: stderr will be available after the client is started
	return &StdioClientResult{
		Client: mcpClient,
		Stderr: nil, // Will be set later via GetStderr after Start()
	}, nil
}

// wrapCommandInShell wraps the original command in a shell to ensure PATH is loaded
func wrapCommandInShell(command string, args []string) (shellCmd string, shellArgs []string) {
	fullCmd := command
	if len(args) > 0 {
		quotedArgs := make([]string, len(args))
		for i, arg := range args {
			// Basic quoting for arguments with spaces
			if strings.Contains(arg, " ") {
				quotedArgs[i] = fmt.Sprintf("%q", arg)
			} else {
				quotedArgs[i] = arg
			}
		}
		fullCmd = fmt.Sprintf("%s %s", command, strings.Join(quotedArgs, " "))
	}

	if runtime.GOOS == osWindows {
		return "cmd.exe", []string{"/c", fullCmd}
	}

	// For Unix-like systems, use a login shell to load profile scripts
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	return shell, []string{"-l", "-c", fullCmd}
}

// ParseCommand parses a command string into command and arguments
func ParseCommand(cmd string) []string {
	var result []string
	var current string
	var inQuote bool
	var quoteChar rune

	for _, r := range cmd {
		switch {
		case r == ' ' && !inQuote:
			if current != "" {
				result = append(result, current)
				current = ""
			}
		case (r == '"' || r == '\''):
			if inQuote && r == quoteChar {
				inQuote = false
				quoteChar = 0
			} else if !inQuote {
				inQuote = true
				quoteChar = r
			} else {
				current += string(r)
			}
		default:
			current += string(r)
		}
	}

	if current != "" {
		result = append(result, current)
	}

	return result
}

// CreateStdioTransportConfig creates a stdio transport config from server config
func CreateStdioTransportConfig(serverConfig *config.ServerConfig, envManager *secureenv.Manager) *StdioTransportConfig {
	var command string
	var args []string

	// Check if command is specified separately (preferred)
	if serverConfig.Command != "" {
		command = serverConfig.Command
		args = serverConfig.Args
	} else {
		// Fallback to parsing from URL
		parsedArgs := ParseCommand(serverConfig.URL)
		if len(parsedArgs) > 0 {
			command = parsedArgs[0]
			args = parsedArgs[1:]
		}
	}

	return &StdioTransportConfig{
		Command:    command,
		Args:       args,
		Env:        serverConfig.Env,
		EnvManager: envManager,
	}
}
