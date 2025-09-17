//go:build darwin

package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Server represents a server from the API
type Server struct {
	Name        string `json:"name"`
	Connected   bool   `json:"connected"`
	Connecting  bool   `json:"connecting"`
	Enabled     bool   `json:"enabled"`
	Quarantined bool   `json:"quarantined"`
	Protocol    string `json:"protocol"`
	URL         string `json:"url"`
	Command     string `json:"command"`
	ToolCount   int    `json:"tool_count"`
	LastError   string `json:"last_error"`
}

// Tool represents a tool from the API
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Server      string                 `json:"server"`
	InputSchema map[string]interface{} `json:"input_schema,omitempty"`
}

// SearchResult represents a search result from the API
type SearchResult struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Server      string                 `json:"server"`
	Score       float64                `json:"score"`
	InputSchema map[string]interface{} `json:"input_schema,omitempty"`
}

// APIResponse represents the standard API response format
type APIResponse struct {
	Success bool                   `json:"success"`
	Data    map[string]interface{} `json:"data,omitempty"`
	Error   string                 `json:"error,omitempty"`
}

// StatusUpdate represents a status update from SSE
type StatusUpdate struct {
	Running       bool                   `json:"running"`
	ListenAddr    string                 `json:"listen_addr"`
	UpstreamStats map[string]interface{} `json:"upstream_stats"`
	Status        map[string]interface{} `json:"status"`
	Timestamp     int64                  `json:"timestamp"`
}

// Client provides access to the mcpproxy API
type Client struct {
	baseURL    string
	httpClient *http.Client
	logger     *zap.SugaredLogger
	statusCh   chan StatusUpdate
	sseCancel  context.CancelFunc
}

// NewClient creates a new API client
func NewClient(baseURL string, logger *zap.SugaredLogger) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger:   logger,
		statusCh: make(chan StatusUpdate, 10),
	}
}

// StartSSE starts the Server-Sent Events connection for real-time updates
func (c *Client) StartSSE(ctx context.Context) error {
	c.logger.Info("Starting SSE connection for real-time updates")

	sseCtx, cancel := context.WithCancel(ctx)
	c.sseCancel = cancel

	go func() {
		defer close(c.statusCh)

		for {
			select {
			case <-sseCtx.Done():
				c.logger.Info("SSE connection stopped")
				return
			default:
				if err := c.connectSSE(sseCtx); err != nil {
					if c.logger != nil {
						c.logger.Error("SSE connection error", "error", err)
					}

					// Retry after delay
					select {
					case <-sseCtx.Done():
						return
					case <-time.After(5 * time.Second):
						continue
					}
				}
			}
		}
	}()

	return nil
}

// StopSSE stops the SSE connection
func (c *Client) StopSSE() {
	if c.sseCancel != nil {
		c.sseCancel()
	}
}

// StatusChannel returns the channel for status updates
func (c *Client) StatusChannel() <-chan StatusUpdate {
	return c.statusCh
}

// connectSSE establishes the SSE connection and processes events
func (c *Client) connectSSE(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/events", nil)
	if err != nil {
		return err
	}

	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("SSE connection failed with status: %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	var eventType string
	var data strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			// End of event, process it
			if eventType != "" && data.Len() > 0 {
				c.processSSEEvent(eventType, data.String())
				eventType = ""
				data.Reset()
			}
		} else if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			dataLine := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data.Len() > 0 {
				data.WriteString("\n")
			}
			data.WriteString(dataLine)
		}
	}

	return scanner.Err()
}

// processSSEEvent processes incoming SSE events
func (c *Client) processSSEEvent(eventType, data string) {
	if eventType == "status" {
		var statusUpdate StatusUpdate
		if err := json.Unmarshal([]byte(data), &statusUpdate); err != nil {
			if c.logger != nil {
				c.logger.Error("Failed to parse SSE status data", "error", err)
			}
			return
		}

		// Send to status channel (non-blocking)
		select {
		case c.statusCh <- statusUpdate:
		default:
			// Channel full, skip this update
		}
	}
}

