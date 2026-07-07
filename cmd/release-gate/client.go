package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a minimal typed client for the mcpproxy REST API (/api/v1),
// decoding the {"success":bool,"data":...} envelope. It exists so invariant
// checks can be unit-tested against httptest fakes (FR-022).
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func newClient(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

type apiEnvelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
	Message string          `json:"message"`
}

// do issues a request and decodes the envelope's data into out (if non-nil).
func (c *Client) do(ctx context.Context, method, path string, body, out any, headers map[string]string) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.APIKey != "" {
		req.Header.Set("X-API-Key", c.APIKey)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return fmt.Errorf("%s %s: read body: %w", method, path, err)
	}
	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("%s %s: status %d, non-envelope body: %s", method, path, resp.StatusCode, truncateStr(string(raw), 300))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !env.Success {
		msg := env.Error
		if msg == "" {
			msg = env.Message
		}
		return &apiError{Status: resp.StatusCode, Msg: msg, Path: path}
	}
	if out != nil {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("%s %s: decode data: %w (body: %s)", method, path, err, truncateStr(string(env.Data), 300))
		}
	}
	return nil
}

// apiError carries the HTTP status so callers can branch on 404/503.
type apiError struct {
	Status int
	Msg    string
	Path   string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("api %s: status %d: %s", e.Path, e.Status, e.Msg)
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out, nil)
}

func (c *Client) postJSON(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, body, out, nil)
}

// --- domain shapes (subset of internal/contracts we assert on) -------------

type serverHealth struct {
	Level      string `json:"level"`
	AdminState string `json:"admin_state"`
}

type serverInfo struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	Enabled        bool          `json:"enabled"`
	Quarantined    bool          `json:"quarantined"`
	Connected      bool          `json:"connected"`
	Status         string        `json:"status"`
	LastError      string        `json:"last_error"`
	ToolCount      int           `json:"tool_count"`
	Health         *serverHealth `json:"health"`
	TokenExpiresAt *time.Time    `json:"token_expires_at"`
	OAuthStatus    string        `json:"oauth_status"`
}

type serversResponse struct {
	Servers []serverInfo `json:"servers"`
}

func (c *Client) servers(ctx context.Context) ([]serverInfo, error) {
	var resp serversResponse
	if err := c.getJSON(ctx, "/api/v1/servers", &resp); err != nil {
		return nil, err
	}
	return resp.Servers, nil
}

func (c *Client) server(ctx context.Context, name string) (*serverInfo, error) {
	servers, err := c.servers(ctx)
	if err != nil {
		return nil, err
	}
	for i := range servers {
		if servers[i].Name == name {
			return &servers[i], nil
		}
	}
	return nil, nil
}

type activityRecord struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Source     string         `json:"source"`
	ServerName string         `json:"server_name"`
	ToolName   string         `json:"tool_name"`
	Status     string         `json:"status"`
	RequestID  string         `json:"request_id"`
	Arguments  map[string]any `json:"arguments"`
}

type activityListResponse struct {
	Activities []activityRecord `json:"activities"`
	Total      int              `json:"total"`
}

func (c *Client) activities(ctx context.Context, query url.Values) ([]activityRecord, int, error) {
	var resp activityListResponse
	path := "/api/v1/activity"
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	if err := c.getJSON(ctx, path, &resp); err != nil {
		return nil, 0, err
	}
	return resp.Activities, resp.Total, nil
}

type tokenStats struct {
	TotalServerToolListSize int `json:"total_server_tool_list_size"`
	SavedTokens             int `json:"saved_tokens"`
}

func (c *Client) tokenStats(ctx context.Context) (*tokenStats, error) {
	var ts tokenStats
	if err := c.getJSON(ctx, "/api/v1/stats/tokens", &ts); err != nil {
		return nil, err
	}
	return &ts, nil
}

type usageToolStat struct {
	Server string `json:"server"`
	Tool   string `json:"tool"`
	Calls  int64  `json:"calls"`
	Errors int64  `json:"errors"`
}

type usageResponse struct {
	Tools []usageToolStat `json:"tools"`
}

