package cliclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"mcpproxy-go/internal/socket"

	"go.uber.org/zap"
)

// Client provides HTTP API access for CLI commands.
type Client struct {
	baseURL    string
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
		httpClient: &http.Client{
			Timeout:   5 * time.Minute, // Generous timeout for long operations
			Transport: transport,
		},
		logger: logger,
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
