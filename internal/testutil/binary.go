package testutil

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// BinaryTestEnv manages a test environment with the actual mcpproxy binary
type BinaryTestEnv struct {
	t          *testing.T
	binaryPath string
	configPath string
	dataDir    string
	port       int
	baseURL    string
	apiURL     string
	cmd        *exec.Cmd
	cleanup    func()
}

// NewBinaryTestEnv creates a new binary test environment
func NewBinaryTestEnv(t *testing.T) *BinaryTestEnv {
	// Find available port
	port := findAvailablePort(t)

	// Create temp directory for test data
	tempDir, err := os.MkdirTemp("", "mcpproxy-binary-test-*")
	require.NoError(t, err)

	dataDir := filepath.Join(tempDir, "data")
	err = os.MkdirAll(dataDir, 0755)
	require.NoError(t, err)

	// Create test config
	configPath := filepath.Join(tempDir, "config.json")
	createTestConfig(t, configPath, port, dataDir)

	env := &BinaryTestEnv{
		t:          t,
		binaryPath: "./mcpproxy", // Relative to project root
		configPath: configPath,
		dataDir:    dataDir,
		port:       port,
		baseURL:    fmt.Sprintf("http://localhost:%d", port),
		apiURL:     fmt.Sprintf("http://localhost:%d/api/v1", port),
	}

	env.cleanup = func() {
		if env.cmd != nil && env.cmd.Process != nil {
			// Try graceful shutdown first
			_ = env.cmd.Process.Signal(syscall.SIGTERM)

			// Wait for graceful shutdown
			done := make(chan error, 1)
			go func() {
				done <- env.cmd.Wait()
			}()

			select {
			case <-done:
				// Process exited gracefully
			case <-time.After(5 * time.Second):
				// Force kill if it doesn't shut down
				_ = env.cmd.Process.Kill()
				<-done
			}
		}

		// Clean up temp directory
		os.RemoveAll(filepath.Dir(env.configPath))
	}

	return env
}

// Start starts the mcpproxy binary
func (env *BinaryTestEnv) Start() {
	// Check if binary exists
	if _, err := os.Stat(env.binaryPath); os.IsNotExist(err) {
		env.t.Fatalf("mcpproxy binary not found at %s. Please run: go build -o mcpproxy ./cmd/mcpproxy", env.binaryPath)
	}

	// Start the binary
	env.cmd = exec.Command(env.binaryPath, "serve", "--config="+env.configPath, "--log-level=debug")
	env.cmd.Env = append(os.Environ(),
		"MCPPROXY_DISABLE_OAUTH=true", // Disable OAuth for testing
	)

	err := env.cmd.Start()
	require.NoError(env.t, err, "Failed to start mcpproxy binary")

	env.t.Logf("Started mcpproxy binary with PID %d on port %d", env.cmd.Process.Pid, env.port)

	// Wait for server to be ready
	env.WaitForReady()
}

// WaitForReady waits for the server to be ready to accept requests
func (env *BinaryTestEnv) WaitForReady() {
	timeout := time.After(30 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			env.t.Fatal("Timeout waiting for mcpproxy binary to be ready")
		case <-ticker.C:
			if env.isServerReady() {
				env.t.Log("mcpproxy binary is ready")
				return
			}
		}
	}
}

// WaitForEverythingServer waits for the everything server to connect and be ready
func (env *BinaryTestEnv) WaitForEverythingServer() {
	timeout := time.After(60 * time.Second) // Longer timeout for everything server
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	env.t.Log("Waiting for everything server to connect...")

	for {
		select {
		case <-timeout:
			env.t.Fatal("Timeout waiting for everything server to connect")
		case <-ticker.C:
			if env.isEverythingServerReady() {
				env.t.Log("Everything server is ready")
				// Wait a bit more for indexing to complete
				time.Sleep(2 * time.Second)
				return
			}
		}
	}
}

// isServerReady checks if the server is accepting HTTP requests
func (env *BinaryTestEnv) isServerReady() bool {
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get(env.apiURL + "/servers")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// isEverythingServerReady checks if the everything server is connected and ready
func (env *BinaryTestEnv) isEverythingServerReady() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(env.apiURL + "/servers")
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	// Parse response to check server status
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Servers []struct {
				Name             string `json:"name"`
				ConnectionStatus string `json:"connection_status"`
			} `json:"servers"`
		} `json:"data"`
	}

	if err := ParseJSONResponse(resp, &response); err != nil {
		return false
	}

	// Look for everything server
	for _, server := range response.Data.Servers {
		if server.Name == "everything" && server.ConnectionStatus == "Ready" {
			return true
		}
	}

	return false
}

