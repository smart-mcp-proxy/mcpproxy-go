package cliclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
)

// CatalogSearchResponse mirrors GET /api/v1/catalog/search's response body
// (contracts/rest-api.md#catalog).
type CatalogSearchResponse struct {
	Query       string                     `json:"query"`
	Results     []registries.CatalogResult `json:"results"`
	Sections    *CatalogSections           `json:"sections"`
	Unavailable []registries.SourceError   `json:"unavailable"`
}

// CatalogSections mirrors the REST empty-query landing sections.
type CatalogSections struct {
	Official []registries.CatalogResult `json:"official"`
	Popular  []registries.CatalogResult `json:"popular"`
}

// CatalogSearch calls GET /api/v1/catalog/search (Spec 109 FR-060/066), the
// backend for `mcpproxy catalog search|show`.
func (c *Client) CatalogSearch(ctx context.Context, q, source, tag string, limit int) (*CatalogSearchResponse, error) {
	u := c.baseURL + "/api/v1/catalog/search"
	params := url.Values{}
	if q != "" {
		params.Set("q", q)
	}
	if source != "" {
		params.Set("source", source)
	}
	if tag != "" {
		params.Set("tag", tag)
	}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}
	if enc := params.Encode(); enc != "" {
		u += "?" + enc
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	c.prepareRequest(ctx, req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call catalog search API: %w", err)
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
		Success bool                  `json:"success"`
		Data    CatalogSearchResponse `json:"data"`
		Error   string                `json:"error"`
	}
	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	if !apiResp.Success {
		return nil, parseAPIError(apiResp.Error, "")
	}
	return &apiResp.Data, nil
}
