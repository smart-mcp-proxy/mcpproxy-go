package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
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
func (fakeRuntimeStats) GetDockerCLISource() string        { return "absent" }

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

// TestHandleGetTelemetryPayload_RendersV7Fields (Spec 080 FR-019) asserts
// the payload endpoint — the data source for `mcpproxy telemetry
// show-payload`, which prints this JSON verbatim — renders every v7 field
// when populated, so users can inspect exactly what would be sent.
func TestHandleGetTelemetryPayload_RendersV7Fields(t *testing.T) {
	logger := zap.NewNop().Sugar()
	ctrl := &telemetryPayloadController{apiKey: "test-key"}
	srv := NewServer(ctrl, logger, nil)

	cfg := &config.Config{APIKey: "test-key"}
	svc := telemetry.New(cfg, "", "v0.0.0-test", "personal", zap.NewNop())
	svc.SetRuntimeStats(fakeRuntimeStats{})

	db, err := bbolt.Open(filepath.Join(t.TempDir(), "v7.db"), 0o600, &bbolt.Options{Timeout: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	funnel := telemetry.NewFunnelStore()
	require.NoError(t, funnel.IncrementWebUIOpened(db))
	svc.SetFunnelStore(funnel, db)

	prechurn := telemetry.NewPreChurnStore()
	require.NoError(t, prechurn.RecordLastErrorCode(db, "MCPX_DOCKER_CLI_NOT_FOUND"))
	svc.SetPreChurn(telemetry.PreviousShutdownClean, prechurn, db)

	svc.SetOnboardingProvider(func() *telemetry.OnboardingSnapshot {
		return &telemetry.OnboardingSnapshot{
			WizardShown:       true,
			WizardConnectStep: "completed_external",
		}
	})

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
	require.True(t, resp.Success)
	require.NotNil(t, resp.Data)

	assert.Equal(t, float64(7), resp.Data["schema_version"])
	assert.Equal(t, true, resp.Data["wizard_shown"])
	assert.Equal(t, "completed_external", resp.Data["wizard_connect_step"])
	assert.Equal(t, float64(1), resp.Data["web_ui_opened"])
	assert.Equal(t, float64(0), resp.Data["days_since_install"])
	assert.Equal(t, float64(1), resp.Data["active_days_30d"])
	assert.Equal(t, "clean", resp.Data["previous_shutdown"])
	assert.Equal(t, "MCPX_DOCKER_CLI_NOT_FOUND", resp.Data["last_error_code"])
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
