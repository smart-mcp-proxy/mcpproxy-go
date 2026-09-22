package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// T080 (Spec 107 PR-C, US4): FR-001 precedence fixtures for the
// session-principal hook, plus the CanRevealSecrets(false) invariant for
// session principals.
//
// [compile-red until T083]: SessionPrincipalResolver and
// (*Server).SetSessionPrincipalResolver do not exist yet —
// internal/httpapi/session_principal.go is T083's file. This file therefore
// cannot compile on its own until T083 lands; that is the point (tasks.md
// T080 label). No production code is added here.
//
// sessionCookieName mirrors internal/serveredition/auth.SessionCookieName
// ("mcpproxy_session", session_store.go:15). This package cannot import that
// build-tagged package (excluded from the personal build), so the literal is
// pinned here independently; both spellings must agree for FR-001 cookie
// extraction to work in either edition.
const sessionCookieName = "mcpproxy_session"

const (
	validCookieValue = "session-cookie-value"
	validBearerJWT   = "header.payload.signature"
)

// sessionTestController is a mock ServerController that answers GetConfig
// (used by apiKeyAuthMiddleware's admin-key check, revealSecrets and
// desiredConfigForPatch) and GetCurrentConfig with the same *config.Config,
// so both the auth middleware and the /config, /info doors under test see a
// consistent operator configuration.
type sessionTestController struct {
	baseController
	cfg *config.Config
}

func (m *sessionTestController) GetCurrentConfig() *config.Config   { return m.cfg }
func (m *sessionTestController) GetConfig() (*config.Config, error) { return m.cfg, nil }

// principalProbeResponse reports exactly what apiKeyAuthMiddleware installed
// in the request context, so a test can assert WHICH credential source won
// FR-001 precedence rather than only whether *some* credential was accepted.
type principalProbeResponse struct {
	Type           string `json:"type"`
	CredentialKind string `json:"credential_kind"`
	CanReveal      bool   `json:"can_reveal"`
	UserID         string `json:"user_id"`
}

