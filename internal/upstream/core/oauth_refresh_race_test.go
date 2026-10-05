package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// rotatingAuthServer issues one-time refresh tokens. Reusing a refresh token
// revokes the whole grant (every access token it issued), like providers that
// implement refresh-token rotation with reuse detection.
type rotatingAuthServer struct {
	srv           *httptest.Server
	tokenRequests atomic.Int32
	mu            sync.Mutex
	currentRT     string
	valid         map[string]bool // access tokens accepted by the resource server
	n             int
	revoked       bool
	clientIDs     []string
}

func newRotatingAuthServer(t *testing.T) *rotatingAuthServer {
	as := &rotatingAuthServer{currentRT: "rt-0", valid: map[string]bool{"at-0": true}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 as.srv.URL,
			"authorization_endpoint": as.srv.URL + "/authorize",
			"token_endpoint":         as.srv.URL + "/token",
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		as.tokenRequests.Add(1)
		// Keep the flight in progress long enough that the concurrent
		// callers overlap it instead of all arriving after it finished.
		time.Sleep(50 * time.Millisecond)
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		as.mu.Lock()
		defer as.mu.Unlock()
		as.clientIDs = append(as.clientIDs, form.Get("client_id"))
		w.Header().Set("Content-Type", "application/json")
		if as.revoked || form.Get("refresh_token") != as.currentRT {
			as.revoked = true
			as.valid = map[string]bool{}
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		as.n++
		as.currentRT = fmt.Sprintf("rt-%d", as.n)
		at := fmt.Sprintf("at-%d", as.n)
		as.valid[at] = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": at, "refresh_token": as.currentRT, "token_type": "Bearer", "expires_in": 3600,
		})
	})
	as.srv = httptest.NewServer(mux)
	t.Cleanup(as.srv.Close)
	return as
}

func (as *rotatingAuthServer) accepts(authz string) bool {
	as.mu.Lock()
	defer as.mu.Unlock()
	return strings.HasPrefix(authz, "Bearer ") && as.valid[strings.TrimPrefix(authz, "Bearer ")]
}

// newProtectedMCPServer serves a Streamable HTTP MCP server that rejects any
// request whose bearer token the AS does not currently accept.
func newProtectedMCPServer(t *testing.T, as *rotatingAuthServer) *httptest.Server {
	s := server.NewMCPServer("protected", "1.0.0", server.WithToolCapabilities(true))
	s.AddTool(mcp.NewTool("ping"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("pong"), nil
	})
	h := server.NewStreamableHTTPServer(s)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !as.accepts(r.Header.Get("Authorization")) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestOAuthRefreshRace_OneTokenRequestPerExpiry is Spec 113 SC-001: with an
// expired token, 50 concurrent tool calls through a real mcp-go Streamable
// HTTP client plus one concurrent proactive RefreshOAuthTokenDirect produce
// exactly one refresh request, and every call succeeds.
func TestOAuthRefreshRace_OneTokenRequestPerExpiry(t *testing.T) {
	for _, tc := range []struct {
		name            string
		handlerClientID string // what createOAuthConfigInternal loaded into the handler
		wantClientID    string
	}{
		{"handler sub-path", "dcr-client", "dcr-client"},
		{"stored DCR credentials sub-path", "", "dcr-client"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			as := newRotatingAuthServer(t)
			mcpSrv := newProtectedMCPServer(t, as)

			db, err := storage.NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })

			name := strings.ReplaceAll(t.Name(), "/", "_")
			cfg := &config.ServerConfig{Name: name, URL: mcpSrv.URL, Protocol: "streamable-http"}
			key := oauth.GenerateServerKey(cfg.Name, cfg.URL)
			require.NoError(t, db.SaveOAuthToken(&storage.OAuthTokenRecord{
				ServerName: key, DisplayName: cfg.Name, AccessToken: "at-0", RefreshToken: "rt-0",
				TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour), ClientID: "dcr-client",
			}))

			store := oauth.NewPersistentTokenStore(cfg.Name, cfg.URL, db)
			oauthCfg := &client.OAuthConfig{
				ClientID:              tc.handlerClientID,
				TokenStore:            store,
				PKCEEnabled:           true,
				AuthServerMetadataURL: as.srv.URL + "/.well-known/oauth-authorization-server",
			}
			tr, err := transport.NewStreamableHTTP(mcpSrv.URL, transport.WithHTTPOAuth(*oauthCfg))
			require.NoError(t, err)
			mcpClient := client.NewClient(tr)

			c := &Client{config: cfg, storage: db, logger: zap.NewNop()}
			c.client = mcpClient
			c.bindOAuthRefresher(oauthCfg)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			require.NoError(t, mcpClient.Start(ctx))
			t.Cleanup(func() { _ = mcpClient.Close() })
			initReq := mcp.InitializeRequest{}
			initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
			initReq.Params.ClientInfo = mcp.Implementation{Name: "race-test", Version: "1"}
			_, err = mcpClient.Initialize(ctx, initReq)
			require.NoError(t, err)
			require.Equal(t, int32(0), as.tokenRequests.Load())

			// The access token expires.
			require.NoError(t, db.UpdateOAuthToken(key, func(r *storage.OAuthTokenRecord) error {
				r.ExpiresAt = time.Now().Add(-time.Second)
				return nil
			}))

			const n = 50
			var wg sync.WaitGroup
			start := make(chan struct{})
			errs := make([]error, n+1)
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					req := mcp.CallToolRequest{}
					req.Params.Name = "ping"
					_, errs[i] = mcpClient.CallTool(ctx, req)
				}(i)
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errs[n] = c.RefreshOAuthTokenDirect(ctx)
			}()
			close(start)
			wg.Wait()

			for i, e := range errs {
				assert.NoError(t, e, "caller %d", i)
			}
			assert.Equal(t, int32(1), as.tokenRequests.Load(), "exactly one refresh request per expiry")
			as.mu.Lock()
			assert.False(t, as.revoked, "the grant was never replayed")
			assert.Equal(t, []string{tc.wantClientID}, as.clientIDs, "never an empty client_id")
			as.mu.Unlock()

			rec, err := db.GetOAuthToken(key)
			require.NoError(t, err)
			assert.Equal(t, "rt-1", rec.RefreshToken)
			assert.Equal(t, "dcr-client", rec.ClientID, "DCR registration preserved")
			assert.True(t, rec.ExpiresAt.After(time.Now()))
		})
	}
}

