//go:build server

package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/broker"
)

// testRecordingSink captures broker audit events for test assertions.
type testRecordingSink struct {
	mu     sync.Mutex
	events []broker.AuditEvent
}

func (s *testRecordingSink) RecordBrokerEvent(_ context.Context, ev broker.AuditEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

func (s *testRecordingSink) last() broker.AuditEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) == 0 {
		return broker.AuditEvent{}
	}
	return s.events[len(s.events)-1]
}

const testUserB = "01HTEST00000000000000USERB"

// credTestStore builds an enabled AES credential store backed by a temp BBolt DB.
func credTestStore(t *testing.T) broker.CredentialStore {
	t.Helper()
	tmp := filepath.Join(t.TempDir(), "cred.db")
	db, err := bbolt.Open(tmp, 0600, &bbolt.Options{Timeout: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// 32 zero bytes is a valid AES-256 key for tests.
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	store, err := broker.NewBBoltAESStore(db, key, zap.NewNop())
	require.NoError(t, err)
	require.True(t, store.Enabled())
	return store
}

// credRouter wires the credential handlers behind an auth context injector.
func credRouter(h *CredentialHandlers, authCtx *auth.AuthContext) *chi.Mux {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.WithAuthContext(req.Context(), authCtx)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	h.RegisterRoutesWithPrefix(r, "/api/v1")
	return r
}

// brokerHTTPServer builds an HTTP-family broker upstream config for the given mode.
func brokerHTTPServer(name, mode string) *config.ServerConfig {
	ab := &config.AuthBrokerConfig{
		Mode:          mode,
		TokenEndpoint: "https://idp.example.com/token",
		ClientID:      "client-" + name,
		Scopes:        []string{"repo"},
	}
	if mode == config.AuthBrokerModeOAuthConnect {
		ab.AuthorizationEndpoint = "https://as.example.com/authorize"
	}
	return &config.ServerConfig{
		Name:       name,
		URL:        "https://" + name + ".example.com/mcp",
		Protocol:   "http",
		Shared:     true,
		AuthBroker: ab,
	}
}

func serverKeyFor(s *config.ServerConfig) string {
	return oauth.GenerateServerKey(s.Name, s.URL)
}

func TestCredentialsList_RedactsSecrets(t *testing.T) {
	store := credTestStore(t)
	srv := brokerHTTPServer("shared-gh", config.AuthBrokerModeTokenExchange)
	require.NoError(t, store.Put(testUserID, serverKeyFor(srv), &broker.UpstreamCredential{
		Type:         "oauth2",
		AccessToken:  "SECRET-ACCESS-TOKEN",
		RefreshToken: "SECRET-REFRESH-TOKEN",
		ExpiresAt:    time.Now().Add(time.Hour),
		Scopes:       []string{"repo"},
		TokenType:    "Bearer",
		ObtainedVia:  "token_exchange",
	}))

	h := NewCredentialHandlers(store, []*config.ServerConfig{srv}, nil, zap.NewNop().Sugar())
	r := credRouter(h, defaultAuthContext())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/credentials", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	// FR-026: secret material must never appear in the response.
	assert.NotContains(t, body, "SECRET-ACCESS-TOKEN")
	assert.NotContains(t, body, "SECRET-REFRESH-TOKEN")

	var resp CredentialListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Credentials, 1)
	got := resp.Credentials[0]
	assert.Equal(t, "shared-gh", got.Server)
	assert.Equal(t, credStatusConnected, got.Status)
	assert.Equal(t, config.AuthBrokerModeTokenExchange, got.Mode)
	assert.Equal(t, []string{"repo"}, got.Scopes)
	assert.NotNil(t, got.ExpiresAt)
}

func TestCredentialsList_Statuses(t *testing.T) {
	store := credTestStore(t)
	connected := brokerHTTPServer("connected-srv", config.AuthBrokerModeTokenExchange)
	expired := brokerHTTPServer("expired-srv", config.AuthBrokerModeTokenExchange)
	fresh := brokerHTTPServer("fresh-srv", config.AuthBrokerModeOAuthConnect)

	require.NoError(t, store.Put(testUserID, serverKeyFor(connected), &broker.UpstreamCredential{
		Type: "oauth2", AccessToken: "a", ExpiresAt: time.Now().Add(time.Hour),
	}))
	require.NoError(t, store.Put(testUserID, serverKeyFor(expired), &broker.UpstreamCredential{
		Type: "oauth2", AccessToken: "b", ExpiresAt: time.Now().Add(-time.Hour),
	}))

	h := NewCredentialHandlers(store, []*config.ServerConfig{connected, expired, fresh}, nil, zap.NewNop().Sugar())
	r := credRouter(h, defaultAuthContext())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/credentials", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp CredentialListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	byName := map[string]CredentialStatus{}
	for _, c := range resp.Credentials {
		byName[c.Server] = c
	}
	require.Len(t, byName, 3)
	assert.Equal(t, credStatusConnected, byName["connected-srv"].Status)
	assert.Equal(t, credStatusExpired, byName["expired-srv"].Status)
	assert.Equal(t, credStatusNotConnected, byName["fresh-srv"].Status)
	// oauth_connect upstreams expose an actionable connect path.
	assert.Equal(t, "/api/v1/user/credentials/fresh-srv/connect", byName["fresh-srv"].ConnectPath)
	assert.Empty(t, byName["connected-srv"].ConnectPath)
}

func TestCredentialsList_StoreDisabled(t *testing.T) {
	// Disabled store (no key): every broker upstream reports "unavailable".
	tmp := filepath.Join(t.TempDir(), "cred.db")
	db, err := bbolt.Open(tmp, 0600, &bbolt.Options{Timeout: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store, err := broker.NewBBoltAESStore(db, "", zap.NewNop())
	require.NoError(t, err)
	require.False(t, store.Enabled())

	srv := brokerHTTPServer("shared-gh", config.AuthBrokerModeTokenExchange)
	h := NewCredentialHandlers(store, []*config.ServerConfig{srv}, nil, zap.NewNop().Sugar())
	r := credRouter(h, defaultAuthContext())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/credentials", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp CredentialListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Credentials, 1)
	assert.Equal(t, credStatusUnavailable, resp.Credentials[0].Status)
}

func TestCredentialsDelete_Removes(t *testing.T) {
	store := credTestStore(t)
	srv := brokerHTTPServer("shared-gh", config.AuthBrokerModeTokenExchange)
	sk := serverKeyFor(srv)
	require.NoError(t, store.Put(testUserID, sk, &broker.UpstreamCredential{
		Type: "oauth2", AccessToken: "a", ExpiresAt: time.Now().Add(time.Hour),
	}))

	h := NewCredentialHandlers(store, []*config.ServerConfig{srv}, nil, zap.NewNop().Sugar())
	r := credRouter(h, defaultAuthContext())

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/user/credentials/shared-gh", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	_, err := store.Get(testUserID, sk)
	assert.ErrorIs(t, err, broker.ErrNotFound)
}

func TestCredentialsDelete_UnknownServer404(t *testing.T) {
	store := credTestStore(t)
	srv := brokerHTTPServer("shared-gh", config.AuthBrokerModeTokenExchange)
	h := NewCredentialHandlers(store, []*config.ServerConfig{srv}, nil, zap.NewNop().Sugar())
	r := credRouter(h, defaultAuthContext())

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/user/credentials/does-not-exist", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCredentials_CrossUserIsolation(t *testing.T) {
	store := credTestStore(t)
	srv := brokerHTTPServer("shared-gh", config.AuthBrokerModeTokenExchange)
	sk := serverKeyFor(srv)
	// User B has a valid credential.
	require.NoError(t, store.Put(testUserB, sk, &broker.UpstreamCredential{
		Type: "oauth2", AccessToken: "B-SECRET", ExpiresAt: time.Now().Add(time.Hour),
	}))

	h := NewCredentialHandlers(store, []*config.ServerConfig{srv}, nil, zap.NewNop().Sugar())
	// Act as user A.
	r := credRouter(h, auth.UserContext(testUserID, "a@example.com", "A", "google"))

	// FR-027: A must not see B's credential — A sees not_connected.
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/user/credentials", http.NoBody)
	listW := httptest.NewRecorder()
	r.ServeHTTP(listW, listReq)
	require.Equal(t, http.StatusOK, listW.Code)
	assert.NotContains(t, listW.Body.String(), "B-SECRET")
	var resp CredentialListResponse
	require.NoError(t, json.Unmarshal(listW.Body.Bytes(), &resp))
	require.Len(t, resp.Credentials, 1)
	assert.Equal(t, credStatusNotConnected, resp.Credentials[0].Status)

	// FR-027: A deleting must not remove B's credential.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/user/credentials/shared-gh", http.NoBody)
	delW := httptest.NewRecorder()
	r.ServeHTTP(delW, delReq)
	require.Equal(t, http.StatusOK, delW.Code)

	bCred, err := store.Get(testUserB, sk)
	require.NoError(t, err)
	assert.Equal(t, "B-SECRET", bCred.AccessToken)
}

func TestCredentialsConnect_Redirects(t *testing.T) {
	store := credTestStore(t)
	srv := brokerHTTPServer("connect-srv", config.AuthBrokerModeOAuthConnect)
	h := NewCredentialHandlers(store, []*config.ServerConfig{srv}, nil, zap.NewNop().Sugar())
	r := credRouter(h, defaultAuthContext())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/credentials/connect-srv/connect", http.NoBody)
	req.Host = "gw.example.com"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusFound, w.Code)
	loc, err := url.Parse(w.Header().Get("Location"))
	require.NoError(t, err)
	assert.Equal(t, "as.example.com", loc.Host)
	q := loc.Query()
	assert.NotEmpty(t, q.Get("state"))
	assert.Equal(t, "client-connect-srv", q.Get("client_id"))
	assert.NotEmpty(t, q.Get("code_challenge"))
	assert.Contains(t, q.Get("redirect_uri"), "/api/v1/user/credentials/connect-srv/callback")
}

func TestCredentialsConnect_NonConnectMode400(t *testing.T) {
	store := credTestStore(t)
	srv := brokerHTTPServer("xchg-srv", config.AuthBrokerModeTokenExchange)
	h := NewCredentialHandlers(store, []*config.ServerConfig{srv}, nil, zap.NewNop().Sugar())
	r := credRouter(h, defaultAuthContext())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/credentials/xchg-srv/connect", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCredentialsConnectCallback_StoresCredential(t *testing.T) {
	// Upstream token endpoint that mints a credential for any code.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"NEW-ACCESS","refresh_token":"NEW-REFRESH","token_type":"Bearer","expires_in":3600,"scope":"repo"}`)
	}))
	defer ts.Close()

	store := credTestStore(t)
	srv := brokerHTTPServer("connect-srv", config.AuthBrokerModeOAuthConnect)
	srv.AuthBroker.TokenEndpoint = ts.URL
	sk := serverKeyFor(srv)

	h := NewCredentialHandlers(store, []*config.ServerConfig{srv}, nil, zap.NewNop().Sugar())
	r := credRouter(h, defaultAuthContext())

	// Step 1: connect → capture state from the redirect.
	connReq := httptest.NewRequest(http.MethodGet, "/api/v1/user/credentials/connect-srv/connect", http.NoBody)
	connReq.Host = "gw.example.com"
	connW := httptest.NewRecorder()
	r.ServeHTTP(connW, connReq)
	require.Equal(t, http.StatusFound, connW.Code)
	loc, err := url.Parse(connW.Header().Get("Location"))
	require.NoError(t, err)
	state := loc.Query().Get("state")
	require.NotEmpty(t, state)

	// Step 2: callback with code+state on the same handler instance.
	cbURL := "/api/v1/user/credentials/connect-srv/callback?code=auth-code&state=" + url.QueryEscape(state)
	cbReq := httptest.NewRequest(http.MethodGet, cbURL, http.NoBody)
	cbReq.Host = "gw.example.com"
	cbW := httptest.NewRecorder()
	r.ServeHTTP(cbW, cbReq)
	require.Equal(t, http.StatusFound, cbW.Code)

	// The per-user credential is now persisted.
	cred, err := store.Get(testUserID, sk)
	require.NoError(t, err)
	assert.Equal(t, "NEW-ACCESS", cred.AccessToken)
	assert.Equal(t, "NEW-REFRESH", cred.RefreshToken)
}

