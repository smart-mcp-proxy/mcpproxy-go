package cliclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"mcpproxy-go/internal/contracts"
	"mcpproxy-go/internal/reqcontext"
	"mcpproxy-go/internal/socket"

	"go.uber.org/zap"
)

// Client provides HTTP API access for CLI commands.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	logger     *zap.SugaredLogger
}

// CodeExecResult represents code execution result.
type CodeExecResult struct {
	OK     bool                   `json:"ok"`
	Result interface{}            `json:"result,omitempty"`
	Error  *CodeExecError         `json:"error,omitempty"`
	Stats  map[string]interface{} `json:"stats,omitempty"`
}

// CodeExecError represents execution error.
type CodeExecError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

// NewClient creates a new CLI HTTP client.
// If endpoint is a socket path, creates a client with socket dialer.
func NewClient(endpoint string, logger *zap.SugaredLogger) *Client {
	return NewClientWithAPIKey(endpoint, "", logger)
}

// NewClientWithAPIKey creates a new CLI HTTP client with API key authentication.
// If endpoint is a socket path, creates a client with socket dialer.
func NewClientWithAPIKey(endpoint, apiKey string, logger *zap.SugaredLogger) *Client {
	// Create custom transport with socket support
	transport := &http.Transport{}

	// Check if we should use a custom dialer (Unix socket or Windows pipe)
	dialer, baseURL, err := socket.CreateDialer(endpoint)
	if err != nil && logger != nil {
		logger.Warnw("Failed to create socket dialer, using TCP",
			"endpoint", endpoint,
			"error", err)
		baseURL = endpoint
	}

	// Apply custom dialer if available
	if dialer != nil {
		transport.DialContext = dialer
		if logger != nil {
			logger.Debugw("Using socket/pipe connection",
				"endpoint", endpoint,
				"base_url", baseURL)
		}
	} else {
		baseURL = endpoint
	}

	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout:   5 * time.Minute, // Generous timeout for long operations
			Transport: transport,
		},
		logger: logger,
	}
}

// prepareRequest adds common headers to a request (correlation ID, API key, etc.)
func (c *Client) prepareRequest(ctx context.Context, req *http.Request) {
	if correlationID := reqcontext.GetCorrelationID(ctx); correlationID != "" {
		req.Header.Set("X-Correlation-ID", correlationID)
	}
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
}

// CodeExec executes JavaScript code via the daemon API.
func (c *Client) CodeExec(
	ctx context.Context,
	code string,
	input map[string]interface{},
	timeoutMS int,
	maxToolCalls int,
	allowedServers []string,
) (*CodeExecResult, error) {
	// Build request body
	reqBody := map[string]interface{}{
		"code":  code,
		"input": input,
		"options": map[string]interface{}{
			"timeout_ms":      timeoutMS,
			"max_tool_calls":  maxToolCalls,
			"allowed_servers": allowedServers,
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	url := c.baseURL + "/api/v1/code/exec"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call code execution API: %w", err)
	}
	defer resp.Body.Close()

	// Parse response
	var result CodeExecResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &result, nil
}

// CallToolResult represents tool call result.
type CallToolResult struct {
	Content  []interface{}          `json:"content"`
	IsError  bool                   `json:"isError"`
	Metadata map[string]interface{} `json:"_meta,omitempty"`
}

// CallTool calls a tool on an upstream server via daemon API.
func (c *Client) CallTool(
	ctx context.Context,
	toolName string,
	args map[string]interface{},
) (*CallToolResult, error) {
	// Build request body (REST API format)
	reqBody := map[string]interface{}{
		"tool_name":  toolName,
		"arguments": args,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request to REST API endpoint
	url := c.baseURL + "/api/v1/tools/call"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call tool API: %w", err)
	}
	defer resp.Body.Close()

	// Read the full response body for debugging
	bodyBytes, err2 := io.ReadAll(resp.Body)
	if err2 != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err2)
	}

	// Log response for debugging
	if c.logger != nil {
		c.logger.Debugw("Received response from CallTool",
			"status_code", resp.StatusCode,
			"body", string(bodyBytes))
	}

	// Parse response (REST API format: {"success": true, "data": <result>})
	var apiResp struct {
		Success bool        `json:"success"`
		Data    interface{} `json:"data"`
		Error   string      `json:"error"`
	}

	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w (body: %s)", err, string(bodyBytes))
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("tool call failed: %s", apiResp.Error)
	}

	// Convert data to CallToolResult format
	result := &CallToolResult{}

	// Try to extract as map with content field
	if dataMap, ok := apiResp.Data.(map[string]interface{}); ok {
		if content, hasContent := dataMap["content"].([]interface{}); hasContent {
			result.Content = content
		} else {
			// Wrap data in content array if not already in that format
			result.Content = []interface{}{
				map[string]interface{}{
					"type": "text",
					"text": fmt.Sprintf("%v", apiResp.Data),
				},
			}
		}

		if isError, ok := dataMap["isError"].(bool); ok {
			result.IsError = isError
		}

		if meta, ok := dataMap["_meta"].(map[string]interface{}); ok {
			result.Metadata = meta
		}
	} else {
		// Fallback: wrap data in content array
		result.Content = []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": fmt.Sprintf("%v", apiResp.Data),
			},
		}
	}

	return result, nil
}

