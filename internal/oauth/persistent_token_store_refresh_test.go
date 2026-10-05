package oauth

import (
	"context"
	"encoding/json"
	"errors"
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
	transport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// rotatingAS is an httptest authorization server whose refresh tokens are
// one-time use: reusing one revokes the grant (invalid_grant).
type rotatingAS struct {
	srv      *httptest.Server
	requests atomic.Int32
	mu       sync.Mutex
	current  string
	n        int
	status   int    // when non-zero, answer with this status
	errCode  string // and this OAuth error code
}

func newRotatingAS(t *testing.T, initialRT string) *rotatingAS {
	as := &rotatingAS{current: initialRT}
	as.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		as.requests.Add(1)
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		as.mu.Lock()
		defer as.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if as.status != 0 {
			w.WriteHeader(as.status)
			if as.errCode != "" {
				_ = json.NewEncoder(w).Encode(map[string]string{"error": as.errCode})
			} else {
				_, _ = w.Write([]byte("<html>down</html>"))
			}
			return
		}
		if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != as.current {
			as.current = "" // replay: revoke the whole grant
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		as.n++
		as.current = fmt.Sprintf("rt-%d", as.n)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": fmt.Sprintf("at-%d", as.n), "refresh_token": as.current,
			"token_type": "Bearer", "expires_in": 3600,
		})
	}))
	t.Cleanup(as.srv.Close)
	return as
}

// asRefreshFunc refreshes against the AS and persists through the store, the
// way mcp-go's handler.RefreshToken does.
func asRefreshFunc(as *rotatingAS, store client.TokenStore) RefreshFunc {
	return func(ctx context.Context, rec *storage.OAuthTokenRecord) (*client.Token, error) {
		form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rec.RefreshToken}, "client_id": {"cid"}}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, as.srv.URL+"/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			var oe transport.OAuthError
			if json.Unmarshal(body, &oe) == nil && oe.ErrorCode != "" {
				return nil, fmt.Errorf("refresh token request failed: %w", oe)
			}
			return nil, fmt.Errorf("refresh token request failed with status %d: %s", resp.StatusCode, body)
		}
		var tok client.Token
		if err := json.Unmarshal(body, &tok); err != nil {
			return nil, err
		}
		tok.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
		if err := store.SaveToken(ctx, &tok); err != nil {
			return nil, err
		}
		return &tok, nil
	}
}

var serverNameSeq atomic.Int64

// uniqueServerName keeps the process-wide coordinator's per-key state from
// leaking between -count repetitions of the same test.
func uniqueServerName(t *testing.T) string {
	return fmt.Sprintf("%s-%d", t.Name(), serverNameSeq.Add(1))
}

func seedToken(t *testing.T, db *storage.BoltDB, name, srvURL, rt string, expiresIn time.Duration) {
	t.Helper()
	require.NoError(t, db.SaveOAuthToken(&storage.OAuthTokenRecord{
		ServerName: GenerateServerKey(name, srvURL), DisplayName: name, AccessToken: "at-0", RefreshToken: rt,
		TokenType: "Bearer", ExpiresAt: time.Now().Add(expiresIn), ClientID: "cid",
	}))
}

func TestPersistentTokenStore_UnboundStoreNeverRefreshes(t *testing.T) {
	db := newTestBolt(t)
	as := newRotatingAS(t, "rt-0")
	name, u := uniqueServerName(t), "https://unbound.example.com/mcp"
	seedToken(t, db, name, u, "rt-0", -time.Minute)
	store := NewPersistentTokenStore(name, u, db)

	tok, err := store.GetToken(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "at-0", tok.AccessToken)
	assert.True(t, tok.IsExpired(), "unbound stores keep today's read-only behaviour")
	assert.Equal(t, int32(0), as.requests.Load())
}

