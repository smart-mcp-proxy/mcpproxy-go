package cliclient

import (
	"bytes"
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
func (c *Client) skillsRequest(ctx context.Context, method, path string, query url.Values, in, out interface{}) error {
	u := c.baseURL + path
	if encoded := query.Encode(); encoded != "" {
		u += "?" + encoded
	}
	var reqBody io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("failed to encode request: %w", err)
		}
		reqBody = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
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
		Success    bool            `json:"success"`
		Data       json.RawMessage `json:"data"`
		Error      string          `json:"error"`
		ErrorCode  string          `json:"error_code"`
		RequestID  string          `json:"request_id"`
		Changed    []string        `json:"changed"`
		Unreviewed []string        `json:"unreviewed"`
		Skills     []string        `json:"skills"`
	}
	jsonErr := json.Unmarshal(body, &env)
	if resp.StatusCode != http.StatusOK || (jsonErr == nil && !env.Success) {
		if jsonErr == nil && env.Error != "" {
			apiErr := &APIError{Message: env.Error, RequestID: env.RequestID}
			switch {
			case resp.StatusCode == http.StatusConflict && env.ErrorCode == "review_out_of_date":
				return &SkillsStaleReviewError{APIError: apiErr, Changed: env.Changed, Unreviewed: env.Unreviewed}
			case resp.StatusCode == http.StatusUnprocessableEntity && env.ErrorCode == "skill_unusable":
				return &SkillsUnusableError{APIError: apiErr, Skills: env.Skills}
			}
			return apiErr
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
	if err := c.skillsRequest(ctx, http.MethodGet, "/api/v1/servers/"+url.PathEscape(serverName)+"/skills", nil, nil, &out); err != nil {
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
	if err := c.skillsRequest(ctx, http.MethodGet, "/api/v1/servers/"+url.PathEscape(serverName)+"/skills/content", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RefreshServerSkills runs POST /api/v1/servers/{id}/skills/refresh and
// returns the listing after the refresh.
func (c *Client) RefreshServerSkills(ctx context.Context, serverName string) (*contracts.SkillsListResponse, error) {
	var out contracts.SkillsListResponse
	if err := c.skillsRequest(ctx, http.MethodPost, "/api/v1/servers/"+url.PathEscape(serverName)+"/skills/refresh", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SkillsStaleReviewError is the 409 review_out_of_date refusal of an approval:
// the content changed after the review, or a skill had no reviewed hash.
// Nothing was approved.
type SkillsStaleReviewError struct {
	*APIError
	Changed    []string
	Unreviewed []string
}

// Unwrap lets errors.As find the *APIError.
func (e *SkillsStaleReviewError) Unwrap() error { return e.APIError }

// SkillsUnusableError is the 422 skill_unusable refusal of an approval.
type SkillsUnusableError struct {
	*APIError
	Skills []string
}

// Unwrap lets errors.As find the *APIError.
func (e *SkillsUnusableError) Unwrap() error { return e.APIError }

// ApproveServerSkills runs POST /api/v1/servers/{id}/skills/approve.
func (c *Client) ApproveServerSkills(ctx context.Context, serverName string, req contracts.SkillsApproveRequest) (*contracts.SkillsApproveResponse, error) {
	var out contracts.SkillsApproveResponse
	if err := c.skillsRequest(ctx, http.MethodPost, "/api/v1/servers/"+url.PathEscape(serverName)+"/skills/approve", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetServerSkillsBlocked runs POST /api/v1/servers/{id}/skills/block (blocked
// true) or .../unblock.
func (c *Client) SetServerSkillsBlocked(ctx context.Context, serverName string, blocked bool, req contracts.SkillsBlockRequest) (*contracts.SkillsBlockResponse, error) {
	route := "unblock"
	if blocked {
		route = "block"
	}
	var out contracts.SkillsBlockResponse
	if err := c.skillsRequest(ctx, http.MethodPost, "/api/v1/servers/"+url.PathEscape(serverName)+"/skills/"+route, nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
