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
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 116 PR2b (#1607, quickstart §T2): an OAuth write, clear or refresh of
// an instance a commit removed, replaced or re-bound cannot land on, reuse or
// erase the credentials of whatever serves the name afterwards. Each test
// parks the REAL token store / clear / coordinator call at its seam, lets the
// competing storage commit (the removal purge, the OAuth-config clear, a
// replacement's save) run, releases it and asserts on storage.

const bindURL = "https://acme.example/mcp"

type bindingFixture struct {
	m   *storage.Manager
	db  *storage.BoltDB
	key string
	g   int64
}

func newBindingFixture(t *testing.T) *bindingFixture {
	t.Helper()
	m, err := storage.NewManager(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })
	return &bindingFixture{m: m, db: m.GetBoltDB(), key: GenerateServerKey("acme", bindURL)}
}

func (f *bindingFixture) entry(scopes ...string) *config.ServerConfig {
	sc := &config.ServerConfig{Name: "acme", URL: bindURL, Protocol: "http", Enabled: true}
	if len(scopes) > 0 {
		sc.OAuth = &config.OAuthConfig{Scopes: scopes}
	}
	return sc
}

// create commits a new incarnation of acme (create or re-add): {G, G, G}.
func (f *bindingFixture) create(t *testing.T) commitview.CredentialBinding {
	t.Helper()
	f.g++
	sg := commitview.ServerGen{Gen: f.g, Incarnation: f.g, OAuthEpoch: f.g}
	require.NoError(t, f.m.ApplyServerCommit(&storage.ServerCommitBatch{Generation: f.g, Saves: []storage.ServerSave{{Config: f.entry(), Stamp: sg}}}))
	return sg.Binding()
}

// remove commits acme's removal (the full purge).
func (f *bindingFixture) remove(t *testing.T) {
	t.Helper()
	f.g++
	require.NoError(t, f.m.ApplyServerCommit(&storage.ServerCommitBatch{Generation: f.g, Removes: []string{"acme"}}))
}

// oauthChange commits an OAuth config change of acme that keeps the name
// and URL: the commit clears its OAuth state and raises the epoch.
func (f *bindingFixture) oauthChange(t *testing.T, inc int64) commitview.CredentialBinding {
	t.Helper()
	f.g++
	sg := commitview.ServerGen{Gen: f.g, Incarnation: inc, OAuthEpoch: f.g}
	require.NoError(t, f.m.ApplyServerCommit(&storage.ServerCommitBatch{
		Generation:  f.g,
		Saves:       []storage.ServerSave{{Config: f.entry(fmt.Sprintf("scope-%d", f.g)), Stamp: sg}},
		OAuthClears: []string{"acme"},
	}))
	return sg.Binding()
}

func (f *bindingFixture) record(t *testing.T) *storage.OAuthTokenRecord {
	t.Helper()
	rec, err := f.db.GetOAuthTokenBound(f.key, commitview.CredentialBinding{})
	if err != nil {
		return nil
	}
	return rec
}

// parkOnce returns a hook that parks the first matching call until release
// is closed, and closes parked when it does.
func parkOnce(match func() bool) (hook func(), parked, release chan struct{}) {
	parked, release = make(chan struct{}), make(chan struct{})
	var once sync.Once
	hook = func() {
		if !match() {
			return
		}
		first := false
		once.Do(func() { first = true })
		if !first {
			return
		}
		close(parked)
		<-release
	}
	return hook, parked, release
}

func await(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func freshToken(access string) *client.Token {
	return &client.Token{AccessToken: access, RefreshToken: "rt-" + access, TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour)}
}

