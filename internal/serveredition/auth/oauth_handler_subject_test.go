//go:build server

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
)

// Spec 107 T038 — subject binding (FR-023) and the administrator-opened rebind
// window (`subject_rebind_armed_at`), driven through HandleLogin → HandleCallback
// against a legacy-shaped fake IdP whose `sub` changes between logins, and
// through users.(*UserStore).UpdateUserLogin directly for the concurrency
// fixture.
//
// Compile-red until T044 (LoginResult/LoginRefusal/LoginResultObserver on the
// handler, NewOAuthHandler taking a ServerEditionConfigProvider,
// users.LoginClaims/LoginOutcome/UpdateUserLogin/ErrSubjectMismatch/
// ErrUserDisabled, User.SubjectRebindArmedAt/Groups/GroupsUpdatedAt) and T045
// (the enable/disable handlers that arm and clear the flag — asserted in
// internal/serveredition/api/admin_handlers_rebind_test.go).
//
// Assertions on the closed reason vocabulary compare the wire value
// (`string(res.Reason)`) rather than a constant name, so T044 is free to name
// the constants; the values are fixed by FR-013.

const subjectTestHMACKey = "test-hmac-key-for-jwt-signing-32b"

// subjectIdP is a legacy-shaped (token + userinfo) fake provider whose claims
// can be changed between logins, and whose userinfo endpoint can be made to
// fail so a login attempt terminates as provider_error after the state was
// consumed but before the user store is consulted.
type subjectIdP struct {
	srv *httptest.Server

	mu           sync.Mutex
	email        string
	name         string
	sub          string
	picture      string
	userinfoFail bool
}

func newSubjectIdP(t *testing.T, email, name, sub string) *subjectIdP {
	t.Helper()
	idp := &subjectIdP{email: email, name: name, sub: sub}

	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "subject-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer subject-access-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		idp.mu.Lock()
		defer idp.mu.Unlock()
		if idp.userinfoFail {
			http.Error(w, "upstream boom", http.StatusInternalServerError)
			return
		}
		claims := map[string]any{
			"sub":   idp.sub,
			"email": idp.email,
			"name":  idp.name,
		}
		if idp.picture != "" {
			claims["picture"] = idp.picture
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(claims)
	})

	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)
	return idp
}

func (p *subjectIdP) setSubject(sub string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sub = sub
}

func (p *subjectIdP) setUserinfoFail(fail bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.userinfoFail = fail
}

// liveServerEditionConfig is a swappable ServerEditionConfigProvider: the
// handler must read the CURRENT pointer on every login, not the one it was
// constructed with (T044 "role derived live", issue #1169's remaining horizon).
type liveServerEditionConfig struct {
	mu  sync.Mutex
	cfg *config.ServerEditionConfig
}

func (l *liveServerEditionConfig) provider() ServerEditionConfigProvider {
	return func() *config.ServerEditionConfig {
		l.mu.Lock()
		defer l.mu.Unlock()
		return l.cfg
	}
}

func (l *liveServerEditionConfig) swap(cfg *config.ServerEditionConfig) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cfg = cfg
}

func subjectTestConfig(adminEmails ...string) *config.ServerEditionConfig {
	return &config.ServerEditionConfig{
		Enabled:     true,
		AdminEmails: adminEmails,
		OAuth: &config.ServerEditionOAuthConfig{
			Provider:     "google",
			ClientID:     "test-client-id",
			ClientSecret: "test-client-secret",
		},
		SessionTTL:     config.Duration(time.Hour),
		BearerTokenTTL: config.Duration(time.Hour),
	}
}

// subjectRig owns the BBolt file so a test can close and reopen it — the
// "restart between enable and login" fixture of FR-023.
type subjectRig struct {
	t       *testing.T
	dbPath  string
	db      *bbolt.DB
	store   *users.UserStore
	live    *liveServerEditionConfig
	handler *OAuthHandler

	resultsMu sync.Mutex
	results   []LoginResult
}

func newSubjectRig(t *testing.T, idp *subjectIdP, cfg *config.ServerEditionConfig) *subjectRig {
	t.Helper()
	registerMockProvider(t, idp.srv)

	rig := &subjectRig{
		t:      t,
		dbPath: filepath.Join(t.TempDir(), "subject.db"),
		live:   &liveServerEditionConfig{cfg: cfg},
	}
	rig.open()
	t.Cleanup(func() { _ = rig.db.Close() })
	return rig
}

