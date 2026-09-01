package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// isolationPatch drives PATCH /api/v1/servers/{name} with an `isolation` body
// against a mock controller, and returns the response plus whatever the
// handler tried to persist.
func isolationPatch(t *testing.T, existing *config.ServerConfig, iso map[string]any) (*httptest.ResponseRecorder, *mockPatchServerController) {
	t.Helper()
	mockCtrl := &mockPatchServerController{apiKey: "test-key", existingServer: existing}
	srv := NewServer(mockCtrl, zap.NewNop().Sugar(), nil)

	body, err := json.Marshal(map[string]any{"isolation": iso})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/servers/"+existing.Name, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w, mockCtrl
}

// TestPatchServer_RejectsUnknownIsolationMode is the defect this handler had
// no guard for: `mode_override` was cast straight to config.IsolationMode, so
// a typo was persisted to BBolt and the config file, and the NEXT daemon start
// failed config validation with a file the operator never hand-edited.
func TestPatchServer_RejectsUnknownIsolationMode(t *testing.T) {
	existing := &config.ServerConfig{Name: "github", Protocol: "stdio", Command: "npx", Enabled: true}

	for _, bad := range []string{"bogus", "Docker", "docker ", "SANDBOX"} {
		t.Run(bad, func(t *testing.T) {
			w, mockCtrl := isolationPatch(t, existing, map[string]any{"mode_override": bad})

			require.Equal(t, http.StatusBadRequest, w.Code,
				"an unknown isolation mode must be rejected at the seam, body=%s", w.Body.String())
			assert.Nil(t, mockCtrl.capturedUpdates, "nothing may be persisted for a rejected mode")

			body := w.Body.String()
			for _, want := range []string{"docker", "sandbox", "none"} {
				assert.Contains(t, body, want, "the 400 must name the accepted vocabulary")
			}
		})
	}
}

// TestPatchServer_AcceptsKnownIsolationModes guards against over-rejecting.
func TestPatchServer_AcceptsKnownIsolationModes(t *testing.T) {
	existing := &config.ServerConfig{Name: "github", Protocol: "stdio", Command: "npx", Enabled: true}

	for _, good := range []string{"docker", "sandbox", "none"} {
		t.Run(good, func(t *testing.T) {
			w, mockCtrl := isolationPatch(t, existing, map[string]any{"mode_override": good})
			require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
			require.NotNil(t, mockCtrl.capturedUpdates)
			require.NotNil(t, mockCtrl.capturedUpdates.Isolation)
			require.NotNil(t, mockCtrl.capturedUpdates.Isolation.Mode)
			assert.Equal(t, config.IsolationMode(good), *mockCtrl.capturedUpdates.Isolation.Mode)
		})
	}
}

// TestPatchServer_EmptyModeOverrideClears keeps the documented "clear the
// override" path working alongside the new validation.
func TestPatchServer_EmptyModeOverrideClears(t *testing.T) {
	sandbox := config.IsolationModeSandbox
	existing := &config.ServerConfig{
		Name: "github", Protocol: "stdio", Command: "npx", Enabled: true,
		Isolation: &config.IsolationConfig{Mode: &sandbox},
	}

	w, mockCtrl := isolationPatch(t, existing, map[string]any{"mode_override": ""})
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.NotNil(t, mockCtrl.capturedUpdates)
	require.NotNil(t, mockCtrl.capturedUpdates.Isolation)
	assert.Nil(t, mockCtrl.capturedUpdates.Isolation.Mode, `"" must clear the mode override back to inherit`)
}

