package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sync"
	"time"
)

// FeedbackRequest is the user-submitted feedback payload.
type FeedbackRequest struct {
	Category string          `json:"category"` // bug, feature, other
	Message  string          `json:"message"`
	Email    string          `json:"email,omitempty"`
	Context  FeedbackContext `json:"context"`
}

// FeedbackContext provides automatic system context alongside feedback.
type FeedbackContext struct {
	Version              string `json:"version"`
	Edition              string `json:"edition"`
	OS                   string `json:"os"`
	Arch                 string `json:"arch"`
	ServerCount          int    `json:"server_count"`
	ConnectedServerCount int    `json:"connected_server_count"`
	RoutingMode          string `json:"routing_mode"`
}

// FeedbackResponse is the response from the telemetry backend.
type FeedbackResponse struct {
	Success  bool   `json:"success"`
	IssueURL string `json:"issue_url,omitempty"`
	Error    string `json:"error,omitempty"`
}

// RateLimiter enforces a maximum number of requests per hour.
type RateLimiter struct {
	mu         sync.Mutex
	timestamps []time.Time
	maxPerHour int
}

// NewRateLimiter creates a rate limiter with the given max requests per hour.
func NewRateLimiter(maxPerHour int) *RateLimiter {
	return &RateLimiter{
		maxPerHour: maxPerHour,
	}
}

// Allow returns true if the request is within the rate limit.
func (rl *RateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-1 * time.Hour)

	// Prune old timestamps
	valid := rl.timestamps[:0]
	for _, ts := range rl.timestamps {
		if ts.After(cutoff) {
			valid = append(valid, ts)
		}
	}
	rl.timestamps = valid

	if len(rl.timestamps) >= rl.maxPerHour {
		return false
	}

	rl.timestamps = append(rl.timestamps, now)
	return true
}

// ValidateCategory checks if the feedback category is valid.
func ValidateCategory(category string) bool {
	switch category {
	case "bug", "feature", "other":
		return true
	}
	return false
}

// ValidateMessage checks if the feedback message meets length requirements.
func ValidateMessage(message string) error {
	if len(message) < 10 {
		return fmt.Errorf("message must be at least 10 characters (got %d)", len(message))
	}
	if len(message) > 5000 {
		return fmt.Errorf("message must be at most 5000 characters (got %d)", len(message))
	}
	return nil
}

// SubmitFeedback sends feedback to the telemetry backend.
func (s *Service) SubmitFeedback(ctx context.Context, req *FeedbackRequest) (*FeedbackResponse, error) {
	if s.feedbackLimiter == nil {
		s.feedbackLimiter = NewRateLimiter(5)
	}

	// Check rate limit
	if !s.feedbackLimiter.Allow() {
		return nil, fmt.Errorf("rate limit exceeded: maximum 5 feedback submissions per hour")
	}

	// Validate inputs
	if !ValidateCategory(req.Category) {
		return nil, fmt.Errorf("invalid category %q: must be bug, feature, or other", req.Category)
	}
	if err := ValidateMessage(req.Message); err != nil {
		return nil, fmt.Errorf("invalid message: %w", err)
	}

	// Auto-populate context
	req.Context.Version = s.version
	req.Context.Edition = s.edition
	req.Context.OS = runtime.GOOS
	req.Context.Arch = runtime.GOARCH
	if s.stats != nil {
		req.Context.ServerCount = s.stats.GetServerCount()
		req.Context.ConnectedServerCount = s.stats.GetConnectedServerCount()
		req.Context.RoutingMode = s.stats.GetRoutingMode()
	}

	// Marshal and send
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal feedback: %w", err)
	}

	url := s.endpoint + "/feedback"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create feedback request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send feedback: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB max
	if err != nil {
		return nil, fmt.Errorf("failed to read feedback response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("feedback submission failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var result FeedbackResponse
	if err := json.Unmarshal(body, &result); err != nil {
		// If we can't parse the response, still report success if HTTP status was OK
		return &FeedbackResponse{Success: resp.StatusCode < 300}, nil
	}

	return &result, nil
}