func (c *Client) usage(ctx context.Context) (*usageResponse, error) {
	var u usageResponse
	if err := c.getJSON(ctx, "/api/v1/activity/usage", &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// telemetryPayload is the subset of the heartbeat payload the counters
// invariant pins (FR-012): builtin tool-call counters are pure in-process
// counters that move with traffic even when telemetry sending is disabled
// (CI / MCPPROXY_TELEMETRY=false); network-dependent fields are deliberately
// excluded.
type telemetryPayload struct {
	BuiltinToolCalls map[string]int64 `json:"builtin_tool_calls"`
}

// telemetry returns (payload, available, error): available=false when the
// endpoint reports the telemetry service unavailable (503).
func (c *Client) telemetry(ctx context.Context) (*telemetryPayload, bool, error) {
	var p telemetryPayload
	err := c.getJSON(ctx, "/api/v1/telemetry/payload", &p)
	if err != nil {
		var ae *apiError
		if asAPIError(err, &ae) && ae.Status == http.StatusServiceUnavailable {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &p, true, nil
}

func asAPIError(err error, target **apiError) bool {
	ae, ok := err.(*apiError)
	if ok {
		*target = ae
	}
	return ok
}

type searchResultTool struct {
	Name       string `json:"name"`
	ServerName string `json:"server_name"`
}

type searchResult struct {
	Tool  searchResultTool `json:"tool"`
	Score float64          `json:"score"`
}

type searchResponse struct {
	Results []searchResult `json:"results"`
	Total   int            `json:"total"`
}

func (c *Client) searchIndex(ctx context.Context, q string, limit int) (*searchResponse, error) {
	var resp searchResponse
	path := fmt.Sprintf("/api/v1/index/search?q=%s&limit=%d", url.QueryEscape(q), limit)
	if err := c.getJSON(ctx, path, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// contentBlock is one MCP content block from a REST tool-call response.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// callToolREST invokes an upstream tool through POST /api/v1/tools/call
// using the call_tool_read variant (Spec 018), sending the given
// X-Request-Id header, and returns the concatenated text content.
func (c *Client) callToolREST(ctx context.Context, qualifiedTool string, args map[string]any, requestID string) (string, error) {
	body := map[string]any{
		"tool_name": "call_tool_read",
		"arguments": map[string]any{
			"name": qualifiedTool,
			"args": args,
		},
	}
	var headers map[string]string
	if requestID != "" {
		headers = map[string]string{"X-Request-Id": requestID}
	}
	var blocks []contentBlock
	if err := c.do(ctx, http.MethodPost, "/api/v1/tools/call", body, &blocks, headers); err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, b := range blocks {
		sb.WriteString(b.Text)
	}
	return sb.String(), nil
}

// addServerRequest mirrors the POST /api/v1/servers body fields we use.
type addServerRequest struct {
	Name        string            `json:"name"`
	URL         string            `json:"url,omitempty"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Protocol    string            `json:"protocol,omitempty"`
	Enabled     *bool             `json:"enabled,omitempty"`
	Quarantined *bool             `json:"quarantined,omitempty"`
	Isolation   *isolationRequest `json:"isolation,omitempty"`
}

// isolationRequest mirrors httpapi.IsolationRequest (the subset we use).
type isolationRequest struct {
	Enabled *bool `json:"enabled,omitempty"`
}

func (c *Client) addServer(ctx context.Context, req addServerRequest) error {
	return c.postJSON(ctx, "/api/v1/servers", req, nil)
}

func (c *Client) removeServer(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/servers/"+url.PathEscape(name), nil, nil, nil)
}

func (c *Client) unquarantineServer(ctx context.Context, name string) error {
	return c.postJSON(ctx, "/api/v1/servers/"+url.PathEscape(name)+"/unquarantine", map[string]any{}, nil)
}

func (c *Client) restartServer(ctx context.Context, name string) error {
	return c.postJSON(ctx, "/api/v1/servers/"+url.PathEscape(name)+"/restart", map[string]any{}, nil)
}

func (c *Client) approveAllTools(ctx context.Context, name string) error {
	return c.postJSON(ctx, "/api/v1/servers/"+url.PathEscape(name)+"/tools/approve", map[string]any{"approve_all": true}, nil)
}

type oauthStartResponse struct {
	AuthURL       string `json:"auth_url"`
	BrowserOpened bool   `json:"browser_opened"`
	BrowserError  string `json:"browser_error"`
}

func (c *Client) serverLogin(ctx context.Context, name string) (*oauthStartResponse, error) {
	var resp oauthStartResponse
	if err := c.postJSON(ctx, "/api/v1/servers/"+url.PathEscape(name)+"/login", map[string]any{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) statusOK(ctx context.Context) error {
	return c.getJSON(ctx, "/api/v1/status", nil)
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
