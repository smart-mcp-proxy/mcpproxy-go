package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// mockRuntimeStats implements RuntimeStats for testing.
type mockRuntimeStats struct {
	serverCount    int
	connectedCount int
	toolCount      int
	routingMode    string
	quarantine     bool
}

func (m *mockRuntimeStats) GetServerCount() int          { return m.serverCount }
func (m *mockRuntimeStats) GetConnectedServerCount() int { return m.connectedCount }
func (m *mockRuntimeStats) GetToolCount() int            { return m.toolCount }
func (m *mockRuntimeStats) GetRoutingMode() string       { return m.routingMode }
func (m *mockRuntimeStats) IsQuarantineEnabled() bool    { return m.quarantine }

func TestHeartbeatSend(t *testing.T) {
	var received atomic.Int32
	var lastPayload HeartbeatPayload

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/heartbeat" && r.Method == http.MethodPost {
			var payload HeartbeatPayload
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("Failed to decode heartbeat: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			lastPayload = payload
			received.Add(1)
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{
			AnonymousID: "test-uuid-1234",
			Endpoint:    server.URL,
		},
		RoutingMode: "retrieve_tools",
	}

	logger := zap.NewNop()
	svc := New(cfg, "", "v1.0.0", "personal", logger)
	svc.initialDelay = 10 * time.Millisecond
	svc.heartbeatInterval = 50 * time.Millisecond
	svc.SetRuntimeStats(&mockRuntimeStats{
		serverCount:    5,
		connectedCount: 3,
		toolCount:      42,
		routingMode:    "retrieve_tools",
		quarantine:     true,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	svc.Start(ctx)

	if received.Load() < 1 {
		t.Fatal("Expected at least one heartbeat to be sent")
	}

	if lastPayload.AnonymousID != "test-uuid-1234" {
		t.Errorf("Expected anonymous_id=test-uuid-1234, got %s", lastPayload.AnonymousID)
	}
	if lastPayload.Version != "v1.0.0" {
		t.Errorf("Expected version=v1.0.0, got %s", lastPayload.Version)
	}
	if lastPayload.Edition != "personal" {
		t.Errorf("Expected edition=personal, got %s", lastPayload.Edition)
	}
	if lastPayload.ServerCount != 5 {
		t.Errorf("Expected server_count=5, got %d", lastPayload.ServerCount)
	}
	if lastPayload.ConnectedServerCount != 3 {
		t.Errorf("Expected connected_server_count=3, got %d", lastPayload.ConnectedServerCount)
	}
	if lastPayload.ToolCount != 42 {
		t.Errorf("Expected tool_count=42, got %d", lastPayload.ToolCount)
	}
	if lastPayload.QuarantineEnabled != true {
		t.Errorf("Expected quarantine_enabled=true, got %v", lastPayload.QuarantineEnabled)
	}
}

func TestSkipWhenDisabled(t *testing.T) {
	var received atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	disabled := false
	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{
			Enabled:  &disabled,
			Endpoint: server.URL,
		},
	}

	logger := zap.NewNop()
	svc := New(cfg, "", "v1.0.0", "personal", logger)
	svc.initialDelay = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	svc.Start(ctx)

	if received.Load() > 0 {
		t.Fatal("Expected no heartbeats when telemetry is disabled")
	}
}

func TestSkipDevBuild(t *testing.T) {
	var received atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{
			AnonymousID: "test-id",
			Endpoint:    server.URL,
		},
	}

	logger := zap.NewNop()
	svc := New(cfg, "", "development", "personal", logger)
	svc.initialDelay = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	svc.Start(ctx)

	if received.Load() > 0 {
		t.Fatal("Expected no heartbeats for non-semver (dev) version")
	}
}

func TestHTTPTimeout(t *testing.T) {
	// Use a non-routable address to trigger a fast connect timeout
	// rather than a slow server that blocks httptest.Close()
	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{
			AnonymousID: "test-id",
			Endpoint:    "http://192.0.2.1:1", // TEST-NET-1, non-routable
		},
	}

	logger := zap.NewNop()
	svc := New(cfg, "", "v1.0.0", "personal", logger)
	svc.client.Timeout = 100 * time.Millisecond
	svc.initialDelay = 10 * time.Millisecond
	svc.heartbeatInterval = 5 * time.Second // Long interval, we just test one request

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Should not panic or hang - the timeout fires and we move on
	svc.Start(ctx)
}

func TestIsValidSemver(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"v1.0.0", true},
		{"v0.21.0", true},
		{"v1.0.0-rc.1", true},
		{"1.0.0", true},
		{"development", false},
		{"", false},
		{"dev", false},
		{"latest", false},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			got := isValidSemver(tt.version)
			if got != tt.want {
				t.Errorf("isValidSemver(%q) = %v, want %v", tt.version, got, tt.want)
			}
		})
	}
}

func TestEnsureAnonymousID(t *testing.T) {
	cfg := &config.Config{}

	logger := zap.NewNop()
	svc := New(cfg, "", "v1.0.0", "personal", logger)

	// Should be empty before
	if cfg.GetAnonymousID() != "" {
		t.Fatal("Expected empty anonymous ID initially")
	}

	svc.ensureAnonymousID()

	// Should be populated after
	if cfg.GetAnonymousID() == "" {
		t.Fatal("Expected anonymous ID to be generated")
	}

	// Should be a valid UUID format
	id := cfg.GetAnonymousID()
	if len(id) < 32 {
		t.Errorf("Expected UUID-like ID, got %q", id)
	}

	// Second call should not change the ID
	firstID := id
	svc.ensureAnonymousID()
	if cfg.GetAnonymousID() != firstID {
		t.Error("Expected anonymous ID to remain stable on second call")
	}
}

func TestMultipleHeartbeats(t *testing.T) {
	var received atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/heartbeat" {
			received.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{
			AnonymousID: "test-id",
			Endpoint:    server.URL,
		},
	}

	logger := zap.NewNop()
	svc := New(cfg, "", "v1.0.0", "personal", logger)
	svc.initialDelay = 10 * time.Millisecond
	svc.heartbeatInterval = 30 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	svc.Start(ctx)

	count := received.Load()
	if count < 2 {
		t.Errorf("Expected at least 2 heartbeats, got %d", count)
	}
}