// open (re)opens the store and builds a fresh handler over it, installing a
// LoginResultObserver that records every terminal LoginResult.
func (rig *subjectRig) open() {
	rig.t.Helper()
	db, err := bbolt.Open(rig.dbPath, 0600, &bbolt.Options{Timeout: time.Second})
	require.NoError(rig.t, err)
	rig.db = db

	rig.store = users.NewUserStore(db)
	require.NoError(rig.t, rig.store.EnsureBuckets())

	sessionMgr := NewSessionManager(rig.store, time.Hour, false)
	rig.handler = NewOAuthHandler(rig.store, sessionMgr, rig.live.provider(), []byte(subjectTestHMACKey), zap.NewNop().Sugar())
	rig.handler.LoginResultObserver = func(res LoginResult) {
		rig.resultsMu.Lock()
		defer rig.resultsMu.Unlock()
		rig.results = append(rig.results, res)
	}
}

// restart closes the BBolt file and reopens it under a new store and handler,
// exactly what a process restart does to the persisted rebind window.
func (rig *subjectRig) restart() {
	rig.t.Helper()
	require.NoError(rig.t, rig.db.Close())
	rig.open()
}

// lastResult returns the single LoginResult reported since the previous call
// and fails when the handler reported zero or more than one for the attempt.
func (rig *subjectRig) lastResult() LoginResult {
	rig.t.Helper()
	rig.resultsMu.Lock()
	defer rig.resultsMu.Unlock()
	require.Len(rig.t, rig.results, 1, "exactly one terminal LoginResult per attempt, got %d", len(rig.results))
	res := rig.results[0]
	rig.results = nil
	return res
}

// login drives GET /auth/login → the callback with the state the handler
// minted, returning the callback response. The code is irrelevant: the fake
// token endpoint accepts anything.
func (rig *subjectRig) login() *http.Response {
	rig.t.Helper()

	loginReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/login?redirect_uri=/dashboard", nil)
	loginReq.Host = "localhost:8080"
	loginW := httptest.NewRecorder()
	rig.handler.HandleLogin(loginW, loginReq)
	require.Equal(rig.t, http.StatusFound, loginW.Code, "login must redirect to the IdP")

	authURL, err := url.Parse(loginW.Header().Get("Location"))
	require.NoError(rig.t, err)
	state := authURL.Query().Get("state")
	require.NotEmpty(rig.t, state)

	cbReq := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/auth/callback?code=any-code&state=%s", url.QueryEscape(state)), nil)
	cbReq.Host = "localhost:8080"
	cbW := httptest.NewRecorder()
	rig.handler.HandleCallback(cbW, cbReq)

	resp := cbW.Result()
	rig.t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func (rig *subjectRig) userByEmail(email string) *users.User {
	rig.t.Helper()
	u, err := rig.store.GetUserByEmail(email)
	require.NoError(rig.t, err)
	require.NotNil(rig.t, u, "user %s must exist", email)
	return u
}

// arm sets subject_rebind_armed_at directly on the stored record — the
// enable-handler half is covered by admin_handlers_rebind_test.go.
func (rig *subjectRig) arm(email string) time.Time {
	rig.t.Helper()
	u := rig.userByEmail(email)
	armedAt := time.Now().UTC().Truncate(time.Millisecond)
	u.SubjectRebindArmedAt = &armedAt
	require.NoError(rig.t, rig.store.UpdateUser(u))
	return armedAt
}

func sessionCookie(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == SessionCookieName {
			return c
		}
	}
	return nil
}

// roleOf reads the role from the bearer JWT stored on the session the callback
// created — the value every later /api/v1 request will carry.
func (rig *subjectRig) roleOf(resp *http.Response) string {
	rig.t.Helper()
	c := sessionCookie(resp)
	require.NotNil(rig.t, c, "successful login must set the session cookie")
	sess, err := rig.store.GetSession(c.Value)
	require.NoError(rig.t, err)
	require.NotNil(rig.t, sess)
	claims, err := ValidateBearerToken(sess.BearerToken, []byte(subjectTestHMACKey))
	require.NoError(rig.t, err)
	return claims.Role
}

func flagsOf(res LoginResult) []string {
	out := []string{}
	for _, f := range res.Flags {
		out = append(out, string(f))
	}
	return out
}

