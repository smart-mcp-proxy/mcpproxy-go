package oauth

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

type funcRuntime func(serverName string) error

func (f funcRuntime) RefreshOAuthToken(serverName string) error { return f(serverName) }

// Sol r9 on #1621: an invalid_client rejection superseded by a same-binding
// registration rewrite (corrected secret, callback port or redirect URI) is
// not terminal for a PROACTIVE refresh either. RefreshManager.executeRefresh
// must not mark the schedule failed, stop its timer or emit a permanent
// failure; a later scheduled attempt uses the corrected registration.
func TestProactiveRefreshSupersededInvalidClientIsNotTerminal(t *testing.T) {
	rewrite := func(t *testing.T, f *bindingFixture, b commitview.CredentialBinding) {
		ok, err := f.db.SaveOAuthClientIfBound(f.key, "acme", b, storage.OAuthClientSaveAlways, "cid", "sec2", 4100, "http://127.0.0.1:4100/oauth/callback")
		assert.NoError(t, err)
		assert.True(t, ok)
	}
	for _, window := range []string{"after-read (r5)", "before-clear (r6)", "after-clear (r7)"} {
		t.Run(window, func(t *testing.T) {
			f := newBindingFixture(t)
			b := f.create(t)
			_, err := f.db.SaveOAuthClientIfBound(f.key, "acme", b, storage.OAuthClientSaveAlways, "cid", "sec", 4000, "http://127.0.0.1:4000/oauth/callback")
			require.NoError(t, err)
			store := NewBoundPersistentTokenStore("acme", bindURL, f.db, b).(*PersistentTokenStore)
			require.NoError(t, store.SaveToken(context.Background(), &client.Token{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Minute)}))

			c := NewRefreshCoordinator()
			var outcomes []RefreshOutcome
			var outMu sync.Mutex
			c.SetCompletionHook(func(o RefreshOutcome) {
				outMu.Lock()
				outcomes = append(outcomes, o)
				outMu.Unlock()
			})

			var rewrote atomic.Bool
			var calls atomic.Int32
			req := RefreshRequest{
				Key: f.key, Binding: b, ServerName: "acme", ObservedRefreshToken: "rt",
				Trigger: RefreshTriggerProactive,
				Load:    func() (*storage.OAuthTokenRecord, error) { return f.db.GetOAuthTokenBound(f.key, b) },
				ClearClientAt: func(rec *storage.OAuthTokenRecord) (bool, error) {
					if window == "before-clear (r6)" && rewrote.CompareAndSwap(false, true) {
						rewrite(t, f, b)
					}
					cleared, err := clearClientIfBound(f.db, f.key, "acme", b, rec)
					if window == "after-clear (r7)" && cleared && err == nil && rewrote.CompareAndSwap(false, true) {
						rewrite(t, f, b)
					}
					return cleared, err
				},
				Refresh: func(ctx context.Context, rec *storage.OAuthTokenRecord) (*client.Token, error) {
					if calls.Add(1) > 1 {
						assert.Equal(t, "sec2", rec.ClientSecret, "the later attempt uses the corrected registration")
						tok := &client.Token{AccessToken: "at2", RefreshToken: "rt2", ExpiresAt: time.Now().Add(time.Hour)}
						return tok, store.SaveToken(ctx, tok)
					}
					if window == "after-read (r5)" && rewrote.CompareAndSwap(false, true) {
						rewrite(t, f, b)
					}
					return nil, fmt.Errorf("x: %w", transport.OAuthError{ErrorCode: "invalid_client"})
				},
			}

			m := NewRefreshManager(newMockTokenStore(), nil, &RefreshManagerConfig{Threshold: 0.1}, zaptest.NewLogger(t))
			m.SetRuntime(funcRuntime(func(string) error {
				_, _, _, err := c.DoServed(context.Background(), req)
				return err
			}))
			em := &mockEventEmitter{}
			m.SetEventEmitter(em)
			require.NoError(t, m.Start(context.Background()))
			t.Cleanup(m.Stop)
			m.OnTokenSaved("acme", time.Now().Add(time.Hour))

			m.executeRefresh("acme")
			require.True(t, rewrote.Load(), "the rewrite landed in the window")

			st := m.GetRefreshState("acme")
			require.NotNil(t, st)
			assert.NotEqual(t, RefreshStateFailed, st.State, "a superseded rejection does not fail the schedule")
			assert.Equal(t, 0, em.GetFailedEvents(), "no permanent-failure event")
			outMu.Lock()
			assert.Empty(t, outcomes, "no terminal outcome")
			outMu.Unlock()
			m.mu.Lock()
			sched := m.schedules["acme"]
			hasTimer := sched != nil && sched.Timer != nil
			m.mu.Unlock()
			assert.True(t, hasTimer, "a later attempt stays scheduled")

			after := f.record(t)
			require.NotNil(t, after)
			assert.Equal(t, "sec2", after.ClientSecret)

			// The later scheduled attempt refreshes with the corrected registration.
			m.executeRefresh("acme")
			assert.Equal(t, int32(2), calls.Load())
			assert.Equal(t, 0, em.GetFailedEvents())
			assert.Equal(t, "at2", f.record(t).AccessToken)
		})
	}
}
