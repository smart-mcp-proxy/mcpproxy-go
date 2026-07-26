package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/index"
)

// LiveClient talks to a running mcpproxy instance (e.g. the bench
// docker-compose substrate on 127.0.0.1:8092) over its REST API. It is used by
// the live benchmark run to pull the exact tool definitions (with schemas) and
// to replay the retrieval golden set through the proxy's BM25 search.
type LiveClient struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// NewLiveClient builds a LiveClient for baseURL (e.g. "http://127.0.0.1:8092")
// authenticating with apiKey via the X-API-Key header.
func NewLiveClient(baseURL, apiKey string) *LiveClient {
	return &LiveClient{
		BaseURL: baseURL,
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// successEnvelope is the standard mcpproxy REST response wrapper
// ({"success":true,"data":{...}}). Data is decoded lazily by each caller.
type successEnvelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error,omitempty"`
}

// getJSON performs an authenticated GET and unmarshals the envelope's data
// field into out.
func (c *LiveClient) getJSON(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build request %q: %w", path, err)
	}
	if c.APIKey != "" {
		req.Header.Set("X-API-Key", c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("GET %q: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read %q: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %q: status %d: %s", path, resp.StatusCode, string(body))
	}
	var env successEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("decode envelope %q: %w", path, err)
	}
	if !env.Success {
		return fmt.Errorf("GET %q: api error: %s", path, env.Error)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("decode data %q: %w", path, err)
	}
	return nil
}

// apiTool mirrors contracts.Tool for the fields the benchmark needs. The schema
// is kept raw so its exact serialized form is what gets tokenized.
type apiTool struct {
	Name        string          `json:"name"`
	ServerName  string          `json:"server_name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema,omitempty"`
}

// FetchUpstreamTools pulls the consolidated tool list (GET /api/v1/tools) and
// returns every upstream tool with its full JSON input schema, ready to feed
// into schema-aware token counting for the baseline.
func (c *LiveClient) FetchUpstreamTools(ctx context.Context) ([]Tool, error) {
	var resp struct {
		Tools []apiTool `json:"tools"`
	}
	if err := c.getJSON(ctx, "/api/v1/tools", &resp); err != nil {
		return nil, err
	}
	tools := make([]Tool, 0, len(resp.Tools))
	for _, t := range resp.Tools {
		schema, err := normalizeSchema(t.Schema)
		if err != nil {
			return nil, fmt.Errorf("tool %s:%s: %w", t.ServerName, t.Name, err)
		}
		tools = append(tools, Tool{
			ToolID:      t.ServerName + ":" + t.Name,
			Server:      t.ServerName,
			Name:        t.Name,
			Description: t.Description,
			Schema:      schema,
		})
	}
	return tools, nil
}

// normalizeSchema treats an empty JSON object ("{}") or JSON null the same as
// an absent schema so a tool with no real parameters does not inflate token
// counts, and CANONICALIZES everything else (CanonicalJSON: sorted keys,
// compact, verbatim numbers) — the live fetch is an ingestion boundary, so the
// bytes it hands out must match what the arm renderers produce (contract
// parity with bench/arms baseline). Invalid schema JSON is an explicit error.
func normalizeSchema(raw json.RawMessage) (json.RawMessage, error) {
	switch string(raw) {
	case "", "null":
		return nil, nil
	}
	canon, err := CanonicalJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("normalize input schema: %w", err)
	}
	if canon == "{}" || canon == "null" {
		return nil, nil
	}
	return json.RawMessage(canon), nil
}

// Search replays one query through the proxy's BM25 tool search
// (GET /api/v1/index/search) and returns the ranked tool IDs (server:tool,
// best first) plus the client-measured round-trip latency.
//
// Latency is measured client-side on purpose: the server's SearchToolsResponse
// "took" field is currently a hardcoded "0ms" stub (internal/httpapi
// handleSearchTools), so it cannot be trusted as the proxy-side timing.
func (c *LiveClient) Search(ctx context.Context, query string, limit int) (ranked []string, latency time.Duration, err error) {
	q := url.Values{}
	q.Set("q", query)
	q.Set("limit", strconv.Itoa(limit))
	path := "/api/v1/index/search?" + q.Encode()

	var resp struct {
		Results []struct {
			Tool  apiTool `json:"tool"`
			Score float64 `json:"score"`
		} `json:"results"`
	}
	start := time.Now()
	err = c.getJSON(ctx, path, &resp)
	latency = time.Since(start)
	if err != nil {
		return nil, latency, err
	}
	ranked = make([]string, 0, len(resp.Results))
	for _, r := range resp.Results {
		// The search endpoint returns canonical "server:tool" names since #871;
		// CanonicalToolName keeps compatibility with older proxies that return
		// the bare tool name.
		ranked = append(ranked, index.CanonicalToolName(r.Tool.ServerName, r.Tool.Name))
	}
	return ranked, latency, nil
}

// LoadGoldenSet reads the Spec 065 retrieval golden set
// (retrieval_golden_v1.json) from disk.
func LoadGoldenSet(path string) (*GoldenSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read golden set %q: %w", path, err)
	}
	var g GoldenSet
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, fmt.Errorf("parse golden set %q: %w", path, err)
	}
	if len(g.Queries) == 0 {
		return nil, fmt.Errorf("golden set %q contains no queries", path)
	}
	return &g, nil
}
