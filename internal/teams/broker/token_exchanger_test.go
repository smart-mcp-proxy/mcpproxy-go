//go:build server

package broker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

const (
	testUserID  = "user-42"
	testSrvKey  = "server-key-abc"
	testSubject = "subject-token-SECRET-do-not-leak"
)

// seedSubjectToken stores an IdP subject token for the user (serverKey == "")
// the way T3 would, so the exchanger can read it.
func seedSubjectToken(t *testing.T, store CredentialStore, token string) {
	t.Helper()
	if err := store.Put(testUserID, "", &UpstreamCredential{
		Type:        "idp_subject_token",
		AccessToken: token,
	}); err != nil {
		t.Fatalf("seed subject token: %v", err)
	}
}

// newExchangeServer spins up a mock token endpoint. handler receives the parsed
// POST form and writes the response.
func newExchangeServer(t *testing.T, handler func(t *testing.T, form url.Values, w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
			t.Errorf("expected form content-type, got %q", ct)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		handler(t, r.PostForm, w)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeJSON(w http.ResponseWriter, status int, body map[string]interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func TestTokenExchanger_RFC8693HappyPath(t *testing.T) {
	store := newTestStore(t, openTestDB(t), newTestKey(t))
	seedSubjectToken(t, store, testSubject)

	srv := newExchangeServer(t, func(t *testing.T, form url.Values, w http.ResponseWriter) {
		t.Helper()
		if got := form.Get("grant_type"); got != "urn:ietf:params:oauth:grant-type:token-exchange" {
			t.Errorf("grant_type = %q", got)
		}
		if got := form.Get("subject_token"); got != testSubject {
			t.Errorf("subject_token = %q", got)
		}
		if got := form.Get("subject_token_type"); got != "urn:ietf:params:oauth:token-type:access_token" {
			t.Errorf("subject_token_type = %q", got)
		}
		if got := form.Get("resource"); got != "https://upstream.example.com" {
			t.Errorf("resource = %q", got)
		}
		if got := form.Get("scope"); got != "read write" {
			t.Errorf("scope = %q", got)
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"access_token":      "exchanged-access-token",
			"issued_token_type": "urn:ietf:params:oauth:token-type:access_token",
			"token_type":        "Bearer",
			"expires_in":        3600,
			"scope":             "read write",
		})
	})

	cfg := &config.AuthBrokerConfig{
		Mode:          config.AuthBrokerModeTokenExchange,
		TokenEndpoint: srv.URL,
		Resource:      "https://upstream.example.com",
		Scopes:        []string{"read", "write"},
		ClientID:      "gateway-client",
		ClientSecret:  "gateway-secret",
	}

	ex := NewTokenExchanger(store, nil, zap.NewNop())
	cred, err := ex.Exchange(context.Background(), testUserID, testSrvKey, cfg)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if cred.AccessToken != "exchanged-access-token" {
		t.Errorf("AccessToken = %q", cred.AccessToken)
	}
	if cred.TokenType != "Bearer" {
		t.Errorf("TokenType = %q", cred.TokenType)
	}
	if cred.Audience != "https://upstream.example.com" {
		t.Errorf("Audience = %q", cred.Audience)
	}
	if cred.ObtainedVia != "token_exchange" {
		t.Errorf("ObtainedVia = %q", cred.ObtainedVia)
	}
	if len(cred.Scopes) != 2 || cred.Scopes[0] != "read" || cred.Scopes[1] != "write" {
		t.Errorf("Scopes = %v", cred.Scopes)
	}
	// expires_in 3600 -> ExpiresAt roughly an hour out.
	if d := time.Until(cred.ExpiresAt); d < 55*time.Minute || d > 60*time.Minute {
		t.Errorf("ExpiresAt not ~1h out: %v (delta %v)", cred.ExpiresAt, d)
	}

	// Result must be cached under (userID, serverKey).
	cached, err := store.Get(testUserID, testSrvKey)
	if err != nil {
		t.Fatalf("expected cached credential: %v", err)
	}
	if cached.AccessToken != "exchanged-access-token" {
		t.Errorf("cached AccessToken = %q", cached.AccessToken)
	}
}

func TestTokenExchanger_EntraOBOMode(t *testing.T) {
	store := newTestStore(t, openTestDB(t), newTestKey(t))
	seedSubjectToken(t, store, testSubject)

	srv := newExchangeServer(t, func(t *testing.T, form url.Values, w http.ResponseWriter) {
		t.Helper()
		if got := form.Get("grant_type"); got != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
			t.Errorf("grant_type = %q", got)
		}
		if got := form.Get("assertion"); got != testSubject {
			t.Errorf("assertion = %q", got)
		}
		if got := form.Get("requested_token_use"); got != "on_behalf_of" {
			t.Errorf("requested_token_use = %q", got)
		}
		if got := form.Get("client_id"); got != "entra-client" {
			t.Errorf("client_id = %q", got)
		}
		if got := form.Get("client_secret"); got != "entra-secret" {
			t.Errorf("client_secret = %q", got)
		}
		if got := form.Get("scope"); got != "api://upstream/.default" {
			t.Errorf("scope = %q", got)
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"access_token": "obo-access-token",
			"token_type":   "Bearer",
			"expires_in":   1800,
			"scope":        "api://upstream/.default",
		})
	})

	cfg := &config.AuthBrokerConfig{
		Mode:          config.AuthBrokerModeEntraOBO,
		TokenEndpoint: srv.URL,
		Scopes:        []string{"api://upstream/.default"},
		ClientID:      "entra-client",
		ClientSecret:  "entra-secret",
	}

	ex := NewTokenExchanger(store, nil, zap.NewNop())
	cred, err := ex.Exchange(context.Background(), testUserID, testSrvKey, cfg)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if cred.AccessToken != "obo-access-token" {
		t.Errorf("AccessToken = %q", cred.AccessToken)
	}
	if cred.ObtainedVia != "entra_obo" {
		t.Errorf("ObtainedVia = %q", cred.ObtainedVia)
	}
	if d := time.Until(cred.ExpiresAt); d < 25*time.Minute || d > 30*time.Minute {
		t.Errorf("ExpiresAt not ~30m out: %v", cred.ExpiresAt)
	}
}

