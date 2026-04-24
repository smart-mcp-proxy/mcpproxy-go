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
	serverCount           int
	connectedCount        int
	toolCount             int
	routingMode           string
	quarantine            bool
	dockerAvailable       bool
	dockerIsolatedServers int
}

func (m *mockRuntimeStats) GetServerCount() int          { return m.serverCount }
func (m *mockRuntimeStats) GetConnectedServerCount() int { return m.connectedCount }
func (m *mockRuntimeStats) GetToolCount() int            { return m.toolCount }
func (m *mockRuntimeStats) GetRoutingMode() string       { return m.routingMode }
func (m *mockRuntimeStats) IsQuarantineEnabled() bool    { return m.quarantine }
func (m *mockRuntimeStats) IsDockerAvailable() bool      { return m.dockerAvailable }
func (m *mockRuntimeStats) GetDockerIsolatedServerCount() int {
	return m.dockerIsolatedServers
}

func TestHeartbeatSend(t *testing.T) {
	// Clear env vars that would disable telemetry (GitHub Actions sets CI=true).
	t.Setenv("CI", "")
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")

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

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"already prefixed", "v0.24.2", "v0.24.2"},
		{"missing prefix added", "0.22.0", "v0.22.0"},
		{"invalid unchanged", "dev", "dev"},
		{"empty unchanged", "", ""},
		{"prerelease already prefixed", "v1.2.3-rc.1", "v1.2.3-rc.1"},
		{"prerelease missing prefix added", "1.2.3-rc.1", "v1.2.3-rc.1"},
		{"garbage unchanged", "development", "development"},
		{"latest unchanged", "latest", "latest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeVersion(tt.input)
			if got != tt.want {
				t.Errorf("normalizeVersion(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestNewNormalizesVersion verifies that the New constructor applies
// normalizeVersion to s.version so downstream payloads never see a
// bare "0.22.0" form.
func TestNewNormalizesVersion(t *testing.T) {
	cfg := &config.Config{}
	logger := zap.NewNop()

	svc := New(cfg, "", "0.22.0", "personal", logger)
	if svc.version != "v0.22.0" {
		t.Errorf("Expected s.version=v0.22.0 after normalization, got %q", svc.version)
	}

	svc2 := New(cfg, "", "v0.22.0", "personal", logger)
	if svc2.version != "v0.22.0" {
		t.Errorf("Expected prefixed version to remain v0.22.0, got %q", svc2.version)
	}

	svc3 := New(cfg, "", "dev", "personal", logger)
	if svc3.version != "dev" {
		t.Errorf("Expected invalid 'dev' to remain unchanged, got %q", svc3.version)
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

// TestSchemaVersionV3 verifies that HeartbeatPayload carries schema_version=3
// once the v3 fields ship. This is a tripwire against accidental downgrades.
func TestSchemaVersionV3(t *testing.T) {
	if SchemaVersion != 3 {
		t.Fatalf("SchemaVersion = %d, want 3", SchemaVersion)
	}

	cfg := &config.Config{}
	svc := New(cfg, "", "v1.0.0", "personal", zap.NewNop())
	svc.SetRuntimeStats(&mockRuntimeStats{})
	payload := svc.BuildPayload()
	if payload.SchemaVersion != 3 {
		t.Errorf("payload.SchemaVersion = %d, want 3", payload.SchemaVersion)
	}
}

// TestV3PayloadDockerAvailable verifies that the telemetry service forwards
// the runtime IsDockerAvailable() probe into feature_flags.docker_available.
func TestV3PayloadDockerAvailable(t *testing.T) {
	cfg := &config.Config{}

	// Docker NOT available on host.
	svc := New(cfg, "", "v1.0.0", "personal", zap.NewNop())
	svc.SetRuntimeStats(&mockRuntimeStats{dockerAvailable: false})
	p1 := svc.BuildPayload()
	if p1.FeatureFlags == nil {
		t.Fatal("FeatureFlags nil")
	}
	if p1.FeatureFlags.DockerAvailable {
		t.Error("DockerAvailable should be false when runtime reports false")
	}

	// Docker available on host.
	svc2 := New(cfg, "", "v1.0.0", "personal", zap.NewNop())
	svc2.SetRuntimeStats(&mockRuntimeStats{dockerAvailable: true})
	p2 := svc2.BuildPayload()
	if p2.FeatureFlags == nil {
		t.Fatal("FeatureFlags nil")
	}
	if !p2.FeatureFlags.DockerAvailable {
		t.Error("DockerAvailable should be true when runtime reports true")
	}
}

// TestV3PayloadServerProtocolCounts verifies that the payload carries
// per-protocol counts for the configured servers.
func TestV3PayloadServerProtocolCounts(t *testing.T) {
	cfg := &config.Config{
		Servers: []*config.ServerConfig{
			{Name: "a", Protocol: "stdio"},
			{Name: "b", Protocol: "http"},
			{Name: "c", Protocol: "streamable-http"},
			{Name: "d", Protocol: ""},
		},
	}
	svc := New(cfg, "", "v1.0.0", "personal", zap.NewNop())
	svc.SetRuntimeStats(&mockRuntimeStats{})
	p := svc.BuildPayload()
	if p.ServerProtocolCounts == nil {
		t.Fatal("ServerProtocolCounts nil")
	}
	if p.ServerProtocolCounts["stdio"] != 1 {
		t.Errorf("stdio = %d, want 1", p.ServerProtocolCounts["stdio"])
	}
	if p.ServerProtocolCounts["http"] != 1 {
		t.Errorf("http = %d, want 1", p.ServerProtocolCounts["http"])
	}
	if p.ServerProtocolCounts["streamable_http"] != 1 {
		t.Errorf("streamable_http = %d, want 1", p.ServerProtocolCounts["streamable_http"])
	}
	if p.ServerProtocolCounts["auto"] != 1 {
		t.Errorf("auto = %d, want 1 (missing protocol should bucket to auto)", p.ServerProtocolCounts["auto"])
	}
}

// TestV3PayloadDockerIsolatedCount verifies that the payload forwards the
// runtime's count of servers wrapped in Docker isolation.
func TestV3PayloadDockerIsolatedCount(t *testing.T) {
	cfg := &config.Config{}
	svc := New(cfg, "", "v1.0.0", "personal", zap.NewNop())
	svc.SetRuntimeStats(&mockRuntimeStats{dockerIsolatedServers: 4})
	p := svc.BuildPayload()
	if p.ServerDockerIsolatedCount != 4 {
		t.Errorf("ServerDockerIsolatedCount = %d, want 4", p.ServerDockerIsolatedCount)
	}
}

func TestMultipleHeartbeats(t *testing.T) {
	// Clear env vars that would disable telemetry (GitHub Actions sets CI=true).
	t.Setenv("CI", "")
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")

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

// TestAnonymousIDStable_V2ToV3 (Spec 044 FR-017 / T023) asserts that moving
// from a v2 payload (no env_kind / env_markers) to a v3 payload preserves the
// exact anonymous_id byte string for the same fixture. If anonymous_id ever
// changes due to a payload-builder refactor, the telemetry retention cohort
// shifts silently and the dashboard can't join pre-044 rows to post-044 rows.
func TestAnonymousIDStable_V2ToV3(t *testing.T) {
	fixedID := "550e8400-e29b-41d4-a716-446655440000"
	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{
			AnonymousID:          fixedID,
			AnonymousIDCreatedAt: "2026-04-10T12:00:00Z",
		},
	}
	svc := New(cfg, "", "v1.2.3", "personal", zap.NewNop())
	svc.SetRuntimeStats(&mockRuntimeStats{})

	// Build twice; each build goes through maybeRotateAnonymousID. The ID
	// must be byte-identical across builds and match the fixture input.
	p1 := svc.BuildPayload()
	p2 := svc.BuildPayload()
	if p1.AnonymousID != fixedID {
		t.Errorf("v3 payload anonymous_id=%q, want %q (FR-017 byte-identical)", p1.AnonymousID, fixedID)
	}
	if p1.AnonymousID != p2.AnonymousID {
		t.Errorf("anonymous_id drifted between builds: %q vs %q", p1.AnonymousID, p2.AnonymousID)
	}
	// SchemaVersion must be 3 (spec 044 does not re-bump).
	if p1.SchemaVersion != 3 {
		t.Errorf("schema_version = %d, want 3 (spec 044 does NOT re-bump)", p1.SchemaVersion)
	}
}