func TestCredentialsCallback_DeniedByUpstream(t *testing.T) {
	store := credTestStore(t)
	srv := brokerHTTPServer("connect-srv", config.AuthBrokerModeOAuthConnect)
	sk := serverKeyFor(srv)
	sink := &testRecordingSink{}
	h := NewCredentialHandlers(store, []*config.ServerConfig{srv}, sink, zap.NewNop().Sugar())
	r := credRouter(h, defaultAuthContext())

	// Begin a flow to register a state.
	connReq := httptest.NewRequest(http.MethodGet, "/api/v1/user/credentials/connect-srv/connect", http.NoBody)
	connReq.Host = "gw.example.com"
	connW := httptest.NewRecorder()
	r.ServeHTTP(connW, connReq)
	require.Equal(t, http.StatusFound, connW.Code)
	loc, _ := url.Parse(connW.Header().Get("Location"))
	state := loc.Query().Get("state")

	cbURL := "/api/v1/user/credentials/connect-srv/callback?error=access_denied&state=" + url.QueryEscape(state)
	cbReq := httptest.NewRequest(http.MethodGet, cbURL, http.NoBody)
	cbW := httptest.NewRecorder()
	r.ServeHTTP(cbW, cbReq)
	// Denied flow redirects back to the UI and stores nothing.
	require.Equal(t, http.StatusFound, cbW.Code)
	_, err := store.Get(testUserID, sk)
	assert.ErrorIs(t, err, broker.ErrNotFound)

	// FR-029: audit event carries a coerced, secret-free reason — the known
	// access_denied code passes through the sanitizer unchanged. Deny prepends
	// "connect denied: " so the full reason is "connect denied: access_denied".
	ev := sink.last()
	assert.Equal(t, "connect denied: access_denied", ev.Reason)
	assert.Equal(t, broker.AuditOutcomeFailure, ev.Outcome)
}