func TestPersistentTokenStore_BoundRefreshesExpiredToken(t *testing.T) {
	db := newTestBolt(t)
	as := newRotatingAS(t, "rt-0")
	name, u := uniqueServerName(t), "https://bound.example.com/mcp"
	seedToken(t, db, name, u, "rt-0", -time.Minute)
	store := NewPersistentTokenStore(name, u, db)
	require.True(t, BindRefresher(store, RefreshBinding{Refresh: asRefreshFunc(as, store)}))

	const n = 20
	var wg sync.WaitGroup
	start := make(chan struct{})
	toks := make([]*client.Token, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			toks[i], errs[i] = store.GetToken(context.Background())
		}(i)
	}
	close(start)
	wg.Wait()

	assert.Equal(t, int32(1), as.requests.Load(), "exactly one token request")
	for i := 0; i < n; i++ {
		require.NoError(t, errs[i])
		assert.Equal(t, "at-1", toks[i].AccessToken)
		assert.True(t, toks[i].ExpiresAt.IsZero(), "expiry is owned by mcpproxy; mcp-go must never refresh")
		assert.False(t, toks[i].IsExpired())
	}
	rec, _ := db.GetOAuthToken(GenerateServerKey(name, u))
	assert.Equal(t, "rt-1", rec.RefreshToken)
	assert.False(t, rec.ExpiresAt.IsZero(), "persisted record keeps the real expiry")
	assert.Equal(t, "cid", rec.ClientID)
}

func TestPersistentTokenStore_BoundValidTokenNoRefresh(t *testing.T) {
	db := newTestBolt(t)
	as := newRotatingAS(t, "rt-0")
	name, u := uniqueServerName(t), "https://valid.example.com/mcp"
	seedToken(t, db, name, u, "rt-0", time.Hour)
	store := NewPersistentTokenStore(name, u, db)
	BindRefresher(store, RefreshBinding{Refresh: asRefreshFunc(as, store)})

	tok, err := store.GetToken(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "at-0", tok.AccessToken)
	assert.True(t, tok.ExpiresAt.IsZero())
	assert.Equal(t, int32(0), as.requests.Load())
}

func TestPersistentTokenStore_BoundTerminalAndTransient(t *testing.T) {
	t.Run("invalid_grant on expired token wraps ErrOAuthAuthorizationRequired", func(t *testing.T) {
		db := newTestBolt(t)
		as := newRotatingAS(t, "other")
		name, u := uniqueServerName(t), "https://terminal.example.com/mcp"
		seedToken(t, db, name, u, "rt-0", -time.Minute)
		store := NewPersistentTokenStore(name, u, db)
		BindRefresher(store, RefreshBinding{Refresh: asRefreshFunc(as, store)})

		_, err := store.GetToken(context.Background())
		assert.ErrorIs(t, err, transport.ErrOAuthAuthorizationRequired)
		_, err = store.GetToken(context.Background())
		assert.ErrorIs(t, err, transport.ErrOAuthAuthorizationRequired)
		assert.Equal(t, int32(1), as.requests.Load(), "latched: no network after invalid_grant")
	})

	t.Run("5xx on expired token returns ErrTokenRefreshTransient", func(t *testing.T) {
		db := newTestBolt(t)
		as := newRotatingAS(t, "rt-0")
		as.status = http.StatusServiceUnavailable
		name, u := uniqueServerName(t), "https://transient.example.com/mcp"
		seedToken(t, db, name, u, "rt-0", -time.Minute)
		store := NewPersistentTokenStore(name, u, db)
		BindRefresher(store, RefreshBinding{Refresh: asRefreshFunc(as, store)})

		tok, err := store.GetToken(context.Background())
		assert.Nil(t, tok, "the expired token is not handed to mcp-go")
		assert.ErrorIs(t, err, ErrTokenRefreshTransient)
		assert.False(t, errors.Is(err, transport.ErrOAuthAuthorizationRequired))
		_, err = store.GetToken(context.Background())
		assert.ErrorIs(t, err, ErrTokenRefreshTransient)
		assert.Equal(t, int32(1), as.requests.Load(), "cooldown: no network")
	})

	t.Run("5xx inside the grace period keeps serving the still-valid token", func(t *testing.T) {
		db := newTestBolt(t)
		as := newRotatingAS(t, "rt-0")
		as.status = http.StatusBadGateway
		name, u := uniqueServerName(t), "https://grace.example.com/mcp"
		seedToken(t, db, name, u, "rt-0", time.Hour)
		store := NewPersistentTokenStore(name, u, db).(*PersistentTokenStore)
		// 58 minutes later: 2 minutes left, inside the 5 minute grace period.
		store.now = func() time.Time { return time.Now().Add(58 * time.Minute) }
		BindRefresher(store, RefreshBinding{Refresh: asRefreshFunc(as, store)})

		tok, err := store.GetToken(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "at-0", tok.AccessToken)
		assert.Equal(t, int32(1), as.requests.Load())
	})

	t.Run("expired without refresh token needs authorization", func(t *testing.T) {
		db := newTestBolt(t)
		as := newRotatingAS(t, "rt-0")
		name, u := uniqueServerName(t), "https://nort.example.com/mcp"
		seedToken(t, db, name, u, "", -time.Minute)
		store := NewPersistentTokenStore(name, u, db)
		BindRefresher(store, RefreshBinding{Refresh: asRefreshFunc(as, store)})
		_, err := store.GetToken(context.Background())
		assert.ErrorIs(t, err, transport.ErrOAuthAuthorizationRequired)
		assert.Equal(t, int32(0), as.requests.Load())
	})
}

