package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

// updateFailureController captures every RecordUpdateFailure invocation so the
// tests can assert not just the status code but that nothing was recorded on a
// rejected request (FR-011).
type updateFailureController struct {
	baseController
	apiKey   string
	calls    []string
	recorded bool
	err      error
}

func (c *updateFailureController) GetCurrentConfig() interface{} {
	return &config.Config{APIKey: c.apiKey}
}

func (c *updateFailureController) RecordUpdateFailure(stage string) (bool, error) {
	c.calls = append(c.calls, stage)
	return c.recorded, c.err
}

const updateFailurePath = "/api/v1/telemetry/update-failure"

func newUpdateFailureServer(t *testing.T, ctrl *updateFailureController) *Server {
	t.Helper()
	return NewServer(ctrl, zap.NewNop().Sugar(), nil)
}

func postUpdateFailure(t *testing.T, srv *Server, apiKey, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, updateFailurePath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

// TestUpdateFailure_ValidStages — every member of the closed enum is accepted
// and forwarded verbatim to the seam, and the response is a bodyless 204.
func TestUpdateFailure_ValidStages(t *testing.T) {
	for _, stage := range []string{"appcast", "download", "install", "other"} {
		t.Run(stage, func(t *testing.T) {
			ctrl := &updateFailureController{apiKey: "k", recorded: true}
			srv := newUpdateFailureServer(t, ctrl)

			w := postUpdateFailure(t, srv, "k", `{"stage":"`+stage+`"}`)

			require.Equal(t, http.StatusNoContent, w.Code, "body=%s", w.Body.String())
			assert.Empty(t, w.Body.String(), "204 must carry no body")
			assert.Equal(t, []string{stage}, ctrl.calls)
		})
	}
}

// TestUpdateFailure_Rejects — strict validation (FR-011): anything outside the
// exact `{"stage": "<enum>"}` shape is a 400 that records nothing.
func TestUpdateFailure_Rejects(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"stage outside the enum", `{"stage":"network"}`},
		{"empty stage", `{"stage":""}`},
		{"missing stage", `{}`},
		{"unknown extra field", `{"stage":"download","error":"boom"}`},
		{"trailing JSON value", `{"stage":"download"}{"stage":"install"}`},
		{"trailing garbage", `{"stage":"download"} oops`},
		{"malformed JSON", `{"stage":`},
		{"empty body", ``},
		{"JSON null", `null`},
		{"wrong stage type", `{"stage":123}`},
		{"array body", `[{"stage":"download"}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := &updateFailureController{apiKey: "k", recorded: true}
			srv := newUpdateFailureServer(t, ctrl)

			w := postUpdateFailure(t, srv, "k", tt.body)

			assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
			assert.Empty(t, ctrl.calls, "a rejected request must record nothing")
		})
	}
}

// TestUpdateFailure_TelemetryInactiveIsStill204 — FR-013: a deliberate no-op is
// indistinguishable from a durable record on the wire.
func TestUpdateFailure_TelemetryInactiveIsStill204(t *testing.T) {
	ctrl := &updateFailureController{apiKey: "k", recorded: false}
	srv := newUpdateFailureServer(t, ctrl)

	w := postUpdateFailure(t, srv, "k", `{"stage":"install"}`)

	require.Equal(t, http.StatusNoContent, w.Code, "body=%s", w.Body.String())
	assert.Equal(t, []string{"install"}, ctrl.calls)
}

// TestUpdateFailure_PersistenceFailureIs500 — 204 promises durability, so a
// failed write must not masquerade as success.
func TestUpdateFailure_PersistenceFailureIs500(t *testing.T) {
	ctrl := &updateFailureController{apiKey: "k", err: assertAnError{}}
	srv := newUpdateFailureServer(t, ctrl)

	w := postUpdateFailure(t, srv, "k", `{"stage":"appcast"}`)

	assert.Equal(t, http.StatusInternalServerError, w.Code, "body=%s", w.Body.String())
}

type assertAnError struct{}

func (assertAnError) Error() string { return "bbolt write failed" }

// TestUpdateFailure_RequiresAPIKeyOverTCP — the route sits inside /api/v1, so
// it inherits the standard auth chain (FR-015).
func TestUpdateFailure_RequiresAPIKeyOverTCP(t *testing.T) {
	ctrl := &updateFailureController{apiKey: "k", recorded: true}
	srv := newUpdateFailureServer(t, ctrl)

	w := postUpdateFailure(t, srv, "", `{"stage":"download"}`)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Empty(t, ctrl.calls)
}

// TestUpdateFailure_SocketCallerBypassesAPIKey — the tray posts over the Unix
// socket, which is admitted as admin without a key.
func TestUpdateFailure_SocketCallerBypassesAPIKey(t *testing.T) {
	ctrl := &updateFailureController{apiKey: "k", recorded: true}
	srv := newUpdateFailureServer(t, ctrl)

	req := httptest.NewRequest(http.MethodPost, updateFailurePath, strings.NewReader(`{"stage":"download"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(transport.TagConnectionContext(req.Context(), transport.ConnectionSourceTray))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code, "body=%s", w.Body.String())
	assert.Equal(t, []string{"download"}, ctrl.calls)
}

// TestUpdateFailure_MethodNotAllowed — only POST is mounted.
func TestUpdateFailure_MethodNotAllowed(t *testing.T) {
	ctrl := &updateFailureController{apiKey: "k", recorded: true}
	srv := newUpdateFailureServer(t, ctrl)

	req := httptest.NewRequest(http.MethodGet, updateFailurePath, nil)
	req.Header.Set("X-API-Key", "k")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	assert.Empty(t, ctrl.calls)
}

// TestUpdateFailure_OversizedTrailingValueIsRejected — regression for the
// io.LimitReader bypass (Codex code-review round 1): a valid object padded
// with whitespace out to the body cap, with a second JSON value hidden beyond
// it, must be a 400 that records nothing — not a silently truncated 204.
func TestUpdateFailure_OversizedTrailingValueIsRejected(t *testing.T) {
	ctrl := &updateFailureController{apiKey: "k", recorded: true}
	srv := newUpdateFailureServer(t, ctrl)

	valid := `{"stage":"download"}`
	padding := strings.Repeat(" ", maxUpdateFailureBody-len(valid))
	body := valid + padding + `{"stage":"install"}`

	w := postUpdateFailure(t, srv, "k", body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Empty(t, ctrl.calls, "nothing may be recorded when the body overflows the cap")
}