func seedUser(t *testing.T, store *users.UserStore, email, provider, sub string) *users.User {
	t.Helper()
	u := users.NewUser(email, "Alice Seed", provider, sub)
	// Push the seed's timestamps into the past so "refreshed on login" is
	// observable without sleeping.
	u.CreatedAt = u.CreatedAt.Add(-time.Hour)
	u.LastLoginAt = u.LastLoginAt.Add(-time.Hour)
	require.NoError(t, store.CreateUser(u))
	return u
}

// --- FR-023: same provider + email, different sub → subject_mismatch ---------

func TestSubjectBinding_SameProviderDifferentSubjectIsRefused(t *testing.T) {
	idp := newSubjectIdP(t, "alice@example.com", "Alice", "sub-B")
	rig := newSubjectRig(t, idp, subjectTestConfig())
	seeded := seedUser(t, rig.store, "alice@example.com", "google", "sub-A")
	before := rig.userByEmail("alice@example.com")

	resp := rig.login()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "subject mismatch renders the generic 403 page")
	assert.Nil(t, sessionCookie(resp), "no session on a refused login")

	res := rig.lastResult()
	assert.Equal(t, "subject_mismatch", string(res.Reason))
	assert.Equal(t, seeded.ID, res.UserID, "the attempt reached the store and a record exists → user_id is carried")
	assert.Empty(t, res.EmailHash, "user_id and email_hash never appear together")
	assert.NotContains(t, flagsOf(res), "provider_rebound")

	after := rig.userByEmail("alice@example.com")
	assert.Equal(t, before, after, "a refused login writes nothing: subject, provider and last_login stay as they were")
	assert.Equal(t, "sub-A", after.ProviderSubjectID)
	assert.Nil(t, after.SubjectRebindArmedAt)

	all, err := rig.store.ListUsers()
	require.NoError(t, err)
	assert.Len(t, all, 1, "no second record for the colliding email")
}

// --- FR-023: configured-provider change → the first login re-binds -----------

func TestSubjectBinding_ProviderChangeRebindsOnFirstLogin(t *testing.T) {
	idp := newSubjectIdP(t, "alice@example.com", "Alice", "g-1")
	rig := newSubjectRig(t, idp, subjectTestConfig())
	// The record was bound while `github` was the configured provider; the
	// operator has since switched the configuration to `google` (the fake).
	seeded := seedUser(t, rig.store, "alice@example.com", "github", "gh-1")

	resp := rig.login()

	assert.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "/dashboard", resp.Header.Get("Location"))
	require.NotNil(t, sessionCookie(resp))

	res := rig.lastResult()
	assert.Equal(t, "ok", string(res.Reason))
	assert.Equal(t, seeded.ID, res.UserID)
	assert.Contains(t, flagsOf(res), "provider_rebound")

	after := rig.userByEmail("alice@example.com")
	assert.Equal(t, seeded.ID, after.ID, "same record, re-bound — never a second user")
	assert.Equal(t, "google", after.Provider, "Provider is refreshed to the configured provider")
	assert.Equal(t, "g-1", after.ProviderSubjectID, "ProviderSubjectID is refreshed to the new provider's sub")
	assert.Nil(t, after.SubjectRebindArmedAt, "a provider change needs no armed window and leaves none behind")
	assert.True(t, after.LastLoginAt.After(seeded.LastLoginAt))

	// The binding now stands: the same email from the new provider with yet
	// another subject is a plain mismatch.
	idp.setSubject("g-2")
	resp = rig.login()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, "subject_mismatch", string(rig.lastResult().Reason))
	assert.Equal(t, "g-1", rig.userByEmail("alice@example.com").ProviderSubjectID)
}