// T2.9 (and the login half of T2.13): a token save of the old instance,
// parked after its network response and before it persists, does not
// re-create a token (or the login's client registration) after a removal, a
// remove and same-URL re-add, or an OAuth config change; the replacement
// reads nothing of the old server.
func TestLateTokenSave_T2_9_T2_13(t *testing.T) {
	for _, variant := range []string{"remove", "readd", "oauth_change"} {
		t.Run(variant, func(t *testing.T) {
			f := newBindingFixture(t)
			old := f.create(t)
			store := NewBoundPersistentTokenStore("acme", bindURL, f.db, old)
			require.NoError(t, store.SaveToken(context.Background(), freshToken("before")), "the live instance saves")

			hook, parked, release := parkOnce(func() bool { return true })
			restore := SetBindingTestHooks(BindingTestHooks{BeforePersist: func(server string, b commitview.CredentialBinding) {
				if server == "acme" && b == old {
					hook()
				}
			}})
			defer restore()

			ctx := WithLoginClient(context.Background(), LoginClient{ClientID: "old-client", ClientSecret: "old-secret", CallbackPort: 5555, RedirectURI: "http://127.0.0.1:5555/oauth/callback"})
			done := make(chan error, 1)
			go func() { done <- store.SaveToken(ctx, freshToken("late")) }()
			await(t, parked, "the old instance's token save to park")

			var cur commitview.CredentialBinding
			switch variant {
			case "remove":
				f.remove(t)
			case "readd":
				f.remove(t)
				cur = f.create(t)
			case "oauth_change":
				cur = f.oauthChange(t, old.Incarnation)
			}
			assert.Nil(t, f.record(t), "precondition: the commit removed acme's OAuth record")

			close(release)
			err := <-done
			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrCredentialBindingStale), "the stale save reports its binding: %v", err)
			assert.Nil(t, f.record(t), "no token or client registration is re-created after the commit")

			if !cur.IsZero() {
				replacement := NewBoundPersistentTokenStore("acme", bindURL, f.db, cur)
				_, err := replacement.GetToken(context.Background())
				assert.True(t, errors.Is(err, transport.ErrNoToken), "the replacement starts unauthenticated")
				id, _, _, _, _, err := f.db.GetOAuthClientCredentialsBound(f.key, cur)
				require.NoError(t, err)
				assert.Empty(t, id, "the replacement inherits no client registration")
			}
		})
	}
}

// T2.15 (a): the 5xx-recovery ClearToken of the old instance, parked before
// its storage transaction, never erases the replacement's token: after an
// OAuth config change (binding moved), after a same-binding restart whose
// replacement saved a new token, and after one that re-saved the SAME access
// token (review R7.3: the revision, not the content, decides).
func TestBoundTokenClear_T2_15(t *testing.T) {
	for _, variant := range []string{"oauth_change", "restart_new_token", "restart_same_token"} {
		t.Run(variant, func(t *testing.T) {
			f := newBindingFixture(t)
			old := f.create(t)
			oldStore := NewBoundPersistentTokenStore("acme", bindURL, f.db, old).(*PersistentTokenStore)
			require.NoError(t, oldStore.SaveToken(context.Background(), freshToken("shared")))
			_, err := oldStore.GetToken(context.Background())
			require.NoError(t, err, "the old instance read the record it later clears")

			hook, parked, release := parkOnce(func() bool { return true })
			restore := SetBindingTestHooks(BindingTestHooks{BeforeClear: func(server string, b commitview.CredentialBinding, op string) {
				if server == "acme" && op == "token" {
					hook()
				}
			}})
			defer restore()
			done := make(chan error, 1)
			go func() { done <- oldStore.ClearToken() }()
			await(t, parked, "the old instance's 5xx clear to park")

			replBinding := old
			if variant == "oauth_change" {
				replBinding = f.oauthChange(t, old.Incarnation)
			}
			repl := NewBoundPersistentTokenStore("acme", bindURL, f.db, replBinding)
			access := "replacement"
			if variant == "restart_same_token" {
				access = "shared"
			}
			require.NoError(t, repl.SaveToken(context.Background(), freshToken(access)))
			before := f.record(t)
			require.NotNil(t, before)

			close(release)
			require.NoError(t, <-done)
			after := f.record(t)
			require.NotNil(t, after, "the replacement's token survives the stale clear")
			assert.Equal(t, before.AccessToken, after.AccessToken)
			assert.Equal(t, before.Revision, after.Revision, "the record is untouched")
		})
	}
}

