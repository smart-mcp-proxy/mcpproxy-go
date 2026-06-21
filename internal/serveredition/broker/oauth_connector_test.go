//go:build server

package broker

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

// connectorTestConfig returns a ConnectorConfig pointed at the given token and
// authorization endpoints. The authorization endpoint never receives traffic in
// tests (the browser would); only its host/path is reflected into the URL.
func connectorTestConfig(tokenEndpoint string) ConnectorConfig {
	return ConnectorConfig{
		ServerName:            "github-mcp",
		ServerURL:             "https://api.github.com/mcp",
		AuthorizationEndpoint: "https://auth.example.com/authorize",
		TokenEndpoint:         tokenEndpoint,
		ClientID:              "gateway-client-id",
		ClientSecret:          "gateway-client-secret",
		Scopes:                []string{"repo", "read:user"},
		RedirectURI:           "https://gw.example.com/api/v1/user/credentials/callback",
		Resource:              "https://api.github.com/mcp",
	}
}

// s256 returns the base64url-encoded SHA-256 of the given verifier string,
// i.e. the expected PKCE code_challenge for code_challenge_method=S256.
func s256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// newConnectorTestStore returns an enabled in-memory credential store.
func newConnectorTestStore(t *testing.T) CredentialStore {
	t.Helper()
	db := openTestDB(t)
	return newTestStore(t, db, newTestKey(t))
}

func newTestConnector(t *testing.T, cfg ConnectorConfig) *OAuthConnector {
	t.Helper()
	c, err := NewOAuthConnector(newConnectorTestStore(t), cfg, zap.NewNop(), nil)
	if err != nil {
		t.Fatalf("NewOAuthConnector: %v", err)
	}
	return c
}

// mockTokenServer stands in for the upstream AS token endpoint. It records the
// last received form values and replies with a canned token response.
type mockTokenServer struct {
	srv          *httptest.Server
	lastForm     url.Values
	accessToken  string
	refreshToken string
	expiresIn    int
	status       int
}