// --- FR-023: Provider / ProviderSubjectID refreshed on every login ------------
//
// Today ProviderSubjectID is only written when the IdP also sent an avatar URL
// (oauth_handler.go upsertUser, critic C4) and Provider is never written. The
// fake here sends no `picture`, so the old guard would leave the empty subject
// empty.
func TestSubjectBinding_SubjectRefreshedWithoutAvatar(t *testing.T) {
	idp := newSubjectIdP(t, "alice@example.com", "Alice", "sub-A")
	rig := newSubjectRig(t, idp, subjectTestConfig())

	// A record with an empty subject (bound before the IdP exposed one).
	// CreateUser refuses an empty subject, so it is written straight into the
	// buckets the way an upgraded record would be found.
	seeded := users.NewUser("alice@example.com", "Alice Seed", "google", "placeholder")
	seeded.ProviderSubjectID = ""
	seeded.LastLoginAt = seeded.LastLoginAt.Add(-time.Hour)
	raw, err := json.Marshal(seeded)
	require.NoError(t, err)
	require.NoError(t, rig.db.Update(func(tx *bbolt.Tx) error {
		if err := tx.Bucket([]byte(users.BucketUsers)).Put([]byte(seeded.ID), raw); err != nil {
			return err
		}
		return tx.Bucket([]byte(users.BucketUsersByEmail)).Put([]byte(seeded.Email), []byte(seeded.ID))
	}))

	resp := rig.login()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	res := rig.lastResult()
	assert.Equal(t, "ok", string(res.Reason))
	assert.NotContains(t, flagsOf(res), "provider_rebound", "binding an empty subject is a bind, not a rebind")

	after := rig.userByEmail("alice@example.com")
	assert.Equal(t, seeded.ID, after.ID)
	assert.Equal(t, "sub-A", after.ProviderSubjectID, "subject bound on login even though no avatar URL was sent")
	assert.Equal(t, "google", after.Provider)
	assert.Equal(t, "Alice", after.DisplayName)
	assert.True(t, after.LastLoginAt.After(seeded.LastLoginAt))

	// Second login, same subject: plain bind, last_login moves again.
	time.Sleep(5 * time.Millisecond)
	resp = rig.login()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "ok", string(rig.lastResult().Reason))
	again := rig.userByEmail("alice@example.com")
	assert.Equal(t, "sub-A", again.ProviderSubjectID)
	assert.True(t, again.LastLoginAt.After(after.LastLoginAt))
}

// --- FR-023: the armed window is consumed by the first successful login -------

func TestSubjectRebind_ArmedWindowIsSingleUse(t *testing.T) {
	idp := newSubjectIdP(t, "alice@example.com", "Alice", "sub-B")
	rig := newSubjectRig(t, idp, subjectTestConfig())
	seeded := seedUser(t, rig.store, "alice@example.com", "google", "sub-A")
	rig.arm("alice@example.com")

	resp := rig.login()
	require.Equal(t, http.StatusFound, resp.StatusCode, "the first successful login inside the window rebinds")
	res := rig.lastResult()
	assert.Equal(t, "ok", string(res.Reason))
	assert.Equal(t, seeded.ID, res.UserID)
	assert.Contains(t, flagsOf(res), "provider_rebound")

	after := rig.userByEmail("alice@example.com")
	assert.Equal(t, "sub-B", after.ProviderSubjectID)
	assert.Equal(t, "google", after.Provider)
	assert.Nil(t, after.SubjectRebindArmedAt, "the flag is cleared in the same write as the rebind")

	// Window closed: the next different subject is refused and nothing moves.
	idp.setSubject("sub-C")
	resp = rig.login()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, "subject_mismatch", string(rig.lastResult().Reason))
	closed := rig.userByEmail("alice@example.com")
	assert.Equal(t, "sub-B", closed.ProviderSubjectID)
	assert.Nil(t, closed.SubjectRebindArmedAt)

	// The winner keeps logging in.
	idp.setSubject("sub-B")
	resp = rig.login()
	assert.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "ok", string(rig.lastResult().Reason))
}

func TestSubjectRebind_ArmedWindowSurvivesRestart(t *testing.T) {
	idp := newSubjectIdP(t, "alice@example.com", "Alice", "sub-B")
	rig := newSubjectRig(t, idp, subjectTestConfig())
	seeded := seedUser(t, rig.store, "alice@example.com", "google", "sub-A")
	armedAt := rig.arm("alice@example.com")

	rig.restart()

	persisted := rig.userByEmail("alice@example.com")
	require.NotNil(t, persisted.SubjectRebindArmedAt, "the window is persisted state, not inferred")
	assert.True(t, persisted.SubjectRebindArmedAt.Equal(armedAt))

	resp := rig.login()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	res := rig.lastResult()
	assert.Equal(t, "ok", string(res.Reason))
	assert.Equal(t, seeded.ID, res.UserID)
	assert.Contains(t, flagsOf(res), "provider_rebound")

	after := rig.userByEmail("alice@example.com")
	assert.Equal(t, "sub-B", after.ProviderSubjectID)
	assert.Nil(t, after.SubjectRebindArmedAt)
}

