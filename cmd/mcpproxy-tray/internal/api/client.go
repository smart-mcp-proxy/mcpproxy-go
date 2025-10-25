//go:build darwin || windows

package api

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	"mcpproxy-go/internal/tray"
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
	Status      string `json:"status"`
	ShouldRetry bool   `json:"should_retry"`
	RetryCount  int    `json:"retry_count"`
	LastRetry   string `json:"last_retry_time"`
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

// Response represents the standard API response format
type Response struct {
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
	baseURL           string
	apiKey            string
	httpClient        *http.Client
	logger            *zap.SugaredLogger
	statusCh          chan StatusUpdate
	sseCancel         context.CancelFunc
	connectionStateCh chan tray.ConnectionState

	// State tracking to reduce logging noise
	lastServerState string // Hash of server states to detect changes
}

// NewClient creates a new API client with automatic socket/pipe support
func NewClient(endpoint string, logger *zap.SugaredLogger) *Client {
	// Create TLS config that trusts the local CA
	tlsConfig := createTLSConfig(logger)

	// Create custom transport
	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}

	// Check if we should use a custom dialer (Unix socket or Windows pipe)
	dialer, baseURL, err := CreateDialer(endpoint)
	if err != nil {
		if logger != nil {
			logger.Warn("Failed to create custom dialer, falling back to TCP",
				"endpoint", endpoint,
				"error", err)
		}
		baseURL = endpoint
		dialer = nil
	}

	// Apply custom dialer if available
	if dialer != nil {
		transport.DialContext = dialer
		if logger != nil {
			logger.Infow("Using socket/pipe connection",
				"endpoint", endpoint,
				"base_url", baseURL)
		}
	} else {
		if logger != nil {
			logger.Infow("Using TCP connection", "endpoint", endpoint)
		}
	}

	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{
			Timeout:   0,
			Transport: transport,
		},
		logger:            logger,
		statusCh:          make(chan StatusUpdate, 10),
		connectionStateCh: make(chan tray.ConnectionState, 8),
	}
}

// CreateHTTPClient creates an HTTP client with socket/pipe awareness and optional timeout.
// This is used by both the API client and health monitor to ensure consistent behavior.
func CreateHTTPClient(endpoint string, timeout time.Duration, logger *zap.SugaredLogger) *http.Client {
	// Create TLS config that trusts the local CA
	tlsConfig := createTLSConfig(logger)

	// Create custom transport
	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}

	// Check if we should use a custom dialer (Unix socket or Windows pipe)
	dialer, _, err := CreateDialer(endpoint)
	if err != nil {
		if logger != nil {
			logger.Debug("Using standard TCP dialer",
				"endpoint", endpoint,
				"error", err)
		}
	}

	// Apply custom dialer if available
	if dialer != nil {
		transport.DialContext = dialer
		if logger != nil {
			logger.Debug("Using socket/pipe dialer for HTTP client",
				"endpoint", endpoint)
		}
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

func (c *Client) buildURL(path string) (string, error) {
	base := strings.TrimSuffix(c.baseURL, "/")
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid base URL %q: %w", c.baseURL, err)
	}

	rel, err := url.Parse(path)
	if err != nil {
		return "", fmt.Errorf("invalid path %q: %w", path, err)
	}

	return baseURL.ResolveReference(rel).String(), nil
}

// SetAPIKey sets the API key for authentication
func (c *Client) SetAPIKey(apiKey string) {
	c.apiKey = apiKey
}

