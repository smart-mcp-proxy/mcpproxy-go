package core

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/dialgate"
)

// Synthetic AS with the idempotent replay contract:
// one rotation, replay of the same pair for 45s. The first response body is
// truncated after rotation, before the real mcp-go handler can persist it.
func TestRefreshReplay_LostResponse(t *testing.T) {
	for _, handlerID := range []string{"synthetic-client", ""} {
		t.Run("handler_"+handlerID, func(t *testing.T) {
			var mu sync.Mutex
			requests, rotations := 0, 0
			var rotatedAt time.Time
			as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/metadata" {
					baseURL := "http://" + r.Host
					_ = json.NewEncoder(w).Encode(map[string]string{"issuer": baseURL, "authorization_endpoint": baseURL + "/authorize", "token_endpoint": baseURL + "/token"})
					return
				}
				require.Equal(t, "/token", r.URL.Path)
				require.NoError(t, r.ParseForm())
				require.Equal(t, "rt-0", r.Form.Get("refresh_token"))
				require.Equal(t, "synthetic-client", r.Form.Get("client_id"))
				mu.Lock()
				defer mu.Unlock()
				requests++
				if requests == 1 {
					rotations++
					rotatedAt = time.Now()
					w.Header().Set("Content-Length", "200")
					_, _ = io.WriteString(w, `{`)
					return
				}
				require.Less(t, time.Since(rotatedAt), 45*time.Second)
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-1", "refresh_token": "rt-1", "token_type": "Bearer", "expires_in": 3600})
			}))
			defer as.Close()
			db, err := storage.NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
			require.NoError(t, err)
			defer db.Close()
			cfg := &config.ServerConfig{Name: "replay-fixture", URL: as.URL + "/mcp", OAuth: &config.OAuthConfig{RefreshReplayEnabled: replayEnabled(true), TokenEndpoint: as.URL + "/token"}}
			key := oauth.GenerateServerKey(cfg.Name, cfg.URL)
			require.NoError(t, db.SaveOAuthToken(&storage.OAuthTokenRecord{ServerName: key, DisplayName: cfg.Name, AccessToken: "at-0", RefreshToken: "rt-0", ClientID: "synthetic-client", ExpiresAt: time.Now().Add(-time.Minute)}))
			store := oauth.NewPersistentTokenStore(cfg.Name, cfg.URL, db)
			handler := transport.NewOAuthHandler(transport.OAuthConfig{ClientID: handlerID, TokenStore: store, AuthServerMetadataURL: as.URL + "/metadata", HTTPClient: as.Client()})
			c := &Client{config: cfg, storage: db, logger: zap.NewNop()}
			refresh := c.oauthRefreshFunc(handler)
			coordinator := oauth.NewRefreshCoordinator()
			const n = 20
			var wg sync.WaitGroup
			errs := make([]error, n)
			for i := range n {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					trigger := oauth.RefreshTriggerReactive
					if i == 0 {
						trigger = oauth.RefreshTriggerProactive
					}
					_, _, errs[i] = coordinator.Do(context.Background(), oauth.RefreshRequest{Key: key, ServerName: cfg.Name, ObservedRefreshToken: "rt-0", Trigger: trigger, Load: func() (*storage.OAuthTokenRecord, error) { return db.GetOAuthToken(key) }, Refresh: refresh})
				}(i)
			}
			wg.Wait()
			for _, err := range errs {
				require.NoError(t, err)
			}
			mu.Lock()
			require.Equal(t, 2, requests)
			require.Equal(t, 1, rotations)
			mu.Unlock()
			rec, err := db.GetOAuthToken(key)
			require.NoError(t, err)
			require.Equal(t, "rt-1", rec.RefreshToken)
			require.Equal(t, "synthetic-client", rec.ClientID)
			require.Equal(t, "at-1", rec.AccessToken)
		})
	}
}

