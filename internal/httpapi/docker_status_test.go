package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockDockerStatusController lets a test drive the genuine daemon probe and the
// configured docker_isolation.enabled flag independently, while keeping
// GetDockerRecoveryStatus returning the synthetic DockerAvailable:true that
// production returns when Docker recovery is disabled (isolation off). This
// proves /api/v1/docker/status reports the GENUINE probe value, not the
// synthetic recovery-state value (MCP-2478).
type mockDockerStatusController struct {
	baseController
	apiKey           string
	dockerAvailable  bool
	isolationEnabled bool
}

func (m *mockDockerStatusController) GetCurrentConfig() any {
	return &config.Config{APIKey: m.apiKey}
}

func (m *mockDockerStatusController) IsDockerAvailable() bool { return m.dockerAvailable }

func (m *mockDockerStatusController) GetDockerRecoveryStatus() *storage.DockerRecoveryState {
	// Mirror the production short-circuit: when recovery is disabled the manager
	// returns a synthetic DockerAvailable:true. The endpoint must NOT forward
	// this as genuine availability.
	return &storage.DockerRecoveryState{DockerAvailable: true}
}

func (m *mockDockerStatusController) GetConfig() (*config.Config, error) {
	return &config.Config{
		APIKey:          m.apiKey,
		DockerIsolation: &config.DockerIsolationConfig{Enabled: m.isolationEnabled},
	}, nil
}

func TestHandleGetDockerStatus_GenuineAvailability(t *testing.T) {
	logger := zap.NewNop().Sugar()
	const apiKey = "test-docker-status-key"

	tests := []struct {
		name             string
		dockerAvailable  bool
		isolationEnabled bool
		wantAvailable    bool
		wantIsolationOn  bool
	}{
		{
			// The reported bug: no Docker daemon + isolation disabled must NOT
			// report docker_available:true (which the synthetic recovery state
			// would).
			name:             "no docker daemon and isolation disabled",
			dockerAvailable:  false,
			isolationEnabled: false,
			wantAvailable:    false,
			wantIsolationOn:  false,
		},
		{
			name:             "docker present and isolation enabled",
			dockerAvailable:  true,
			isolationEnabled: true,
			wantAvailable:    true,
			wantIsolationOn:  true,
		},
		{
			name:             "docker present but isolation disabled",
			dockerAvailable:  true,
			isolationEnabled: false,
			wantAvailable:    true,
			wantIsolationOn:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := &mockDockerStatusController{
				apiKey:           apiKey,
				dockerAvailable:  tc.dockerAvailable,
				isolationEnabled: tc.isolationEnabled,
			}
			srv := NewServer(ctrl, logger, nil)

			req := httptest.NewRequest("GET", "/api/v1/docker/status?apikey="+apiKey, nil)
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

			var resp struct {
				Success bool `json:"success"`
				Data    struct {
					DockerAvailable  bool `json:"docker_available"`
					IsolationEnabled bool `json:"isolation_enabled"`
				} `json:"data"`
			}
			require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
			require.True(t, resp.Success)

			// docker_available must reflect the GENUINE probe, not the synthetic
			// DockerAvailable:true returned by GetDockerRecoveryStatus.
			assert.Equal(t, tc.wantAvailable, resp.Data.DockerAvailable,
				"docker_available should report genuine daemon availability")
			assert.Equal(t, tc.wantIsolationOn, resp.Data.IsolationEnabled,
				"isolation_enabled should report docker_isolation.enabled")
		})
	}
}
