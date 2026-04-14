package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func TestNewServiceDisabledByDoNotTrack(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "1")
	t.Setenv("CI", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")

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
	svc := New(cfg, "", "v1.0.0", "personal", zap.NewNop())
	svc.initialDelay = 5 * time.Millisecond

	if svc.EnvDisabledReason() != EnvDisabledByDoNotTrack {
		t.Errorf("expected env disabled reason DO_NOT_TRACK, got %q", svc.EnvDisabledReason())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	svc.Start(ctx)

	if received.Load() > 0 {
		t.Error("DO_NOT_TRACK should suppress all heartbeats")
	}
}

func TestHeartbeatPayloadV2Marshal(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CI", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")

	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{
			AnonymousID:          "fixed-id",
			AnonymousIDCreatedAt: time.Now().UTC().Format(time.RFC3339),
			LastReportedVersion:  "v1.2.0",
			LastStartupOutcome:   "success",
		},
		EnableSocket:        true,
		EnablePrompts:       false,
		Features:            &config.FeatureFlags{EnableWebUI: false},
		RequireMCPAuth:      true,
		EnableCodeExecution: false,
	}
	svc := New(cfg, "", "v1.2.3", "personal", zap.NewNop())
	svc.SetRuntimeStats(&mockRuntimeStats{
		serverCount:    3,
		connectedCount: 2,
		toolCount:      17,
		routingMode:    "dynamic",
		quarantine:     true,
	})

	// Exercise some counters.
	svc.Registry().RecordSurface(SurfaceCLI)
	svc.Registry().RecordSurface(SurfaceMCP)
	svc.Registry().RecordBuiltinTool("retrieve_tools")
	svc.Registry().RecordRESTRequest("GET", "/api/v1/status", "2xx")
	svc.Registry().RecordError(ErrCatOAuthRefreshFailed)
	for i := 0; i < 25; i++ {
		svc.Registry().RecordUpstreamTool()
	}

	payload := svc.BuildPayload()

	if payload.SchemaVersion != 3 {
		t.Errorf("schema_version = %d, want 3", payload.SchemaVersion)
	}
	if payload.AnonymousID != "fixed-id" {
		t.Errorf("anonymous_id = %q", payload.AnonymousID)
	}
	if payload.PreviousVersion != "v1.2.0" {
		t.Errorf("previous_version = %q, want v1.2.0", payload.PreviousVersion)
	}
	if payload.CurrentVersion != "v1.2.3" {
		t.Errorf("current_version = %q, want v1.2.3", payload.CurrentVersion)
	}
	if payload.LastStartupOutcome != "success" {
		t.Errorf("last_startup_outcome = %q", payload.LastStartupOutcome)
	}
	if payload.SurfaceRequests["cli"] != 1 {
		t.Errorf("surface cli = %d", payload.SurfaceRequests["cli"])
	}
	if payload.SurfaceRequests["mcp"] != 1 {
		t.Errorf("surface mcp = %d", payload.SurfaceRequests["mcp"])
	}
	if payload.BuiltinToolCalls["retrieve_tools"] != 1 {
		t.Errorf("builtin retrieve_tools = %d", payload.BuiltinToolCalls["retrieve_tools"])
	}
	if payload.UpstreamToolCallCountBucket != "11-100" {
		t.Errorf("upstream bucket = %q, want 11-100", payload.UpstreamToolCallCountBucket)
	}
	if payload.RESTEndpointCalls["GET /api/v1/status"]["2xx"] != 1 {
		t.Errorf("REST endpoint counter wrong")
	}
	if payload.ErrorCategoryCounts["oauth_refresh_failed"] != 1 {
		t.Errorf("error category counter wrong")
	}
	if payload.FeatureFlags == nil {
		t.Fatal("feature_flags is nil")
	}
	if !payload.FeatureFlags.EnableSocket || !payload.FeatureFlags.RequireMCPAuth {
		t.Errorf("feature flags did not propagate: %+v", payload.FeatureFlags)
	}

	// Marshal and verify the JSON contains all expected top-level keys.
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	js := string(data)
	for _, key := range []string{
		`"schema_version":3`,
		`"surface_requests"`,
		`"builtin_tool_calls"`,
		`"upstream_tool_call_count_bucket":"11-100"`,
		`"rest_endpoint_calls"`,
		`"feature_flags"`,
		`"error_category_counts"`,
		`"previous_version":"v1.2.0"`,
		`"current_version":"v1.2.3"`,
		`"anonymous_id_created_at"`,
	} {
		if !strings.Contains(js, key) {
			t.Errorf("expected JSON to contain %s\nfull payload: %s", key, js)
		}
	}
}

