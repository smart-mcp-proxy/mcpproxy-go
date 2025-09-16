//go:build darwin

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// HTTPServerClient implements the ServerInterface using HTTP API calls
type HTTPServerClient struct {
	baseURL    string
	httpClient *http.Client
	logger     *zap.SugaredLogger
	statusCh   chan interface{}
	cancelSSE  context.CancelFunc
}

// NewHTTPServerClient creates a new HTTP client adapter
func NewHTTPServerClient(baseURL string, logger *zap.SugaredLogger) *HTTPServerClient {
	client := &HTTPServerClient{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger:   logger,
		statusCh: make(chan interface{}, 10),
	}

	// Start SSE connection for status updates
	client.startSSEConnection()

	return client
}

// startSSEConnection starts the Server-Sent Events connection for real-time updates
func (c *HTTPServerClient) startSSEConnection() {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancelSSE = cancel

	go func() {
		defer close(c.statusCh)

		for {
			select {
			case <-ctx.Done():
				return
			default:
				if err := c.connectSSE(ctx); err != nil {
					if c.logger != nil {
						c.logger.Error("SSE connection error", "error", err)
					}
					// Retry after delay
					select {
					case <-ctx.Done():
						return
					case <-time.After(5 * time.Second):
						continue
					}
				}
			}
		}
	}()
}

