package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/launch"
)

// Spec 092 FR-001a: /api/v1/info reports durable launch provenance so a newer
// tray can tell "a tray started this core" (safe to supersede) from
// "user-launched" (consent required) — including for cores started by a tray
// instance that no longer exists.
func TestInfoEndpointReportsLaunchedBy(t *testing.T) {
	tests := []struct {
		name      string
		provided  string
		wantField string
	}{
		{"tray-launched core", launch.ByTray, "tray"},
		{"installer-launched core", launch.ByInstaller, "installer"},
		{"user-launched core reports empty, not a guess", launch.ByUnknown, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prev := launchedByFn
			launchedByFn = func() string { return tt.provided }
			t.Cleanup(func() { launchedByFn = prev })

			logger := zaptest.NewLogger(t).Sugar()
			server := NewServer(&MockServerController{}, logger, nil)

			req := httptest.NewRequest("GET", "/api/v1/info", http.NoBody)
			w := httptest.NewRecorder()
			server.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)

			var response contracts.APIResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			data, ok := response.Data.(map[string]interface{})
			require.True(t, ok, "response data should be a map")

			// The key is always present (even when empty) so consumers can
			// distinguish a 092-aware core from an older one that omits it.
			require.Contains(t, data, "launched_by", "info response must always carry launched_by")
			assert.Equal(t, tt.wantField, data["launched_by"])
		})
	}
}

// The default wiring reads the process-wide capture from internal/launch; a
// core started without the marker must report "" rather than inventing a
// provenance.
func TestInfoEndpointLaunchedByDefaultsToProcessCapture(t *testing.T) {
	assert.Equal(t, launch.LaunchedBy(), launchedByFn(),
		"launchedByFn must default to the internal/launch process capture")
}

// Spec 092 FR-002: the core reports its own pid so an ATTACHED tray — which
// holds no Process handle and has no shutdown endpoint to call — still has a
// mechanism to stop a stale core once the user consents.
func TestInfoEndpointReportsPID(t *testing.T) {
	prev := pidFn
	pidFn = func() int { return 4242 }
	t.Cleanup(func() { pidFn = prev })

	logger := zaptest.NewLogger(t).Sugar()
	server := NewServer(&MockServerController{}, logger, nil)

	req := httptest.NewRequest("GET", "/api/v1/info", http.NoBody)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var response contracts.APIResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	data, ok := response.Data.(map[string]interface{})
	require.True(t, ok, "response data should be a map")

	require.Contains(t, data, "pid", "info response must always carry pid")
	// JSON numbers decode as float64 through interface{}.
	assert.InDelta(t, 4242, data["pid"], 0.0)
}

// The default wiring is the process's real pid — a tray that kills what this
// reports must be killing the core, not a constant.
func TestInfoEndpointPIDDefaultsToProcessID(t *testing.T) {
	assert.Equal(t, os.Getpid(), pidFn(),
		"pidFn must default to this process's pid")
}