func TestUpgradeFunnelAdvancesOnSuccess(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CI", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{
			AnonymousID:         "test-id",
			Endpoint:            server.URL,
			LastReportedVersion: "v1.0.0",
		},
	}
	svc := New(cfg, "", "v1.0.5", "personal", zap.NewNop())
	svc.initialDelay = 5 * time.Millisecond
	svc.heartbeatInterval = 5 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	svc.Start(ctx)

	if cfg.Telemetry.LastReportedVersion != "v1.0.5" {
		t.Errorf("LastReportedVersion = %q, want v1.0.5", cfg.Telemetry.LastReportedVersion)
	}
}

func TestUpgradeFunnelDoesNotAdvanceOnFailure(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CI", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{
			AnonymousID:         "test-id",
			Endpoint:            server.URL,
			LastReportedVersion: "v1.0.0",
		},
	}
	svc := New(cfg, "", "v1.0.5", "personal", zap.NewNop())
	svc.initialDelay = 5 * time.Millisecond
	svc.heartbeatInterval = 5 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	svc.Start(ctx)

	if cfg.Telemetry.LastReportedVersion != "v1.0.0" {
		t.Errorf("LastReportedVersion changed despite 500: %q", cfg.Telemetry.LastReportedVersion)
	}
}

func TestCountersResetOnSuccessfulSend(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CI", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{
			AnonymousID: "test-id",
			Endpoint:    server.URL,
		},
	}
	svc := New(cfg, "", "v1.0.0", "personal", zap.NewNop())
	svc.initialDelay = 5 * time.Millisecond
	svc.heartbeatInterval = 5 * time.Second

	svc.Registry().RecordSurface(SurfaceCLI)
	svc.Registry().RecordBuiltinTool("retrieve_tools")
	svc.Registry().RecordError(ErrCatOAuthRefreshFailed)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	svc.Start(ctx)

	snap := svc.Registry().Snapshot()
	if snap.SurfaceCounts["cli"] != 0 {
		t.Errorf("counters not reset: cli = %d", snap.SurfaceCounts["cli"])
	}
	if len(snap.BuiltinToolCalls) != 0 {
		t.Errorf("counters not reset: builtin = %v", snap.BuiltinToolCalls)
	}
	if len(snap.ErrorCategoryCounts) != 0 {
		t.Errorf("counters not reset: errors = %v", snap.ErrorCategoryCounts)
	}
}

func TestCountersNotResetOnFailedSend(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CI", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{
			AnonymousID: "test-id",
			Endpoint:    server.URL,
		},
	}
	svc := New(cfg, "", "v1.0.0", "personal", zap.NewNop())
	svc.initialDelay = 5 * time.Millisecond
	svc.heartbeatInterval = 5 * time.Second

	svc.Registry().RecordSurface(SurfaceCLI)
	svc.Registry().RecordSurface(SurfaceCLI)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	svc.Start(ctx)

	snap := svc.Registry().Snapshot()
	if snap.SurfaceCounts["cli"] != 2 {
		t.Errorf("counters reset despite failure: cli = %d, want 2", snap.SurfaceCounts["cli"])
	}
}