// connectSSE establishes SSE connection and processes events
func (c *HTTPServerClient) connectSSE(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/api/events", nil)
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
func (c *HTTPServerClient) processSSEEvent(eventType, data string) {
	if eventType == "status" {
		var statusData map[string]interface{}
		if err := json.Unmarshal([]byte(data), &statusData); err != nil {
			if c.logger != nil {
				c.logger.Error("Failed to parse SSE status data", "error", err)
			}
			return
		}

		// Send to status channel
		select {
		case c.statusCh <- statusData:
		default:
			// Channel full, skip this update
		}
	}
}

// apiCall makes an HTTP API call
func (c *HTTPServerClient) apiCall(method, path string, body interface{}) (map[string]interface{}, error) {
	var reqBody []byte
	var err error

	if body != nil {
		reqBody, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
	}

	req, err := http.NewRequest(method, c.baseURL+path, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API call failed with status %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result, nil
}

// ServerInterface implementation

func (c *HTTPServerClient) IsRunning() bool {
	result, err := c.apiCall("GET", "/api/status", nil)
	if err != nil {
		return false
	}

	if running, ok := result["running"].(bool); ok {
		return running
	}
	return false
}

func (c *HTTPServerClient) GetListenAddress() string {
	result, err := c.apiCall("GET", "/api/status", nil)
	if err != nil {
		return ""
	}

	if addr, ok := result["listen_addr"].(string); ok {
		return addr
	}
	return ""
}

func (c *HTTPServerClient) GetUpstreamStats() map[string]interface{} {
	result, err := c.apiCall("GET", "/api/status", nil)
	if err != nil {
		return map[string]interface{}{}
	}

	if stats, ok := result["upstream_stats"].(map[string]interface{}); ok {
		return stats
	}
	return map[string]interface{}{}
}

func (c *HTTPServerClient) StartServer(ctx context.Context) error {
	result, err := c.apiCall("POST", "/api/control/start", nil)
	if err != nil {
		return err
	}

	if success, ok := result["success"].(bool); ok && success {
		return nil
	}

	if errMsg, ok := result["error"].(string); ok {
		return fmt.Errorf("start failed: %s", errMsg)
	}

	return fmt.Errorf("start failed with unknown error")
}

func (c *HTTPServerClient) StopServer() error {
	result, err := c.apiCall("POST", "/api/control/stop", nil)
	if err != nil {
		return err
	}

	if success, ok := result["success"].(bool); ok && success {
		return nil
	}

	if errMsg, ok := result["error"].(string); ok {
		return fmt.Errorf("stop failed: %s", errMsg)
	}

	return fmt.Errorf("stop failed with unknown error")
}

func (c *HTTPServerClient) GetStatus() interface{} {
	result, err := c.apiCall("GET", "/api/status", nil)
	if err != nil {
		return map[string]interface{}{
			"phase":   "Error",
			"message": err.Error(),
		}
	}

	if status, ok := result["status"]; ok {
		return status
	}

	return result
}

func (c *HTTPServerClient) StatusChannel() <-chan interface{} {
	return c.statusCh
}

func (c *HTTPServerClient) GetQuarantinedServers() ([]map[string]interface{}, error) {
	// Get all servers and filter quarantined ones
	allServers, err := c.GetAllServers()
	if err != nil {
		return nil, err
	}

	var quarantined []map[string]interface{}
	for _, server := range allServers {
		if q, ok := server["quarantined"].(bool); ok && q {
			quarantined = append(quarantined, server)
		}
	}

	return quarantined, nil
}

func (c *HTTPServerClient) UnquarantineServer(serverName string) error {
	body := map[string]interface{}{
		"quarantined": false,
	}

	result, err := c.apiCall("POST", fmt.Sprintf("/api/servers/%s/quarantine", serverName), body)
	if err != nil {
		return err
	}

	if success, ok := result["success"].(bool); ok && success {
		return nil
	}

	if errMsg, ok := result["error"].(string); ok {
		return fmt.Errorf("unquarantine failed: %s", errMsg)
	}

	return fmt.Errorf("unquarantine failed with unknown error")
}

func (c *HTTPServerClient) EnableServer(serverName string, enabled bool) error {
	body := map[string]interface{}{
		"enabled": enabled,
	}

	result, err := c.apiCall("POST", fmt.Sprintf("/api/servers/%s/enable", serverName), body)
	if err != nil {
		return err
	}

	if success, ok := result["success"].(bool); ok && success {
		return nil
	}

	if errMsg, ok := result["error"].(string); ok {
		return fmt.Errorf("enable/disable failed: %s", errMsg)
	}

	return fmt.Errorf("enable/disable failed with unknown error")
}

func (c *HTTPServerClient) QuarantineServer(serverName string, quarantined bool) error {
	body := map[string]interface{}{
		"quarantined": quarantined,
	}

	result, err := c.apiCall("POST", fmt.Sprintf("/api/servers/%s/quarantine", serverName), body)
	if err != nil {
		return err
	}

	if success, ok := result["success"].(bool); ok && success {
		return nil
	}

	if errMsg, ok := result["error"].(string); ok {
		return fmt.Errorf("quarantine failed: %s", errMsg)
	}

	return fmt.Errorf("quarantine failed with unknown error")
}

func (c *HTTPServerClient) GetAllServers() ([]map[string]interface{}, error) {
	result, err := c.apiCall("GET", "/api/servers", nil)
	if err != nil {
		return nil, err
	}

	if servers, ok := result["servers"].([]interface{}); ok {
		var serverList []map[string]interface{}
		for _, server := range servers {
			if serverMap, ok := server.(map[string]interface{}); ok {
				serverList = append(serverList, serverMap)
			}
		}
		return serverList, nil
	}

	return []map[string]interface{}{}, nil
}

func (c *HTTPServerClient) ReloadConfiguration() error {
	result, err := c.apiCall("POST", "/api/control/reload", nil)
	if err != nil {
		return err
	}

	if success, ok := result["success"].(bool); ok && success {
		return nil
	}

	if errMsg, ok := result["error"].(string); ok {
		return fmt.Errorf("reload failed: %s", errMsg)
	}

	return fmt.Errorf("reload failed with unknown error")
}

func (c *HTTPServerClient) GetConfigPath() string {
	// This information isn't available via API, return default
	return "~/.mcpproxy/mcp_config.json"
}

func (c *HTTPServerClient) GetLogDir() string {
	// This information isn't available via API, return default
	return "~/.mcpproxy/logs"
}

func (c *HTTPServerClient) TriggerOAuthLogin(serverName string) error {
	result, err := c.apiCall("POST", fmt.Sprintf("/api/oauth/%s", serverName), nil)
	if err != nil {
		return err
	}

	if success, ok := result["success"].(bool); ok && success {
		return nil
	}

	if errMsg, ok := result["error"].(string); ok {
		return fmt.Errorf("OAuth trigger failed: %s", errMsg)
	}

	return fmt.Errorf("OAuth trigger failed with unknown error")
}

// Close cleanly shuts down the HTTP client
func (c *HTTPServerClient) Close() {
	if c.cancelSSE != nil {
		c.cancelSSE()
	}
}