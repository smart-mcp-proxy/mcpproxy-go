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
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
)

// Spec 098 T010: the REST auth path used to build the agent AuthContext by
// hand and omit ProfilePin, so a profile-pinned token was evaluated against the
// unpinned server set. The pin must reach the request context, because the
// preflight evaluation scope is token scope ∩ token pin ∩ requested profile.
func TestAPIKeyAuth_AgentToken_PropagatesProfilePin(t *testing.T) {
	logger := zap.NewNop().Sugar()
	tmpDir := t.TempDir()
	_, err := auth.GetOrCreateHMACKey(tmpDir)
	require.NoError(t, err)

	rawToken, err := auth.GenerateToken()
	require.NoError(t, err)

	agentToken := &auth.AgentToken{
		Name:           "pinned-agent",
		TokenPrefix:    auth.TokenPrefix(rawToken),
		AllowedServers: []string{"github", "fs"},
		Permissions:    []string{auth.PermRead},
		ProfilePin:     "work",
		ExpiresAt:      time.Now().Add(24 * time.Hour),
	}

	store := &testTokenStore{
		validateFunc: func(token string, _ []byte) (*auth.AgentToken, error) {
			if token == rawToken {
				return agentToken, nil
			}
			return nil, fmt.Errorf("token not found")
		},
	}

	srv := NewServer(&testControllerWithConfig{cfg: &config.Config{APIKey: "admin-key"}}, logger, nil)
	srv.SetTokenStore(store, tmpDir)

	var capturedCtx *auth.AuthContext
	handler := srv.apiKeyAuthMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCtx = auth.AuthContextFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-API-Key", rawToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, capturedCtx)
	assert.Equal(t, "work", capturedCtx.ProfilePin, "the token's profile pin must survive REST authentication")
	assert.Equal(t, []string{"github", "fs"}, capturedCtx.AllowedServers)
}

// The end-to-end consequence: with the pin propagated, the evaluation scope is
// the intersection of the three restrictions — never wider than the pin.
func TestAgentTokenScope_IntersectionUsesPropagatedPin(t *testing.T) {
	authCtx := (&auth.AgentToken{
		Name:           "pinned-agent",
		AllowedServers: []string{"github", "fs", "jira"},
		ProfilePin:     "work",
	}).AuthContext()

	scope := preflight.ResolveScope(preflight.ScopeInputs{
		TokenServers:            authCtx.AllowedServers,
		TokenPinName:            authCtx.ProfilePin,
		TokenPinServers:         []string{"github", "jira"},
		RequestedProfileName:    "review",
		RequestedProfileServers: []string{"github", "fs"},
	})

	assert.True(t, scope.Allows("github"))
	assert.False(t, scope.Allows("fs"), "excluded by the token pin")
	assert.False(t, scope.Allows("jira"), "excluded by the requested profile")

	// Dropping the pin (the pre-fix behavior) would have widened the scope —
	// this is the regression the propagation prevents.
	unpinned := preflight.ResolveScope(preflight.ScopeInputs{
		TokenServers:            authCtx.AllowedServers,
		RequestedProfileName:    "review",
		RequestedProfileServers: []string{"github", "fs"},
	})
	assert.True(t, unpinned.Allows("fs"), "sanity: without the pin, fs would have been in scope")
}