func newRefreshReplayClient(t *testing.T) (*Client, *storage.OAuthTokenRecord) {
	t.Helper()
	db, err := storage.NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	c := &Client{config: &config.ServerConfig{Name: "replay-fixture", URL: "https://replay.example/mcp", OAuth: &config.OAuthConfig{RefreshReplayEnabled: replayEnabled(true), TokenEndpoint: "https://replay.example/token"}}, storage: db, logger: zap.NewNop()}
	key := oauth.GenerateServerKey(c.config.Name, c.config.URL)
	require.NoError(t, db.SaveOAuthToken(&storage.OAuthTokenRecord{ServerName: key, DisplayName: "replay-fixture", ClientID: "synthetic-client", AccessToken: "at-0", RefreshToken: "rt-0", ExpiresAt: time.Now().Add(-time.Minute)}))
	rec, err := db.GetOAuthToken(key)
	require.NoError(t, err)
	return c, rec
}

func replayEnabled(v bool) *bool { return &v }

func TestRefreshReplay_RequiresExplicitProviderContract(t *testing.T) {
	for _, tc := range []struct {
		name      string
		modify    func(*Client)
		target    string
		lookupErr error
	}{
		{name: "unset", modify: func(c *Client) { c.config.OAuth.RefreshReplayEnabled = nil }},
		{name: "disabled", modify: func(c *Client) { c.config.OAuth.RefreshReplayEnabled = replayEnabled(false) }},
		{name: "unconfigured_endpoint", modify: func(c *Client) { c.config.OAuth.TokenEndpoint = "" }},
		{name: "invalid_endpoint", modify: func(c *Client) { c.config.OAuth.TokenEndpoint = "http://remote.example/token" }},
		{name: "stdio", modify: func(c *Client) { c.config.Command = "synthetic" }},
		{name: "no_oauth", modify: func(c *Client) { c.config.OAuth = nil }},
		{name: "no_config", modify: func(c *Client) { c.config = nil }},
		{name: "no_store", modify: func(c *Client) { c.storage = nil }},
		{name: "different_endpoint", target: "https://other.example/token"},
		{name: "metadata_error", lookupErr: io.EOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newRefreshReplayClient(t)
			if tc.modify != nil {
				tc.modify(c)
			}
			calls := 0
			fn := c.withRefreshReplay(func(ctx context.Context, _ *storage.OAuthTokenRecord) (*client.Token, error) {
				calls++
				// The disabled/unverified path keeps the caller's original deadline.
				_, bounded := ctx.Deadline()
				require.False(t, bounded)
				return nil, io.ErrUnexpectedEOF
			}, func(context.Context) (string, error) { return tc.target, tc.lookupErr })
			_, err := fn(context.Background(), rec)
			require.Error(t, err)
			require.Equal(t, 1, calls)
		})
	}
}

func TestRefreshReplay_EndpointChangesBeforeRetry(t *testing.T) {
	c, rec := newRefreshReplayClient(t)
	calls, lookups := 0, 0
	fn := c.withRefreshReplay(func(context.Context, *storage.OAuthTokenRecord) (*client.Token, error) {
		calls++
		return nil, io.ErrUnexpectedEOF
	}, func(context.Context) (string, error) {
		lookups++
		if lookups == 1 {
			return "https://replay.example/token", nil
		}
		return "https://other.example/token", nil
	})
	_, err := fn(context.Background(), rec)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.Equal(t, 1, calls)
}