func TestCredentialsCallback_Denied_UnknownErrorCoerced(t *testing.T) {
	store := credTestStore(t)
	srv := brokerHTTPServer("connect-srv", config.AuthBrokerModeOAuthConnect)
	sink := &testRecordingSink{}
	h := NewCredentialHandlers(store, []*config.ServerConfig{srv}, sink, zap.NewNop().Sugar())
	r := credRouter(h, defaultAuthContext())

	// Begin a flow to register a state.
	connReq := httptest.NewRequest(http.MethodGet, "/api/v1/user/credentials/connect-srv/connect", http.NoBody)
	connReq.Host = "gw.example.com"
	connW := httptest.NewRecorder()
	r.ServeHTTP(connW, connReq)
	require.Equal(t, http.StatusFound, connW.Code)
	loc, _ := url.Parse(connW.Header().Get("Location"))
	state := loc.Query().Get("state")

	// An arbitrary/non-standard error code — must be coerced to the generic label.
	cbURL := "/api/v1/user/credentials/connect-srv/callback?error=some_arbitrary_upstream_error&state=" + url.QueryEscape(state)
	cbReq := httptest.NewRequest(http.MethodGet, cbURL, http.NoBody)
	cbW := httptest.NewRecorder()
	r.ServeHTTP(cbW, cbReq)
	require.Equal(t, http.StatusFound, cbW.Code)

	// FR-029: the audit reason must be the generic coerced label, not the raw
	// upstream error string. Deny prepends "connect denied: ".
	ev := sink.last()
	assert.Equal(t, "connect denied: authorization_denied", ev.Reason)
	assert.Equal(t, broker.AuditOutcomeFailure, ev.Outcome)
}