// T2.15 (b): the config-build DCR clear (clearOAuthCredentials, stale
// redirect URI) of the old instance, parked before its storage transaction,
// leaves the replacement's registration alone: after an OAuth config change,
// and after a same-binding replacement that re-pinned the SAME client id with
// a new callback port and redirect URI (review R7.3).
func TestBoundDCRClear_T2_15(t *testing.T) {
	for _, variant := range []string{"oauth_change", "same_binding_repin"} {
		t.Run(variant, func(t *testing.T) {
			f := newBindingFixture(t)
			old := f.create(t)
			_, err := f.db.SaveOAuthClientIfBound(f.key, "acme", old, storage.OAuthClientSaveAlways, "cid", "sec", 4000, "http://127.0.0.1:4000/oauth/callback")
			require.NoError(t, err)
			_, _, _, _, observed, err := f.db.GetOAuthClientCredentialsBound(f.key, old)
			require.NoError(t, err)

			hook, parked, release := parkOnce(func() bool { return true })
			restore := SetBindingTestHooks(BindingTestHooks{BeforeClear: func(server string, b commitview.CredentialBinding, op string) {
				if server == "acme" && op == "dcr_client" && b == old {
					hook()
				}
			}})
			defer restore()
			done := make(chan struct{})
			go func() {
				defer close(done)
				clearOAuthCredentials(zap.NewNop(), f.db, Env{Binding: old}, f.key, "acme", observed)
			}()
			await(t, parked, "the old instance's DCR clear to park")

			replBinding := old
			if variant == "oauth_change" {
				replBinding = f.oauthChange(t, old.Incarnation)
			}
			ok, err := f.db.SaveOAuthClientIfBound(f.key, "acme", replBinding, storage.OAuthClientSaveAlways, "cid", "sec2", 4100, "http://127.0.0.1:4100/oauth/callback")
			require.NoError(t, err)
			require.True(t, ok)
			before := f.record(t)

			close(release)
			await(t, done, "the released clear")
			after := f.record(t)
			require.NotNil(t, after)
			assert.Equal(t, "cid", after.ClientID)
			assert.Equal(t, "sec2", after.ClientSecret)
			assert.Equal(t, 4100, after.CallbackPort)
			assert.Equal(t, "http://127.0.0.1:4100/oauth/callback", after.RedirectURI)
			assert.Equal(t, before.Revision, after.Revision, "the replacement's registration is byte-for-byte unchanged")
		})
	}
}