// StartSSE starts the Server-Sent Events connection for real-time updates with enhanced retry logic
func (c *Client) StartSSE(ctx context.Context) error {
	c.logger.Info("Starting enhanced SSE connection for real-time updates over socket/pipe transport")

	sseCtx, cancel := context.WithCancel(ctx)
	c.sseCancel = cancel

	go func() {
		defer close(c.statusCh)
		defer close(c.connectionStateCh)

		attemptCount := 0
		maxRetries := 10
		baseDelay := 2 * time.Second
		maxDelay := 30 * time.Second

		for {
			if sseCtx.Err() != nil {
				c.publishConnectionState(tray.ConnectionStateDisconnected)
				return
			}

			attemptCount++

			// Calculate exponential backoff delay
			minVal := attemptCount - 1
			if minVal > 4 {
				minVal = 4
			}
			if minVal < 0 {
				minVal = 0
			}
			backoffFactor := 1 << minVal
			delay := time.Duration(int64(baseDelay) * int64(backoffFactor))
			if delay > maxDelay {
				delay = maxDelay
			}

			if attemptCount > 1 {
				if c.logger != nil {
					c.logger.Infow("SSE reconnection attempt",
						"attempt", attemptCount,
						"max_retries", maxRetries,
						"delay", delay,
						"base_url", c.baseURL)
				}

				// Wait before reconnecting (except first attempt)
				select {
				case <-sseCtx.Done():
					c.publishConnectionState(tray.ConnectionStateDisconnected)
					return
				case <-time.After(delay):
				}
			}

			// Check if we've exceeded max retries
			if attemptCount > maxRetries {
				if c.logger != nil {
					c.logger.Errorw("SSE connection failed after max retries",
						"attempts", attemptCount,
						"max_retries", maxRetries,
						"base_url", c.baseURL)
				}
				c.publishConnectionState(tray.ConnectionStateDisconnected)
				return
			}

			c.publishConnectionState(tray.ConnectionStateConnecting)

			if err := c.connectSSE(sseCtx); err != nil {
				if c.logger != nil {
					c.logger.Errorw("SSE connection error",
						"error", err,
						"attempt", attemptCount,
						"max_retries", maxRetries,
						"base_url", c.baseURL)
				}

				// Check if it's a context cancellation
				if sseCtx.Err() != nil {
					c.publishConnectionState(tray.ConnectionStateDisconnected)
					return
				}

				c.publishConnectionState(tray.ConnectionStateReconnecting)
				continue
			}

			// Successful connection - reset attempt count
			if attemptCount > 1 && c.logger != nil {
				c.logger.Infow("SSE connection established successfully",
					"after_attempts", attemptCount,
					"base_url", c.baseURL)
			}
			attemptCount = 0
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

// ConnectionStateChannel exposes connectivity updates for tray consumers.
func (c *Client) ConnectionStateChannel() <-chan tray.ConnectionState {
	return c.connectionStateCh
}

// connectSSE establishes the SSE connection and processes events
func (c *Client) connectSSE(ctx context.Context) error {
	url, err := c.buildURL("/events")
	if err != nil {
		return err
	}
	if c.apiKey != "" {
		separator := "?"
		if strings.Contains(url, "?") {
			separator = "&"
		}
		url += separator + "apikey=" + c.apiKey
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, http.NoBody)
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

	c.publishConnectionState(tray.ConnectionStateConnected)

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
				c.logger.Errorw("Failed to parse SSE status data", "error", err)
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

// publishConnectionState attempts to deliver a connection state update without blocking the SSE loop.
func (c *Client) publishConnectionState(state tray.ConnectionState) {
	select {
	case c.connectionStateCh <- state:
	default:
		if c.logger != nil {
			c.logger.Debugw("Dropping connection state update", "state", state)
		}
	}
}

// GetReady checks if the core API is ready to serve requests
func (c *Client) GetReady(ctx context.Context) error {
	url, err := c.buildURL("/ready")
	if err != nil {
		return fmt.Errorf("failed to build ready URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create ready request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ready request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ready endpoint returned status %d", resp.StatusCode)
	}

	return nil
}

// GetServers fetches the list of servers from the API
func (c *Client) GetServers() ([]Server, error) {
	resp, err := c.makeRequest("GET", "/api/v1/servers", nil)
	if err != nil {
		if c.logger != nil {
			c.logger.Warnw("Failed to fetch upstream servers", "error", err)
		}
		return nil, err
	}

	if !resp.Success {
		if c.logger != nil {
			c.logger.Warnw("API reported failure while fetching servers", "error", resp.Error)
		}
		return nil, fmt.Errorf("API error: %s", resp.Error)
	}

	servers, ok := resp.Data["servers"].([]interface{})
	if !ok {
		if c.logger != nil {
			c.logger.Warnw("Unexpected server list payload shape", "data_keys", keys(resp.Data))
		}
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
			Status:      getString(serverMap, "status"),
			ShouldRetry: getBool(serverMap, "should_retry"),
			RetryCount:  getInt(serverMap, "retry_count"),
			LastRetry:   getString(serverMap, "last_retry_time"),
		}
		result = append(result, server)
	}

	// Compute state hash to detect changes and reduce logging noise
	stateHash := c.computeServerStateHash(result)
	stateChanged := stateHash != c.lastServerState

	if c.logger != nil {
		if len(result) == 0 {
			c.logger.Warnw("API returned zero upstream servers",
				"base_url", c.baseURL)
		} else if stateChanged {
			// Only log when server states actually change
			c.logger.Infow("Server state changed",
				"count", len(result),
				"connected", countConnected(result),
				"quarantined", countQuarantined(result))
			c.lastServerState = stateHash
		}
		// Silent when no changes - reduces log noise from frequent polling
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
// GetInfo fetches server information from /api/v1/info endpoint
func (c *Client) GetInfo() (map[string]interface{}, error) {
	resp, err := c.makeRequest("GET", "/api/v1/info", nil)
	if err != nil {
		return nil, err
	}

	if !resp.Success {
		return nil, fmt.Errorf("API error: %s", resp.Error)
	}

	// Return the full response including the "data" field
	result := map[string]interface{}{
		"success": resp.Success,
		"data":    resp.Data,
	}

	return result, nil
}

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
	// Get the actual web UI URL from the /api/v1/info endpoint
	// This ensures we use the correct HTTP URL even when connected via socket
	resp, err := c.makeRequest("GET", "/api/v1/info", nil)
	if err != nil {
		if c.logger != nil {
			c.logger.Errorw("Failed to get server info", "error", err)
		}
		return fmt.Errorf("failed to get server info: %w", err)
	}

	// Extract web_ui_url from response
	if resp.Data == nil {
		return fmt.Errorf("no data in response from /api/v1/info")
	}

	webUIURL, ok := resp.Data["web_ui_url"].(string)
	if !ok || webUIURL == "" {
		return fmt.Errorf("web_ui_url not found in server info")
	}

	// Add API key if not using socket communication
	url := webUIURL
	if c.apiKey != "" && !strings.HasPrefix(c.baseURL, "unix://") && !strings.HasPrefix(c.baseURL, "npipe://") {
		separator := "?"
		if strings.Contains(url, "?") {
			separator = "&"
		}
		url += separator + "apikey=" + c.apiKey
	}

	displayURL := url
	if c.apiKey != "" {
		displayURL = strings.ReplaceAll(url, c.apiKey, maskForLog(c.apiKey))
	}
	if c.logger != nil {
		c.logger.Infow("Opening web control panel", "url", displayURL)
	}

	switch runtime.GOOS {
	case "darwin":
		cmd := exec.Command("open", url)
		if err := cmd.Run(); err != nil {
			if c.logger != nil {
				c.logger.Errorw("Failed to open web control panel", "url", displayURL, "error", err)
			}
			return fmt.Errorf("failed to open web control panel: %w", err)
		}
		return nil
	case "windows":
		// Try rundll32 first
		if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Run(); err == nil {
			return nil
		}
		// Fallback to cmd start
		if err := exec.Command("cmd", "/c", "start", "", url).Run(); err != nil {
			if c.logger != nil {
				c.logger.Errorw("Failed to open web control panel", "url", displayURL, "error", err)
			}
			return fmt.Errorf("failed to open web control panel: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported OS for OpenWebUI: %s", runtime.GOOS)
	}
}

// makeRequest makes an HTTP request to the API with enhanced error handling and retry logic
func (c *Client) makeRequest(method, path string, _ interface{}) (*Response, error) {
	url, err := c.buildURL(path)
	if err != nil {
		return nil, err
	}
	maxRetries := 3
	baseDelay := 1 * time.Second

	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequest(method, url, http.NoBody)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "mcpproxy-tray/1.0")

		// Add API key header if available
		if c.apiKey != "" {
			req.Header.Set("X-API-Key", c.apiKey)
		}

		// Increased timeout to 15s to allow core to gather status from all servers
		// With 14 servers, some may be connecting/Docker starting which takes time
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel() // Defer cancel to ensure response body is fully read before canceling
		req = req.WithContext(ctx)

		resp, err := c.httpClient.Do(req)

		if err != nil {
			if attempt < maxRetries {
				delay := time.Duration(attempt) * baseDelay
				if c.logger != nil {
					c.logger.Debugw("Request failed, retrying",
						"attempt", attempt,
						"max_retries", maxRetries,
						"delay", delay,
						"error", err)
				}
				time.Sleep(delay)
				continue
			}
			return nil, fmt.Errorf("request failed after %d attempts: %w", maxRetries, err)
		}

		// Process response with proper cleanup
		result, shouldContinue, err := c.processResponse(resp, attempt, maxRetries, baseDelay, path)
		if err != nil {
			return nil, err
		}
		if shouldContinue {
			continue
		}
		return result, nil
	}

	return nil, fmt.Errorf("unexpected error in request retry loop")
}

// processResponse handles response processing with proper cleanup
func (c *Client) processResponse(resp *http.Response, attempt, maxRetries int, baseDelay time.Duration, path string) (*Response, bool, error) {
	defer resp.Body.Close()

	// Handle specific HTTP status codes
	switch resp.StatusCode {
	case 401:
		return nil, false, fmt.Errorf("authentication failed: invalid or missing API key")
	case 403:
		return nil, false, fmt.Errorf("authorization failed: insufficient permissions")
	case 404:
		return nil, false, fmt.Errorf("endpoint not found: %s", path)
	case 429:
		// Rate limited - retry with exponential backoff
		if attempt < maxRetries {
			delay := time.Duration(attempt*attempt) * baseDelay
			if c.logger != nil {
				c.logger.Warnw("Rate limited, retrying",
					"attempt", attempt,
					"delay", delay,
					"status", resp.StatusCode)
			}
			time.Sleep(delay)
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("rate limited after %d attempts", maxRetries)
	case 500, 502, 503, 504:
		// Server errors - retry
		if attempt < maxRetries {
			delay := time.Duration(attempt) * baseDelay
			if c.logger != nil {
				c.logger.Warnw("Server error, retrying",
					"attempt", attempt,
					"status", resp.StatusCode,
					"delay", delay)
			}
			time.Sleep(delay)
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("server error after %d attempts: status %d", maxRetries, resp.StatusCode)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false, fmt.Errorf("API call failed with status %d", resp.StatusCode)
	}

	var apiResp Response
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, false, fmt.Errorf("failed to decode response: %w", err)
	}

	return &apiResp, false, nil
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

func keys(m map[string]interface{}) []string {
	if len(m) == 0 {
		return nil
	}

	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func maskForLog(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

// createTLSConfig creates a TLS config that trusts the local mcpproxy CA
func createTLSConfig(logger *zap.SugaredLogger) *tls.Config {
	// Start with system cert pool
	rootCAs, err := x509.SystemCertPool()
	if err != nil {
		if logger != nil {
			logger.Warn("Failed to load system cert pool, creating empty pool", "error", err)
		}
		rootCAs = x509.NewCertPool()
	}

	// Try to load the local mcpproxy CA certificate
	caPath := getLocalCAPath()
	if caPath != "" {
		if caCert, err := os.ReadFile(caPath); err == nil {
			if rootCAs.AppendCertsFromPEM(caCert) {
				if logger != nil {
					logger.Debug("Successfully loaded local mcpproxy CA certificate", "ca_path", caPath)
				}
			} else {
				if logger != nil {
					logger.Warn("Failed to parse local mcpproxy CA certificate", "ca_path", caPath)
				}
			}
		} else {
			if logger != nil {
				logger.Debug("Local mcpproxy CA certificate not found, will use system certs only", "ca_path", caPath)
			}
		}
	}

	return &tls.Config{
		RootCAs:            rootCAs,
		InsecureSkipVerify: false, // Keep verification enabled for security
		MinVersion:         tls.VersionTLS12,
	}
}

// getLocalCAPath returns the path to the local mcpproxy CA certificate
func getLocalCAPath() string {
	// Check environment variable first
	if customCertsDir := os.Getenv("MCPPROXY_CERTS_DIR"); customCertsDir != "" {
		return filepath.Join(customCertsDir, "ca.pem")
	}

	// Use default location
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	return filepath.Join(homeDir, ".mcpproxy", "certs", "ca.pem")
}

// computeServerStateHash generates a hash of server states to detect changes
func (c *Client) computeServerStateHash(servers []Server) string {
	// Build a deterministic string representation of server states
	var parts []string
	for _, s := range servers {
		// Include relevant state fields that matter for logging changes
		state := fmt.Sprintf("%s:%t:%t:%t:%d:%s",
			s.Name, s.Connected, s.Enabled, s.Quarantined, s.ToolCount, s.Status)
		parts = append(parts, state)
	}
	sort.Strings(parts) // Sort for consistency

	// Hash the combined state
	combined := strings.Join(parts, "|")
	hash := sha256.Sum256([]byte(combined))
	return fmt.Sprintf("%x", hash[:8]) // Use first 8 bytes for compact representation
}

// countConnected returns the number of connected servers
func countConnected(servers []Server) int {
	count := 0
	for _, s := range servers {
		if s.Connected {
			count++
		}
	}
	return count
}

// countQuarantined returns the number of quarantined servers
func countQuarantined(servers []Server) int {
	count := 0
	for _, s := range servers {
		if s.Quarantined {
			count++
		}
	}
	return count
}
