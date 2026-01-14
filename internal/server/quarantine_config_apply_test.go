package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// TestE2E_QuarantineConfigApply tests that changing quarantine state via config apply
// properly updates server state and tool discoverability
func TestE2E_QuarantineConfigApply(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Skip when running with race detector due to known race during async shutdown
	// The race is in cleanup/shutdown code paths, not in the actual functionality being tested
	if raceEnabled {
		t.Skip("Skipping test with race detector enabled - known race in shutdown path")
	}

	env := NewTestEnvironment(t)
	defer env.Cleanup()

	// Step 1: Create a mock server with tools
	mockTools := []mcp.Tool{
		{Name: "tool1", Description: "First test tool"},
		{Name: "tool2", Description: "Second test tool"},
		{Name: "tool3", Description: "Third test tool"},
	}
	mockServer := env.CreateMockUpstreamServer("test-server", mockTools)
	require.NotNil(t, mockServer)

	serverConfig := &config.ServerConfig{
		Name:        "test-server",
		URL:         mockServer.addr,
		Protocol:    "http",
		Enabled:     true,
		Quarantined: true, // Start quarantined
	}

	// Step 2: Add server to config and runtime (simulating Web UI add + save)
	currentConfig, err := env.GetConfig()
	require.NoError(t, err)

	// Set default values if not present (config from API may be incomplete)
	if currentConfig.TopK == 0 {
		currentConfig.TopK = 5
	}
	if currentConfig.ToolsLimit == 0 {
		currentConfig.ToolsLimit = 15
	}
	if currentConfig.ToolResponseLimit == 0 {
		currentConfig.ToolResponseLimit = 10000
	}
	if currentConfig.CallToolTimeout == 0 {
		currentConfig.CallToolTimeout = config.Duration(60 * time.Second)
	}
	if currentConfig.DataDir == "" {
		currentConfig.DataDir = env.tempDir
	}
	if currentConfig.Listen == "" {
		currentConfig.Listen = env.proxyServer.GetListenAddress()
	}

	// Add the test server to config
	currentConfig.Servers = append(currentConfig.Servers, serverConfig)

	// Apply config to persist the server
	applyResult, err := env.ApplyConfig(currentConfig)
	require.NoError(t, err)
	require.True(t, applyResult.Success, "Initial config apply should succeed")

	// Wait for server to connect and be indexed
	time.Sleep(2 * time.Second)

	// Step 3: Verify server is quarantined
	servers, err := env.GetServers()
	require.NoError(t, err)
	require.NotEmpty(t, servers)

	var testServer *contracts.Server
	for i := range servers {
		if servers[i].Name == "test-server" {
			testServer = &servers[i]
			break
		}
	}
	require.NotNil(t, testServer, "test-server not found in servers list")
	assert.True(t, testServer.Quarantined, "Server should be quarantined initially")

	// Step 4: Verify tools are NOT searchable when quarantined
	searchResults, err := env.SearchTools("tool1", 10)
	require.NoError(t, err)

	// Tools should not appear in search when server is quarantined
	for _, result := range searchResults {
		assert.NotContains(t, result.Tool.Name, "test-server:", "Quarantined server tools should not appear in search")
	}

	t.Logf("✓ Server correctly quarantined, tools not searchable")

	// Step 5: Get current config and modify quarantine state
	currentConfig, err = env.GetConfig()
	require.NoError(t, err)

	// Find and update the test-server to unquarantine it
	configModified := false
	for i := range currentConfig.Servers {
		if currentConfig.Servers[i].Name == "test-server" {
			currentConfig.Servers[i].Quarantined = false
			configModified = true
			t.Logf("Updated config to unquarantine test-server")
			break
		}
	}
	require.True(t, configModified, "Failed to find test-server in config")

	// Step 6: Apply the modified config
	applyResult, err = env.ApplyConfig(currentConfig)
	require.NoError(t, err)
	require.True(t, applyResult.Success, "Config apply should succeed")
	assert.Contains(t, applyResult.ChangedFields, "mcpServers", "mcpServers should be in changed fields")

	t.Logf("Config apply result: success=%v, applied_immediately=%v, changed_fields=%v",
		applyResult.Success, applyResult.AppliedImmediately, applyResult.ChangedFields)

	// Step 7: Wait for async reload to complete
	// The fix triggers LoadConfiguredServers() and DiscoverAndIndexTools() asynchronously
	// This includes a 500ms delay + connection time + indexing time
	time.Sleep(4 * time.Second)

	// Step 8: Verify server is now unquarantined
	servers, err = env.GetServers()
	require.NoError(t, err)

	testServer = nil
	for i := range servers {
		if servers[i].Name == "test-server" {
			testServer = &servers[i]
			break
		}
	}
	require.NotNil(t, testServer, "test-server not found after config apply")
	assert.False(t, testServer.Quarantined, "Server should be unquarantined after config apply")

	t.Logf("✓ Server successfully unquarantined via config apply")

	// Step 9: Verify tools are NOW searchable
	searchResults, err = env.SearchTools("tool1", 10)
	require.NoError(t, err)

	// Log all results for debugging
	t.Logf("Search results for 'tool1': %d results", len(searchResults))
	for i, result := range searchResults {
		t.Logf("  [%d] Server: %s, Tool: %s, Score: %.2f", i, result.Tool.ServerName, result.Tool.Name, result.Score)
	}

	foundTool := false
	for _, result := range searchResults {
		if result.Tool.ServerName == "test-server" && result.Tool.Name == "tool1" {
			foundTool = true
			t.Logf("✓ Found tool in search: %s", result.Tool.Name)
			break
		}
	}

	if !foundTool {
		t.Logf("WARNING: Tool not found in search. This may be a timing issue with async indexing.")
		t.Logf("Retrying search after additional delay...")
		time.Sleep(2 * time.Second)
		searchResults, err = env.SearchTools("tool1", 10)
		require.NoError(t, err)

		t.Logf("Retry search results: %d results", len(searchResults))
		for i, result := range searchResults {
			t.Logf("  [%d] Server: %s, Tool: %s, Score: %.2f", i, result.Tool.ServerName, result.Tool.Name, result.Score)
			if result.Tool.ServerName == "test-server" && result.Tool.Name == "tool1" {
				foundTool = true
				t.Logf("✓ Found tool in search after retry: %s", result.Tool.Name)
				break
			}
		}
	}

	assert.True(t, foundTool, "Tools from unquarantined server should be searchable")

	t.Logf("✓ All tests passed - quarantine state properly updates via config apply")
}