// T2.16 (review R7.1): refresh flights are keyed by binding. An old-binding
// caller parked after its bound read, released while the replacement's
// flight runs, gets ErrNoRefreshToken from its own bound load with no
// network request and never the replacement's token; and the replacement
// inherits neither the old flight's failure nor its terminal latch.
func TestRefreshFlightsByBinding_T2_16(t *testing.T) {
	t.Run("old caller does not join the replacement flight", func(t *testing.T) {
		f := newBindingFixture(t)
		old := f.create(t)
		oldStore := NewBoundPersistentTokenStore("acme", bindURL, f.db, old).(*PersistentTokenStore)
		// A token inside its refresh margin, so GetToken refreshes.
		require.NoError(t, oldStore.SaveToken(context.Background(), &client.Token{AccessToken: "old-at", RefreshToken: "old-rt", ExpiresAt: time.Now().Add(-time.Second)}))
		var oldNetwork atomic.Int32
		BindRefresher(oldStore, RefreshBinding{Refresh: func(ctx context.Context, rec *storage.OAuthTokenRecord) (*client.Token, error) {
			oldNetwork.Add(1)
			return nil, errors.New("must not be called")
		}})

		hook, parked, release := parkOnce(func() bool { return true })
		restore := SetBindingTestHooks(BindingTestHooks{BeforeRefreshDo: func(server string, b commitview.CredentialBinding) {
			if server == "acme" && b == old {
				hook()
			}
		}})
		defer restore()
		type result struct {
			tok *client.Token
			err error
		}
		done := make(chan result, 1)
		go func() {
			tok, err := oldStore.GetToken(context.Background())
			done <- result{tok, err}
		}()
		await(t, parked, "the old caller to park after its bound read")

		cur := f.oauthChange(t, old.Incarnation)
		replStore := NewBoundPersistentTokenStore("acme", bindURL, f.db, cur).(*PersistentTokenStore)
		require.NoError(t, replStore.SaveToken(context.Background(), &client.Token{AccessToken: "new-at", RefreshToken: "new-rt", ExpiresAt: time.Now().Add(-time.Second)}))
		flightStarted, flightRelease := make(chan struct{}), make(chan struct{})
		BindRefresher(replStore, RefreshBinding{Refresh: func(ctx context.Context, rec *storage.OAuthTokenRecord) (*client.Token, error) {
			close(flightStarted)
			<-flightRelease
			tok := &client.Token{AccessToken: "new-at-2", RefreshToken: "new-rt-2", ExpiresAt: time.Now().Add(time.Hour)}
			return tok, replStore.SaveToken(ctx, tok)
		}})
		replDone := make(chan result, 1)
		go func() {
			tok, err := replStore.GetToken(context.Background())
			replDone <- result{tok, err}
		}()
		await(t, flightStarted, "the replacement's refresh flight")

		close(release)
		var got result
		select {
		case got = <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("the old caller joined the replacement's flight instead of answering from its own binding")
		}
		require.Error(t, got.err)
		assert.Nil(t, got.tok, "the old caller never receives the replacement's token")
		assert.Zero(t, oldNetwork.Load(), "no network request under the obsolete binding")

		close(flightRelease)
		r := <-replDone
		require.NoError(t, r.err)
		assert.Equal(t, "new-at-2", r.tok.AccessToken)
	})

	t.Run("replacement inherits no latch of the old binding", func(t *testing.T) {
		c := NewRefreshCoordinator()
		oldB := commitview.CredentialBinding{Incarnation: 1, OAuthEpoch: 1}
		newB := commitview.CredentialBinding{Incarnation: 1, OAuthEpoch: 2}
		var recMu sync.Mutex
		rec := &storage.OAuthTokenRecord{AccessToken: "at", RefreshToken: "rt"}
		load := func() (*storage.OAuthTokenRecord, error) {
			recMu.Lock()
			defer recMu.Unlock()
			cp := *rec
			return &cp, nil
		}
		var hookBindings []commitview.CredentialBinding
		var hookMu sync.Mutex
		c.SetCompletionHook(func(o RefreshOutcome) {
			hookMu.Lock()
			hookBindings = append(hookBindings, o.Binding)
			hookMu.Unlock()
		})
		_, _, err := c.Do(context.Background(), RefreshRequest{Key: "acme_k", Binding: oldB, ServerName: "acme", ObservedRefreshToken: "rt", Trigger: RefreshTriggerReactive, Load: load,
			Refresh: func(context.Context, *storage.OAuthTokenRecord) (*client.Token, error) {
				return nil, &RefreshFailure{Class: RefreshClassInvalidGrant, Message: "revoked", Err: ErrRefreshFailed}
			}})
		require.Error(t, err, "the old binding's flight fails terminally and latches")
		var calls atomic.Int32
		tok, _, err := c.Do(context.Background(), RefreshRequest{Key: "acme_k", Binding: newB, ServerName: "acme", ObservedRefreshToken: "rt", Trigger: RefreshTriggerReactive, Load: load,
			Refresh: func(context.Context, *storage.OAuthTokenRecord) (*client.Token, error) {
				calls.Add(1)
				recMu.Lock()
				rec.AccessToken = "at2"
				recMu.Unlock()
				return &client.Token{AccessToken: "at2", RefreshToken: "rt"}, nil
			}})
		require.NoError(t, err, "the replacement's binding is not latched by the old flight")
		assert.Equal(t, int32(1), calls.Load(), "the replacement runs its own flight")
		assert.Equal(t, "at2", tok.AccessToken)
		hookMu.Lock()
		assert.Equal(t, []commitview.CredentialBinding{oldB}, hookBindings, "the terminal outcome names the binding it ran under")
		hookMu.Unlock()

		c.Forget("acme_k", oldB)
		c.mu.Lock()
		_, still := c.keys[flightKey{"acme_k", oldB}]
		c.mu.Unlock()
		assert.False(t, still, "Forget drops the obsolete binding's state")
	})
}