// Error responses from the AS must be sanitized: the surfaced error names the
// OAuth error code + HTTP status but never leaks error_description (which can
// reflect secrets) or the subject token, and nothing is cached.
func TestTokenExchanger_ErrorSanitizedAndNotCached(t *testing.T) {
	store := newTestStore(t, openTestDB(t), newTestKey(t))
	seedSubjectToken(t, store, testSubject)

	const leak = "this-description-leaks-a-SECRET-value"
	srv := newExchangeServer(t, func(_ *testing.T, _ url.Values, w http.ResponseWriter) {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":             "invalid_grant",
			"error_description": leak,
		})
	})

	cfg := &config.AuthBrokerConfig{
		Mode:          config.AuthBrokerModeTokenExchange,
		TokenEndpoint: srv.URL,
		Resource:      "https://upstream.example.com",
		ClientID:      "gateway-client",
		ClientSecret:  "gateway-secret",
	}

	ex := NewTokenExchanger(store, nil, zap.NewNop())
	_, err := ex.Exchange(context.Background(), testUserID, testSrvKey, cfg)
	if err == nil {
		t.Fatal("expected error on 400 response")
	}
	msg := err.Error()
	if !strings.Contains(msg, "invalid_grant") {
		t.Errorf("error should name the OAuth error code, got %q", msg)
	}
	if strings.Contains(msg, leak) || strings.Contains(msg, "SECRET") {
		t.Errorf("error leaked error_description: %q", msg)
	}
	if strings.Contains(msg, testSubject) {
		t.Errorf("error leaked subject token: %q", msg)
	}
	// Nothing cached on failure.
	if _, gerr := store.Get(testUserID, testSrvKey); gerr == nil {
		t.Error("nothing should be cached after a failed exchange")
	}
}

// A missing IdP subject token is a clean error, not a panic, and surfaces no
// HTTP call (the endpoint is never hit because we have nothing to exchange).
func TestTokenExchanger_MissingSubjectToken(t *testing.T) {
	store := newTestStore(t, openTestDB(t), newTestKey(t))
	// No subject token seeded.

	called := false
	srv := newExchangeServer(t, func(_ *testing.T, _ url.Values, w http.ResponseWriter) {
		called = true
		writeJSON(w, http.StatusOK, map[string]interface{}{"access_token": "x"})
	})

	cfg := &config.AuthBrokerConfig{
		Mode:          config.AuthBrokerModeTokenExchange,
		TokenEndpoint: srv.URL,
	}
	ex := NewTokenExchanger(store, nil, zap.NewNop())
	if _, err := ex.Exchange(context.Background(), testUserID, testSrvKey, cfg); err == nil {
		t.Fatal("expected error when subject token is missing")
	}
	if called {
		t.Error("token endpoint should not be called without a subject token")
	}
}