// TestCredentialsCallback_Denied_RedirectSanitized proves the callback's
// browser redirect never reflects a raw, AS-controlled error string. The
// upstream authorization server fully controls the ?error= query value, so a
// hostile/misconfigured AS could embed secrets or echoed input there; the
// redirect's credential_error must carry only the coerced label (FR-029/SC-005).
func TestCredentialsCallback_Denied_RedirectSanitized(t *testing.T) {
	store := credTestStore(t)
	srv := brokerHTTPServer("connect-srv", config.AuthBrokerModeOAuthConnect)
	sink := &testRecordingSink{}
	h := NewCredentialHandlers(store, []*config.ServerConfig{srv}, sink, zap.NewNop().Sugar())
	r := credRouter(h, defaultAuthContext())

	// Begin a flow to register a state.
	connReq := httptest.NewRequest(http.MethodGet, "/api/v1/user/credentials/connect-srv/connect", http.NoBody)
	connReq.Host = "gw.example.com"
	connW := httptest.NewRecorder()
	r.ServeHTTP(connW, connReq)
	require.Equal(t, http.StatusFound, connW.Code)
	loc, _ := url.Parse(connW.Header().Get("Location"))
	state := loc.Query().Get("state")

	const leak = "leaked_secret_AKIAIOSFODNN7EXAMPLE"
	cbURL := "/api/v1/user/credentials/connect-srv/callback?error=" + url.QueryEscape(leak) + "&state=" + url.QueryEscape(state)
	cbReq := httptest.NewRequest(http.MethodGet, cbURL, http.NoBody)
	cbW := httptest.NewRecorder()
	r.ServeHTTP(cbW, cbReq)
	require.Equal(t, http.StatusFound, cbW.Code)

	redirect, err := url.Parse(cbW.Header().Get("Location"))
	require.NoError(t, err)
	gotErr := redirect.Query().Get("credential_error")
	assert.Equal(t, "authorization_denied", gotErr,
		"redirect must carry the coerced label, not the raw upstream error")
	assert.NotContains(t, cbW.Header().Get("Location"), leak,
		"redirect must not reflect the raw AS-controlled error string")
}

func TestCredentials_Unauthenticated(t *testing.T) {
	store := credTestStore(t)
	srv := brokerHTTPServer("shared-gh", config.AuthBrokerModeTokenExchange)
	h := NewCredentialHandlers(store, []*config.ServerConfig{srv}, nil, zap.NewNop().Sugar())
	// Empty auth context → unauthenticated.
	r := credRouter(h, &auth.AuthContext{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/credentials", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