// Sol r5 on #1621: an invalid_client refresh failure clears the DCR
// registration of the record the failed request used (its revision), not of
// the record re-read after the request. A same-binding replacement that
// rewrote the registration meanwhile (same client id and refresh token,
// corrected secret, callback port and redirect URI) keeps it.
func TestBoundRefreshInvalidClientKeepsSameBindingRewrite(t *testing.T) {
	f := newBindingFixture(t)
	b := f.create(t)
	_, err := f.db.SaveOAuthClientIfBound(f.key, "acme", b, storage.OAuthClientSaveAlways, "cid", "sec", 4000, "http://127.0.0.1:4000/oauth/callback")
	require.NoError(t, err)
	store := NewBoundPersistentTokenStore("acme", bindURL, f.db, b).(*PersistentTokenStore)
	require.NoError(t, store.SaveToken(context.Background(), &client.Token{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(-time.Second)}))

	parked, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	BindRefresher(store, RefreshBinding{Refresh: func(ctx context.Context, rec *storage.OAuthTokenRecord) (*client.Token, error) {
		if calls.Add(1) > 1 {
			// The replacement's corrected registration is accepted; the
			// refresh persists through the store like the real one.
			tok := &client.Token{AccessToken: "at2", RefreshToken: "rt2", ExpiresAt: time.Now().Add(time.Hour)}
			return tok, store.SaveToken(ctx, tok)
		}
		close(parked)
		<-release
		return nil, fmt.Errorf("x: %w", transport.OAuthError{ErrorCode: "invalid_client"})
	}})
	done := make(chan error, 1)
	go func() {
		_, err := store.GetToken(context.Background())
		done <- err
	}()
	await(t, parked, "the refresh request to park after its read")

	ok, err := f.db.SaveOAuthClientIfBound(f.key, "acme", b, storage.OAuthClientSaveAlways, "cid", "sec2", 4100, "http://127.0.0.1:4100/oauth/callback")
	require.NoError(t, err)
	require.True(t, ok)
	before := f.record(t)
	require.Equal(t, "rt", before.RefreshToken)

	close(release)
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the refresh")
	}
	after := f.record(t)
	require.NotNil(t, after)
	assert.Equal(t, "cid", after.ClientID)
	assert.Equal(t, "sec2", after.ClientSecret)
	assert.Equal(t, 4100, after.CallbackPort)
	assert.Equal(t, "http://127.0.0.1:4100/oauth/callback", after.RedirectURI)
	assert.Equal(t, before.Revision, after.Revision, "the replacement's registration is unchanged")

	// The superseded failure latched nothing: the next refresh runs against
	// the replacement's registration.
	tok, err := store.GetToken(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "at2", tok.AccessToken)
	assert.Equal(t, int32(2), calls.Load())
}

// Sol r6 on #1621: the same-binding rewrite lands after the flight's
// revision check, inside the window before its revision-checked DCR clear.
// The clear correctly refuses (the record moved on), and that refusal must
// reach the flight: the old rejection is of the registration the request
// sent, so it latches nothing and emits no terminal outcome, and the next
// GetToken refreshes with the corrected registration.
func TestBoundRefreshInvalidClientKeepsRewriteBeforeClear(t *testing.T) {
	f := newBindingFixture(t)
	b := f.create(t)
	t.Cleanup(func() { DefaultRefreshCoordinator().Forget(f.key, b) })
	_, err := f.db.SaveOAuthClientIfBound(f.key, "acme", b, storage.OAuthClientSaveAlways, "cid", "sec", 4000, "http://127.0.0.1:4000/oauth/callback")
	require.NoError(t, err)
	store := NewBoundPersistentTokenStore("acme", bindURL, f.db, b).(*PersistentTokenStore)
	require.NoError(t, store.SaveToken(context.Background(), &client.Token{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(-time.Second)}))

	var outcomes []RefreshOutcome
	var outMu sync.Mutex
	DefaultRefreshCoordinator().SetCompletionHook(func(o RefreshOutcome) {
		outMu.Lock()
		outcomes = append(outcomes, o)
		outMu.Unlock()
	})
	t.Cleanup(func() { DefaultRefreshCoordinator().SetCompletionHook(nil) })

	var rewrote atomic.Bool
	restore := SetBindingTestHooks(BindingTestHooks{BeforeClear: func(server string, cb commitview.CredentialBinding, op string) {
		if op != "dcr_client" || !rewrote.CompareAndSwap(false, true) {
			return
		}
		ok, err := f.db.SaveOAuthClientIfBound(f.key, "acme", b, storage.OAuthClientSaveAlways, "cid", "sec2", 4100, "http://127.0.0.1:4100/oauth/callback")
		assert.NoError(t, err)
		assert.True(t, ok)
	}})
	t.Cleanup(restore)

	var calls atomic.Int32
	BindRefresher(store, RefreshBinding{Refresh: func(ctx context.Context, rec *storage.OAuthTokenRecord) (*client.Token, error) {
		if calls.Add(1) > 1 {
			assert.Equal(t, "sec2", rec.ClientSecret, "the retry uses the corrected registration")
			tok := &client.Token{AccessToken: "at2", RefreshToken: "rt2", ExpiresAt: time.Now().Add(time.Hour)}
			return tok, store.SaveToken(ctx, tok)
		}
		return nil, fmt.Errorf("x: %w", transport.OAuthError{ErrorCode: "invalid_client"})
	}})

	_, err = store.GetToken(context.Background())
	require.Error(t, err)
	require.True(t, rewrote.Load(), "the rewrite ran inside the clear window")

	after := f.record(t)
	require.NotNil(t, after)
	assert.Equal(t, "cid", after.ClientID)
	assert.Equal(t, "sec2", after.ClientSecret)
	assert.Equal(t, 4100, after.CallbackPort)
	assert.Equal(t, "http://127.0.0.1:4100/oauth/callback", after.RedirectURI)
	outMu.Lock()
	assert.Empty(t, outcomes, "a superseded rejection emits no terminal outcome")
	outMu.Unlock()

	tok, err := store.GetToken(context.Background())
	require.NoError(t, err, "nothing was latched against the corrected registration")
	assert.Equal(t, "at2", tok.AccessToken)
	assert.Equal(t, int32(2), calls.Load())
}

