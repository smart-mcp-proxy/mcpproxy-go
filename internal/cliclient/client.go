package cliclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	// Build request body (MCP tools/call format)
	reqBody := map[string]interface{}{
		"method": "tools/call",
		"params": map[string]interface{}{
			"name":      toolName,
			"arguments": args,
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request to MCP endpoint
	url := c.baseURL + "/mcp"
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

	// Parse response (MCP JSON-RPC format)
	var mcpResp struct {
		Result CallToolResult `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&mcpResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if mcpResp.Error != nil {
		return nil, fmt.Errorf("tool call failed: %s", mcpResp.Error.Message)
	}

	return &mcpResp.Result, nil
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
