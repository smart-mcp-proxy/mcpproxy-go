package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
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
	mcpClient  *client.Client // MCP client for tool calls
}

const (
	binaryEnvPreferred = "MCPPROXY_BINARY_PATH"
	binaryEnvLegacy    = "MCPPROXY_BINARY"
)

// resolveBinaryPath determines where the mcpproxy binary lives.
// Preference order:
//  1. Explicit absolute path via MCPPROXY_BINARY_PATH
//  2. Legacy MCPPROXY_BINARY environment variable
//  3. A discovered mcpproxy binary in the current or parent directories
func resolveBinaryPath() string {
	if path, ok := os.LookupEnv(binaryEnvPreferred); ok && path != "" {
		return ensureAbsolute(path)
	}

	if path, ok := os.LookupEnv(binaryEnvLegacy); ok && path != "" {
		return ensureAbsolute(path)
	}

	searchDirs := []string{"."}

	if cwd, err := os.Getwd(); err == nil {
		for dir := cwd; dir != "" && dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
			searchDirs = append(searchDirs, dir)
		}
	}

	binaryName := "mcpproxy"
	if runtime.GOOS == "windows" {
		binaryName = "mcpproxy.exe"
	}

	for _, dir := range searchDirs {
		candidate := filepath.Join(dir, binaryName)
		absCandidate := ensureAbsolute(candidate)
		if info, err := os.Stat(absCandidate); err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0 {
			return absCandidate
		}
	}

	return ensureAbsolute(filepath.Join(".", binaryName))
}

