//go:build !server

package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

const notifyAPIKey = "profile-notify-key"

func newNotifyServer(t *testing.T) (*Server, *httpapi.Server) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.APIKey = notifyAPIKey
	cfg.RequireMCPAuth = true
	cfg.Profiles = []config.ProfileConfig{{Name: "ro", Servers: []string{"a"}}, {Name: "full", Servers: []string{"a", "b"}}}
	srv, err := NewServer(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown() })

	api := httpapi.NewServer(srv, zap.NewNop().Sugar(), nil)
	api.SetTokenStore(srv.runtime.StorageManager(), cfg.DataDir)
	api.SetClientsService(srv.runtime.ClientsService())
	return srv, api
}

func mintClient(t *testing.T, srv *Server, clientID, prof string) {
	t.Helper()
	m := srv.runtime.ClientsService().ConnectMinter()
	intent := connect.CredentialIntent{Profile: &prof, ActorKind: "api_key", Surface: "api"}
	issued, err := m.Issue(clientID, intent)
	require.NoError(t, err)
	require.NoError(t, m.Commit(clientID, intent, issued))
}

func putBinding(t *testing.T, api *httpapi.Server, clientID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/clients/"+clientID+"/binding", bytes.NewBufferString(body))
	req.Header.Set("X-API-Key", notifyAPIKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)
	return w
}

// TestBindingChange_NotifiesEachSessionOfTheTokenOnce pins T032 / FR-026: a
// binding change driven through PUT /clients/{id}/binding sends
// tools/list_changed to every live session of that token on the server
// instance serving it, clears their stored set_profile selection, leaves other
// tokens' sessions alone and writes exactly one profile_change record.
func TestBindingChange_NotifiesEachSessionOfTheTokenOnce(t *testing.T) {
	srv, api := newNotifyServer(t)
	proxy := srv.mcpProxy
	require.NotNil(t, proxy.directServer)
	mintClient(t, srv, "cursor", "ro")
	mintClient(t, srv, "windsurf", "ro")

	cursor := clientCtx("cursor", "ro", auth.ProfileModeLocked)
	windsurf := clientCtx("windsurf", "ro", auth.ProfileModeLocked)
	sDefault := initSession(t, defaultSurface(proxy), "cursor-default", cursor)
	sDirect := initSession(t, directSurface(proxy), "cursor-direct", cursor)
	sOther := initSession(t, defaultSurface(proxy), "windsurf-1", windsurf)
	sAdmin := initSession(t, defaultSurface(proxy), "admin-1", auth.WithAuthContext(context.Background(), auth.AdminContext()))
	for _, s := range []*notifyingSession{sDefault, sDirect, sOther, sAdmin} {
		s.drainListChanged() // registration/initialize-time noise
	}
	proxy.sessionStore.SetActiveProfile("cursor-default", "full")
	proxy.sessionStore.SetActiveProfile("cursor-direct", "full")
	proxy.sessionStore.SetActiveProfile("windsurf-1", "full")

	w := putBinding(t, api, "cursor", `{"profile":"full"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	require.Equal(t, 1, sDefault.drainListChanged(), "exactly one list_changed on the default instance")
	require.Equal(t, 1, sDirect.drainListChanged(), "and one on the direct instance")
	require.Equal(t, 0, sOther.drainListChanged(), "another token's session is not notified")
	require.Equal(t, 0, sAdmin.drainListChanged())
	require.Empty(t, proxy.sessionStore.GetActiveProfile("cursor-default"), "the stored selection is cleared")
	require.Empty(t, proxy.sessionStore.GetActiveProfile("cursor-direct"))
	require.Equal(t, "full", proxy.sessionStore.GetActiveProfile("windsurf-1"), "another token's selection is untouched")

	recs, _, err := srv.runtime.StorageManager().ListActivities(storage.ActivityFilter{Types: []string{"profile_change"}})
	require.NoError(t, err)
	var assigns int
	for _, r := range recs {
		if r.Metadata["client_id"] == "cursor" && r.Metadata["previous_profile"] == "ro" {
			assigns++
			require.Equal(t, "assign", r.Metadata["change"])
		}
	}
	require.Equal(t, 1, assigns, "one profile_change record for the change")

	// a no-op write notifies nobody and writes nothing
	w = putBinding(t, api, "cursor", `{"profile":"full"}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 0, sDefault.drainListChanged())
	recs2, _, err := srv.runtime.StorageManager().ListActivities(storage.ActivityFilter{Types: []string{"profile_change"}})
	require.NoError(t, err)
	require.Len(t, recs2, len(recs))

	// the next resolution reports the new binding (per-request enforcement)
	idx := proxy.profileIndexFor(proxy.currentConfig())
	res := proxy.ResolveProfileV3(clientCtx("cursor", "full", auth.ProfileModeLocked), idx)
	require.Equal(t, "full", res.Name)
}

// TestBindingChange_RefusedWithoutActiveCredentialWritesNothing: none, a
// revoked credential and the admin key in a config all refuse with
// no_client_credential; no token is minted, no record written.
func TestBindingChange_RefusedWithoutActiveCredentialWritesNothing(t *testing.T) {
	srv, api := newNotifyServer(t)
	sm := srv.runtime.StorageManager()

	w := putBinding(t, api, "cursor", `{"profile":"ro"}`)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"code":"no_client_credential"`)

	mintClient(t, srv, "windsurf", "ro")
	_, err := sm.ForgetClientCredential("windsurf")
	require.NoError(t, err)
	before, _, err := sm.ListActivities(storage.ActivityFilter{Types: []string{"profile_change"}})
	require.NoError(t, err)
	w = putBinding(t, api, "windsurf", `{"profile":"full"}`)
	require.Equal(t, http.StatusConflict, w.Code)
	after, _, err := sm.ListActivities(storage.ActivityFilter{Types: []string{"profile_change"}})
	require.NoError(t, err)
	require.Len(t, after, len(before))

	toks, err := sm.ListAgentTokens()
	require.NoError(t, err)
	for _, tk := range toks {
		require.NotEqual(t, "client-cursor", tk.Name, "no credential is minted implicitly")
	}
}
