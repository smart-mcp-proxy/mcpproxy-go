package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func TestValidateCategory(t *testing.T) {
	tests := []struct {
		category string
		want     bool
	}{
		{"bug", true},
		{"feature", true},
		{"other", true},
		{"invalid", false},
		{"", false},
		{"Bug", false}, // case-sensitive
	}

	for _, tt := range tests {
		t.Run(tt.category, func(t *testing.T) {
			got := ValidateCategory(tt.category)
			if got != tt.want {
				t.Errorf("ValidateCategory(%q) = %v, want %v", tt.category, got, tt.want)
			}
		})
	}
}

func TestValidateMessage(t *testing.T) {
	tests := []struct {
		name    string
		message string
		wantErr bool
	}{
		{"valid", "This is a valid feedback message with enough characters", false},
		{"min length", "0123456789", false},
		{"too short", "short", true},
		{"empty", "", true},
		{"just under min", "123456789", true}, // 9 chars
		{"max length", string(make([]byte, 5000)), false},
		{"over max", string(make([]byte, 5001)), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMessage(tt.message)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateMessage() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRateLimiter(t *testing.T) {
	rl := NewRateLimiter(3)

	// Should allow first 3 requests
	for i := 0; i < 3; i++ {
		if !rl.Allow() {
			t.Errorf("Request %d should be allowed", i+1)
		}
	}

	// 4th request should be denied
	if rl.Allow() {
		t.Error("4th request should be denied")
	}
}

func TestRateLimiterExpiry(t *testing.T) {
	rl := NewRateLimiter(2)

	// Fill the limiter
	rl.Allow()
	rl.Allow()

	// Should be denied
	if rl.Allow() {
		t.Error("Should be denied when full")
	}

	// Manually age out the timestamps
	rl.mu.Lock()
	for i := range rl.timestamps {
		rl.timestamps[i] = time.Now().Add(-2 * time.Hour)
	}
	rl.mu.Unlock()

	// Should now be allowed (old entries expired)
	if !rl.Allow() {
		t.Error("Should be allowed after entries expire")
	}
}

func TestSubmitFeedbackSuccess(t *testing.T) {
	var receivedReq FeedbackRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/feedback" && r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&receivedReq); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(FeedbackResponse{
				Success:  true,
				IssueURL: "https://github.com/org/repo/issues/123",
			})
		}
	}))
	defer server.Close()

	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{
			Endpoint: server.URL,
		},
	}

	logger := zap.NewNop()
	svc := New(cfg, "", "v1.0.0", "personal", logger)
	svc.SetRuntimeStats(&mockRuntimeStats{
		serverCount:    3,
		connectedCount: 2,
		routingMode:    "retrieve_tools",
	})

	resp, err := svc.SubmitFeedback(context.Background(), &FeedbackRequest{
		Category: "bug",
		Message:  "Something is broken and I want to report it",
		Email:    "test@example.com",
	})

	if err != nil {
		t.Fatalf("SubmitFeedback failed: %v", err)
	}

	if !resp.Success {
		t.Error("Expected successful response")
	}
	if resp.IssueURL != "https://github.com/org/repo/issues/123" {
		t.Errorf("Expected issue URL, got %q", resp.IssueURL)
	}

	// Verify context was auto-populated
	if receivedReq.Context.Version != "v1.0.0" {
		t.Errorf("Expected version in context, got %q", receivedReq.Context.Version)
	}
	if receivedReq.Context.Edition != "personal" {
		t.Errorf("Expected edition in context, got %q", receivedReq.Context.Edition)
	}
	if receivedReq.Context.ServerCount != 3 {
		t.Errorf("Expected server_count=3, got %d", receivedReq.Context.ServerCount)
	}
}

func TestSubmitFeedbackValidation(t *testing.T) {
	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{
			Endpoint: "http://localhost:9999", // won't be called
		},
	}

	logger := zap.NewNop()
	svc := New(cfg, "", "v1.0.0", "personal", logger)

	// Invalid category
	_, err := svc.SubmitFeedback(context.Background(), &FeedbackRequest{
		Category: "invalid",
		Message:  "This is a valid message",
	})
	if err == nil {
		t.Error("Expected error for invalid category")
	}

	// Message too short
	_, err = svc.SubmitFeedback(context.Background(), &FeedbackRequest{
		Category: "bug",
		Message:  "short",
	})
	if err == nil {
		t.Error("Expected error for short message")
	}
}

func TestSubmitFeedbackRateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(FeedbackResponse{Success: true})
	}))
	defer server.Close()

	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{
			Endpoint: server.URL,
		},
	}

	logger := zap.NewNop()
	svc := New(cfg, "", "v1.0.0", "personal", logger)
	// Set a strict rate limit for testing
	svc.feedbackLimiter = NewRateLimiter(2)

	req := &FeedbackRequest{
		Category: "other",
		Message:  "This is a test feedback message for rate limiting",
	}

	// First 2 should succeed
	for i := 0; i < 2; i++ {
		_, err := svc.SubmitFeedback(context.Background(), req)
		if err != nil {
			t.Fatalf("Request %d should succeed: %v", i+1, err)
		}
	}

	// 3rd should be rate limited
	_, err := svc.SubmitFeedback(context.Background(), req)
	if err == nil {
		t.Fatal("Expected rate limit error")
	}
}

func TestSubmitFeedbackServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{
			Endpoint: server.URL,
		},
	}

	logger := zap.NewNop()
	svc := New(cfg, "", "v1.0.0", "personal", logger)

	_, err := svc.SubmitFeedback(context.Background(), &FeedbackRequest{
		Category: "bug",
		Message:  "This is a valid bug report message",
	})

	if err == nil {
		t.Fatal("Expected error for server failure")
	}
}