// TestAddServer_RejectsUnknownIsolationMode covers the same seam on create.
func TestAddServer_RejectsUnknownIsolationMode(t *testing.T) {
	mockCtrl := &mockAddServerController{apiKey: "test-key"}
	srv := NewServer(mockCtrl, zap.NewNop().Sugar(), nil)

	body, err := json.Marshal(map[string]any{
		"name":      "new-server",
		"command":   "npx",
		"isolation": map[string]any{"mode_override": "bogus"},
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/servers", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "sandbox")
}

// TestPatchServer_IsolationEnabledIsReadOnly is the read/write coherence guard
// (GH #1142). `isolation.enabled` on a READ is the EFFECTIVE state; the write
// path used to decode the same key into the RAW tri-state override. Any
// read-modify-write client — GET the server, PATCH the isolation object back —
// therefore converted "inherits the global setting" into a permanent explicit
// override, which is the original bug arriving from the other direction.
//
// The write key is `enabled_override`; `enabled` is rejected rather than
// silently applied or silently dropped.
func TestPatchServer_IsolationEnabledIsReadOnly(t *testing.T) {
	existing := &config.ServerConfig{
		Name: "github", Protocol: "stdio", Command: "npx", Enabled: true,
		// An operator's standing "always isolate this one".
		Isolation: &config.IsolationConfig{Enabled: config.BoolPtr(true)},
	}

	for _, echoed := range []any{true, false, nil} {
		t.Run(fmt.Sprintf("echoed %v", echoed), func(t *testing.T) {
			w, mockCtrl := isolationPatch(t, existing, map[string]any{"enabled": echoed})

			require.Equal(t, http.StatusBadRequest, w.Code,
				"isolation.enabled is read-only on writes, body=%s", w.Body.String())
			assert.Nil(t, mockCtrl.capturedUpdates, "a rejected request must not persist anything")
			assert.Contains(t, w.Body.String(), "enabled_override",
				"the 400 must name the field the client should use instead")
		})
	}
}

// TestPatchServer_EnabledOverrideTriState pins the write key's three states.
func TestPatchServer_EnabledOverrideTriState(t *testing.T) {
	newExisting := func() *config.ServerConfig {
		return &config.ServerConfig{
			Name: "github", Protocol: "stdio", Command: "npx", Enabled: true,
			Isolation: &config.IsolationConfig{Enabled: config.BoolPtr(true), Image: "python:3.12"},
		}
	}

	t.Run("absent leaves the persisted override alone", func(t *testing.T) {
		w, mockCtrl := isolationPatch(t, newExisting(), map[string]any{"image": "python:3.13"})
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		require.NotNil(t, mockCtrl.capturedUpdates.Isolation)
		require.NotNil(t, mockCtrl.capturedUpdates.Isolation.Enabled,
			"an unrelated field edit must not drop the standing opt-in")
		assert.True(t, *mockCtrl.capturedUpdates.Isolation.Enabled)
	})

	t.Run("null clears back to inherit", func(t *testing.T) {
		w, mockCtrl := isolationPatch(t, newExisting(), map[string]any{"enabled_override": nil})
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		require.NotNil(t, mockCtrl.capturedUpdates.Isolation)
		assert.Nil(t, mockCtrl.capturedUpdates.Isolation.Enabled)
	})

	t.Run("false sets an explicit opt-out", func(t *testing.T) {
		w, mockCtrl := isolationPatch(t, newExisting(), map[string]any{"enabled_override": false})
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		require.NotNil(t, mockCtrl.capturedUpdates.Isolation)
		require.NotNil(t, mockCtrl.capturedUpdates.Isolation.Enabled)
		assert.False(t, *mockCtrl.capturedUpdates.Isolation.Enabled)
	})
}

// TestAddServer_EnabledOverrideOnCreate keeps the create-time opt-in reachable
// under the new key.
func TestAddServer_EnabledOverrideOnCreate(t *testing.T) {
	mockCtrl := &mockAddServerController{apiKey: "test-key"}
	srv := NewServer(mockCtrl, zap.NewNop().Sugar(), nil)

	body, err := json.Marshal(map[string]any{
		"name":      "new-server",
		"command":   "npx",
		"isolation": map[string]any{"enabled_override": true},
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/servers", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.NotNil(t, mockCtrl.captured, "AddServer should have been called")
	require.NotNil(t, mockCtrl.captured.Isolation)
	require.NotNil(t, mockCtrl.captured.Isolation.Enabled)
	assert.True(t, *mockCtrl.captured.Isolation.Enabled)
}

// TestIsolationRequest_EnabledIsNeverWritable is the contract guard in one
// place: no key the read surface emits as a DERIVED value may also mutate
// state on the way back in. `enabled` must be refused by validate() and, if a
// call site ever forgets to call it, must still be inert in resolve().
func TestIsolationRequest_EnabledIsNeverWritable(t *testing.T) {
	var req IsolationRequest
	require.NoError(t, json.Unmarshal([]byte(`{"enabled":false}`), &req))
	require.Error(t, req.validate(), "`enabled` must be rejected on writes")

	// Defense in depth: even unvalidated, it cannot rewrite the override.
	existing := &config.IsolationConfig{Enabled: config.BoolPtr(true)}
	out := req.resolve(existing)
	require.NotNil(t, out.Enabled)
	assert.True(t, *out.Enabled, "`enabled` must not reach the persisted override")
}

// TestIsolationRequest_UnsetFieldsMarshalAway guards the re-encode trap: an
// untouched request must not serialize `enabled_override: null`, because that
// is the CLEAR-the-override shape on the way back in.
func TestIsolationRequest_UnsetFieldsMarshalAway(t *testing.T) {
	raw, err := json.Marshal(IsolationRequest{})
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(raw))

	raw, err = json.Marshal(IsolationRequest{EnabledOverride: NullableBool{Set: true}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"enabled_override":null}`, string(raw),
		"an explicit clear must still round-trip as null")
}