// Cleanup cleans up the test environment
func (env *BinaryTestEnv) Cleanup() {
	if env.cleanup != nil {
		env.cleanup()
	}
}

// GetBaseURL returns the base URL of the test server
func (env *BinaryTestEnv) GetBaseURL() string {
	return env.baseURL
}

// GetAPIURL returns the API base URL of the test server
func (env *BinaryTestEnv) GetAPIURL() string {
	return env.apiURL
}

// GetPort returns the port the server is running on
func (env *BinaryTestEnv) GetPort() int {
	return env.port
}

// findAvailablePort finds an available port for testing
func findAvailablePort(t *testing.T) int {
	listener, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	return port
}

// createTestConfig creates a test configuration file
func createTestConfig(t *testing.T, configPath string, port int, dataDir string) {
	config := fmt.Sprintf(`{
  "listen": ":%d",
  "data_dir": "%s",
  "enable_tray": false,
  "debug_search": true,
  "top_k": 10,
  "tools_limit": 50,
  "tool_response_limit": 20000,
  "call_tool_timeout": "30s",
  "mcpServers": [
    {
      "name": "everything",
      "protocol": "stdio",
      "command": "npx",
      "args": [
        "-y",
        "@modelcontextprotocol/server-everything"
      ],
      "enabled": true,
      "quarantined": false,
      "created": "2025-01-01T00:00:00Z"
    }
  ],
  "environment": {
    "inherit_system_safe": true,
    "allowed_system_vars": [
      "PATH",
      "HOME",
      "TMPDIR",
      "TEMP",
      "TMP",
      "NODE_PATH",
      "NPM_CONFIG_PREFIX"
    ]
  },
  "docker_isolation": {
    "enabled": false
  }
}`, port, dataDir)

	err := os.WriteFile(configPath, []byte(config), 0600)
	require.NoError(t, err)
}

// MCPCallRequest represents an MCP call_tool request
type MCPCallRequest struct {
	ToolName string                 `json:"name"`
	Args     map[string]interface{} `json:"args"`
}

// CallMCPTool calls an MCP tool through the proxy using the mcpproxy binary
func (env *BinaryTestEnv) CallMCPTool(toolName string, args map[string]interface{}) ([]byte, error) {
	// Use the mcpproxy binary to call the tool
	cmdArgs := []string{"call", "tool", "--tool-name=" + toolName}

	if len(args) > 0 {
		argsJSON, err := ParseJSONToString(args)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal args: %w", err)
		}
		cmdArgs = append(cmdArgs, "--json_args="+argsJSON)
	}

	cmd := exec.Command(env.binaryPath, cmdArgs...)
	cmd.Env = append(os.Environ(),
		"MCPPROXY_DISABLE_OAUTH=true",
	)

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("tool call failed: %s", string(exitErr.Stderr))
		}
		return nil, err
	}

	return output, nil
}

// TestServerList represents a simplified server list response
type TestServerList struct {
	Success bool `json:"success"`
	Data    struct {
		Servers []TestServer `json:"servers"`
	} `json:"data"`
}

// TestServer represents a server in the test environment
type TestServer struct {
	Name             string `json:"name"`
	Protocol         string `json:"protocol"`
	Enabled          bool   `json:"enabled"`
	Quarantined      bool   `json:"quarantined"`
	ConnectionStatus string `json:"connection_status"`
}

// TestToolList represents a tool list response
type TestToolList struct {
	Success bool `json:"success"`
	Data    struct {
		Server string     `json:"server"`
		Tools  []TestTool `json:"tools"`
	} `json:"data"`
}

// TestTool represents a tool in the test environment
type TestTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// TestSearchResults represents search results
type TestSearchResults struct {
	Success bool `json:"success"`
	Data    struct {
		Query   string           `json:"query"`
		Results []TestSearchTool `json:"results"`
	} `json:"data"`
}

// TestSearchTool represents a search result tool
type TestSearchTool struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Server      string  `json:"server"`
	Score       float64 `json:"score"`
}
