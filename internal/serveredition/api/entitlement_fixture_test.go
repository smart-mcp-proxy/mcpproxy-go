//go:build server

package api

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
)

// Spec 107 T075 fixture support. The entitlement predicate
// (entitledServerNamesFor) takes the LOADED user record and its wrapper
// performs one GetUser per request, failing closed when the record is absent
// (contracts/entitlement-predicate.md §1: never an empty grant that could be
// mistaken for "no groups"). Every per-user door therefore needs the caller's
// record in the store — which, in production, the auth middleware has just
// proved exists. Pre-107 test rigs built contexts for users that were never
// persisted; ensureUserRecord persists such a record the first time a rig
// acts as that context, without touching a record a test created itself.

// ensureUserRecord persists a minimal user record for ac's UserID when the
// store has none. A record a test created itself (by id) is left untouched.
// The email is synthesised from the id so it can never collide with a record
// the test creates later under the context's real email.
func ensureUserRecord(store *users.UserStore, ac *auth.AuthContext) {
	if store == nil || ac == nil || !ac.IsUser() || ac.UserID == "" {
		return
	}
	if existing, err := store.GetUser(ac.UserID); err == nil && existing != nil {
		return
	}
	provider := ac.Provider
	if provider == "" {
		provider = "google"
	}
	now := time.Now().UTC()
	_ = store.CreateUser(&users.User{
		ID:                ac.UserID,
		Email:             strings.ToLower(ac.UserID) + "@fixture.invalid",
		DisplayName:       ac.DisplayName,
		Provider:          provider,
		ProviderSubjectID: "sub-" + ac.UserID,
		CreatedAt:         now,
		LastLoginAt:       now,
	})
}

// withCookieKind stamps CredentialKindCookie on a context, the way the
// server-edition middleware does for a session-cookie request (T078): the
// minting doors admit only that kind, so a rig acting through them as a
// "logged-in user" must carry it.
func withCookieKind(ac *auth.AuthContext) *auth.AuthContext {
	if ac == nil {
		return nil
	}
	clone := *ac
	clone.CredentialKind = auth.CredentialKindCookie
	return &clone
}

// newFixtureUserStore opens a fresh per-test user store with its buckets.
func newFixtureUserStore(t *testing.T) *users.UserStore {
	t.Helper()
	db, err := bbolt.Open(filepath.Join(t.TempDir(), "users.db"), 0600, &bbolt.Options{Timeout: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store := users.NewUserStore(db)
	require.NoError(t, store.EnsureBuckets())
	return store
}

// installFixtureEntitlement gives standalone CredentialHandlers the predicate
// they cannot build themselves (they hold no user store): a UserHandlers over
// a fresh store and the handlers' own admin-servers view, no access block
// (today's Shared-only semantics). Returns the store so the caller can
// persist the acting user's record.
func installFixtureEntitlement(t *testing.T, h *CredentialHandlers) *users.UserStore {
	t.Helper()
	store := newFixtureUserStore(t)
	h.SetEntitlement(NewUserHandlers(store, h.currentAdminServers, nil, nil, h.logger))
	return store
}
