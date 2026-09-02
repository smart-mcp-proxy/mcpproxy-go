//go:build server

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
)

// TestAuthEndpoints_RejectAgentTierContext: POST /api/v1/auth/token mints a
// full user session JWT from the caller's context. Once an agent token carries
// its owner's UserID, a gate that only checks GetUserID() != "" would let a
// scoped, read-only agent token upgrade itself into its owner's session
// credential. Both endpoints must require the user TIER.
//
// The test registers on the PRODUCTION route table shape (the same routes
// setup.go mounts) and runs a positive control first, so a 401 from a
// mis-typed path cannot masquerade as the guard working.
//
// BITES: with UserID plumbed through AuthContext() and the IsUser() guards
// removed, /auth/me returns the owner's profile (200) and /auth/token returns
// a signed JWT for the owner (200).
func TestAuthEndpoints_RejectAgentTierContext(t *testing.T) {
	endpoints, store := authTestSetup(t)

	user := &users.User{
		ID:                testUserID,
		Email:             "owner@example.com",
		DisplayName:       "Owner",
		Provider:          "google",
		ProviderSubjectID: "google-sub-owner",
		CreatedAt:         time.Now().UTC(),
		LastLoginAt:       time.Now().UTC(),
	}
	require.NoError(t, store.CreateUser(user))

	newRouter := func(ac *auth.AuthContext) *chi.Mux {
		r := chi.NewRouter()
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				next.ServeHTTP(w, req.WithContext(auth.WithAuthContext(req.Context(), ac)))
			})
		})
		endpoints.RegisterRoutesWithPrefix(r, "/api/v1")
		return r
	}

	call := func(r *chi.Mux, method, target string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, http.NoBody)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// Positive control: as the USER, both routes work. This proves the routes
	// are registered at these paths and the fixture user exists, so the 401s
	// below are the guard and not a routing mistake.
	userRouter := newRouter(auth.UserContext(user.ID, user.Email, user.DisplayName, user.Provider))
	require.Equal(t, http.StatusOK, call(userRouter, http.MethodGet, "/api/v1/auth/me").Code)
	ctlToken := call(userRouter, http.MethodPost, "/api/v1/auth/token")
	require.Equal(t, http.StatusOK, ctlToken.Code)
	require.Contains(t, ctlToken.Body.String(), `"token"`)

	// The agent tier, carrying the SAME owner id.
	agent := (&auth.AgentToken{
		Name:        "ci",
		TokenPrefix: "mcp_agt_abcd",
		Permissions: []string{auth.PermRead},
		UserID:      user.ID,
	}).AuthContext()
	require.Equal(t, user.ID, agent.GetUserID(), "the fixture must actually carry the owner's id")
	agentRouter := newRouter(agent)

	me := call(agentRouter, http.MethodGet, "/api/v1/auth/me")
	assert.Equal(t, http.StatusUnauthorized, me.Code,
		"an agent token read its owner's profile (body: %s)", me.Body.String())

	tok := call(agentRouter, http.MethodPost, "/api/v1/auth/token")
	assert.Equal(t, http.StatusUnauthorized, tok.Code,
		"an agent token minted a user session JWT for its owner (body: %s)", tok.Body.String())
	assert.NotContains(t, tok.Body.String(), `"token"`,
		"the response carried a bearer token to an agent-tier caller")
}