func newMockTokenServer(t *testing.T) *mockTokenServer {
	t.Helper()
	m := &mockTokenServer{
		accessToken:  "upstream-access-token",
		refreshToken: "upstream-refresh-token",
		expiresIn:    3600,
		status:       http.StatusOK,
	}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		m.lastForm = r.PostForm
		if m.status != http.StatusOK {
			w.WriteHeader(m.status)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"access_token": m.accessToken,
			"token_type":   "Bearer",
			"expires_in":   m.expiresIn,
			"scope":        "repo read:user",
		}
		if m.refreshToken != "" {
			resp["refresh_token"] = m.refreshToken
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func TestOAuthConnector_BuildAuthorizationURL(t *testing.T) {
	c := newTestConnector(t, connectorTestConfig("https://unused.example.com/token"))

	authURL, state, err := c.BuildAuthorizationURL("user-alice")
	if err != nil {
		t.Fatalf("BuildAuthorizationURL: %v", err)
	}
	if state == "" {
		t.Fatal("expected non-empty state")
	}

	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse authURL: %v", err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != "https://auth.example.com/authorize" {
		t.Errorf("authorize endpoint = %q, want https://auth.example.com/authorize", got)
	}
	q := u.Query()
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q, want code", q.Get("response_type"))
	}
	if q.Get("client_id") != "gateway-client-id" {
		t.Errorf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("redirect_uri") != "https://gw.example.com/api/v1/user/credentials/callback" {
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	if q.Get("scope") != "repo read:user" {
		t.Errorf("scope = %q, want %q", q.Get("scope"), "repo read:user")
	}
	if q.Get("state") != state {
		t.Errorf("state in URL = %q, returned %q", q.Get("state"), state)
	}
	if q.Get("code_challenge") == "" {
		t.Error("expected non-empty code_challenge")
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", q.Get("code_challenge_method"))
	}
	if q.Get("resource") != "https://api.github.com/mcp" {
		t.Errorf("resource = %q", q.Get("resource"))
	}
}

func TestOAuthConnector_BuildAuthorizationURL_UniquePerFlow(t *testing.T) {
	c := newTestConnector(t, connectorTestConfig("https://unused.example.com/token"))
	url1, s1, _ := c.BuildAuthorizationURL("user-a")
	url2, s2, _ := c.BuildAuthorizationURL("user-b")
	if s1 == s2 {
		t.Error("expected distinct state per flow")
	}
	ch1 := mustQuery(t, url1, "code_challenge")
	ch2 := mustQuery(t, url2, "code_challenge")
	if ch1 == ch2 {
		t.Error("expected distinct PKCE challenge per flow")
	}
}

func TestOAuthConnector_Complete_StoresEncryptedToken(t *testing.T) {
	m := newMockTokenServer(t)
	cfg := connectorTestConfig(m.srv.URL)
	store := newConnectorTestStore(t)
	c, err := NewOAuthConnector(store, cfg, zap.NewNop(), nil)
	if err != nil {
		t.Fatalf("NewOAuthConnector: %v", err)
	}

	authURL, state, err := c.BuildAuthorizationURL("user-alice")
	if err != nil {
		t.Fatalf("BuildAuthorizationURL: %v", err)
	}
	challenge := mustQuery(t, authURL, "code_challenge")

	cred, err := c.Complete(context.Background(), state, "auth-code-xyz")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if cred.AccessToken != "upstream-access-token" {
		t.Errorf("AccessToken = %q", cred.AccessToken)
	}
	if cred.RefreshToken != "upstream-refresh-token" {
		t.Errorf("RefreshToken = %q", cred.RefreshToken)
	}
	if cred.ObtainedVia != "connect_flow" {
		t.Errorf("ObtainedVia = %q, want connect_flow", cred.ObtainedVia)
	}
	if cred.ExpiresAt.IsZero() {
		t.Error("expected non-zero ExpiresAt from expires_in")
	}

	// PKCE roundtrip: token endpoint must have received the verifier matching
	// the challenge from the authorize URL.
	gotVerifier := m.lastForm.Get("code_verifier")
	if gotVerifier == "" {
		t.Fatal("token endpoint received no code_verifier")
	}
	if s256(gotVerifier) != challenge {
		t.Errorf("PKCE mismatch: S256(verifier)=%q challenge=%q", s256(gotVerifier), challenge)
	}
	if m.lastForm.Get("grant_type") != "authorization_code" {
		t.Errorf("grant_type = %q", m.lastForm.Get("grant_type"))
	}
	if m.lastForm.Get("code") != "auth-code-xyz" {
		t.Errorf("code = %q", m.lastForm.Get("code"))
	}

	// Stored per-user, retrievable, ObtainedVia preserved through the encrypted
	// round-trip.
	serverKey := c.ServerKey()
	stored, err := store.Get("user-alice", serverKey)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if stored.AccessToken != "upstream-access-token" || stored.ObtainedVia != "connect_flow" {
		t.Errorf("stored cred wrong: %+v", stored)
	}
}

func TestOAuthConnector_Complete_InvalidState(t *testing.T) {
	m := newMockTokenServer(t)
	store := newConnectorTestStore(t)
	c, _ := NewOAuthConnector(store, connectorTestConfig(m.srv.URL), zap.NewNop(), nil)

	if _, err := c.Complete(context.Background(), "bogus-state", "code"); err == nil {
		t.Fatal("expected error for unknown state")
	}
	if m.lastForm != nil {
		t.Error("token endpoint should not be called for an invalid state")
	}
}

func TestOAuthConnector_Complete_ExpiredState(t *testing.T) {
	m := newMockTokenServer(t)
	store := newConnectorTestStore(t)
	c, _ := NewOAuthConnector(store, connectorTestConfig(m.srv.URL), zap.NewNop(), nil)

	base := time.Now()
	c.now = func() time.Time { return base }
	_, state, err := c.BuildAuthorizationURL("user-alice")
	if err != nil {
		t.Fatalf("BuildAuthorizationURL: %v", err)
	}
	// Advance past the state TTL.
	c.now = func() time.Time { return base.Add(c.stateTTL + time.Minute) }

	if _, err := c.Complete(context.Background(), state, "code"); err == nil {
		t.Fatal("expected error for expired state")
	}
	if _, err := store.Get("user-alice", c.ServerKey()); err == nil {
		t.Error("nothing should be stored for an expired flow")
	}
}

func TestOAuthConnector_Complete_StateIsOneTime(t *testing.T) {
	m := newMockTokenServer(t)
	c, _ := NewOAuthConnector(newConnectorTestStore(t), connectorTestConfig(m.srv.URL), zap.NewNop(), nil)

	_, state, _ := c.BuildAuthorizationURL("user-alice")
	if _, err := c.Complete(context.Background(), state, "code"); err != nil {
		t.Fatalf("first Complete: %v", err)
	}
	if _, err := c.Complete(context.Background(), state, "code"); err == nil {
		t.Fatal("expected error reusing a consumed state")
	}
}

func TestOAuthConnector_Deny_StoresNothing(t *testing.T) {
	m := newMockTokenServer(t)
	store := newConnectorTestStore(t)
	c, _ := NewOAuthConnector(store, connectorTestConfig(m.srv.URL), zap.NewNop(), nil)

	_, state, _ := c.BuildAuthorizationURL("user-alice")
	if err := c.Deny(state, "access_denied"); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	if _, err := store.Get("user-alice", c.ServerKey()); err == nil {
		t.Error("denied consent must store nothing")
	}
	// State is cleared: a follow-up Complete must fail.
	if _, err := c.Complete(context.Background(), state, "code"); err == nil {
		t.Error("expected error completing a denied/cleared flow")
	}
	if m.lastForm != nil {
		t.Error("token endpoint must not be called on denial")
	}
}

func TestOAuthConnector_Refresh(t *testing.T) {
	m := newMockTokenServer(t)
	m.accessToken = "refreshed-access-token"
	m.refreshToken = "" // emulate AS that does not rotate the refresh token
	store := newConnectorTestStore(t)
	c, _ := NewOAuthConnector(store, connectorTestConfig(m.srv.URL), zap.NewNop(), nil)

	// Seed an existing connect-flow credential with a refresh token.
	seed := &UpstreamCredential{
		Type:         "oauth2",
		AccessToken:  "old-access-token",
		RefreshToken: "seed-refresh-token",
		ExpiresAt:    time.Now().Add(-time.Minute), // expired
		ObtainedVia:  "connect_flow",
	}
	if err := store.Put("user-alice", c.ServerKey(), seed); err != nil {
		t.Fatalf("seed Put: %v", err)
	}

	cred, err := c.Refresh(context.Background(), "user-alice")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if cred.AccessToken != "refreshed-access-token" {
		t.Errorf("AccessToken = %q, want refreshed-access-token", cred.AccessToken)
	}
	// Refresh token preserved when the AS omits a new one.
	if cred.RefreshToken != "seed-refresh-token" {
		t.Errorf("RefreshToken = %q, want preserved seed-refresh-token", cred.RefreshToken)
	}
	if cred.ObtainedVia != "connect_flow" {
		t.Errorf("ObtainedVia = %q", cred.ObtainedVia)
	}
	if m.lastForm.Get("grant_type") != "refresh_token" {
		t.Errorf("grant_type = %q, want refresh_token", m.lastForm.Get("grant_type"))
	}
	if m.lastForm.Get("refresh_token") != "seed-refresh-token" {
		t.Errorf("sent refresh_token = %q", m.lastForm.Get("refresh_token"))
	}

	// Persisted.
	stored, err := store.Get("user-alice", c.ServerKey())
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if stored.AccessToken != "refreshed-access-token" {
		t.Errorf("stored AccessToken = %q", stored.AccessToken)
	}
}

func TestOAuthConnector_Refresh_NoRefreshToken(t *testing.T) {
	m := newMockTokenServer(t)
	store := newConnectorTestStore(t)
	c, _ := NewOAuthConnector(store, connectorTestConfig(m.srv.URL), zap.NewNop(), nil)

	seed := &UpstreamCredential{AccessToken: "at", ObtainedVia: "connect_flow"} // no refresh token
	if err := store.Put("user-alice", c.ServerKey(), seed); err != nil {
		t.Fatalf("seed Put: %v", err)
	}
	if _, err := c.Refresh(context.Background(), "user-alice"); err == nil {
		t.Fatal("expected error refreshing a credential with no refresh token")
	}
}

func TestOAuthConnector_Complete_TokenEndpointError(t *testing.T) {
	m := newMockTokenServer(t)
	m.status = http.StatusBadRequest
	store := newConnectorTestStore(t)
	c, _ := NewOAuthConnector(store, connectorTestConfig(m.srv.URL), zap.NewNop(), nil)

	_, state, _ := c.BuildAuthorizationURL("user-alice")
	if _, err := c.Complete(context.Background(), state, "code"); err == nil {
		t.Fatal("expected error when token endpoint returns 400")
	}
	if _, err := store.Get("user-alice", c.ServerKey()); err == nil {
		t.Error("nothing should be stored on token-exchange failure")
	}
}

// TestOAuthConnector_Complete_TokenEndpointError_NoBodyLeak proves that a
// non-200 token-endpoint response body — which an upstream AS controls and could
// stuff with access/refresh tokens or echoed input — never reaches the error
// returned to (and logged by) callers. Only the HTTP status and an allowlisted
// OAuth error code may surface (FR-029 / SC-005).
func TestOAuthConnector_Complete_TokenEndpointError_NoBodyLeak(t *testing.T) {
	const leak = "super-secret-access-token-AKIAIOSFODNN7EXAMPLE"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		// A hostile AS reflecting secrets/free-text in both the body and the
		// error_description field.
		_, _ = w.Write([]byte(`{"error":"` + leak + `","error_description":"` + leak + `","access_token":"` + leak + `"}`))
	}))
	t.Cleanup(srv.Close)

	store := newConnectorTestStore(t)
	c, _ := NewOAuthConnector(store, connectorTestConfig(srv.URL), zap.NewNop(), nil)

	_, state, _ := c.BuildAuthorizationURL("user-alice")
	_, err := c.Complete(context.Background(), state, "code")
	if err == nil {
		t.Fatal("expected error when token endpoint returns 400")
	}
	if strings.Contains(err.Error(), leak) {
		t.Errorf("error leaks upstream response body: %q", err.Error())
	}
	// A non-allowlisted error code must be dropped entirely (only the status remains).
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should name the HTTP status: %q", err.Error())
	}
}