// Ping checks if the daemon is reachable.
func (c *Client) Ping(ctx context.Context) error {
	url := c.baseURL + "/api/v1/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon returned status %d", resp.StatusCode)
	}

	return nil
}

// GetServers retrieves list of servers from daemon.
func (c *Client) GetServers(ctx context.Context) ([]map[string]interface{}, error) {
	url := c.baseURL + "/api/v1/servers"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call servers API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp struct {
		Success bool `json:"success"`
		Data    struct {
			Servers []map[string]interface{} `json:"servers"`
		} `json:"data"`
		Error string `json:"error"`
	}

	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("API call failed: %s", apiResp.Error)
	}

	return apiResp.Data.Servers, nil
}

// GetServerLogs retrieves logs for a specific server.
func (c *Client) GetServerLogs(ctx context.Context, serverName string, tail int) ([]contracts.LogEntry, error) {
	url := fmt.Sprintf("%s/api/v1/servers/%s/logs?tail=%d", c.baseURL, serverName, tail)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call logs API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp struct {
		Success bool `json:"success"`
		Data    struct {
			Logs []contracts.LogEntry `json:"logs"`
		} `json:"data"`
		Error string `json:"error"`
	}

	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("API call failed: %s", apiResp.Error)
	}

	return apiResp.Data.Logs, nil
}

// ServerAction performs an action on a server (enable, disable, restart).
func (c *Client) ServerAction(ctx context.Context, serverName, action string) error {
	var url string
	method := http.MethodPost

	switch action {
	case "enable":
		url = fmt.Sprintf("%s/api/v1/servers/%s/enable", c.baseURL, serverName)
	case "disable":
		url = fmt.Sprintf("%s/api/v1/servers/%s/disable", c.baseURL, serverName)
	case "restart":
		url = fmt.Sprintf("%s/api/v1/servers/%s/restart", c.baseURL, serverName)
	default:
		return fmt.Errorf("unknown action: %s", action)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to call server action API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}

	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return fmt.Errorf("action failed: %s", apiResp.Error)
	}

	return nil
}

// GetDiagnostics retrieves diagnostics information from daemon.
func (c *Client) GetDiagnostics(ctx context.Context) (map[string]interface{}, error) {
	url := c.baseURL + "/api/v1/diagnostics"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call diagnostics API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
		Error   string                 `json:"error"`
	}

	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("API call failed: %s", apiResp.Error)
	}

	return apiResp.Data, nil
}

// GetInfo retrieves server info including version and update status.
func (c *Client) GetInfo(ctx context.Context) (map[string]interface{}, error) {
	return c.GetInfoWithRefresh(ctx, false)
}

