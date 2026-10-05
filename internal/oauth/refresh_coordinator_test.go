package oauth

import (
	"context"
	"errors"
	"fmt"
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

// coordFixture is a BoltDB-backed token record plus a counting refresh func
// that rotates the refresh token, like a rotating authorization server.
type coordFixture struct {
	t     *testing.T
	db    *storage.BoltDB
	key   string
	name  string
	calls atomic.Int32
	// behaviour of the next refresh calls
	mu      sync.Mutex
	failErr error
	block   chan struct{} // when non-nil, refresh waits on it
	started chan struct{} // closed (once) when refresh starts
	once    sync.Once
}

func newCoordFixture(t *testing.T, clientID string) *coordFixture {
	t.Helper()
	f := &coordFixture{t: t, db: newTestBolt(t), name: "srv", key: GenerateServerKey("srv", "https://srv.example.com/mcp"), started: make(chan struct{})}
	require.NoError(t, f.db.SaveOAuthToken(&storage.OAuthTokenRecord{
		ServerName: f.key, DisplayName: f.name, AccessToken: "at-0", RefreshToken: "rt-0", TokenType: "Bearer",
		ExpiresAt: time.Now().Add(-time.Minute), ClientID: clientID,
	}))
	return f
}

func (f *coordFixture) load() (*storage.OAuthTokenRecord, error) { return f.db.GetOAuthToken(f.key) }

func (f *coordFixture) refresh(ctx context.Context, rec *storage.OAuthTokenRecord) (*client.Token, error) {
	n := f.calls.Add(1)
	f.once.Do(func() { close(f.started) })
	f.mu.Lock()
	block, failErr := f.block, f.failErr
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if failErr != nil {
		return nil, failErr
	}
	tok := &client.Token{AccessToken: fmt.Sprintf("at-%d", n), RefreshToken: fmt.Sprintf("rt-%d", n), TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour)}
	gen := generationOf(rec)
	err := f.db.UpdateOAuthToken(f.key, func(cur *storage.OAuthTokenRecord) error {
		if generationOf(cur) != gen {
			return storage.ErrSkipOAuthTokenUpdate
		}
		cur.AccessToken, cur.RefreshToken, cur.ExpiresAt = tok.AccessToken, tok.RefreshToken, tok.ExpiresAt
		return nil
	})
	return tok, err
}

func (f *coordFixture) req(trigger RefreshTrigger, observed string) RefreshRequest {
	return RefreshRequest{
		Key: f.key, ServerName: f.name, ObservedRefreshToken: observed, Trigger: trigger,
		Load: f.load, Refresh: f.refresh,
		ClearClient: func(expected string) (bool, error) { return f.db.ClearOAuthClientCredentialsIf(f.key, expected) },
	}
}

func newTestCoordinator() *RefreshCoordinator {
	c := NewRefreshCoordinator()
	c.flowActive = func(string) bool { return false }
	return c
}

func TestRefreshCoordinator_SingleFlight(t *testing.T) {
	f := newCoordFixture(t, "cid")
	c := newTestCoordinator()

	const n = 50
	var wg sync.WaitGroup
	start := make(chan struct{})
	toks := make([]*client.Token, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			trig := RefreshTriggerReactive
			if i == 0 {
				trig = RefreshTriggerProactive
			}
			toks[i], _, errs[i] = c.Do(context.Background(), f.req(trig, "rt-0"))
		}(i)
	}
	close(start)
	wg.Wait()

	assert.Equal(t, int32(1), f.calls.Load(), "exactly one refresh flight")
	for i := 0; i < n; i++ {
		require.NoError(t, errs[i])
		assert.Equal(t, "at-1", toks[i].AccessToken)
	}
}

func TestRefreshCoordinator_StaleObservedTokenSkipsNetwork(t *testing.T) {
	f := newCoordFixture(t, "cid")
	c := newTestCoordinator()

	tok, skipped, err := c.Do(context.Background(), f.req(RefreshTriggerReactive, "rt-0"))
	require.NoError(t, err)
	assert.False(t, skipped)
	assert.Equal(t, "rt-1", tok.RefreshToken)

	// A caller that read rt-0 before the rotation must not send it again.
	tok, skipped, err = c.Do(context.Background(), f.req(RefreshTriggerReactive, "rt-0"))
	require.NoError(t, err)
	assert.True(t, skipped)
	assert.Equal(t, "at-1", tok.AccessToken)
	assert.Equal(t, int32(1), f.calls.Load())
}

