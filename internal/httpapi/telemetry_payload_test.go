package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// telemetryPayloadController is a minimal ServerController for testing
// /api/v1/telemetry/payload without wiring a full runtime.
type telemetryPayloadController struct {
	baseController
	apiKey string
}

func (m *telemetryPayloadController) GetCurrentConfig() any {
	return &config.Config{APIKey: m.apiKey}
}

// fakeRuntimeStats feeds deterministic values to the telemetry service so
// the rendered payload has non-zero runtime fields.
type fakeRuntimeStats struct{}

func (fakeRuntimeStats) GetServerCount() int               { return 7 }
func (fakeRuntimeStats) GetConnectedServerCount() int      { return 5 }
func (fakeRuntimeStats) GetToolCount() int                 { return 42 }
func (fakeRuntimeStats) GetRoutingMode() string            { return "retrieve_tools" }
func (fakeRuntimeStats) IsQuarantineEnabled() bool         { return true }
func (fakeRuntimeStats) IsDockerAvailable() bool           { return false }
func (fakeRuntimeStats) GetDockerIsolatedServerCount() int { return 0 }

func TestHandleGetTelemetryPayload_OK(t *testing.T) {
	logger := zap.NewNop().Sugar()
	ctrl := &telemetryPayloadController{apiKey: "test-key"}
	srv := NewServer(ctrl, logger, nil)

	cfg := &config.Config{APIKey: "test-key"}
	svc := telemetry.New(cfg, "", "v0.0.0-test", "personal", zap.NewNop())
	svc.SetRuntimeStats(fakeRuntimeStats{})

	srv.SetTelemetryPayloadProvider(func() *telemetry.Service { return svc })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/telemetry/payload", nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var resp struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.True(t, resp.Success)
	require.NotNil(t, resp.Data)

	// Spec 042 fields: runtime stats must be populated from the attached
	// RuntimeStats, not zero values from an offline service.
	assert.Equal(t, float64(7), resp.Data["server_count"])
	assert.Equal(t, float64(5), resp.Data["connected_server_count"])
	assert.Equal(t, float64(42), resp.Data["tool_count"])
	assert.Equal(t, "retrieve_tools", resp.Data["routing_mode"])
	assert.Equal(t, true, resp.Data["quarantine_enabled"])
	assert.Equal(t, "personal", resp.Data["edition"])
	assert.Equal(t, "v0.0.0-test", resp.Data["version"])
}

func TestHandleGetTelemetryPayload_NoProvider(t *testing.T) {
	logger := zap.NewNop().Sugar()
	ctrl := &telemetryPayloadController{apiKey: "test-key"}
	srv := NewServer(ctrl, logger, nil)
	// Do not call SetTelemetryPayloadProvider — simulates SetTelemetry
	// never having been called in the daemon.

	req := httptest.NewRequest(http.MethodGet, "/api/v1/telemetry/payload", nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code, "body=%s", w.Body.String())

	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Error, "telemetry service unavailable")
}

func TestHandleGetTelemetryPayload_NilProvider(t *testing.T) {
	logger := zap.NewNop().Sugar()
	ctrl := &telemetryPayloadController{apiKey: "test-key"}
	srv := NewServer(ctrl, logger, nil)
	// Provider returns nil — simulates race where telemetry service is
	// briefly unavailable between SetTelemetry and runtime being ready.
	srv.SetTelemetryPayloadProvider(func() *telemetry.Service { return nil })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/telemetry/payload", nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}