// GetServers fetches the list of servers from the API
func (c *Client) GetServers() ([]Server, error) {
	resp, err := c.makeRequest("GET", "/api/v1/servers", nil)
	if err != nil {
		return nil, err
	}

	if !resp.Success {
		return nil, fmt.Errorf("API error: %s", resp.Error)
	}

	servers, ok := resp.Data["servers"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected response format")
	}

	var result []Server
	for _, serverData := range servers {
		serverMap, ok := serverData.(map[string]interface{})
		if !ok {
			continue
		}

		server := Server{
			Name:        getString(serverMap, "name"),
			Connected:   getBool(serverMap, "connected"),
			Connecting:  getBool(serverMap, "connecting"),
			Enabled:     getBool(serverMap, "enabled"),
			Quarantined: getBool(serverMap, "quarantined"),
			Protocol:    getString(serverMap, "protocol"),
			URL:         getString(serverMap, "url"),
			Command:     getString(serverMap, "command"),
			ToolCount:   getInt(serverMap, "tool_count"),
			LastError:   getString(serverMap, "last_error"),
		}
		result = append(result, server)
	}

	return result, nil
}

// EnableServer enables or disables a server
func (c *Client) EnableServer(serverName string, enabled bool) error {
	var endpoint string
	if enabled {
		endpoint = fmt.Sprintf("/api/v1/servers/%s/enable", serverName)
	} else {
		endpoint = fmt.Sprintf("/api/v1/servers/%s/disable", serverName)
	}

	resp, err := c.makeRequest("POST", endpoint, nil)
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("API error: %s", resp.Error)
	}

	return nil
}

// RestartServer restarts a server
func (c *Client) RestartServer(serverName string) error {
	endpoint := fmt.Sprintf("/api/v1/servers/%s/restart", serverName)

	resp, err := c.makeRequest("POST", endpoint, nil)
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("API error: %s", resp.Error)
	}

	return nil
}

// TriggerOAuthLogin triggers OAuth login for a server
func (c *Client) TriggerOAuthLogin(serverName string) error {
	endpoint := fmt.Sprintf("/api/v1/servers/%s/login", serverName)

	resp, err := c.makeRequest("POST", endpoint, nil)
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("API error: %s", resp.Error)
	}

	return nil
}

// GetServerTools gets tools for a specific server
func (c *Client) GetServerTools(serverName string) ([]Tool, error) {
	endpoint := fmt.Sprintf("/api/v1/servers/%s/tools", serverName)

	resp, err := c.makeRequest("GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	if !resp.Success {
		return nil, fmt.Errorf("API error: %s", resp.Error)
	}

	tools, ok := resp.Data["tools"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected response format")
	}

	var result []Tool
	for _, toolData := range tools {
		toolMap, ok := toolData.(map[string]interface{})
		if !ok {
			continue
		}

		tool := Tool{
			Name:        getString(toolMap, "name"),
			Description: getString(toolMap, "description"),
			Server:      getString(toolMap, "server"),
		}

		if schema, ok := toolMap["input_schema"].(map[string]interface{}); ok {
			tool.InputSchema = schema
		}

		result = append(result, tool)
	}

	return result, nil
}

// SearchTools searches for tools
func (c *Client) SearchTools(query string, limit int) ([]SearchResult, error) {
	endpoint := fmt.Sprintf("/api/v1/index/search?q=%s&limit=%d", query, limit)

	resp, err := c.makeRequest("GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	if !resp.Success {
		return nil, fmt.Errorf("API error: %s", resp.Error)
	}

	results, ok := resp.Data["results"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected response format")
	}

	var searchResults []SearchResult
	for _, resultData := range results {
		resultMap, ok := resultData.(map[string]interface{})
		if !ok {
			continue
		}

		result := SearchResult{
			Name:        getString(resultMap, "name"),
			Description: getString(resultMap, "description"),
			Server:      getString(resultMap, "server"),
			Score:       getFloat64(resultMap, "score"),
		}

		if schema, ok := resultMap["input_schema"].(map[string]interface{}); ok {
			result.InputSchema = schema
		}

		searchResults = append(searchResults, result)
	}

	return searchResults, nil
}

// OpenWebUI opens the web control panel in the default browser
func (c *Client) OpenWebUI() error {
	url := c.baseURL + "/ui/"
	c.logger.Info("Opening web control panel", "url", url)

	cmd := exec.Command("open", url)
	return cmd.Run()
}

// makeRequest makes an HTTP request to the API
func (c *Client) makeRequest(method, path string, body interface{}) (*APIResponse, error) {
	url := c.baseURL + path

	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API call failed with status %d", resp.StatusCode)
	}

	var apiResp APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &apiResp, nil
}

// Helper functions to safely extract values from maps
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

func getFloat64(m map[string]interface{}, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0.0
}
