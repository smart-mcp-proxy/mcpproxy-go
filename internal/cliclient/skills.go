package cliclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// Spec 125 (skills over MCP), PR-1: client calls of the administrator-only
// skills endpoints.

// skillsRequest sends one request and decodes the success envelope's data into
// out. A refusal keeps the daemon's own message (it starts with the stable
// error code, for example "skills_not_supported: ...") and the request id.
func (c *Client) skillsRequest(ctx context.Context, method, path string, query url.Values, out interface{}) error {
	u := c.baseURL + path
	if encoded := query.Encode(); encoded != "" {
		u += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to call skills API: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}
	var env struct {
		Success   bool            `json:"success"`
		Data      json.RawMessage `json:"data"`
		Error     string          `json:"error"`
		RequestID string          `json:"request_id"`
	}
	jsonErr := json.Unmarshal(body, &env)
	if resp.StatusCode != http.StatusOK || (jsonErr == nil && !env.Success) {
		if jsonErr == nil && env.Error != "" {
			return parseAPIError(env.Error, env.RequestID)
		}
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}
	if jsonErr != nil {
		return fmt.Errorf("failed to parse response: %w", jsonErr)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	return nil
}

// ListServerSkills reads GET /api/v1/servers/{id}/skills.
func (c *Client) ListServerSkills(ctx context.Context, serverName string) (*contracts.SkillsListResponse, error) {
	var out contracts.SkillsListResponse
	if err := c.skillsRequest(ctx, http.MethodGet, "/api/v1/servers/"+url.PathEscape(serverName)+"/skills", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetServerSkillContent reads one chunk of one skill file from
// GET /api/v1/servers/{id}/skills/content.
func (c *Client) GetServerSkillContent(ctx context.Context, serverName, skill, file string, approved bool, offset int64) (*contracts.SkillContentResponse, error) {
	q := url.Values{}
	q.Set("skill", skill)
	if file != "" {
		q.Set("file", file)
	}
	if approved {
		q.Set("copy", "approved")
	}
	if offset > 0 {
		q.Set("offset", strconv.FormatInt(offset, 10))
	}
	var out contracts.SkillContentResponse
	if err := c.skillsRequest(ctx, http.MethodGet, "/api/v1/servers/"+url.PathEscape(serverName)+"/skills/content", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RefreshServerSkills runs POST /api/v1/servers/{id}/skills/refresh and
// returns the listing after the refresh.
func (c *Client) RefreshServerSkills(ctx context.Context, serverName string) (*contracts.SkillsListResponse, error) {
	var out contracts.SkillsListResponse
	if err := c.skillsRequest(ctx, http.MethodPost, "/api/v1/servers/"+url.PathEscape(serverName)+"/skills/refresh", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
