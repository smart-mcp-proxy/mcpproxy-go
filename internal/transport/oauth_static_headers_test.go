package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// GH #1271: with an oauth block the ladder skips the headers strategy for
// non-credential headers and relies on the OAuth transport to carry them.
// Both OAuth constructors used to drop cfg.Headers entirely, so a server that
// needs e.g. x-goog-user-project alongside its bearer would lose it the moment
// OAuth won. The bearer must still come from the token store, never from a
// stale static Authorization header.
func TestOAuthClients_CarryStaticHeaders(t *testing.T) {
	tests := []struct {
		name   string
		create func(cfg *HTTPTransportConfig) (*mcpclient.Client, error)
	}{
		{"streamable-http", CreateHTTPClient},
		{"sse", CreateSSEClient},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			seen := http.Header{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				seen = r.Header.Clone()
				mu.Unlock()
				w.WriteHeader(http.StatusTooManyRequests) // any terminal answer; we only inspect the request
			}))
			defer srv.Close()

			cfg := &HTTPTransportConfig{
				URL:         srv.URL + "/mcp",
				OAuthConfig: oauthTestConfig(),
				UseOAuth:    true,
				Headers: map[string]string{
					"X-Goog-User-Project": "quota-project",
					"Authorization":       "Bearer stale-static",
				},
			}
			mcpClient, err := tt.create(cfg)
			if err != nil {
				t.Fatalf("create OAuth %s client: %v", tt.name, err)
			}
			defer mcpClient.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := mcpClient.Start(ctx); err == nil {
				_, _ = mcpClient.Initialize(ctx, mcp.InitializeRequest{})
			}

			mu.Lock()
			defer mu.Unlock()
			if got := seen.Get("X-Goog-User-Project"); got != "quota-project" {
				t.Fatalf("static header dropped by the OAuth %s transport: X-Goog-User-Project=%q", tt.name, got)
			}
			if got := seen.Get("Authorization"); got != "Bearer test-access-token" {
				t.Fatalf("Authorization must come from the token store, got %q", got)
			}
		})
	}
}

// Spec 074 fail-closed at the transport: a brokered per-user credential must
// never meet an OAuth transport, where the shared token would overwrite or
// ride beside it.
func TestOAuthClients_RefuseBrokeredAuth(t *testing.T) {
	cfg := &HTTPTransportConfig{
		URL:          "http://127.0.0.1:1/mcp",
		OAuthConfig:  oauthTestConfig(),
		UseOAuth:     true,
		BrokeredAuth: &BrokeredAuth{Header: "Authorization", Format: "Bearer {token}", Token: "alice"},
	}
	if _, err := CreateHTTPClient(cfg); err == nil {
		t.Fatal("CreateHTTPClient must refuse brokered auth on an OAuth transport")
	}
	if _, err := CreateSSEClient(cfg); err == nil {
		t.Fatal("CreateSSEClient must refuse brokered auth on an OAuth transport")
	}
}
