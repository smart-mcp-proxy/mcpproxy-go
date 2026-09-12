//go:build server

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"net/http"
	"net/http/httptest"
)

func TestAdminTokenOwnerQualifiedRevocation(t *testing.T) {
	h, _, store := adminTokenTestSetup(t)
	h.SetAgentTokenStore(store)
	first := seedAgentToken(t, store, "alice", "same")
	second := seedAgentToken(t, store, "bob", "same")
	router := adminTestRouter(h, &auth.AuthContext{Type: auth.AuthTypeAdmin})
	list := httptest.NewRecorder()
	router.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/v1/admin/tokens", nil))
	require.Equal(t, 200, list.Code)
	require.Contains(t, list.Body.String(), "alice")
	require.Contains(t, list.Body.String(), "bob")
	require.NotContains(t, list.Body.String(), first)
	require.NotContains(t, list.Body.String(), "token_hash")
	denied := httptest.NewRecorder()
	adminTestRouter(h, &auth.AuthContext{Type: auth.AuthTypeAgent, UserID: "alice"}).ServeHTTP(denied, httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/alice/tokens/same/revoke", nil))
	require.Equal(t, 403, denied.Code)
	revoked := httptest.NewRecorder()
	router.ServeHTTP(revoked, httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/alice/tokens/same/revoke", nil))
	require.Equal(t, 200, revoked.Code)
	_, err := store.ValidateAgentToken(first, tokenTestHMACKey)
	require.Error(t, err)
	_, err = store.ValidateAgentToken(second, tokenTestHMACKey)
	require.NoError(t, err)
}

func TestAdminTokenBodyRevocationTreatsNamesAsOpaqueData(t *testing.T) {
	h, _, store := adminTokenTestSetup(t)
	h.SetAgentTokenStore(store)
	router := adminTestRouter(h, &auth.AuthContext{Type: auth.AuthTypeAdmin})

	for i, name := range []string{"release/blue", "percent%token", "space token", "deploy-保安"} {
		raw := seedAgentToken(t, store, "owner", name)
		body, err := json.Marshal(revokeAgentTokenRequest{Name: name})
		require.NoError(t, err)
		path := "/api/v1/admin/users/owner/tokens/revoke"
		for attempt := 0; attempt < 2; attempt++ {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code, "name %d %q attempt %d: %s", i, name, attempt, rec.Body.String())
		}
		_, err = store.ValidateAgentToken(raw, tokenTestHMACKey)
		require.Error(t, err)
	}
}

type failingAdminAgentTokenStore struct {
	listErr   error
	revokeErr error
}

func (s failingAdminAgentTokenStore) ListAgentTokens() ([]auth.AgentToken, error) {
	return nil, s.listErr
}

func (s failingAdminAgentTokenStore) RevokeAgentTokenForOwner(string, string) error {
	return s.revokeErr
}

func TestAdminTokenRoutesFailClosedOnStorageErrors(t *testing.T) {
	h, _, _ := adminTokenTestSetup(t)
	router := adminTestRouter(h, &auth.AuthContext{Type: auth.AuthTypeAdmin})

	list := httptest.NewRecorder()
	router.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/v1/admin/tokens", nil))
	require.Equal(t, http.StatusServiceUnavailable, list.Code)

	revokeBody := strings.NewReader(`{"name":"opaque/name"}`)
	revoke := httptest.NewRecorder()
	router.ServeHTTP(revoke, httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/owner/tokens/revoke", revokeBody))
	require.Equal(t, http.StatusServiceUnavailable, revoke.Code)

	sentinel := errors.New("storage details must stay private")
	h.SetAgentTokenStore(failingAdminAgentTokenStore{listErr: sentinel, revokeErr: sentinel})
	list = httptest.NewRecorder()
	router.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/v1/admin/tokens", nil))
	require.Equal(t, http.StatusInternalServerError, list.Code)
	require.NotContains(t, list.Body.String(), sentinel.Error())

	revoke = httptest.NewRecorder()
	router.ServeHTTP(revoke, httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/owner/tokens/revoke", strings.NewReader(`{"name":"opaque/name"}`)))
	require.Equal(t, http.StatusInternalServerError, revoke.Code)
	require.NotContains(t, revoke.Body.String(), sentinel.Error())

	h.SetAgentTokenStore(failingAdminAgentTokenStore{revokeErr: storage.ErrAgentTokenNotFound})
	revoke = httptest.NewRecorder()
	router.ServeHTTP(revoke, httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/owner/tokens/revoke", strings.NewReader(`{"name":"missing"}`)))
	require.Equal(t, http.StatusNotFound, revoke.Code)
}

func TestAdminTokenRevocationPersistsAcrossStorageReopen(t *testing.T) {
	h, _, _ := adminTokenTestSetup(t)
	dataDir := t.TempDir()
	logger := zap.NewNop().Sugar()
	store, err := storage.NewManager(dataDir, logger)
	require.NoError(t, err)
	raw, err := auth.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, store.CreateAgentToken(auth.AgentToken{
		Name: "persistent/token", UserID: "owner", Permissions: []string{auth.PermRead}, CreatedAt: time.Now().UTC(),
	}, raw, tokenTestHMACKey))
	h.SetAgentTokenStore(store)
	router := adminTestRouter(h, &auth.AuthContext{Type: auth.AuthTypeAdmin})
	body, err := json.Marshal(revokeAgentTokenRequest{Name: "persistent/token"})
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/owner/tokens/revoke", bytes.NewReader(body)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, store.Close())

	reopened, err := storage.NewManager(filepath.Clean(dataDir), logger)
	require.NoError(t, err)
	defer reopened.Close()
	stored, err := reopened.GetAgentTokenByOwnerAndName("owner", "persistent/token")
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.True(t, stored.Revoked)
}