func TestRefreshCoordinator_WaitersShareFailure(t *testing.T) {
	f := newCoordFixture(t, "cid")
	f.block = make(chan struct{})
	f.failErr = fmt.Errorf("refresh token request failed: %w", transport.OAuthError{ErrorCode: "server_error"})
	c := newTestCoordinator()

	const n = 10
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, errs[i] = c.Do(context.Background(), f.req(RefreshTriggerProactive, "rt-0"))
		}(i)
	}
	<-f.started
	waitForWaiters(t, c, f.key, n)
	close(f.block)
	wg.Wait()

	assert.Equal(t, int32(1), f.calls.Load())
	for i := 1; i < n; i++ {
		assert.Same(t, errs[0], errs[i], "all waiters get the same error value")
	}
	cls, _ := ClassifyRefreshError(errs[0])
	assert.Equal(t, RefreshClassServerError, cls)
}

func TestRefreshCoordinator_CancelledInitiatorDoesNotCancelFlight(t *testing.T) {
	f := newCoordFixture(t, "cid")
	f.block = make(chan struct{})
	c := newTestCoordinator()

	ctx, cancel := context.WithCancel(context.Background())
	initDone := make(chan error, 1)
	go func() {
		_, _, err := c.Do(ctx, f.req(RefreshTriggerReactive, "rt-0"))
		initDone <- err
	}()
	<-f.started
	cancel()
	assert.ErrorIs(t, <-initDone, context.Canceled)

	waiter := make(chan *client.Token, 1)
	go func() {
		tok, _, err := c.Do(context.Background(), f.req(RefreshTriggerReactive, "rt-0"))
		assert.NoError(t, err)
		waiter <- tok
	}()
	waitForWaiters(t, c, f.key, 1)
	close(f.block)
	tok := <-waiter
	assert.Equal(t, "at-1", tok.AccessToken)
	assert.Equal(t, int32(1), f.calls.Load())
}

func TestRefreshCoordinator_DifferentKeysRunInParallel(t *testing.T) {
	a := newCoordFixture(t, "cid")
	b := newCoordFixture(t, "cid")
	b.key = GenerateServerKey("other", "https://other.example.com/mcp")
	require.NoError(t, b.db.SaveOAuthToken(&storage.OAuthTokenRecord{ServerName: b.key, AccessToken: "at-0", RefreshToken: "rt-0", ExpiresAt: time.Now().Add(-time.Minute), ClientID: "cid"}))
	a.block = make(chan struct{})
	c := newTestCoordinator()

	aDone := make(chan struct{})
	go func() {
		defer close(aDone)
		_, _, _ = c.Do(context.Background(), a.req(RefreshTriggerReactive, "rt-0"))
	}()
	<-a.started
	// b must complete while a's flight is still blocked.
	tok, _, err := c.Do(context.Background(), b.req(RefreshTriggerReactive, "rt-0"))
	require.NoError(t, err)
	assert.Equal(t, "at-1", tok.AccessToken)
	close(a.block)
	<-aDone
}

func TestRefreshCoordinator_TerminalLatchUntilNewToken(t *testing.T) {
	f := newCoordFixture(t, "cid")
	f.failErr = fmt.Errorf("refresh token request failed: %w", transport.OAuthError{ErrorCode: "invalid_grant"})
	c := newTestCoordinator()
	var hookCalls atomic.Int32
	c.SetCompletionHook(func(o RefreshOutcome) {
		hookCalls.Add(1)
		assert.Equal(t, RefreshClassInvalidGrant, o.Class)
		assert.Equal(t, "srv", o.ServerName)
	})

	_, _, err := c.Do(context.Background(), f.req(RefreshTriggerReactive, "rt-0"))
	cls, _ := ClassifyRefreshError(err)
	require.Equal(t, RefreshClassInvalidGrant, cls)
	assert.Equal(t, int32(1), hookCalls.Load())

	for i := 0; i < 5; i++ {
		_, skipped, err := c.Do(context.Background(), f.req(RefreshTriggerReactive, "rt-0"))
		cls, _ := ClassifyRefreshError(err)
		assert.Equal(t, RefreshClassInvalidGrant, cls)
		assert.True(t, skipped, "latched: no network")
	}
	assert.Equal(t, int32(1), f.calls.Load(), "no network after invalid_grant")
	assert.Equal(t, int32(1), hookCalls.Load(), "hook runs once per flight")

	// A login saves a new grant: the latch no longer applies.
	f.failErr = nil
	require.NoError(t, f.db.UpdateOAuthToken(f.key, func(r *storage.OAuthTokenRecord) error {
		r.AccessToken, r.RefreshToken, r.ExpiresAt = "login-at", "login-rt", time.Now().Add(-time.Second)
		return nil
	}))
	tok, _, err := c.Do(context.Background(), f.req(RefreshTriggerReactive, "login-rt"))
	require.NoError(t, err)
	assert.Equal(t, "at-2", tok.AccessToken)
}