// GetInfoWithRefresh retrieves server info with optional update check refresh.
// When refresh is true, forces an immediate update check against GitHub.
func (c *Client) GetInfoWithRefresh(ctx context.Context, refresh bool) (map[string]interface{}, error) {
	url := c.baseURL + "/api/v1/info"
	if refresh {
		url += "?refresh=true"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call info API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
		Error   string                 `json:"error"`
	}

	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("API call failed: %s", apiResp.Error)
	}

	return apiResp.Data, nil
}

// BulkOperationResult holds the results of a bulk operation across multiple servers.
type BulkOperationResult struct {
	Total      int               `json:"total"`
	Successful int               `json:"successful"`
	Failed     int               `json:"failed"`
	Errors     map[string]string `json:"errors"`
}

// T079: RestartAll restarts all configured servers.
func (c *Client) RestartAll(ctx context.Context) (*BulkOperationResult, error) {
	url := c.baseURL + "/api/v1/servers/restart_all"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add correlation ID from context to request headers
	c.prepareRequest(ctx, req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call restart_all API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp struct {
		Success bool                 `json:"success"`
		Data    *BulkOperationResult `json:"data"`
		Error   string               `json:"error"`
	}

	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("restart_all failed: %s", apiResp.Error)
	}

	return apiResp.Data, nil
}

// T080: EnableAll enables all configured servers.
func (c *Client) EnableAll(ctx context.Context) (*BulkOperationResult, error) {
	url := c.baseURL + "/api/v1/servers/enable_all"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add correlation ID from context to request headers
	c.prepareRequest(ctx, req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call enable_all API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp struct {
		Success bool                 `json:"success"`
		Data    *BulkOperationResult `json:"data"`
		Error   string               `json:"error"`
	}

	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("enable_all failed: %s", apiResp.Error)
	}

	return apiResp.Data, nil
}

// T080: DisableAll disables all configured servers.
func (c *Client) DisableAll(ctx context.Context) (*BulkOperationResult, error) {
	url := c.baseURL + "/api/v1/servers/disable_all"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add correlation ID from context to request headers
	c.prepareRequest(ctx, req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call disable_all API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp struct {
		Success bool                 `json:"success"`
		Data    *BulkOperationResult `json:"data"`
		Error   string               `json:"error"`
	}

	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("disable_all failed: %s", apiResp.Error)
	}

	return apiResp.Data, nil
}

// GetServerTools retrieves tools for a specific server from daemon.
func (c *Client) GetServerTools(ctx context.Context, serverName string) ([]map[string]interface{}, error) {
	url := fmt.Sprintf("%s/api/v1/servers/%s/tools", c.baseURL, serverName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call tools API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp struct {
		Success bool `json:"success"`
		Data    struct {
			Tools []map[string]interface{} `json:"tools"`
		} `json:"data"`
		Error string `json:"error"`
	}

	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("API call failed: %s", apiResp.Error)
	}

	return apiResp.Data.Tools, nil
}

// TriggerOAuthLogin initiates OAuth authentication flow for a server.
func (c *Client) TriggerOAuthLogin(ctx context.Context, serverName string) error {
	url := fmt.Sprintf("%s/api/v1/servers/%s/login", c.baseURL, serverName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to call login API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp struct {
		Success bool `json:"success"`
		Data    struct {
			Server  string `json:"server"`
			Action  string `json:"action"`
			Success bool   `json:"success"`
		} `json:"data"`
		Error string `json:"error"`
	}

	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return fmt.Errorf("login failed: %s", apiResp.Error)
	}

	return nil
}

// TriggerOAuthLogout clears OAuth token and disconnects a server.
func (c *Client) TriggerOAuthLogout(ctx context.Context, serverName string) error {
	url := fmt.Sprintf("%s/api/v1/servers/%s/logout", c.baseURL, serverName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to call logout API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp struct {
		Success bool `json:"success"`
		Data    struct {
			Server  string `json:"server"`
			Action  string `json:"action"`
			Success bool   `json:"success"`
		} `json:"data"`
		Error string `json:"error"`
	}

	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return fmt.Errorf("logout failed: %s", apiResp.Error)
	}

	return nil
}