func TestRefreshReplay_ErrorsAndAttemptLimit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
		calls   int
	}{
		{"eof", io.EOF, 2},
		{"truncated", io.ErrUnexpectedEOF, 2},
		{"timeout", context.DeadlineExceeded, 2},
		{"connection_reset", &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}, 2},
		{"http_client_connection_reset", &url.Error{Op: "Post", URL: "https://replay.example/token", Err: &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}}, 2},
		{"http_client_timeout", &url.Error{Op: "Post", URL: "https://replay.example/token", Err: context.DeadlineExceeded}, 2},
		// *url.Error implements net.Error for every http.Client.Do failure.
		// The server answered a refused redirect, and no request left for a
		// certificate failure: neither is a lost response.
		{"refused_redirect", &url.Error{Op: "Post", URL: "https://replay.example/token", Err: errors.New("refusing cross-origin redirect of an OAuth POST")}, 1},
		{"certificate", &url.Error{Op: "Post", URL: "https://replay.example/token", Err: x509.UnknownAuthorityError{}}, 1},
		{"invalid_grant", transport.OAuthError{ErrorCode: "invalid_grant", ErrorDescription: "network timeout"}, 1},
		{"invalid_client", transport.OAuthError{ErrorCode: "invalid_client"}, 1},
		{"invalid_scope", transport.OAuthError{ErrorCode: "invalid_scope"}, 1},
		{"rate_limit", &oauth.RefreshHTTPError{Status: 429}, 1},
		{"server_error", &oauth.RefreshHTTPError{Status: 503}, 1},
		{"cancelled", context.Canceled, 1},
		{"retired", dialgate.ErrRetired, 1},
		{"unknown", errors.New("unexpected response"), 1},
		{"untyped_timeout_string", errors.New("network timeout"), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newRefreshReplayClient(t)
			calls := 0
			fn := c.withRefreshReplay(func(ctx context.Context, got *storage.OAuthTokenRecord) (*client.Token, error) {
				calls++
				require.Equal(t, rec.Revision, got.Revision)
				deadline, ok := ctx.Deadline()
				require.True(t, ok)
				limit := refreshReplayFirstAttemptTimeout
				if calls == 2 {
					limit = refreshReplayBudget
				}
				require.LessOrEqual(t, time.Until(deadline), limit)
				return nil, tc.failure
			}, func(context.Context) (string, error) { return "https://replay.example/token", nil })
			_, err := fn(context.Background(), rec)
			require.ErrorIs(t, err, tc.failure)
			require.Equal(t, tc.calls, calls)
		})
	}
}

func TestRefreshReplay_StateChangeStopsReplay(t *testing.T) {
	for _, mode := range []string{"new_grant", "same_token_new_revision", "logout", "retired", "cancelled", "deadline_during_metadata"} {
		t.Run(mode, func(t *testing.T) {
			c, rec := newRefreshReplayClient(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			fn := c.withRefreshReplay(func(context.Context, *storage.OAuthTokenRecord) (*client.Token, error) {
				calls++
				switch mode {
				case "new_grant", "same_token_new_revision":
					require.NoError(t, c.storage.UpdateOAuthToken(rec.ServerName, func(current *storage.OAuthTokenRecord) error {
						if mode == "new_grant" {
							current.RefreshToken = "rt-new-login"
							current.AccessToken = "at-new-login"
						}
						return nil
					}))
				case "logout":
					require.NoError(t, c.storage.DeleteOAuthToken(rec.ServerName))
				case "retired":
					c.Retire()
				case "cancelled":
					cancel()
				}
				return nil, io.ErrUnexpectedEOF
			}, func(context.Context) (string, error) {
				if mode == "deadline_during_metadata" {
					cancel()
				}
				return "https://replay.example/token", nil
			})
			_, err := fn(ctx, rec)
			require.Error(t, err)
			want := 1
			if mode == "deadline_during_metadata" {
				want = 0
			}
			require.Equal(t, want, calls)
		})
	}
}

func TestRefreshReplay_LogsContainOnlyClosedFields(t *testing.T) {
	c, rec := newRefreshReplayClient(t)
	core, logs := observer.New(zap.InfoLevel)
	c.logger = zap.New(core)
	sentinel := "synthetic-private-value-do-not-log"
	fn := c.withRefreshReplay(func(context.Context, *storage.OAuthTokenRecord) (*client.Token, error) {
		return nil, &net.OpError{Op: sentinel, Err: io.ErrUnexpectedEOF}
	}, func(context.Context) (string, error) { return "https://replay.example/token", nil })
	_, _ = fn(context.Background(), rec)
	require.Equal(t, 2, logs.Len())
	for _, entry := range logs.All() {
		raw, err := json.Marshal(entry.ContextMap())
		require.NoError(t, err)
		require.NotContains(t, string(raw), sentinel)
		for key := range entry.ContextMap() {
			require.Contains(t, []string{"attempt", "outcome", "transport_failure", "http_status", "duration"}, key)
		}
	}
}

func TestRefreshReplay_HTTPTimeout(t *testing.T) {
	c, rec := newRefreshReplayClient(t)
	var mu sync.Mutex
	requests := 0
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metadata" {
			base := "http://" + r.Host
			_ = json.NewEncoder(w).Encode(map[string]string{"issuer": base, "authorization_endpoint": base + "/authorize", "token_endpoint": base + "/token"})
			return
		}
		require.NoError(t, r.ParseForm())
		require.Equal(t, "rt-0", r.Form.Get("refresh_token"))
		mu.Lock()
		requests++
		n := requests
		mu.Unlock()
		if n == 1 {
			// Rotation has completed, but the response hangs until the
			// first-attempt HTTP context expires. No manual sleep or timer.
			w.Header().Set("Content-Length", "200")
			_, _ = io.WriteString(w, `{`)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-1", "refresh_token": "rt-1", "token_type": "Bearer", "expires_in": 3600})
	}))
	defer as.Close()
	handler := transport.NewOAuthHandler(transport.OAuthConfig{ClientID: "synthetic-client", TokenStore: oauth.NewPersistentTokenStore(c.config.Name, c.config.URL, c.storage), AuthServerMetadataURL: as.URL + "/metadata", HTTPClient: as.Client()})
	c.config.OAuth.TokenEndpoint = as.URL + "/token"
	fn := c.oauthRefreshFunc(handler)
	start := time.Now()
	tok, err := fn(context.Background(), rec)
	require.NoError(t, err)
	require.Equal(t, "rt-1", tok.RefreshToken)
	require.Less(t, time.Since(start), refreshReplayBudget)
	mu.Lock()
	require.Equal(t, 2, requests)
	mu.Unlock()
}