func TestRefreshCoordinator_TransientCooldown(t *testing.T) {
	f := newCoordFixture(t, "cid")
	f.failErr = errors.New("refresh token request failed with status 503: <html>")
	c := newTestCoordinator()
	now := time.Now()
	c.now = func() time.Time { return now }

	_, _, err := c.Do(context.Background(), f.req(RefreshTriggerReactive, "rt-0"))
	cls, status := ClassifyRefreshError(err)
	require.Equal(t, RefreshClassServerError, cls)
	assert.Equal(t, 503, status)

	_, skipped, err := c.Do(context.Background(), f.req(RefreshTriggerReactive, "rt-0"))
	assert.ErrorIs(t, err, ErrTokenRefreshTransient)
	assert.True(t, skipped)
	assert.Equal(t, int32(1), f.calls.Load(), "cooldown: no network for reactive callers")

	// The proactive path has its own backoff and is not held by the cooldown.
	_, _, _ = c.Do(context.Background(), f.req(RefreshTriggerProactive, "rt-0"))
	assert.Equal(t, int32(2), f.calls.Load())

	// After the cooldown (2nd consecutive failure: 20 s) reactive callers retry.
	now = now.Add(RetryBackoffBase*2 + time.Second)
	_, _, _ = c.Do(context.Background(), f.req(RefreshTriggerReactive, "rt-0"))
	assert.Equal(t, int32(3), f.calls.Load())
}

// FR-006a: a login that supersedes an in-flight refresh wins.
func TestRefreshCoordinator_StaleFlightAfterLogin(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failErr error
	}{
		{"success discarded", nil},
		{"invalid_grant ignored", fmt.Errorf("x: %w", transport.OAuthError{ErrorCode: "invalid_grant"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCoordFixture(t, "old-client")
			f.block = make(chan struct{})
			f.failErr = tc.failErr
			c := newTestCoordinator()
			var hookCalls atomic.Int32
			c.SetCompletionHook(func(RefreshOutcome) { hookCalls.Add(1) })

			type res struct {
				tok *client.Token
				err error
			}
			done := make(chan res, 1)
			go func() {
				tok, _, err := c.Do(context.Background(), f.req(RefreshTriggerReactive, "rt-0"))
				done <- res{tok, err}
			}()
			<-f.started
			// Login: new DCR client and new token.
			require.NoError(t, f.db.UpdateOAuthClientCredentials(f.key, "new-client", "s", 1, "r"))
			require.NoError(t, f.db.UpdateOAuthToken(f.key, func(r *storage.OAuthTokenRecord) error {
				r.AccessToken, r.RefreshToken, r.ExpiresAt = "login-at", "login-rt", time.Now().Add(time.Hour)
				return nil
			}))
			close(f.block)
			r := <-done
			require.NoError(t, r.err)
			assert.Equal(t, "login-at", r.tok.AccessToken)

			rec, _ := f.db.GetOAuthToken(f.key)
			assert.Equal(t, "login-at", rec.AccessToken)
			assert.Equal(t, "login-rt", rec.RefreshToken)
			assert.Equal(t, "new-client", rec.ClientID)
			assert.Equal(t, int32(0), hookCalls.Load(), "stale completion has no terminal side effects")

			// Not latched: the next flight (on the login grant) reaches the network.
			f.failErr = nil
			_, _, err := c.Do(context.Background(), f.req(RefreshTriggerReactive, "login-rt"))
			require.NoError(t, err)
			assert.Equal(t, int32(2), f.calls.Load())
		})
	}
}

