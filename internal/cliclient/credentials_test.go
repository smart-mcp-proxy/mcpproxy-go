//go:build server

package cliclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func newTestClient(t *testing.T, baseURL, bearer string) *Client {
	t.Helper()
	return NewClientWithBearer(baseURL, bearer, zap.NewNop().Sugar())
}

func TestListCredentials_ParsesNonSecretFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/user/credentials" || r.Method != http.MethodGet {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"credentials":[
			{"server":"github","mode":"oauth_connect","status":"connected","token_type":"Bearer","scopes":["repo"],"obtained_via":"connect_flow"},
			{"server":"jira","mode":"oauth_connect","status":"not_connected","connect_path":"/api/v1/user/credentials/jira/connect"}
		]}`))
	}))
	defer srv.Close()

	creds, err := newTestClient(t, srv.URL, "tok").ListCredentials(context.Background())
	if err != nil {
		t.Fatalf("ListCredentials: %v", err)
	}
	if len(creds) != 2 {
		t.Fatalf("expected 2 credentials, got %d", len(creds))
	}
	if creds[0].Server != "github" || creds[0].Status != "connected" || creds[0].TokenType != "Bearer" {
		t.Errorf("unexpected first credential: %+v", creds[0])
	}
	if creds[1].ConnectPath != "/api/v1/user/credentials/jira/connect" {
		t.Errorf("expected connect_path on jira, got %q", creds[1].ConnectPath)
	}
}

// FR-026: even if a (mis-)behaving server returns secret token material, the
// CLI client must never surface it. Decoding into the typed struct drops it.
func TestListCredentials_DropsSecretMaterial(t *testing.T) {
	const secret = "super-secret-access-token-value"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"credentials":[{"server":"github","mode":"oauth_connect","status":"connected","access_token":"` + secret + `","refresh_token":"` + secret + `"}]}`))
	}))
	defer srv.Close()

	creds, err := newTestClient(t, srv.URL, "tok").ListCredentials(context.Background())
	if err != nil {
		t.Fatalf("ListCredentials: %v", err)
	}
	// Re-marshal the typed result and confirm the secret cannot appear.
	blob, _ := json.Marshal(creds)
	if strings.Contains(string(blob), secret) {
		t.Fatalf("secret material leaked into client result: %s", blob)
	}
}

func TestListCredentials_SendsBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"credentials":[]}`))
	}))
	defer srv.Close()

	if _, err := newTestClient(t, srv.URL, "my-jwt").ListCredentials(context.Background()); err != nil {
		t.Fatalf("ListCredentials: %v", err)
	}
	if gotAuth != "Bearer my-jwt" {
		t.Fatalf("expected Authorization 'Bearer my-jwt', got %q", gotAuth)
	}
}

func TestListCredentials_UnauthorizedHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Authentication required"}`))
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv.URL, "").ListCredentials(context.Background())
	if err == nil || !strings.Contains(err.Error(), "--token") {
		t.Fatalf("expected unauthenticated hint mentioning --token, got %v", err)
	}
}

func TestDeleteCredential_ReturnsMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/user/credentials/github" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"message":"Disconnected credential for \"github\""}`))
	}))
	defer srv.Close()

	msg, err := newTestClient(t, srv.URL, "tok").DeleteCredential(context.Background(), "github")
	if err != nil {
		t.Fatalf("DeleteCredential: %v", err)
	}
	if !strings.Contains(msg, "Disconnected") {
		t.Fatalf("unexpected message: %q", msg)
	}
}

func TestDeleteCredential_EscapesSlashInServerName(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	}))
	defer srv.Close()

	if _, err := newTestClient(t, srv.URL, "tok").DeleteCredential(context.Background(), "ns/name"); err != nil {
		t.Fatalf("DeleteCredential: %v", err)
	}
	if !strings.Contains(gotPath, "ns%2Fname") {
		t.Fatalf("expected slash to be escaped in path, got %q", gotPath)
	}
}
