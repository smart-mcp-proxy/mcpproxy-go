package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcptransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// oauthTestConfig builds an OAuth config whose token store already holds a live
// token, so the mcp-go OAuth handler attaches an Authorization header and
// actually issues the MCP request instead of short-circuiting into "no token".
// That is what lets the test observe the upstream's 429 on the MCP endpoint.
func oauthTestConfig() *mcpclient.OAuthConfig {
	store := mcptransport.NewMemoryTokenStore()
	_ = store.SaveToken(context.Background(), &mcptransport.Token{
		AccessToken: "test-access-token",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		ExpiresAt:   time.Now().Add(time.Hour),
	})
	return &mcpclient.OAuthConfig{
		ClientID:    "test-client-id",
		RedirectURI: "http://127.0.0.1:0/callback",
		Scopes:      []string{"mcp"},
		TokenStore:  store,
		PKCEEnabled: true,
	}
}

// TestOAuthClients_RecordRetryAfter covers the branch the non-OAuth tests cannot
// reach: mcp-go builds the OAuth client's transport itself, and OAuthConfig.HTTPClient
// governs only the metadata/DCR/token calls. If the basic-client option were
// dropped from either OAuth constructor, a 429 on the MCP endpoint of an
// OAuth'd upstream would go unrecorded and the server would keep being redialed
// on the plain ladder (#1040).
func TestOAuthClients_RecordRetryAfter(t *testing.T) {
	tests := []struct {
		name   string
		create func(cfg *HTTPTransportConfig) (*mcpclient.Client, error)
	}{
		{"streamable-http", CreateHTTPClient},
		{"sse", CreateSSEClient},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sawAuthHeader atomic.Bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					sawAuthHeader.Store(true)
				}
				w.Header().Set("Retry-After", "1800")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":"rate limit exceeded"}`))
			}))
			defer srv.Close()

			recorder := NewRetryAfterRecorder()
			cfg := &HTTPTransportConfig{
				URL:         srv.URL + "/mcp",
				OAuthConfig: oauthTestConfig(),
				UseOAuth:    true,
				RetryAfter:  recorder,
			}

			mcpClient, err := tt.create(cfg)
			if err != nil {
				t.Fatalf("failed to create OAuth %s client: %v", tt.name, err)
			}
			defer mcpClient.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			// Both Start and Initialize are expected to fail against a 429 upstream;
			// what matters is that the request went out through OUR transport.
			if err := mcpClient.Start(ctx); err == nil {
				_, _ = mcpClient.Initialize(ctx, mcp.InitializeRequest{})
			}

			if !sawAuthHeader.Load() {
				t.Fatal("the OAuth handler never attached a token, so the request under test never left the client")
			}
			deadline := recorder.Deadline()
			if deadline.IsZero() {
				t.Fatalf("the OAuth %s client dropped the Retry-After recorder", tt.name)
			}
			if got := time.Until(deadline); got < 25*time.Minute || got > 35*time.Minute {
				t.Fatalf("recorded window = %v, want ~30m", got)
			}
		})
	}
}

// TestOAuthClients_NoRecorder_KeepsMcpGoDefaults pins the opt-out: with no
// recorder and no tracing we must hand mcp-go no client at all, leaving its own
// defaults (and the OAuth wiring) exactly as they were.
func TestOAuthClients_NoRecorder_KeepsMcpGoDefaults(t *testing.T) {
	cfg := &HTTPTransportConfig{
		URL:         "http://127.0.0.1:1/mcp",
		OAuthConfig: oauthTestConfig(),
		UseOAuth:    true,
	}
	if cfg.needsCustomTransport() {
		t.Fatal("a config with neither tracing nor a recorder must not force a custom transport")
	}
	if c, err := CreateHTTPClient(cfg); err != nil {
		t.Fatalf("CreateHTTPClient: %v", err)
	} else {
		c.Close()
	}
	if c, err := CreateSSEClient(cfg); err != nil {
		t.Fatalf("CreateSSEClient: %v", err)
	} else {
		c.Close()
	}
}
