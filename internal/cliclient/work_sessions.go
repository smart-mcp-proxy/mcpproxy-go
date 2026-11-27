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

// Spec 124 PR-2: client calls of the work-session doors
// (GET /api/v1/work-sessions, /{id}, /{id}/timeline).

// WorkSessionListParams are the filters of the list door. A zero field is not
// sent, so the door's own default applies.
type WorkSessionListParams struct {
	// Failed keeps only sessions with at least one failure.
	Failed bool
	// IncludeBlocked, with Failed, says whether a policy block counts as a
	// failure. Nil leaves the door's default (it does).
	IncludeBlocked *bool
	Token          string
	Client         string
	Profile        string
	Run            string
	ErrorClass     string
	// Since and Until are RFC3339 times.
	Since string
	Until string
	// Sort is "last_activity" or "last_failure".
	Sort   string
	Limit  int
	Offset int
}

func (p WorkSessionListParams) query() url.Values {
	q := url.Values{}
	if p.Failed {
		q.Set("failed", "true")
	}
	if p.IncludeBlocked != nil {
		q.Set("include_blocked", strconv.FormatBool(*p.IncludeBlocked))
	}
	for key, value := range map[string]string{
		"token": p.Token, "client": p.Client, "profile": p.Profile, "run": p.Run,
		"error_class": p.ErrorClass, "since": p.Since, "until": p.Until, "sort": p.Sort,
	} {
		if value != "" {
			q.Set(key, value)
		}
	}
	// A value the door refuses is sent, not dropped: the refusal is the
	// caller's answer, and a default page in its place would hide the mistake.
	if p.Limit != 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	if p.Offset != 0 {
		q.Set("offset", strconv.Itoa(p.Offset))
	}
	return q
}

// WorkSessionTimelineParams position one page of a session timeline.
type WorkSessionTimelineParams struct {
	// At is "first_failure", "failure:<n>", "row:<activity id>" or "end".
	// Empty leaves the door's default, the first failure.
	At string
	// Before and After are the rows around the position. Nil leaves the
	// door's default; a pointer to zero asks for none.
	Before        *int
	After         *int
	IncludeBodies bool
}

func (p WorkSessionTimelineParams) query() url.Values {
	q := url.Values{}
	if p.At != "" {
		q.Set("at", p.At)
	}
	if p.Before != nil {
		q.Set("before", strconv.Itoa(*p.Before))
	}
	if p.After != nil {
		q.Set("after", strconv.Itoa(*p.After))
	}
	if p.IncludeBodies {
		q.Set("include_bodies", "true")
	}
	return q
}

// workSessionBodyPreview caps how much of a body that is not the JSON
// envelope is quoted in an error.
const workSessionBodyPreview = 300

// WorkSessionNotFoundError is the door's answer for an id that matches no
// session the caller may read. The door gives the same answer for a session
// that does not exist and for one that belongs to someone else. Another 404
// (a reverse proxy's, or a daemon that has no such route) is not this error.
type WorkSessionNotFoundError struct {
	*APIError
	ID string
}

func (e *WorkSessionNotFoundError) Error() string {
	return fmt.Sprintf("work session %q not found", e.ID)
}

// Unwrap keeps the daemon's own text and request id reachable.
func (e *WorkSessionNotFoundError) Unwrap() error { return e.APIError }

// workSessionGet sends one GET to a work-session door and decodes the success
// envelope's data into out. id is the session the path names, or empty for
// the list.
func (c *Client) workSessionGet(ctx context.Context, path string, query url.Values, id string, out interface{}) error {
	u := c.baseURL + path
	if encoded := query.Encode(); encoded != "" {
		u += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	c.prepareRequest(ctx, req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to call work sessions API: %w", err)
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
	if resp.StatusCode != http.StatusOK || jsonErr != nil || !env.Success {
		if jsonErr != nil || env.Error == "" {
			preview := string(body)
			if len(preview) > workSessionBodyPreview {
				preview = preview[:workSessionBodyPreview] + "..."
			}
			return fmt.Errorf("API returned status %d: %s", resp.StatusCode, preview)
		}
		apiErr := &APIError{Message: env.Error, RequestID: env.RequestID}
		if resp.StatusCode == http.StatusNotFound && id != "" && env.Error == contracts.WorkSessionNotFoundMessage {
			return &WorkSessionNotFoundError{APIError: apiErr, ID: id}
		}
		return apiErr
	}
	// A success without a data object is not an empty result.
	if data := bytes.TrimSpace(env.Data); len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return fmt.Errorf("API returned status %d with no data", resp.StatusCode)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	return nil
}

// ListWorkSessions lists work sessions with their failure facts.
func (c *Client) ListWorkSessions(ctx context.Context, params WorkSessionListParams) (*contracts.WorkSessionListResponse, error) {
	var out contracts.WorkSessionListResponse
	if err := c.workSessionGet(ctx, "/api/v1/work-sessions", params.query(), "", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetWorkSession returns one work session.
func (c *Client) GetWorkSession(ctx context.Context, id string) (*contracts.WorkSession, error) {
	var out contracts.WorkSession
	if err := c.workSessionGet(ctx, "/api/v1/work-sessions/"+url.PathEscape(id), nil, id, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetWorkSessionTimeline returns one page of a session's timeline.
func (c *Client) GetWorkSessionTimeline(ctx context.Context, id string, params WorkSessionTimelineParams) (*contracts.WorkSessionTimelineResponse, error) {
	var out contracts.WorkSessionTimelineResponse
	if err := c.workSessionGet(ctx, "/api/v1/work-sessions/"+url.PathEscape(id)+"/timeline", params.query(), id, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