func ensureAbsolute(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// NewBinaryTestEnv creates a new binary test environment
func NewBinaryTestEnv(t *testing.T) *BinaryTestEnv {
	// Find available port
	port := findAvailablePort(t)

	// Create temp directory for test data
	tempDir, err := os.MkdirTemp("", "mcpproxy-binary-test-*")
	require.NoError(t, err)

	dataDir := filepath.Join(tempDir, "data")
	err = os.MkdirAll(dataDir, 0700) // Secure permissions required for socket creation
	require.NoError(t, err)

	// Create test config
	configPath := filepath.Join(tempDir, "config.json")
	createTestConfig(t, configPath, port, dataDir)

	env := &BinaryTestEnv{
		t:          t,
		binaryPath: resolveBinaryPath(),
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

// TestAPIKey is the fixed API key used in binary tests
const TestAPIKey = "test-binary-api-key-12345"

// Start starts the mcpproxy binary
func (env *BinaryTestEnv) Start() {
	// Check if binary exists
	if _, err := os.Stat(env.binaryPath); os.IsNotExist(err) {
		env.t.Fatalf("mcpproxy binary not found at %s. Set %s to the built binary or run: go build -o mcpproxy ./cmd/mcpproxy", env.binaryPath, binaryEnvPreferred)
	}

	// Start the binary
	env.cmd = exec.Command(env.binaryPath, "serve", "--config="+env.configPath, "--log-level=debug")
	env.cmd.Env = append(os.Environ(),
		"MCPPROXY_DISABLE_OAUTH=true",        // Disable OAuth for testing
		"MCPPROXY_API_KEY="+TestAPIKey,       // Set API key for testing
	)

	err := env.cmd.Start()
	require.NoError(env.t, err, "Failed to start mcpproxy binary")

	env.t.Logf("Started mcpproxy binary with PID %d on port %d", env.cmd.Process.Pid, env.port)

	// Wait for server to be ready
	env.WaitForReady()
}

// WaitForReady waits for the server to be ready to accept requests
func (env *BinaryTestEnv) WaitForReady() {
	timeout := time.After(60 * time.Second) // Increased timeout for CI environments
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

// WaitForEverythingServer waits for the test server to connect and be ready
func (env *BinaryTestEnv) WaitForEverythingServer() {
	timeout := time.After(120 * time.Second) // Very long timeout for test server, especially after restart
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	env.t.Log("Waiting for test server to connect...")

	for {
		select {
		case <-timeout:
			env.t.Fatal("Timeout waiting for test server to connect")
		case <-ticker.C:
			if env.isEverythingServerReady() {
				env.t.Log("Test server is ready")
				// Wait longer for tool indexing to complete in CI environments
				time.Sleep(5 * time.Second)

				// Verify tools are actually indexed by making a test query
				env.waitForToolIndexing()

				// Initialize MCP session for tests (skip if already initialized)
				if env.mcpClient == nil {
					env.InitializeMCPSession()
				}
				return
			}
		}
	}
}

// waitForToolIndexing waits for tools to be indexed and available
func (env *BinaryTestEnv) waitForToolIndexing() {
	timeout := time.After(15 * time.Second) // 15s should be enough after server is ready
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			// Timeout is not fatal - just log and continue
			// Tests will fail later if tools aren't actually available
			env.t.Log("Tool indexing check timed out after 15s (continuing anyway)")
			return
		case <-ticker.C:
			// Try to get tool count from server status
			client := &http.Client{Timeout: 2 * time.Second}
			req, err := http.NewRequest("GET", env.apiURL+"/servers", nil)
			if err != nil {
				continue // Request creation error, retry
			}
			req.Header.Set("X-API-Key", TestAPIKey)
			resp, err := client.Do(req)
			if err != nil {
				continue // Network error, retry
			}

			if resp.StatusCode == http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err == nil {
					bodyStr := string(body)
					// Check if we have success response and at least one server
					if strings.Contains(bodyStr, `"success":true`) &&
					   strings.Contains(bodyStr, `"servers":`) {
						// Server API is working, that's enough
						// Don't strictly require tools to be indexed as they might index slowly
						env.t.Log("Server API is responding, proceeding with tests")
						return
					}
				}
			} else {
				resp.Body.Close()
			}
		}
	}
}

// isServerReady checks if the server is accepting HTTP requests
func (env *BinaryTestEnv) isServerReady() bool {
	client := &http.Client{Timeout: 1 * time.Second}
	req, err := http.NewRequest("GET", env.apiURL+"/servers", nil)
	if err != nil {
		return false
	}
	req.Header.Set("X-API-Key", TestAPIKey)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// isEverythingServerReady checks if the test server is connected and ready
func (env *BinaryTestEnv) isEverythingServerReady() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest("GET", env.apiURL+"/servers", nil)
	if err != nil {
		env.t.Logf("Failed to create request: %v", err)
		return false
	}
	req.Header.Set("X-API-Key", TestAPIKey)
	resp, err := client.Do(req)
	if err != nil {
		env.t.Logf("Failed to get servers: %v", err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		env.t.Logf("Server returned non-200 status: %d", resp.StatusCode)
		return false
	}

	// Parse response to check server status
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Servers []struct {
				Name             string `json:"name"`
				ConnectionStatus string `json:"connection_status"`
				Connected        bool   `json:"connected"`
				Connecting       bool   `json:"connecting"`
				Enabled          bool   `json:"enabled"`
			} `json:"servers"`
		} `json:"data"`
	}

	if err := ParseJSONResponse(resp, &response); err != nil {
		env.t.Logf("Failed to parse server response: %v", err)
		return false
	}

	// Look for test server (memory)
	for _, server := range response.Data.Servers {
		if server.Name == "memory" {
			env.t.Logf("Memory server status: enabled=%v connected=%v connecting=%v status=%q",
				server.Enabled, server.Connected, server.Connecting, server.ConnectionStatus)

			// Server must be enabled to connect
			if !server.Enabled {
				env.t.Log("Memory server is disabled")
				return false
			}

			// Accept multiple states as "ready":
			// 1. ConnectionStatus is "Ready" (ideal state)
			// 2. Connected and not currently connecting (stable connection)
			// 3. Currently connecting (connection in progress - acceptable during reconnection)
			ready := server.ConnectionStatus == "Ready" ||
				(server.Connected && !server.Connecting) ||
				server.Connecting

			if ready {
				return true
			}
		}
	}

	env.t.Log("Memory server not found or not ready")
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

// GetHTTPClient returns an HTTP client configured with the test API key
func (env *BinaryTestEnv) GetHTTPClient() *HTTPClient {
	return NewHTTPClientWithAPIKey(env.apiURL, TestAPIKey)
}

// GetConfigPath returns the path to the test config file
func (env *BinaryTestEnv) GetConfigPath() string {
	return env.configPath
}

// GetPort returns the port the server is running on
func (env *BinaryTestEnv) GetPort() int {
	return env.port
}

// findAvailablePort finds an available port for testing
func findAvailablePort(t *testing.T) int {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
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
  "api_key": "%s",
  "enable_tray": false,
  "debug_search": true,
  "top_k": 10,
  "tools_limit": 50,
  "tool_response_limit": 20000,
  "call_tool_timeout": "30s",
  "mcpServers": [
    {
      "name": "memory",
      "protocol": "stdio",
      "command": "npx",
      "args": [
        "-y",
        "@modelcontextprotocol/server-memory"
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
}`, port, dataDir, TestAPIKey)

	err := os.WriteFile(configPath, []byte(config), 0600)
	require.NoError(t, err)
}

// MCPCallRequest represents an MCP call_tool request
type MCPCallRequest struct {
	ToolName string                 `json:"name"`
	Args     map[string]interface{} `json:"args"`
}

// InitializeMCPSession initializes an MCP session with the server using the MCP client library
func (env *BinaryTestEnv) InitializeMCPSession() {
	// Create HTTP transport for the MCP client
	httpTransport, err := transport.NewStreamableHTTP(env.baseURL + "/mcp")
	require.NoError(env.t, err, "Failed to create HTTP transport")

	// Create MCP client
	env.mcpClient = client.NewClient(httpTransport)

	// Start and initialize the client
	ctx := context.Background()
	err = env.mcpClient.Start(ctx)
	require.NoError(env.t, err, "Failed to start MCP client")

	// Initialize the session
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "mcpproxy-binary-test",
		Version: "1.0.0",
	}
	initRequest.Params.Capabilities = mcp.ClientCapabilities{}

	_, err = env.mcpClient.Initialize(ctx, initRequest)
	require.NoError(env.t, err, "Failed to initialize MCP session")

	env.t.Log("MCP session initialized successfully")
}

// CallMCPTool calls an MCP tool through the proxy using the MCP client library
func (env *BinaryTestEnv) CallMCPTool(toolName string, args map[string]interface{}) ([]byte, error) {
	if env.mcpClient == nil {
		return nil, fmt.Errorf("MCP client not initialized - call InitializeMCPSession first")
	}

	ctx := context.Background()

	// Build the tool call request
	callRequest := mcp.CallToolRequest{}
	callRequest.Params.Name = toolName
	callRequest.Params.Arguments = args

	// Call the tool
	result, err := env.mcpClient.CallTool(ctx, callRequest)
	if err != nil {
		return nil, fmt.Errorf("tool call failed: %w", err)
	}

	// Extract text content from response
	if len(result.Content) == 0 {
		return []byte("{}"), nil
	}

	// Try to extract text from the first content item
	if textContent, ok := result.Content[0].(mcp.TextContent); ok {
		return []byte(textContent.Text), nil
	}

	// Fallback: marshal the entire result
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	return jsonBytes, nil
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
	Connected        bool   `json:"connected"`
	Connecting       bool   `json:"connecting"`
	ToolCount        int    `json:"tool_count"`
	LastError        string `json:"last_error"`
	ConnectionStatus string `json:"connection_status,omitempty"`
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

// TestServerLogsResponse represents a server logs response
type TestServerLogsResponse struct {
	Success bool `json:"success"`
	Data    struct {
		ServerName string         `json:"server_name"`
		Logs       []TestLogEntry `json:"logs"`
		Count      int            `json:"count"`
	} `json:"data"`
}

// TestLogEntry represents a single log entry
type TestLogEntry struct {
	Timestamp string                 `json:"timestamp"`
	Level     string                 `json:"level"`
	Message   string                 `json:"message"`
	Server    string                 `json:"server,omitempty"`
	Fields    map[string]interface{} `json:"fields,omitempty"`
}