// AddServerRequest represents the request body for adding a server.
type AddServerRequest struct {
	Name        string            `json:"name"`
	URL         string            `json:"url,omitempty"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	WorkingDir  string            `json:"working_dir,omitempty"`
	Protocol    string            `json:"protocol,omitempty"`
	Enabled     *bool             `json:"enabled,omitempty"`
	Quarantined *bool             `json:"quarantined,omitempty"`
}

// AddServerResult represents the result of adding a server.
type AddServerResult struct {
	Name        string `json:"name"`
	ID          int    `json:"id"`
	Quarantined bool   `json:"quarantined"`
}

// AddServer adds a new upstream server via the daemon API.
func (c *Client) AddServer(ctx context.Context, req *AddServerRequest) (*AddServerResult, error) {
	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := c.baseURL + "/api/v1/servers"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Add correlation ID from context
	c.prepareRequest(ctx, httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to call add server API: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Handle conflict (server already exists)
	if resp.StatusCode == http.StatusConflict {
		return nil, fmt.Errorf("server '%s' already exists", req.Name)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	var apiResp struct {
		Success bool             `json:"success"`
		Data    *AddServerResult `json:"data"`
		Error   string           `json:"error"`
	}

	if err := json.Unmarshal(respBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("add server failed: %s", apiResp.Error)
	}

	return apiResp.Data, nil
}

// RemoveServer removes an upstream server via the daemon API.
func (c *Client) RemoveServer(ctx context.Context, serverName string) error {
	url := fmt.Sprintf("%s/api/v1/servers/%s", c.baseURL, serverName)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Add correlation ID from context
	c.prepareRequest(ctx, req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to call remove server API: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Handle not found
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("server '%s' not found", serverName)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	var apiResp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}

	if err := json.Unmarshal(respBytes, &apiResp); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return fmt.Errorf("remove server failed: %s", apiResp.Error)
	}

	return nil
}

// ActivityFilterParams contains options for filtering activity records.
type ActivityFilterParams interface {
	ToQueryParams() url.Values
}

// ListActivities retrieves activity records with filtering.
func (c *Client) ListActivities(ctx context.Context, filter ActivityFilterParams) ([]map[string]interface{}, int, error) {
	apiURL := c.baseURL + "/api/v1/activity"
	if filter != nil {
		params := filter.ToQueryParams()
		if encoded := params.Encode(); encoded != "" {
			apiURL += "?" + encoded
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}

	c.prepareRequest(ctx, req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to call activity API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp struct {
		Success bool `json:"success"`
		Data    struct {
			Activities []map[string]interface{} `json:"activities"`
			Total      int                      `json:"total"`
		} `json:"data"`
		Error string `json:"error"`
	}

	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, 0, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return nil, 0, fmt.Errorf("API call failed: %s", apiResp.Error)
	}

	return apiResp.Data.Activities, apiResp.Data.Total, nil
}

// GetActivityDetail retrieves details for a specific activity record.
func (c *Client) GetActivityDetail(ctx context.Context, activityID string) (map[string]interface{}, error) {
	url := fmt.Sprintf("%s/api/v1/activity/%s", c.baseURL, activityID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.prepareRequest(ctx, req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call activity detail API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("activity not found: %s", activityID)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp struct {
		Success bool `json:"success"`
		Data    struct {
			Activity map[string]interface{} `json:"activity"`
		} `json:"data"`
		Error string `json:"error"`
	}

	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("API call failed: %s", apiResp.Error)
	}

	return apiResp.Data.Activity, nil
}

// GetActivitySummary retrieves activity summary statistics.
func (c *Client) GetActivitySummary(ctx context.Context, period, groupBy string) (map[string]interface{}, error) {
	url := c.baseURL + "/api/v1/activity/summary?period=" + period
	if groupBy != "" {
		url += "&group_by=" + groupBy
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.prepareRequest(ctx, req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call activity summary API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
		Error   string                 `json:"error"`
	}

	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("API call failed: %s", apiResp.Error)
	}

	return apiResp.Data, nil
}