// FR-006a: a SaveToken that belongs to a flight whose generation no longer
// matches the stored record is discarded.
func TestPersistentTokenStore_SaveTokenCompareAndSwap(t *testing.T) {
	db := newTestBolt(t)
	name, u := uniqueServerName(t), "https://cas.example.com/mcp"
	seedToken(t, db, name, u, "rt-0", -time.Minute)
	key := GenerateServerKey(name, u)
	store := NewPersistentTokenStore(name, u, db)

	rec, _ := db.GetOAuthToken(key)
	staleCtx := withFlightGeneration(context.Background(), generationOf(rec))
	// A login rotates the grant first.
	require.NoError(t, store.SaveToken(context.Background(), &client.Token{AccessToken: "login-at", RefreshToken: "login-rt", ExpiresAt: time.Now().Add(time.Hour)}))
	// The old flight's result arrives late.
	require.NoError(t, store.SaveToken(staleCtx, &client.Token{AccessToken: "stale-at", RefreshToken: "stale-rt", ExpiresAt: time.Now().Add(time.Hour)}))

	got, _ := db.GetOAuthToken(key)
	assert.Equal(t, "login-at", got.AccessToken)
	assert.Equal(t, "login-rt", got.RefreshToken)
	assert.Equal(t, "cid", got.ClientID)
}

// Review round 5: "still valid" is judged when the flight returns. A token
// that expired while a slow refresh failed transiently is not handed out.
func TestPersistentTokenStore_ExpiryJudgedAfterSlowFlight(t *testing.T) {
	db := newTestBolt(t)
	as := newRotatingAS(t, "rt-0")
	as.status = http.StatusServiceUnavailable
	name, u := uniqueServerName(t), "https://slow.example.com/mcp"
	seedToken(t, db, name, u, "rt-0", time.Hour)
	store := NewPersistentTokenStore(name, u, db).(*PersistentTokenStore)
	var calls atomic.Int32
	base := time.Now()
	store.now = func() time.Time {
		if calls.Add(1) == 1 { // on arrival: one second left
			return base.Add(time.Hour - time.Second)
		}
		return base.Add(time.Hour + time.Second) // after the flight: expired
	}
	BindRefresher(store, RefreshBinding{Refresh: asRefreshFunc(as, store)})

	tok, err := store.GetToken(context.Background())
	assert.Nil(t, tok)
	assert.ErrorIs(t, err, ErrTokenRefreshTransient)
}