// Sol r7 on #1621: the flight's revision-checked DCR clear succeeds, then a
// same-binding writer restores the same client id and refresh token with a
// corrected secret, callback port and redirect URI before the flight's
// terminal re-read. The rewrite has the flight's generation again, but it is
// not the record the flight cleared: the old rejection latches nothing and
// emits no terminal outcome, and the next refresh runs against the corrected
// registration.
func TestBoundRefreshInvalidClientKeepsRewriteAfterClear(t *testing.T) {
	f := newBindingFixture(t)
	b := f.create(t)
	_, err := f.db.SaveOAuthClientIfBound(f.key, "acme", b, storage.OAuthClientSaveAlways, "cid", "sec", 4000, "http://127.0.0.1:4000/oauth/callback")
	require.NoError(t, err)
	store := NewBoundPersistentTokenStore("acme", bindURL, f.db, b).(*PersistentTokenStore)
	require.NoError(t, store.SaveToken(context.Background(), &client.Token{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(-time.Second)}))

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
	req := func() RefreshRequest {
		return RefreshRequest{
			Key: f.key, Binding: b, ServerName: "acme", ObservedRefreshToken: "rt",
			Trigger: RefreshTriggerReactive,
			Load:    func() (*storage.OAuthTokenRecord, error) { return f.db.GetOAuthTokenBound(f.key, b) },
			ClearClientAt: func(rec *storage.OAuthTokenRecord) (bool, error) {
				cleared, err := clearClientIfBound(f.db, f.key, "acme", b, rec)
				if cleared && err == nil && rewrote.CompareAndSwap(false, true) {
					ok, serr := f.db.SaveOAuthClientIfBound(f.key, "acme", b, storage.OAuthClientSaveAlways, "cid", "sec2", 4100, "http://127.0.0.1:4100/oauth/callback")
					assert.NoError(t, serr)
					assert.True(t, ok)
				}
				return cleared, err
			},
			Refresh: func(ctx context.Context, rec *storage.OAuthTokenRecord) (*client.Token, error) {
				if calls.Add(1) > 1 {
					assert.Equal(t, "sec2", rec.ClientSecret, "the retry uses the corrected registration")
					return &client.Token{AccessToken: "at2", RefreshToken: "rt2", ExpiresAt: time.Now().Add(time.Hour)}, store.SaveToken(ctx, &client.Token{AccessToken: "at2", RefreshToken: "rt2", ExpiresAt: time.Now().Add(time.Hour)})
				}
				return nil, fmt.Errorf("x: %w", transport.OAuthError{ErrorCode: "invalid_client"})
			},
		}
	}

	_, _, _, err = c.DoServed(context.Background(), req())
	require.Error(t, err)
	require.True(t, rewrote.Load(), "the clear succeeded and the rewrite landed after it")

	after := f.record(t)
	require.NotNil(t, after)
	assert.Equal(t, "cid", after.ClientID)
	assert.Equal(t, "sec2", after.ClientSecret)
	outMu.Lock()
	assert.Empty(t, outcomes, "a rejection superseded by a rewrite after the clear emits no terminal outcome")
	outMu.Unlock()

	tok, _, _, err := c.DoServed(context.Background(), req())
	require.NoError(t, err, "nothing was latched against the corrected registration")
	assert.Equal(t, "at2", tok.AccessToken)
	assert.Equal(t, int32(2), calls.Load())
}
