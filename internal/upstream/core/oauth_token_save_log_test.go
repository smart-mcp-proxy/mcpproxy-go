package core

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// GH #1827: the stored-DCR refresh writes the new pair itself. Its save
// outcome goes to the server's OAuth logger with the flight that produced it,
// the same line the token store writes on the mcp-go path.
func TestRefreshWithStoredCredentials_LogsSaveOutcomeWithFlight(t *testing.T) {
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metadata" {
			base := "http://" + r.Host
			_ = json.NewEncoder(w).Encode(map[string]string{"issuer": base, "authorization_endpoint": base + "/authorize", "token_endpoint": base + "/token"})
			return
		}
		if !refreshReplayTokenRequest(t, w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-1", "refresh_token": "rt-1", "token_type": "Bearer", "expires_in": 3600})
	}))
	defer as.Close()
	db, err := storage.NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	defer db.Close()
	cfg := &config.ServerConfig{Name: "save-log-fixture", URL: as.URL + "/mcp"}
	key := oauth.GenerateServerKey(cfg.Name, cfg.URL)
	require.NoError(t, db.SaveOAuthToken(&storage.OAuthTokenRecord{ServerName: key, DisplayName: cfg.Name, AccessToken: "at-0", RefreshToken: "rt-0", ClientID: "stored-client", ExpiresAt: time.Now().Add(-time.Minute)}))
	store := oauth.NewPersistentTokenStore(cfg.Name, cfg.URL, db)
	handler := transport.NewOAuthHandler(transport.OAuthConfig{TokenStore: store, AuthServerMetadataURL: as.URL + "/metadata", HTTPClient: as.Client()})
	obs, logs := observer.New(zapcore.DebugLevel)
	c := &Client{config: cfg, storage: db, logger: zap.New(obs)}

	_, _, err = oauth.NewRefreshCoordinator().Do(context.Background(), oauth.RefreshRequest{
		Key: key, ServerName: cfg.Name, ObservedRefreshToken: "rt-0", Trigger: oauth.RefreshTriggerProactive,
		Load: func() (*storage.OAuthTokenRecord, error) { return db.GetOAuthToken(key) }, Refresh: c.oauthRefreshFunc(handler),
	})
	require.NoError(t, err)

	entries := logs.FilterMessage("OAuth token save finished").All()
	require.Len(t, entries, 1)
	m := entries[0].ContextMap()
	assert.Equal(t, "saved", m["result"])
	assert.Equal(t, uint64(1), m["flight"])
	assert.Equal(t, "proactive", m["trigger"])
	assert.Equal(t, true, m["refresh_token_changed"])
	for _, e := range logs.All() {
		raw, err := json.Marshal(e.ContextMap())
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "rt-1")
		assert.NotContains(t, string(raw), "at-1")
	}
}

// GH #1827: the quick-retry lines name the flight they belong to, so the two
// requests of one flight can be told from two flights.
func TestRefreshReplay_LogsNameTheFlight(t *testing.T) {
	c, rec := newRefreshReplayClient(t)
	obs, logs := observer.New(zap.InfoLevel)
	c.logger = zap.New(obs)
	fn := c.withRefreshReplay(func(context.Context, *storage.OAuthTokenRecord) (*client.Token, error) {
		return nil, &net.OpError{Op: "read", Err: io.ErrUnexpectedEOF}
	}, func(context.Context) (string, error) { return "https://replay.example/token", nil })
	key := oauth.GenerateServerKey(c.config.Name, c.config.URL)
	_, _, _ = oauth.NewRefreshCoordinator().Do(context.Background(), oauth.RefreshRequest{
		Key: key, ServerName: c.config.Name, ObservedRefreshToken: rec.RefreshToken, Trigger: oauth.RefreshTriggerReactive,
		Load: func() (*storage.OAuthTokenRecord, error) { return c.storage.GetOAuthToken(key) }, Refresh: fn,
	})
	attempts := logs.FilterMessage("OAuth refresh attempt finished").All()
	require.Len(t, attempts, 2)
	for _, e := range attempts {
		assert.Equal(t, uint64(1), e.ContextMap()["flight"])
		assert.Equal(t, "reactive", e.ContextMap()["trigger"])
	}
}
