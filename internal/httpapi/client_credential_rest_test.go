package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// clientCredentialServer returns a Server wired with a fake kind=client
// credential (mcp_cli_ secret) so a request carrying it goes through the
// middleware exactly as it would against real storage.
func clientCredentialServer(t *testing.T, ctrl ServerController) (*Server, string) {
	t.Helper()
	tmpDir := t.TempDir()
	_, err := auth.GetOrCreateHMACKey(tmpDir)
	require.NoError(t, err)

	rawToken, err := auth.GenerateClientToken()
	require.NoError(t, err)

	clientTok := &auth.AgentToken{
		Name:           "client-cursor",
		Kind:           auth.KindClient,
		ClientID:       "cursor",
		ProfileMode:    auth.ProfileModeSwitchable,
		TokenPrefix:    auth.TokenPrefix(rawToken),
		AllowedServers: []string{"*"},
		Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
		ExpiresAt:      time.Now().Add(24 * time.Hour),
		CreatedAt:      time.Now(),
	}

	store := &testTokenStore{
		validateFunc: func(token string, _ []byte) (*auth.AgentToken, error) {
			if token == rawToken {
				return clientTok, nil
			}
			return nil, fmt.Errorf("token not found")
		},
	}

	logger := zap.NewNop().Sugar()
	srv := NewServer(ctrl, logger, nil)
	srv.SetTokenStore(store, tmpDir)
	return srv, rawToken
}

// clientCredentialRESTRoutes samples one route from each route family the
// FR-023 refusal must cover (T030): a read, a write, the config surface,
// tokens/activity, and /events.
var clientCredentialRESTRoutes = []struct {
	name   string
	method string
	path   string
}{
	{"servers-list", http.MethodGet, "/api/v1/servers"},
	{"server-patch", http.MethodPatch, "/api/v1/servers/test-server"},
	{"config-get", http.MethodGet, "/api/v1/config"},
	{"config-patch", http.MethodPatch, "/api/v1/config"},
	{"tokens-list", http.MethodGet, "/api/v1/tokens"},
	{"activity-list", http.MethodGet, "/api/v1/activity"},
	{"status", http.MethodGet, "/api/v1/status"},
	{"events", http.MethodGet, "/events"},
}

// TestClientCredential_RejectedOnREST pins FR-023 (T030): a kind=client
// credential is rejected on every REST route family with 403, recognised by
// the mcp_cli_ prefix — never dispatched to any handler.
func TestClientCredential_RejectedOnREST(t *testing.T) {
	for _, rt := range clientCredentialRESTRoutes {
		t.Run(rt.name, func(t *testing.T) {
			ctrl := &adminConfigController{ServerController: &MockServerController{}, apiKey: "admin-secret"}
			srv, rawToken := clientCredentialServer(t, ctrl)

			req := httptest.NewRequest(rt.method, rt.path, nil)
			req.Header.Set("X-API-Key", rawToken)
			w := httptest.NewRecorder()

			srv.ServeHTTP(w, req)

			assert.Equal(t, http.StatusForbidden, w.Code, "client credential must be forbidden on REST")
			assert.Contains(t, w.Body.String(), "client credentials are valid on MCP endpoints only")
		})
	}
}

// TestClientCredential_RejectedOnREST_BearerAndQueryParam pins that the
// refusal applies to every carrier (Authorization: Bearer, ?apikey=), not
// only X-API-Key.
func TestClientCredential_RejectedOnREST_BearerAndQueryParam(t *testing.T) {
	ctrl := &adminConfigController{ServerController: &MockServerController{}, apiKey: "admin-secret"}
	srv, rawToken := clientCredentialServer(t, ctrl)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/servers", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "client credentials are valid on MCP endpoints only")

	ctrl2 := &adminConfigController{ServerController: &MockServerController{}, apiKey: "admin-secret"}
	srv2, rawToken2 := clientCredentialServer(t, ctrl2)
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/servers?apikey="+rawToken2, nil)
	w2 := httptest.NewRecorder()
	srv2.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusForbidden, w2.Code)
	assert.Contains(t, w2.Body.String(), "client credentials are valid on MCP endpoints only")
}