func TestRefreshCoordinator_InvalidClient(t *testing.T) {
	invalidClient := fmt.Errorf("x: %w", transport.OAuthError{ErrorCode: "invalid_client"})

	t.Run("dcr clears once then re-registration rejected is terminal", func(t *testing.T) {
		f := newCoordFixture(t, "dcr-1")
		f.failErr = invalidClient
		c := newTestCoordinator()

		_, _, err := c.Do(context.Background(), f.req(RefreshTriggerProactive, "rt-0"))
		var failure *RefreshFailure
		require.ErrorAs(t, err, &failure)
		assert.Equal(t, RefreshClassInvalidClient, failure.Class)
		assert.Contains(t, err.Error(), "sign in again to re-register")
		rec, _ := f.db.GetOAuthToken(f.key)
		assert.Empty(t, rec.ClientID, "DCR registration cleared")
		assert.Equal(t, "rt-0", rec.RefreshToken, "token fields untouched")

		// Latched on the post-clear record: no further network.
		_, _, err = c.Do(context.Background(), f.req(RefreshTriggerProactive, "rt-0"))
		assert.Error(t, err)
		assert.Equal(t, int32(1), f.calls.Load())

		// Re-registration (no successful token response yet) is rejected again.
		require.NoError(t, f.db.UpdateOAuthClientCredentials(f.key, "dcr-2", "", 1, "r"))
		_, _, err = c.Do(context.Background(), f.req(RefreshTriggerProactive, "rt-0"))
		require.ErrorAs(t, err, &failure)
		assert.Contains(t, err.Error(), "rejects new client registrations")
		rec, _ = f.db.GetOAuthToken(f.key)
		assert.Equal(t, "dcr-2", rec.ClientID, "second rejection does not clear again")

		// The same rule applies to the login flow's code exchange.
		err = c.AnnotateCodeExchangeError(f.key, false, invalidClient)
		assert.Contains(t, err.Error(), "rejects new client registrations")
		cls, _ := ClassifyRefreshError(err)
		assert.Equal(t, RefreshClassInvalidClient, cls)

		// A successful token response resets the rule.
		c.NoteTokenSaved(f.key)
		err = c.AnnotateCodeExchangeError(f.key, false, invalidClient)
		assert.Same(t, invalidClient, err)
	})

	t.Run("static client is terminal and keeps credentials", func(t *testing.T) {
		f := newCoordFixture(t, "static-id")
		f.failErr = invalidClient
		c := newTestCoordinator()
		req := f.req(RefreshTriggerProactive, "rt-0")
		req.StaticClient = true
		var cleared atomic.Int32
		req.ClearClient = func(string) (bool, error) { cleared.Add(1); return true, nil }

		_, _, err := c.Do(context.Background(), req)
		assert.Contains(t, err.Error(), "oauth.client_id")
		assert.Equal(t, int32(0), cleared.Load())
		cls, _ := ClassifyRefreshError(err)
		assert.True(t, cls.IsTerminal())
	})

	t.Run("active login flow prevents the clear", func(t *testing.T) {
		f := newCoordFixture(t, "dcr-1")
		f.failErr = invalidClient
		c := newTestCoordinator()
		c.flowActive = func(string) bool { return true }

		_, _, err := c.Do(context.Background(), f.req(RefreshTriggerProactive, "rt-0"))
		assert.Error(t, err)
		rec, _ := f.db.GetOAuthToken(f.key)
		assert.Equal(t, "dcr-1", rec.ClientID)
	})

	t.Run("compare-and-clear keeps a registration saved by a concurrent login", func(t *testing.T) {
		f := newCoordFixture(t, "dcr-1")
		f.block = make(chan struct{})
		f.failErr = invalidClient
		c := newTestCoordinator()
		done := make(chan error, 1)
		go func() {
			_, _, err := c.Do(context.Background(), f.req(RefreshTriggerProactive, "rt-0"))
			done <- err
		}()
		<-f.started
		require.NoError(t, f.db.UpdateOAuthClientCredentials(f.key, "dcr-login", "", 1, "r"))
		close(f.block)
		<-done
		rec, _ := f.db.GetOAuthToken(f.key)
		assert.Equal(t, "dcr-login", rec.ClientID)
	})
}

// waitForWaiters blocks until n callers are attached to the key's flight.
func waitForWaiters(t *testing.T, c *RefreshCoordinator, key string, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c.waiters(key) >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d waiters", n)
}
