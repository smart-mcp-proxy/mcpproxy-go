package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockRoutingController is a mock controller for routing endpoint tests
type mockRoutingController struct {
	baseController
	apiKey      string
	routingMode string
	// Serialization axes (Spec 085 / Spec 102) as they stand in the LIVE
	// config — both hot-reloadable, unlike routingMode.
	toolResponseMode       string
	directToolResponseMode string
	codeExecutionEnabled   bool
	// desiredRoutingMode is the routing_mode as PERSISTED: a restart-gated
	// change lands on disk while the running process keeps serving the old one
	// (ApplyConfig's restart contract). Empty means "same as live".
	desiredRoutingMode string
	// servedMode is what /mcp actually bound at startup. Empty means the
	// controller does not record one (stdio transport, older core).
	servedMode string
	// noDesired makes the controller behave like one that predates
	// GetDesiredConfig, so the handler's fallback is exercised.
	noDesired bool
}

func (m *mockRoutingController) ServedRoutingMode() string { return m.servedMode }

func (m *mockRoutingController) GetDesiredConfig() (*config.Config, error) {
	if m.noDesired {
		return nil, assert.AnError
	}
	cfg, err := m.GetConfig()
	if err != nil {
		return nil, err
	}
	clone := *cfg
	if m.desiredRoutingMode != "" {
		clone.RoutingMode = m.desiredRoutingMode
	}
	return &clone, nil
}

func (m *mockRoutingController) GetCurrentConfig() any {
	return &config.Config{
		APIKey: m.apiKey,
	}
}

func (m *mockRoutingController) GetConfig() (*config.Config, error) {
	return &config.Config{
		APIKey:                 m.apiKey,
		RoutingMode:            m.routingMode,
		ToolResponseMode:       m.toolResponseMode,
		DirectToolResponseMode: m.directToolResponseMode,
		EnableCodeExecution:    m.codeExecutionEnabled,
	}, nil
}

func TestHandleGetRouting(t *testing.T) {
	t.Run("returns default retrieve_tools mode", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: ""}
		srv := NewServer(mockCtrl, logger, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/routing", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool                   `json:"success"`
			Data    map[string]interface{} `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Equal(t, config.RoutingModeRetrieveTools, resp.Data["routing_mode"])
		assert.NotEmpty(t, resp.Data["description"])
		assert.NotNil(t, resp.Data["endpoints"])
		assert.NotNil(t, resp.Data["available_modes"])
	})

	t.Run("returns direct mode", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "direct"}
		srv := NewServer(mockCtrl, logger, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/routing", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool                   `json:"success"`
			Data    map[string]interface{} `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.Equal(t, "direct", resp.Data["routing_mode"])
		assert.Contains(t, resp.Data["description"], "directly")
	})

	t.Run("returns code_execution mode", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "code_execution"}
		srv := NewServer(mockCtrl, logger, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/routing", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool                   `json:"success"`
			Data    map[string]interface{} `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.Equal(t, "code_execution", resp.Data["routing_mode"])
		assert.Contains(t, resp.Data["description"], "JavaScript")
	})

	t.Run("includes all endpoints", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "retrieve_tools"}
		srv := NewServer(mockCtrl, logger, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/routing", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool                   `json:"success"`
			Data    map[string]interface{} `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		endpoints, ok := resp.Data["endpoints"].(map[string]interface{})
		require.True(t, ok, "endpoints should be an object")
		assert.Equal(t, "/mcp", endpoints["default"])
		assert.Equal(t, "/mcp/all", endpoints["direct"])
		assert.Equal(t, "/mcp/code", endpoints["code_execution"])
		assert.Equal(t, "/mcp/call", endpoints["retrieve_tools"])
	})
}

func TestHandleGetStatus_IncludesRoutingMode(t *testing.T) {
	t.Run("includes routing_mode in status response", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "direct"}
		srv := NewServer(mockCtrl, logger, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool                   `json:"success"`
			Data    map[string]interface{} `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Equal(t, "direct", resp.Data["routing_mode"])
	})

	t.Run("defaults routing_mode to retrieve_tools", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: ""}
		srv := NewServer(mockCtrl, logger, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool                   `json:"success"`
			Data    map[string]interface{} `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.Equal(t, config.RoutingModeRetrieveTools, resp.Data["routing_mode"])
	})
}

// TestHandleGetStatus_IncludesDefaultInstructions verifies the status response
// exposes the built-in default MCP instructions so the Web UI can render them as
// the instructions textarea placeholder without hardcoding the text (MCP-2176).
func TestHandleGetStatus_IncludesDefaultInstructions(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: ""}
	srv := NewServer(mockCtrl, logger, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
	}
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	require.True(t, resp.Success)

	defaultInstructions, ok := resp.Data["default_instructions"].(string)
	require.True(t, ok, "default_instructions must be present and a string")
	assert.NotEmpty(t, defaultInstructions)
	assert.Contains(t, defaultInstructions, "retrieve_tools")
}

// getRouting is a small helper for the mode-switcher tests below.
func getRouting(t *testing.T, ctrl *mockRoutingController) map[string]interface{} {
	t.Helper()
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/routing", nil)
	req.Header.Set("X-API-Key", ctrl.apiKey)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.True(t, resp.Success)
	return resp.Data
}

