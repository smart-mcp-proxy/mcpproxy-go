//go:build !server

package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// T038: on -> off after use answers 404 on every OAuth path; off -> on brings
// the logins back; the switch needs no restart.
func TestOAuthLoginToggle_OffAfterUse404_OnRestores(t *testing.T) {
	f := newOAuthFlow(t, nil)
	_, tok := f.login("cursor")

	f.setEnabled(false)
	for _, path := range oauthLoginGatedPaths {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			req, _ := http.NewRequest(method, path, http.NoBody)
			assert.Equal(t, http.StatusNotFound, f.do(req).Code, method+" "+path)
		}
	}
	assert.Equal(t, http.StatusNotFound, f.get("/mcp/oauth/cursor", bearer(tok.access)).Code)

	f.setEnabled(true)
	assert.Equal(t, http.StatusOK, f.get("/mcp/oauth/cursor", bearer(tok.access)).Code)
	assert.Equal(t, http.StatusOK, f.get("/.well-known/oauth-authorization-server", nil).Code)
}

// T038: switching off cancels pending AND approved requests with their
// unconsumed codes, synchronously with the config apply, and a quick off -> on
// leaves no approved request usable.
func TestOAuthLoginToggle_OffCancelsPendingAndApproved(t *testing.T) {
	f := newOAuthFlow(t, nil)
	regID := f.register(oauthTestRedirect)
	verifier, challenge := pkce()
	resource := f.origin() + "/mcp/oauth/cursor"
	pending := f.authorize(regID, oauthTestRedirect, challenge, resource)
	approvedID := f.authorize(regID, oauthTestRedirect, challenge, resource)
	code := f.approve(approvedID, "cursor")

	svc := f.srv.oauthLoginSvc()
	require.Len(t, svc.ListPending(), 1)

	// A quick off -> on, back to back, with no pause for any event.
	f.setEnabled(false)
	f.setEnabled(true)

	assert.Empty(t, svc.ListPending(), "pending requests are cancelled")
	_, err := svc.Approve(adminCtx(), pending, runtime.ApprovalChoice{Client: runtime.ClientChoice{ID: "cursor"}})
	assert.Error(t, err, "a cancelled pending request cannot be approved")
	rec := f.exchange(code, regID, verifier, resource)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "an approved code does not survive off -> on")
	assert.Equal(t, "invalid_grant", decodeBody(t, rec)["error"])
	// Nothing was stored.
	grants, err := svc.ListGrants("cursor")
	require.NoError(t, err)
	assert.Empty(t, grants)
}

// T038: the same cancellation holds on the file-reload path.
func TestOAuthLoginToggle_FileReloadCancels(t *testing.T) {
	f := newOAuthFlow(t, nil)
	regID := f.register(oauthTestRedirect)
	verifier, challenge := pkce()
	resource := f.origin() + "/mcp/oauth/cursor"
	id := f.authorize(regID, oauthTestRedirect, challenge, resource)
	code := f.approve(id, "cursor")

	cfg := f.srv.runtime.Config()
	require.NotNil(t, cfg)
	off := *cfg
	off.MCPOAuthEnabled = false
	raw, err := json.Marshal(&off)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "mcp_config.json")
	f.srv.runtime.UpdateConfig(cfg, path) // point the runtime at the file (still on); this writes the file
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	require.NoError(t, f.srv.runtime.ReloadConfiguration())

	require.False(t, f.srv.runtime.Config().MCPOAuthEnabled, "the reload switched the feature off")
	f.setEnabled(true)
	rec := f.exchange(code, regID, verifier, resource)
	assert.Equal(t, "invalid_grant", decodeBody(t, rec)["error"], "the code was cancelled by the reload itself")
}

// T038: forgetting the last client keeps the 404 of an off-after-use install.
func TestOAuthLoginToggle_ForgettingLastClientKeepsOff404(t *testing.T) {
	f := newOAuthFlow(t, nil)
	f.login("cursor")

	clients := f.srv.runtime.ClientsService()
	require.NotNil(t, clients)
	_, err := clients.Forget(adminCtx(), runtime.Actor{}, "cursor", false)
	require.NoError(t, err)

	f.setEnabled(false)
	for _, path := range oauthLoginGatedPaths {
		assert.Equal(t, http.StatusNotFound, f.get(path, nil).Code, path)
	}
	assert.True(t, f.srv.oauthLoginEverUsedNow(), "the marker is never cleared")
}