// Helper methods for TestEnvironment

// GetServers fetches the servers list via HTTP API
func (env *TestEnvironment) GetServers() ([]contracts.Server, error) {
	// Extract port from listen address (format: "[::]:port" or ":port")
	listenAddr := env.proxyServer.GetListenAddress()
	port := listenAddr
	if i := strings.LastIndex(listenAddr, ":"); i >= 0 {
		port = listenAddr[i+1:]
	}

	url := fmt.Sprintf("http://localhost:%s/api/v1/servers", port)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", "test-api-key-e2e")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var response contracts.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	data, ok := response.Data.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response data type")
	}

	serversData, ok := data["servers"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid servers data type")
	}

	// Convert to typed servers
	servers := make([]contracts.Server, 0, len(serversData))
	for _, serverData := range serversData {
		serverMap, ok := serverData.(map[string]interface{})
		if !ok {
			continue
		}

		server := contracts.Server{
			Name:        getString(serverMap, "name"),
			Protocol:    getString(serverMap, "protocol"),
			URL:         getString(serverMap, "url"),
			Enabled:     getBool(serverMap, "enabled"),
			Quarantined: getBool(serverMap, "quarantined"),
			Connected:   getBool(serverMap, "connected"),
			ToolCount:   getInt(serverMap, "tool_count"),
		}
		servers = append(servers, server)
	}

	return servers, nil
}

