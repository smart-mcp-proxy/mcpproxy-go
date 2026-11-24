//go:build !server

package httpapi

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// A browser-login request, refused or not, must never finalise a staged key
// rotation: the client's API key is not Connect's to change in that request.
func stagedRotationHarness(t *testing.T) *connectHarness {
	t.Helper()
	h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
	h.conn.WithMCPOAuthEnabled(func() bool { return true })
	h.svc.SetConfigReader(h.conn)
	cfgPath := connect.ConfigPath("claude-code", h.home)
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o755))
	require.NoError(t, os.WriteFile(cfgPath, []byte("{}\n"), 0o644))

	w := h.do(http.MethodPost, "/api/v1/connect/claude-code", `{}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	// Stage a rotation whose new secret the config already holds: exactly what
	// the reconciler would finalise.
	m := h.svc.ConnectMinter()
	issued, err := m.Issue("claude-code", connect.CredentialIntent{ActorKind: "api_key", Surface: "api"})
	require.NoError(t, err)
	m.Release("claude-code", issued)
	require.NoError(t, os.WriteFile(cfgPath, []byte(fmt.Sprintf(
		`{"mcpServers":{"mcpproxy":{"type":"http","url":"http://127.0.0.1:8080/mcp","headers":{"X-API-Key":%q}}}}`, issued.Secret)), 0o644))
	view, err := h.svc.Get("claude-code")
	require.NoError(t, err)
	require.True(t, view.RotationPending, "fixture: a rotation is staged")
	return h
}

func (h *connectHarness) assertRotationUntouched(activityBefore int) {
	h.t.Helper()
	view, err := h.svc.Get("claude-code")
	require.NoError(h.t, err)
	require.True(h.t, view.RotationPending, "the staged rotation must still be pending")
	recs, _, err := h.sm.ListActivities(storage.ActivityFilter{Types: []string{"profile_change"}})
	require.NoError(h.t, err)
	require.Len(h.t, recs, activityBefore, "no activity may be written")
}

func (h *connectHarness) activityCount() int {
	recs, _, err := h.sm.ListActivities(storage.ActivityFilter{Types: []string{"profile_change"}})
	require.NoError(h.t, err)
	return len(recs)
}

func TestConnectREST_RefusedOAuthDoesNotReconcileAKeyRotation(t *testing.T) {
	h := stagedRotationHarness(t)
	h.conn.WithMCPOAuthEnabled(func() bool { return false }) // now refused
	before := h.activityCount()

	w := h.do(http.MethodGet, "/api/v1/connect/claude-code/preview?auth=oauth", "", nil, bindingAdminKey)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	h.assertRotationUntouched(before)

	w = h.do(http.MethodPost, "/api/v1/connect/claude-code", `{"auth":"oauth","force":true}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	h.assertRotationUntouched(before)
}

func TestConnectREST_SuccessfulOAuthDoesNotReconcileAKeyRotation(t *testing.T) {
	h := stagedRotationHarness(t)
	before := h.activityCount()

	w := h.do(http.MethodGet, "/api/v1/connect/claude-code/preview?auth=oauth", "", nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	h.assertRotationUntouched(before)

	w = h.do(http.MethodPost, "/api/v1/connect/claude-code", `{"auth":"oauth","force":true}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	h.assertRotationUntouched(before)
}