func TestSubjectRebind_FailedAttemptDoesNotConsumeTheWindow(t *testing.T) {
	idp := newSubjectIdP(t, "alice@example.com", "Alice", "sub-B")
	rig := newSubjectRig(t, idp, subjectTestConfig())
	seedUser(t, rig.store, "alice@example.com", "google", "sub-A")
	armedAt := rig.arm("alice@example.com")

	// First attempt fails before the store is consulted (userinfo down →
	// provider_error, the 503 class of FR-024).
	idp.setUserinfoFail(true)
	resp := rig.login()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Nil(t, sessionCookie(resp))
	res := rig.lastResult()
	assert.Equal(t, "provider_error", string(res.Reason))
	assert.Empty(t, res.UserID, "the store was never consulted on this path")

	still := rig.userByEmail("alice@example.com")
	require.NotNil(t, still.SubjectRebindArmedAt, "a failed attempt leaves the window open")
	assert.True(t, still.SubjectRebindArmedAt.Equal(armedAt))
	assert.Equal(t, "sub-A", still.ProviderSubjectID)

	// Then the real login consumes it.
	idp.setUserinfoFail(false)
	resp = rig.login()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	res = rig.lastResult()
	assert.Equal(t, "ok", string(res.Reason))
	assert.Contains(t, flagsOf(res), "provider_rebound")

	after := rig.userByEmail("alice@example.com")
	assert.Equal(t, "sub-B", after.ProviderSubjectID)
	assert.Nil(t, after.SubjectRebindArmedAt)
}