// SearchTools searches for tools via HTTP API
func (env *TestEnvironment) SearchTools(query string, limit int) ([]contracts.SearchResult, error) {
	// Extract port from listen address
	listenAddr := env.proxyServer.GetListenAddress()
	port := listenAddr
	if i := strings.LastIndex(listenAddr, ":"); i >= 0 {
		port = listenAddr[i+1:]
	}

	url := fmt.Sprintf("http://localhost:%s/api/v1/index/search?q=%s&limit=%d", port, query, limit)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", "test-api-key-e2e")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	// Read and log response for debugging
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var response contracts.APIResponse
	if err := json.Unmarshal(bodyBytes, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w\nBody: %s", err, string(bodyBytes))
	}

	data, ok := response.Data.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response data type, got: %T\nBody: %s", response.Data, string(bodyBytes))
	}

	resultsData, ok := data["results"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid results data type, got: %T\nData: %+v", data["results"], data)
	}

	// Convert to typed results
	results := make([]contracts.SearchResult, 0, len(resultsData))
	for _, resultData := range resultsData {
		resultMap, ok := resultData.(map[string]interface{})
		if !ok {
			continue
		}

		// Parse nested tool object
		toolData, ok := resultMap["tool"].(map[string]interface{})
		if !ok {
			// Debug: log what we got instead
			fmt.Printf("WARNING: Expected 'tool' to be map[string]interface{}, got: %T, value: %+v\n", resultMap["tool"], resultMap["tool"])
			fmt.Printf("Full resultMap: %+v\n", resultMap)
			continue
		}

		// Debug: print tool data keys
		fmt.Printf("DEBUG: toolData keys: %v\n", getMapKeys(toolData))
		fmt.Printf("DEBUG: toolData values - name: %v, server_name: %v\n", toolData["name"], toolData["server_name"])

		tool := contracts.Tool{
			Name:        getString(toolData, "name"),
			ServerName:  getString(toolData, "server_name"),
			Description: getString(toolData, "description"),
			Usage:       getInt(toolData, "usage"),
		}

		result := contracts.SearchResult{
			Tool:    tool,
			Score:   getFloat(resultMap, "score"),
			Snippet: getString(resultMap, "snippet"),
			Matches: getInt(resultMap, "matches"),
		}
		results = append(results, result)
	}

	return results, nil
}

// GetConfig fetches current configuration via HTTP API
func (env *TestEnvironment) GetConfig() (*config.Config, error) {
	// Extract port from listen address
	listenAddr := env.proxyServer.GetListenAddress()
	port := listenAddr
	if i := strings.LastIndex(listenAddr, ":"); i >= 0 {
		port = listenAddr[i+1:]
	}

	url := fmt.Sprintf("http://localhost:%s/api/v1/config", port)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", "test-api-key-e2e")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var response contracts.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	data, ok := response.Data.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response data type")
	}

	configData, ok := data["config"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid config data type")
	}

	// Re-marshal and unmarshal to convert to config.Config type
	configJSON, err := json.Marshal(configData)
	if err != nil {
		return nil, err
	}

	var cfg config.Config
	if err := json.Unmarshal(configJSON, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// ApplyConfig applies a new configuration via HTTP API
func (env *TestEnvironment) ApplyConfig(cfg *config.Config) (*contracts.ConfigApplyResult, error) {
	// Extract port from listen address
	listenAddr := env.proxyServer.GetListenAddress()
	port := listenAddr
	if i := strings.LastIndex(listenAddr, ":"); i >= 0 {
		port = listenAddr[i+1:]
	}

	url := fmt.Sprintf("http://localhost:%s/api/v1/config/apply", port)

	body, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-api-key-e2e")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var response contracts.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	data, ok := response.Data.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response data type")
	}

	// Re-marshal and unmarshal to convert to ConfigApplyResult type
	resultJSON, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	var result contracts.ConfigApplyResult
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// Helper functions for JSON parsing

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getBool(m map[string]interface{}, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

func getInt(m map[string]interface{}, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return 0
}

func getFloat(m map[string]interface{}, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0.0
}

func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