// mountPrincipalProbe registers a minimal GET route directly behind
// apiKeyAuthMiddleware, bypassing the /api/v1 route table and every door's
// own authorization rules, so these tests observe the middleware's decision
// in isolation.
func mountPrincipalProbe(srv *Server) {
	srv.Router().With(srv.apiKeyAuthMiddleware()).Get("/test/principal", func(w http.ResponseWriter, r *http.Request) {
		var resp principalProbeResponse
		if ac := auth.AuthContextFromContext(r.Context()); ac != nil {
			resp = principalProbeResponse{
				Type:           ac.Type,
				CredentialKind: string(ac.CredentialKind),
				CanReveal:      ac.CanRevealSecrets(),
				UserID:         ac.UserID,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
}

// acceptingResolver is a SessionPrincipalResolver test double: it accepts
// exactly validCookieValue for kind=cookie (installing a tenant "user"
// principal) and exactly validBearerJWT for kind=bearer_jwt (installing an
// "admin_user" principal) — deliberately DIFFERENT principals per source, so
// a precedence test can prove which source actually won rather than merely
// that a session was accepted. Anything else, or an unexpected kind, is "not
// a principal" ((nil, nil) per the contract) → 401.
func acceptingResolver() SessionPrincipalResolver {
	return func(_ *http.Request, kind auth.CredentialKind, value string) (*auth.AuthContext, error) {
		switch kind {
		case auth.CredentialKindCookie:
			if value != validCookieValue {
				return nil, nil
			}
			return &auth.AuthContext{
				Type:           auth.AuthTypeUser,
				UserID:         "user-cookie",
				CredentialKind: auth.CredentialKindCookie,
			}, nil
		case auth.CredentialKindBearerJWT:
			if value != validBearerJWT {
				return nil, nil
			}
			return &auth.AuthContext{
				Type:           auth.AuthTypeAdminUser,
				UserID:         "user-bearer",
				CredentialKind: auth.CredentialKindBearerJWT,
			}, nil
		default:
			return nil, nil
		}
	}
}

// newSessionTestServer builds a Server with an admin API key configured and,
// unless resolver is nil (simulating the personal edition, which never calls
// SetSessionPrincipalResolver), the given session-principal resolver
// installed.
func newSessionTestServer(t *testing.T, resolver SessionPrincipalResolver) (*Server, *config.Config) {
	t.Helper()
	cfg := &config.Config{APIKey: "admin-key-12345", RevealSecretHeaders: true}
	ctrl := &sessionTestController{cfg: cfg}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
	if resolver != nil {
		srv.SetSessionPrincipalResolver(resolver)
	}
	mountPrincipalProbe(srv)
	return srv, cfg
}

func doProbe(t *testing.T, srv *Server, mutate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/test/principal", nil)
	if mutate != nil {
		mutate(req)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func withValidCookie(r *http.Request) {
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: validCookieValue})
}

func decodeProbe(t *testing.T, w *httptest.ResponseRecorder) principalProbeResponse {
	t.Helper()
	var resp principalProbeResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	return resp
}

// --- FR-001 precedence: an explicit, WRONG credential is terminal ---
//
// Presence of a credential source is header/query MEMBERSHIP
// (r.Header.Values, r.URL.Query().Has), not a non-empty value, so a wrong OR
// merely empty-but-present X-API-Key / Authorization: Bearer / ?apikey= must
// never fall through to a valid cookie sitting right there on the same
// request.

func TestSessionPrincipal_WrongAPIKeyWithValidCookie_401(t *testing.T) {
	srv, _ := newSessionTestServer(t, acceptingResolver())
	w := doProbe(t, srv, func(r *http.Request) {
		r.Header.Set("X-API-Key", "not-the-admin-key")
		withValidCookie(r)
	})
	assert.Equal(t, http.StatusUnauthorized, w.Code, "a wrong X-API-Key must not fall through to a valid cookie")
}

func TestSessionPrincipal_WrongBearerWithValidCookie_401(t *testing.T) {
	srv, _ := newSessionTestServer(t, acceptingResolver())
	w := doProbe(t, srv, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer not-a-valid-jwt")
		withValidCookie(r)
	})
	assert.Equal(t, http.StatusUnauthorized, w.Code, "a wrong bearer must not fall through to a valid cookie")
}

func TestSessionPrincipal_WrongAPIKeyQueryWithValidCookie_401(t *testing.T) {
	srv, _ := newSessionTestServer(t, acceptingResolver())
	w := doProbe(t, srv, func(r *http.Request) {
		q := r.URL.Query()
		q.Set("apikey", "not-the-admin-key")
		r.URL.RawQuery = q.Encode()
		withValidCookie(r)
	})
	assert.Equal(t, http.StatusUnauthorized, w.Code, "a wrong ?apikey= must not fall through to a valid cookie")
}

func TestSessionPrincipal_EmptyPresentXAPIKeyWithValidCookie_401(t *testing.T) {
	srv, _ := newSessionTestServer(t, acceptingResolver())
	w := doProbe(t, srv, func(r *http.Request) {
		r.Header.Set("X-API-Key", "") // present (header membership), value empty
		withValidCookie(r)
	})
	assert.Equal(t, http.StatusUnauthorized, w.Code, "an empty-but-PRESENT X-API-Key is a present, failing credential — presence is membership, not a non-empty value")
}

func TestSessionPrincipal_EmptyPresentBearerWithValidCookie_401(t *testing.T) {
	srv, _ := newSessionTestServer(t, acceptingResolver())
	w := doProbe(t, srv, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer ") // present, empty token
		withValidCookie(r)
	})
	assert.Equal(t, http.StatusUnauthorized, w.Code, "an empty-but-PRESENT Authorization: Bearer  is a present, failing credential")
}

func TestSessionPrincipal_EmptyPresentAPIKeyQueryWithValidCookie_401(t *testing.T) {
	srv, _ := newSessionTestServer(t, acceptingResolver())
	w := doProbe(t, srv, func(r *http.Request) {
		r.URL.RawQuery = "apikey=" // Query().Has("apikey") is true, value empty
		withValidCookie(r)
	})
	assert.Equal(t, http.StatusUnauthorized, w.Code, "an empty-but-PRESENT ?apikey= is a present, failing credential")
}

// --- FR-001 precedence: which source wins when more than one is present ---

func TestSessionPrincipal_ValidBearerWithCookie_BearerWins(t *testing.T) {
	srv, _ := newSessionTestServer(t, acceptingResolver())
	w := doProbe(t, srv, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+validBearerJWT)
		withValidCookie(r)
	})
	require.Equal(t, http.StatusOK, w.Code)
	resp := decodeProbe(t, w)
	assert.Equal(t, string(auth.CredentialKindBearerJWT), resp.CredentialKind, "the bearer must win over a cookie present on the same request")
	assert.Equal(t, "user-bearer", resp.UserID, "the bearer's principal, not the cookie's, must be installed")
}