// TestOAuthConnector_TokenEndpointError_AllowlistedCode confirms a standard
// RFC 6749 §5.2 error code does pass through (so operators still get a useful
// reason), while everything else is dropped.
func TestOAuthConnector_TokenEndpointError_AllowlistedCode(t *testing.T) {
	allow := sanitizedTokenEndpointError(http.StatusBadRequest, []byte(`{"error":"invalid_grant"}`))
	if !strings.Contains(allow.Error(), "invalid_grant") {
		t.Errorf("allowlisted code should surface: %q", allow.Error())
	}
	drop := sanitizedTokenEndpointError(http.StatusBadRequest, []byte(`{"error":"AKIAIOSFODNN7EXAMPLE"}`))
	if strings.Contains(drop.Error(), "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("non-allowlisted code must be dropped: %q", drop.Error())
	}
}

func TestNewOAuthConnector_Validation(t *testing.T) {
	store := newConnectorTestStore(t)
	base := connectorTestConfig("https://idp/token")
	cases := map[string]func(*ConnectorConfig){
		"missing authorization_endpoint": func(c *ConnectorConfig) { c.AuthorizationEndpoint = "" },
		"missing token_endpoint":         func(c *ConnectorConfig) { c.TokenEndpoint = "" },
		"missing client_id":              func(c *ConnectorConfig) { c.ClientID = "" },
		"missing redirect_uri":           func(c *ConnectorConfig) { c.RedirectURI = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := base
			mutate(&cfg)
			if _, err := NewOAuthConnector(store, cfg, zap.NewNop(), nil); err == nil {
				t.Errorf("expected validation error for %s", name)
			}
		})
	}
}

func mustQuery(t *testing.T, rawURL, key string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse %q: %v", rawURL, err)
	}
	v := u.Query().Get(key)
	if v == "" {
		t.Fatalf("missing query param %q in %q", key, rawURL)
	}
	return v
}
