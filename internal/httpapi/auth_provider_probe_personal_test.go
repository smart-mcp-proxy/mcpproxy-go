//go:build !server

package httpapi

// Spec 107 T049 (US2, FR-030): the personal build has no
// `GET /api/v1/auth/provider` route — the probe is registered only by the
// server edition's SetupAll (T053) on an enabled block — so it never answers
// 200 and never emits `display_name`, whatever the opaque `server_edition`
// carrier (FR-040) says.
//
// Pin, not a red test: it is green on the PR-B base and stays green after
// T053, which touches only build-tagged server-edition files.
//
// DISCREPANCY with FR-030 / T053 ("404 falls out of chi"): the probe path sits
// under the `/api/v1` mount (server.go:751), and chi runs a sub-mux's
// middleware — here apiKeyAuthMiddleware (server.go:756) — BEFORE that
// sub-mux's 404. An unauthenticated caller therefore receives 401, not 404;
// only a caller presenting the API key sees the route's absence as 404. The
// frontend probe (T054a, "a 404 means personal edition") has to treat both as
// "not a server edition", or the mount has to answer 404 for unknown paths
// before the key check. The cases below pin the behaviour of the code as it
// stands.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

type providerProbePersonalController struct {
	baseController
	cfg *config.Config
}

func (m *providerProbePersonalController) GetCurrentConfig() *config.Config { return m.cfg }
func (m *providerProbePersonalController) GetConfig() (*config.Config, error) {
	return m.cfg, nil
}

func newProviderProbePersonalServer(t *testing.T) *Server {
	t.Helper()
	// The carrier claims an enabled server edition; the personal build must
	// not interpret it (FR-040), so the route stays absent.
	carrier := &config.ServerEditionConfig{}
	require.NoError(t, json.Unmarshal([]byte(`{"enabled":true,"admin_emails":["admin@example.com"],"oauth":{"provider":"oidc","client_id":"id","client_secret":"secret","issuer_url":"https://idp.example.test","display_name":"Acme SSO"}}`), carrier))
	ctrl := &providerProbePersonalController{cfg: &config.Config{
		Listen:        "127.0.0.1:8080",
		APIKey:        "test-key",
		ServerEdition: carrier,
	}}
	return NewServer(ctrl, zap.NewNop().Sugar(), nil)
}

func TestAuthProviderProbe_PersonalBuildHasNoRoute(t *testing.T) {
	srv := newProviderProbePersonalServer(t)

	t.Run("unauthenticated caller never gets the probe", func(t *testing.T) {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/provider", http.NoBody))
		// Code as it stands: the /api/v1 sub-mux's key middleware answers
		// before chi's 404 (see the file comment). Either way: not 200, no
		// label, nothing that looks like a server edition.
		assert.Equal(t, http.StatusUnauthorized, rec.Code, "body=%s", rec.Body.String())
		assert.NotEqual(t, http.StatusOK, rec.Code)
		assert.NotContains(t, rec.Body.String(), "display_name")
		assert.NotContains(t, rec.Body.String(), "Acme SSO")
	})

	t.Run("API-key caller sees the route is absent", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/provider", http.NoBody)
		req.Header.Set("X-API-Key", "test-key")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusNotFound, rec.Code, "body=%s", rec.Body.String())
		assert.NotContains(t, rec.Body.String(), "display_name")
	})

	t.Run("the carrier is not interpreted", func(t *testing.T) {
		// Same request, a carrier that is absent: identical answer, so the
		// block's content cannot be what decides.
		bare := NewServer(&providerProbePersonalController{cfg: &config.Config{Listen: "127.0.0.1:8080", APIKey: "test-key"}}, zap.NewNop().Sugar(), nil)
		for _, key := range []string{"", "test-key"} {
			with := httptest.NewRecorder()
			without := httptest.NewRecorder()
			reqA := httptest.NewRequest(http.MethodGet, "/api/v1/auth/provider", http.NoBody)
			reqB := httptest.NewRequest(http.MethodGet, "/api/v1/auth/provider", http.NoBody)
			if key != "" {
				reqA.Header.Set("X-API-Key", key)
				reqB.Header.Set("X-API-Key", key)
			}
			srv.ServeHTTP(with, reqA)
			bare.ServeHTTP(without, reqB)
			assert.Equal(t, without.Code, with.Code, "key=%q", key)
		}
	})
}
