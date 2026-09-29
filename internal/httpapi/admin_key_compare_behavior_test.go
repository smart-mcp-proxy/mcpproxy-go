package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Behavior lock for the switch from `token == cfg.APIKey` to
// auth.ConstantTimeEqual in authenticateExplicitToken and authenticateBearer.
//
// The compare is timing-safe now, but the accept/reject verdict must be
// byte-for-byte what it was. The case that matters most is the empty
// credential: subtle.ConstantTimeCompare([]byte(""), []byte("")) returns 1,
// so dropping the emptiness guard during the rewrite would turn an absent
// credential into an admin match. Each source is exercised with an empty
// value for exactly that reason.
func TestAdminKeyCompare_BehaviorPreservedAcrossSources(t *testing.T) {
	const adminKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	// applySource installs a credential on the request the way a client would.
	type sourceFn func(r *http.Request, token string)

	xAPIKey := func(r *http.Request, token string) { r.Header.Set("X-API-Key", token) }
	bearer := func(r *http.Request, token string) { r.Header.Set("Authorization", "Bearer "+token) }
	query := func(r *http.Request, token string) {
		q := r.URL.Query()
		q.Set("apikey", token)
		r.URL.RawQuery = q.Encode()
	}

	sources := []struct {
		name  string
		apply sourceFn
	}{
		{"X-API-Key", xAPIKey},
		{"Authorization Bearer", bearer},
		{"apikey query param", query},
	}

	tokens := []struct {
		name     string
		token    string
		wantCode int
	}{
		{"correct key", adminKey, http.StatusOK},
		{"wrong key, same length", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdee", http.StatusUnauthorized},
		{"wrong key, shorter", "0123456789abcdef", http.StatusUnauthorized},
		{"wrong key, longer", adminKey + "ff", http.StatusUnauthorized},
		{"prefix of the key", adminKey[:32], http.StatusUnauthorized},
		{"empty value", "", http.StatusUnauthorized},
	}

	for _, src := range sources {
		for _, tc := range tokens {
			t.Run(src.name+"/"+tc.name, func(t *testing.T) {
				srv := newAdminKeyTestServer(t, adminKey)

				var capturedCtx *auth.AuthContext
				handler := srv.apiKeyAuthMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					capturedCtx = auth.AuthContextFromContext(r.Context())
					w.WriteHeader(http.StatusOK)
				}))

				req := httptest.NewRequest("GET", "/test", nil)
				src.apply(req, tc.token)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)

				assert.Equal(t, tc.wantCode, w.Code)
				if tc.wantCode == http.StatusOK {
					require.NotNil(t, capturedCtx, "AuthContext should be set for an accepted admin key")
					assert.True(t, capturedCtx.IsAdmin(), "a correct admin key must yield admin context")
				} else {
					assert.Nil(t, capturedCtx, "a rejected credential must never reach the handler")
				}
			})
		}
	}
}

// TestAdminKeyCompare_AgentTokenPrefixStillPreemptsAdminCompare pins the
// ordering the rewrite had to leave alone: an mcp_agt_-prefixed token is
// routed to the agent-token path before the admin compare runs, so it never
// falls through to the admin branch — including when the agent token store is
// unconfigured, which is its own distinct rejection, not an admin miss.
func TestAdminKeyCompare_AgentTokenPrefixStillPreemptsAdminCompare(t *testing.T) {
	const adminKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	agentToken := auth.TokenPrefixStr + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	for _, source := range []string{"X-API-Key", "Bearer"} {
		t.Run(source, func(t *testing.T) {
			srv := newAdminKeyTestServer(t, adminKey)

			reached := false
			handler := srv.apiKeyAuthMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest("GET", "/test", nil)
			if source == "X-API-Key" {
				req.Header.Set("X-API-Key", agentToken)
			} else {
				req.Header.Set("Authorization", "Bearer "+agentToken)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			assert.False(t, reached, "an agent token must not be admitted as admin")
			assert.Equal(t, http.StatusUnauthorized, w.Code)
			// The agent-token path (no token store configured here) says so
			// explicitly; the admin path's message is "Invalid or missing API
			// key". Seeing the former proves the prefix routing ran first.
			assert.Contains(t, w.Body.String(), "Agent tokens are not configured",
				"the mcp_agt_ prefix must route to the agent-token path, not the admin compare")
		})
	}
}

// TestAdminKeyCompare_EmptyConfiguredKeyAdmitsNobody covers the empty/empty
// case at middleware level, which the table above cannot reach because it
// always configures a real key.
//
// This is the failure mode a careless constant-time rewrite produces:
// subtle.ConstantTimeCompare of two empty slices returns 1, so an
// unconfigured server presented with an absent credential would hand out
// admin context. Two independent things prevent it — apiKeyAuthMiddleware
// rejects an empty cfg.APIKey before any compare runs, and ConstantTimeEqual
// rejects empty arguments — and this pins the outer one.
func TestAdminKeyCompare_EmptyConfiguredKeyAdmitsNobody(t *testing.T) {
	presented := []struct {
		name  string
		apply func(r *http.Request)
	}{
		{"no credential", func(*http.Request) {}},
		{"empty X-API-Key", func(r *http.Request) { r.Header.Set("X-API-Key", "") }},
		{"empty Bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer ") }},
		{"empty query apikey", func(r *http.Request) { r.URL.RawQuery = "apikey=" }},
		{"some key", func(r *http.Request) { r.Header.Set("X-API-Key", "anything") }},
	}

	for _, tc := range presented {
		t.Run(tc.name, func(t *testing.T) {
			srv := newAdminKeyTestServer(t, "") // no API key configured

			reached := false
			handler := srv.apiKeyAuthMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest("GET", "/test", nil)
			tc.apply(req)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			assert.False(t, reached, "an unconfigured API key must admit nobody")
			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})
	}
}

func newAdminKeyTestServer(t *testing.T, apiKey string) *Server {
	t.Helper()
	logger := zap.NewNop().Sugar()
	ctrl := &testControllerWithConfig{cfg: &config.Config{APIKey: apiKey}}
	return NewServer(ctrl, logger, nil)
}