func TestRefreshReplay_SuccessHasNoRetry(t *testing.T) {
	c, rec := newRefreshReplayClient(t)
	calls := 0
	fn := c.withRefreshReplay(func(context.Context, *storage.OAuthTokenRecord) (*client.Token, error) {
		calls++
		return &client.Token{AccessToken: "at-1"}, nil
	}, func(context.Context) (string, error) {
		return "https://replay.example/token", nil
	})
	tok, err := fn(context.Background(), rec)
	require.NoError(t, err)
	require.Equal(t, "at-1", tok.AccessToken)
	require.Equal(t, 1, calls)
}

func TestRefreshReplay_CoordinatorKeepsNewLogin(t *testing.T) {
	c, rec := newRefreshReplayClient(t)
	calls := 0
	fn := c.withRefreshReplay(func(context.Context, *storage.OAuthTokenRecord) (*client.Token, error) {
		calls++
		require.NoError(t, c.storage.UpdateOAuthToken(rec.ServerName, func(current *storage.OAuthTokenRecord) error {
			current.AccessToken, current.RefreshToken = "at-new-login", "rt-new-login"
			return nil
		}))
		return nil, io.ErrUnexpectedEOF
	}, func(context.Context) (string, error) { return "https://replay.example/token", nil })
	tok, _, err := oauth.NewRefreshCoordinator().Do(context.Background(), oauth.RefreshRequest{Key: rec.ServerName, ServerName: "replay-fixture", ObservedRefreshToken: rec.RefreshToken, Trigger: oauth.RefreshTriggerReactive, Load: func() (*storage.OAuthTokenRecord, error) { return c.storage.GetOAuthToken(rec.ServerName) }, Refresh: fn})
	require.NoError(t, err)
	require.Equal(t, "rt-new-login", tok.RefreshToken)
	require.Equal(t, 1, calls)
}