// --- FR-023: two concurrent logins, two subjects → exactly one rebinds --------
//
// Driven through the store contract directly: UpdateUserLogin is
// transaction-owned, so the second writer re-reads the record inside its own
// db.Update, sees the flag already consumed, and refuses. A pre-mutated *User
// handed to UpdateUser cannot do this (both callbacks would observe the armed
// flag and rebind to different subjects).
func TestUpdateUserLogin_ConcurrentRebindExactlyOneWins(t *testing.T) {
	const iterations = 10

	dbPath := filepath.Join(t.TempDir(), "concurrent.db")
	db, err := bbolt.Open(dbPath, 0600, &bbolt.Options{Timeout: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store := users.NewUserStore(db)
	require.NoError(t, store.EnsureBuckets())

	seeded := seedUser(t, store, "alice@example.com", "google", "sub-A")

	for i := 0; i < iterations; i++ {
		// Re-arm and reset to the seed binding for every round.
		u, err := store.GetUserByEmail("alice@example.com")
		require.NoError(t, err)
		require.NotNil(t, u)
		u.ProviderSubjectID = "sub-A"
		armedAt := time.Now().UTC()
		u.SubjectRebindArmedAt = &armedAt
		require.NoError(t, store.UpdateUser(u))

		subjects := [2]string{fmt.Sprintf("sub-B-%d", i), fmt.Sprintf("sub-C-%d", i)}
		var (
			outcomes [2]users.LoginOutcome
			errs     [2]error
			wg       sync.WaitGroup
			barrier  = make(chan struct{})
		)
		for k := 0; k < 2; k++ {
			wg.Add(1)
			go func(k int) {
				defer wg.Done()
				<-barrier // both calls start only after the flag is armed
				outcomes[k], errs[k] = store.UpdateUserLogin(context.Background(), users.LoginClaims{
					Email:       "alice@example.com",
					Provider:    "google",
					Subject:     subjects[k],
					Name:        "Alice",
					Groups:      []string{},
					GroupsKnown: true,
				})
			}(k)
		}
		close(barrier)
		wg.Wait()

		winners := 0
		var winnerSubject string
		for k := 0; k < 2; k++ {
			if errs[k] == nil {
				winners++
				winnerSubject = subjects[k]
				assert.True(t, outcomes[k].Rebound, "round %d: the winner re-bound", i)
				assert.True(t, outcomes[k].RebindConsumed, "round %d: the winner consumed the window", i)
				assert.False(t, outcomes[k].Created)
				require.NotNil(t, outcomes[k].User)
				assert.Equal(t, seeded.ID, outcomes[k].User.ID)
				assert.Equal(t, subjects[k], outcomes[k].User.ProviderSubjectID)
				assert.Nil(t, outcomes[k].User.SubjectRebindArmedAt)
			} else {
				assert.True(t, errors.Is(errs[k], users.ErrSubjectMismatch),
					"round %d: the loser is a subject mismatch, got %v", i, errs[k])
			}
		}
		require.Equal(t, 1, winners, "round %d: exactly one of two concurrent logins rebinds (errors: %v, %v)", i, errs[0], errs[1])

		stored, err := store.GetUserByEmail("alice@example.com")
		require.NoError(t, err)
		require.NotNil(t, stored)
		assert.Equal(t, winnerSubject, stored.ProviderSubjectID, "round %d: the stored subject is the winner's", i)
		assert.Nil(t, stored.SubjectRebindArmedAt, "round %d: the window is closed", i)
		assert.Equal(t, "google", stored.Provider)
	}
}

// --- users.(*UserStore).UpdateUserLogin outcomes -----------------------------

func TestUpdateUserLogin_Outcomes(t *testing.T) {
	newStore := func(t *testing.T) *users.UserStore {
		t.Helper()
		db, err := bbolt.Open(filepath.Join(t.TempDir(), "outcomes.db"), 0600, &bbolt.Options{Timeout: time.Second})
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		store := users.NewUserStore(db)
		require.NoError(t, store.EnsureBuckets())
		return store
	}
	claims := func(provider, sub string, groups ...string) users.LoginClaims {
		return users.LoginClaims{
			Email:       "Alice@Example.com", // normalised by the store
			Provider:    provider,
			Subject:     sub,
			Name:        "Alice",
			AvatarURL:   "",
			Groups:      groups,
			GroupsKnown: true,
		}
	}
	ctx := context.Background()

	t.Run("first login creates the record bound to (provider, sub)", func(t *testing.T) {
		store := newStore(t)
		out, err := store.UpdateUserLogin(ctx, claims("google", "sub-A", "eng"))
		require.NoError(t, err)
		assert.True(t, out.Created)
		assert.False(t, out.Rebound)
		assert.False(t, out.RebindConsumed)
		require.NotNil(t, out.User)
		assert.Equal(t, "alice@example.com", out.User.Email)
		assert.Equal(t, "google", out.User.Provider)
		assert.Equal(t, "sub-A", out.User.ProviderSubjectID)
		assert.Equal(t, []string{"eng"}, out.User.Groups)
		assert.False(t, out.User.GroupsUpdatedAt.IsZero())
		assert.Nil(t, out.User.SubjectRebindArmedAt)

		stored, err := store.GetUserByEmail("alice@example.com")
		require.NoError(t, err)
		require.NotNil(t, stored)
		assert.Equal(t, out.User.ID, stored.ID)
	})

	t.Run("same subject binds and refreshes last_login and groups wholesale", func(t *testing.T) {
		store := newStore(t)
		seeded := seedUser(t, store, "alice@example.com", "google", "sub-A")
		out, err := store.UpdateUserLogin(ctx, claims("google", "sub-A", "ops"))
		require.NoError(t, err)
		assert.False(t, out.Created)
		assert.False(t, out.Rebound)
		assert.False(t, out.RebindConsumed)
		assert.Equal(t, seeded.ID, out.User.ID)
		assert.True(t, out.User.LastLoginAt.After(seeded.LastLoginAt))
		assert.Equal(t, []string{"ops"}, out.User.Groups)

		out, err = store.UpdateUserLogin(ctx, claims("google", "sub-A"))
		require.NoError(t, err)
		assert.Equal(t, []string{}, out.User.Groups, "an empty list replaces the previous one wholesale")
	})

	t.Run("provider change rebinds without an armed window", func(t *testing.T) {
		store := newStore(t)
		seeded := seedUser(t, store, "alice@example.com", "github", "gh-1")
		out, err := store.UpdateUserLogin(ctx, claims("google", "g-1"))
		require.NoError(t, err)
		assert.True(t, out.Rebound)
		assert.False(t, out.RebindConsumed, "no window existed to consume")
		assert.Equal(t, seeded.ID, out.User.ID)
		assert.Equal(t, "google", out.User.Provider)
		assert.Equal(t, "g-1", out.User.ProviderSubjectID)
	})

	t.Run("different subject without a window is refused and writes nothing", func(t *testing.T) {
		store := newStore(t)
		seedUser(t, store, "alice@example.com", "google", "sub-A")
		before, err := store.GetUserByEmail("alice@example.com")
		require.NoError(t, err)

		_, err = store.UpdateUserLogin(ctx, claims("google", "sub-B", "eng"))
		require.Error(t, err)
		assert.True(t, errors.Is(err, users.ErrSubjectMismatch), "got %v", err)

		after, err := store.GetUserByEmail("alice@example.com")
		require.NoError(t, err)
		assert.Equal(t, before, after, "nothing written on a mismatch — not even groups or last_login")
	})

	t.Run("different subject with the window armed rebinds and clears it", func(t *testing.T) {
		store := newStore(t)
		seeded := seedUser(t, store, "alice@example.com", "google", "sub-A")
		armedAt := time.Now().UTC()
		seeded.SubjectRebindArmedAt = &armedAt
		require.NoError(t, store.UpdateUser(seeded))

		out, err := store.UpdateUserLogin(ctx, claims("google", "sub-B"))
		require.NoError(t, err)
		assert.True(t, out.Rebound)
		assert.True(t, out.RebindConsumed)
		assert.Equal(t, "sub-B", out.User.ProviderSubjectID)
		assert.Nil(t, out.User.SubjectRebindArmedAt)

		stored, err := store.GetUserByEmail("alice@example.com")
		require.NoError(t, err)
		assert.Nil(t, stored.SubjectRebindArmedAt)
		assert.Equal(t, "sub-B", stored.ProviderSubjectID)
	})

	// FR-023: "the first successful login" while armed "carries provider_rebound
	// in the line's flags" — unconditionally, not only when the presented
	// subject differs from the stored one. A same-subject login while armed
	// still consumes (and must still flag) the administrator-opened window, or
	// its consumption leaves no audit trace at all (RebindConsumed is not
	// logged anywhere; provider_rebound is the only signal, cross-review round 3).
	t.Run("same subject with the window armed still consumes and flags it", func(t *testing.T) {
		store := newStore(t)
		seeded := seedUser(t, store, "alice@example.com", "google", "sub-A")
		armedAt := time.Now().UTC()
		seeded.SubjectRebindArmedAt = &armedAt
		require.NoError(t, store.UpdateUser(seeded))

		out, err := store.UpdateUserLogin(ctx, claims("google", "sub-A"))
		require.NoError(t, err)
		assert.True(t, out.Rebound, "the window's consumption is itself the reportable event")
		assert.True(t, out.RebindConsumed)
		assert.Equal(t, "sub-A", out.User.ProviderSubjectID)
		assert.Nil(t, out.User.SubjectRebindArmedAt)

		stored, err := store.GetUserByEmail("alice@example.com")
		require.NoError(t, err)
		assert.Nil(t, stored.SubjectRebindArmedAt)
	})

	t.Run("disabled record is refused untouched", func(t *testing.T) {
		store := newStore(t)
		seeded := seedUser(t, store, "alice@example.com", "google", "sub-A")
		seeded.Disabled = true
		require.NoError(t, store.UpdateUser(seeded))
		before, err := store.GetUserByEmail("alice@example.com")
		require.NoError(t, err)

		_, err = store.UpdateUserLogin(ctx, claims("google", "sub-A"))
		require.Error(t, err)
		assert.True(t, errors.Is(err, users.ErrUserDisabled), "got %v", err)

		after, err := store.GetUserByEmail("alice@example.com")
		require.NoError(t, err)
		assert.Equal(t, before, after)
	})
}

// --- T044: the login role is derived from the CURRENT admin_emails ----------
//
// Two distinct config pointers, swapped after the handler was built: a handler
// that captured the boot pointer answers with the stale role.
func TestLoginRole_DerivedFromLiveAdminEmails(t *testing.T) {
	idp := newSubjectIdP(t, "alice@example.com", "Alice", "sub-A")
	boot := subjectTestConfig() // nobody is an admin
	rig := newSubjectRig(t, idp, boot)

	resp := rig.login()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "ok", string(rig.lastResult().Reason))
	assert.Equal(t, "user", rig.roleOf(resp))

	// Hot-reload: promote alice on a NEW config value.
	rig.live.swap(subjectTestConfig("alice@example.com"))
	resp = rig.login()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "ok", string(rig.lastResult().Reason))
	assert.Equal(t, "admin", rig.roleOf(resp), "the role comes from the current admin_emails, not the boot pointer")

	// Hot-reload: demote again.
	rig.live.swap(subjectTestConfig())
	resp = rig.login()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "ok", string(rig.lastResult().Reason))
	assert.Equal(t, "user", rig.roleOf(resp), "a demotion takes effect on the very next login")
}