// TestHandleGetRouting_SerializationModes: the header mode switcher decides what
// to show from ONE request, so /api/v1/routing has to carry both serialization
// axes alongside the routing mode — resolved, never raw, so an unset value does
// not render as an empty selection.
func TestHandleGetRouting_SerializationModes(t *testing.T) {
	t.Run("unset resolves to full on both axes", func(t *testing.T) {
		data := getRouting(t, &mockRoutingController{apiKey: "test-key"})
		assert.Equal(t, config.ToolResponseModeFull, data["tool_response_mode"])
		assert.Equal(t, config.DirectToolResponseModeFull, data["direct_tool_response_mode"])
	})

	t.Run("configured values are reported as-is", func(t *testing.T) {
		data := getRouting(t, &mockRoutingController{
			apiKey:                 "test-key",
			toolResponseMode:       config.ToolResponseModeCompact,
			directToolResponseMode: config.DirectToolResponseModeDeferred,
		})
		assert.Equal(t, config.ToolResponseModeCompact, data["tool_response_mode"])
		assert.Equal(t, config.DirectToolResponseModeDeferred, data["direct_tool_response_mode"])
	})
}

// TestHandleGetRouting_ServedMode: routing_mode must name what /mcp BOUND, not
// what the config currently says. The two diverge whenever the config moves
// under a running process — a restart-pending API change, or a hand-edited file
// the watcher hot-reloads — and reporting the config there names a surface /mcp
// is not serving.
func TestHandleGetRouting_ServedMode(t *testing.T) {
	t.Run("the recorded bound mode wins over the live config", func(t *testing.T) {
		data := getRouting(t, &mockRoutingController{
			apiKey:      "test-key",
			routingMode: config.RoutingModeDirect, // e.g. adopted by a file reload
			servedMode:  config.RoutingModeRetrieveTools,
		})
		assert.Equal(t, config.RoutingModeRetrieveTools, data["routing_mode"])
	})

	t.Run("falls back to the config when no mode was recorded", func(t *testing.T) {
		data := getRouting(t, &mockRoutingController{
			apiKey:      "test-key",
			routingMode: config.RoutingModeDirect,
		})
		assert.Equal(t, config.RoutingModeDirect, data["routing_mode"])
	})
}

// TestHandleGetRouting_PendingRoutingMode: a routing_mode change is persisted but
// deliberately NOT adopted in memory (ApplyConfig's restart-required contract),
// so `routing_mode` keeps reporting what /mcp actually serves. Without a pending
// field the Web UI switcher would look broken — the operator picks Direct, the
// badge stays on Retrieve, and nothing says why.
func TestHandleGetRouting_PendingRoutingMode(t *testing.T) {
	t.Run("desired ahead of served reports a pending mode", func(t *testing.T) {
		data := getRouting(t, &mockRoutingController{
			apiKey:             "test-key",
			routingMode:        config.RoutingModeRetrieveTools,
			servedMode:         config.RoutingModeRetrieveTools,
			desiredRoutingMode: config.RoutingModeDirect,
		})
		assert.Equal(t, config.RoutingModeRetrieveTools, data["routing_mode"], "served mode is the bound one")
		assert.Equal(t, config.RoutingModeDirect, data["pending_routing_mode"])
		assert.Equal(t, true, data["restart_required"])
	})

	t.Run("desired matching served reports nothing pending", func(t *testing.T) {
		data := getRouting(t, &mockRoutingController{
			apiKey:      "test-key",
			routingMode: config.RoutingModeDirect,
			servedMode:  config.RoutingModeDirect,
		})
		assert.Empty(t, data["pending_routing_mode"])
		assert.Equal(t, false, data["restart_required"])
	})

	t.Run("unset resolves to the default on both sides before comparing", func(t *testing.T) {
		// A config that never wrote routing_mode is serving retrieve_tools; the
		// empty string must not read as "pending change to nothing".
		data := getRouting(t, &mockRoutingController{
			apiKey:     "test-key",
			servedMode: config.RoutingModeRetrieveTools,
		})
		assert.Empty(t, data["pending_routing_mode"])
		assert.Equal(t, false, data["restart_required"])
	})

	t.Run("a controller without a desired config reports nothing pending", func(t *testing.T) {
		data := getRouting(t, &mockRoutingController{
			apiKey:      "test-key",
			routingMode: config.RoutingModeDirect,
			servedMode:  config.RoutingModeDirect,
			noDesired:   true,
		})
		assert.Empty(t, data["pending_routing_mode"])
		assert.Equal(t, false, data["restart_required"])
	})
}

// TestHandleGetRouting_CodeExecutionEnabled: the code-execution surface has no
// tool-calling path other than the code_execution tool, which is a refusing
// stub while the feature is off. The Web UI switcher has to be able to warn
// BEFORE the operator commits to a restart, so the flag rides along with the
// mode it gates.
func TestHandleGetRouting_CodeExecutionEnabled(t *testing.T) {
	t.Run("off by default", func(t *testing.T) {
		data := getRouting(t, &mockRoutingController{apiKey: "test-key"})
		assert.Equal(t, false, data["code_execution_enabled"])
	})

	t.Run("reported when enabled", func(t *testing.T) {
		data := getRouting(t, &mockRoutingController{apiKey: "test-key", codeExecutionEnabled: true})
		assert.Equal(t, true, data["code_execution_enabled"])
	})
}