// TestOAuthRefreshRace_LateProactiveAfterReactiveFlight is the deterministic
// form of the ordering that broke SC-001 in CI (shuffle run 37307361769): the
// proactive timer fires for the old expiry, but its RefreshOAuthTokenDirect
// only reads the record after the reactive flight already rotated the token.
// Observing the new refresh token, it used to start a second flight and
// rotate the just-minted grant again. A proactive refresh within
// oauth.MinRefreshInterval of a saved token must not reach the network.
func TestOAuthRefreshRace_LateProactiveAfterReactiveFlight(t *testing.T) {
	for _, tc := range []struct {
		name            string
		handlerClientID string
	}{
		{"handler sub-path", "dcr-client"},
		{"stored DCR credentials sub-path", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			as := newRotatingAuthServer(t)
			mcpSrv := newProtectedMCPServer(t, as)

			db, err := storage.NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })

			name := strings.ReplaceAll(t.Name(), "/", "_")
			cfg := &config.ServerConfig{Name: name, URL: mcpSrv.URL, Protocol: "streamable-http"}
			key := oauth.GenerateServerKey(cfg.Name, cfg.URL)
			require.NoError(t, db.SaveOAuthToken(&storage.OAuthTokenRecord{
				ServerName: key, DisplayName: cfg.Name, AccessToken: "at-0", RefreshToken: "rt-0",
				TokenType: "Bearer", ExpiresAt: time.Now().Add(-time.Second), ClientID: "dcr-client",
			}))

			store := oauth.NewPersistentTokenStore(cfg.Name, cfg.URL, db)
			oauthCfg := &client.OAuthConfig{
				ClientID:              tc.handlerClientID,
				TokenStore:            store,
				PKCEEnabled:           true,
				AuthServerMetadataURL: as.srv.URL + "/.well-known/oauth-authorization-server",
			}
			tr, err := transport.NewStreamableHTTP(mcpSrv.URL, transport.WithHTTPOAuth(*oauthCfg))
			require.NoError(t, err)
			mcpClient := client.NewClient(tr)
			c := &Client{config: cfg, storage: db, logger: zap.NewNop()}
			c.client = mcpClient
			c.bindOAuthRefresher(oauthCfg)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Reactive: the token store refreshes the expired token.
			tok, err := store.GetToken(ctx)
			require.NoError(t, err)
			require.Equal(t, "at-1", tok.AccessToken)
			require.Equal(t, int32(1), as.tokenRequests.Load())

			// The proactive timer for the old expiry lands afterwards.
			require.NoError(t, c.RefreshOAuthTokenDirect(ctx))
			assert.Equal(t, int32(1), as.tokenRequests.Load(), "exactly one refresh request per expiry")

			rec, err := db.GetOAuthToken(key)
			require.NoError(t, err)
			assert.Equal(t, "rt-1", rec.RefreshToken)
		})
	}
}

// Review round 1: a client id configured only through oauth.extra_params is
// injected into token requests by oauth.OAuthTransportWrapper; such a grant
// must stay refreshable instead of failing with ErrNoClientCredentials.
func TestOAuthRefresh_ExtraParamsClientID(t *testing.T) {
	as := newRotatingAuthServer(t)
	db, err := storage.NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	name := strings.ReplaceAll(t.Name(), "/", "_")
	srvURL := "https://mcp.example.invalid/mcp"
	extra := map[string]string{"client_id": "xp-client"}
	cfg := &config.ServerConfig{Name: name, URL: srvURL, Protocol: "streamable-http", OAuth: &config.OAuthConfig{ExtraParams: extra}}
	key := oauth.GenerateServerKey(cfg.Name, cfg.URL)
	require.NoError(t, db.SaveOAuthToken(&storage.OAuthTokenRecord{
		ServerName: key, DisplayName: cfg.Name, AccessToken: "at-0", RefreshToken: "rt-0",
		TokenType: "Bearer", ExpiresAt: time.Now().Add(-time.Second),
	}))
	store := oauth.NewPersistentTokenStore(cfg.Name, cfg.URL, db)
	oauthCfg := &client.OAuthConfig{
		TokenStore:            store,
		PKCEEnabled:           true,
		AuthServerMetadataURL: as.srv.URL + "/.well-known/oauth-authorization-server",
		HTTPClient:            &http.Client{Transport: oauth.NewOAuthTransportWrapper(nil, extra, zap.NewNop())},
	}
	tr, err := transport.NewStreamableHTTP(srvURL, transport.WithHTTPOAuth(*oauthCfg))
	require.NoError(t, err)
	c := &Client{config: cfg, storage: db, logger: zap.NewNop()}
	c.client = client.NewClient(tr)
	c.bindOAuthRefresher(oauthCfg)

	tok, err := store.GetToken(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "at-1", tok.AccessToken)
	as.mu.Lock()
	defer as.mu.Unlock()
	assert.Equal(t, []string{"xp-client"}, as.clientIDs)
}