func TestSessionPrincipal_XAPIKeyWithBearer_APIKeyDecides(t *testing.T) {
	srv, cfg := newSessionTestServer(t, acceptingResolver())
	w := doProbe(t, srv, func(r *http.Request) {
		r.Header.Set("X-API-Key", cfg.APIKey)
		r.Header.Set("Authorization", "Bearer "+validBearerJWT)
	})
	require.Equal(t, http.StatusOK, w.Code)
	resp := decodeProbe(t, w)
	assert.Equal(t, auth.AuthTypeAdmin, resp.Type, "X-API-Key must decide over a present bearer")
	assert.Equal(t, "", resp.CredentialKind, "the global admin API key path stamps no session CredentialKind")
}

// --- FR-001: the cookie alone is a valid principal ---

func TestSessionPrincipal_CookieAlone_IsThePrincipal(t *testing.T) {
	srv, _ := newSessionTestServer(t, acceptingResolver())
	w := doProbe(t, srv, withValidCookie)
	require.Equal(t, http.StatusOK, w.Code)
	resp := decodeProbe(t, w)
	assert.Equal(t, string(auth.CredentialKindCookie), resp.CredentialKind)
	assert.Equal(t, "user-cookie", resp.UserID)
}

// --- FR-001: no resolver installed (today's personal edition) behaves as today ---

func TestSessionPrincipal_NilResolver_CookieAlone_401(t *testing.T) {
	srv, _ := newSessionTestServer(t, nil) // personal edition: SetSessionPrincipalResolver is never called
	w := doProbe(t, srv, withValidCookie)
	assert.Equal(t, http.StatusUnauthorized, w.Code, "with no resolver installed, a cookie must be rejected exactly as it is today")
}

// --- FR-002: session principals never satisfy CanRevealSecrets ---

func TestSessionPrincipal_CanRevealSecrets_FalseForCookie(t *testing.T) {
	srv, _ := newSessionTestServer(t, acceptingResolver())
	w := doProbe(t, srv, withValidCookie)
	require.Equal(t, http.StatusOK, w.Code)
	resp := decodeProbe(t, w)
	assert.False(t, resp.CanReveal, "a cookie session principal must never satisfy CanRevealSecrets")
}

func TestSessionPrincipal_CanRevealSecrets_FalseForBearerJWT(t *testing.T) {
	srv, _ := newSessionTestServer(t, acceptingResolver())
	w := doProbe(t, srv, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+validBearerJWT)
	})
	require.Equal(t, http.StatusOK, w.Code)
	resp := decodeProbe(t, w)
	assert.Equal(t, auth.AuthTypeAdminUser, resp.Type, "this principal is an admin_user by Type")
	assert.False(t, resp.CanReveal, "a bearer-JWT session principal must never satisfy CanRevealSecrets, admin_user Type notwithstanding")
}

// TestSessionPrincipal_InfoWebUIURL_MaskedForAdminUser proves the effect on a
// real door: GET /api/v1/info builds web_ui_url from CanRevealSecrets
// (buildWebUIURLWithAPIKey, server.go:1524) — an admin_user SESSION principal
// must get the bare URL, never the one carrying ?apikey=<the admin key>, even
// though ac.IsAdmin() is true for admin_user.
func TestSessionPrincipal_InfoWebUIURL_MaskedForAdminUser(t *testing.T) {
	srv, cfg := newSessionTestServer(t, acceptingResolver())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", nil)
	req.Header.Set("Authorization", "Bearer "+validBearerJWT) // admin_user via the bearer resolver
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	webUIURL, _ := resp.Data["web_ui_url"].(string)
	assert.NotContains(t, webUIURL, cfg.APIKey, "an admin_user SESSION principal must never receive the admin API key in web_ui_url")
	assert.NotContains(t, webUIURL, "apikey=", "an admin_user SESSION principal must never receive an apikey= query on web_ui_url")
}

// TestSessionPrincipal_ConfigReveal_MaskedForAdminUser proves the same
// CanRevealSecrets predicate holds on GET /api/v1/config: reveal_secret_headers
// is on and the caller IsAdmin() (admin_user), yet the raw admin api_key must
// not appear in the published document because the caller is a SESSION
// principal.
func TestSessionPrincipal_ConfigReveal_MaskedForAdminUser(t *testing.T) {
	srv, cfg := newSessionTestServer(t, acceptingResolver())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	req.Header.Set("Authorization", "Bearer "+validBearerJWT)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), cfg.APIKey, "reveal_secret_headers is on and the caller IsAdmin(), but a SESSION principal must never receive the raw admin api_key")
}