func TestRefreshReplay_CancelledWaiterDoesNotCancelFlight(t *testing.T) {
	c, rec := newRefreshReplayClient(t)
	entered, release := make(chan struct{}), make(chan struct{})
	calls := 0
	fn := c.withRefreshReplay(func(ctx context.Context, _ *storage.OAuthTokenRecord) (*client.Token, error) {
		calls++
		if calls == 1 {
			close(entered)
			<-release
			return nil, io.ErrUnexpectedEOF
		}
		require.NoError(t, ctx.Err())
		gen, inFlight := oauth.FlightGeneration(ctx)
		require.True(t, inFlight)
		require.NoError(t, c.storage.UpdateOAuthToken(rec.ServerName, func(current *storage.OAuthTokenRecord) error {
			if !oauth.GenerationMatches(current, gen) {
				return storage.ErrSkipOAuthTokenUpdate
			}
			current.AccessToken, current.RefreshToken = "at-1", "rt-1"
			return nil
		}))
		return &client.Token{AccessToken: "at-1", RefreshToken: "rt-1"}, nil
	}, func(context.Context) (string, error) { return "https://replay.example/token", nil })
	req := oauth.RefreshRequest{Key: rec.ServerName, ServerName: "replay-fixture", ObservedRefreshToken: rec.RefreshToken, Trigger: oauth.RefreshTriggerReactive, Load: func() (*storage.OAuthTokenRecord, error) { return c.storage.GetOAuthToken(rec.ServerName) }, Refresh: fn}
	coordinator := oauth.NewRefreshCoordinator()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	leaderDone := make(chan error, 1)
	go func() { _, _, err := coordinator.Do(ctx, req); leaderDone <- err }()
	<-entered
	cancel()
	require.ErrorIs(t, <-leaderDone, context.Canceled)
	close(release)
	tok, _, err := coordinator.Do(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "rt-1", tok.RefreshToken)
	require.Equal(t, 2, calls)
}

// A status that arrived is an answer, even when its body is cut short: it
// never permits a replay, on the configured-client or the stored-client path.
func TestRefreshReplay_TruncatedErrorStatusHasNoRetry(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		for _, handlerID := range []string{"synthetic-client", ""} {
			t.Run(strconv.Itoa(status)+"_handler_"+handlerID, func(t *testing.T) {
				var mu sync.Mutex
				requests := 0
				as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/metadata" {
						baseURL := "http://" + r.Host
						_ = json.NewEncoder(w).Encode(map[string]string{"issuer": baseURL, "authorization_endpoint": baseURL + "/authorize", "token_endpoint": baseURL + "/token"})
						return
					}
					mu.Lock()
					defer mu.Unlock()
					requests++
					w.Header().Set("Content-Length", "200")
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `{`)
				}))
				defer as.Close()
				db, err := storage.NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
				require.NoError(t, err)
				defer db.Close()
				cfg := &config.ServerConfig{Name: "replay-fixture", URL: as.URL + "/mcp", OAuth: &config.OAuthConfig{RefreshReplayEnabled: replayEnabled(true), TokenEndpoint: as.URL + "/token"}}
				key := oauth.GenerateServerKey(cfg.Name, cfg.URL)
				require.NoError(t, db.SaveOAuthToken(&storage.OAuthTokenRecord{ServerName: key, DisplayName: cfg.Name, AccessToken: "at-0", RefreshToken: "rt-0", ClientID: "synthetic-client", ExpiresAt: time.Now().Add(-time.Minute)}))
				store := oauth.NewPersistentTokenStore(cfg.Name, cfg.URL, db)
				handler := transport.NewOAuthHandler(transport.OAuthConfig{ClientID: handlerID, TokenStore: store, AuthServerMetadataURL: as.URL + "/metadata", HTTPClient: as.Client()})
				c := &Client{config: cfg, storage: db, logger: zap.NewNop()}
				_, _, err = oauth.NewRefreshCoordinator().Do(context.Background(), oauth.RefreshRequest{Key: key, ServerName: cfg.Name, ObservedRefreshToken: "rt-0", Trigger: oauth.RefreshTriggerReactive, Load: func() (*storage.OAuthTokenRecord, error) { return db.GetOAuthToken(key) }, Refresh: c.oauthRefreshFunc(handler)})
				require.Error(t, err)
				class, got := oauth.ClassifyRefreshError(err)
				require.NotEqual(t, oauth.RefreshClassNetwork, class)
				require.Equal(t, status, got)
				mu.Lock()
				defer mu.Unlock()
				require.Equal(t, 1, requests)
			})
		}
	}
}
