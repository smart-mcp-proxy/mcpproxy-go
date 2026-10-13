package oauth

import (
	"errors"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 116 PR2b (#1607): OAuth state is bound to the committed credential
// binding of the upstream instance that owns it (storage/oauth_binding.go).

// ErrCredentialBindingStale reports that an OAuth write of an upstream
// instance was refused because a config commit removed, replaced or
// re-bound (OAuth config change) the server since the instance was built.
// Nothing was written; the instance that serves the server now runs its own
// flow.
var ErrCredentialBindingStale = errors.New("server was removed, replaced or reconfigured; OAuth result discarded")

// clearClientIfBound clears the DCR registration of the exact record rec
// (its revision) under binding b: the refresh flight's invalid_client
// handling (FR-009) acts only on the record it read, and only while the
// server's row still carries b.
func clearClientIfBound(db *storage.BoltDB, serverKey, server string, b commitview.CredentialBinding, rec *storage.OAuthTokenRecord) (bool, error) {
	if db == nil || rec == nil {
		return false, nil
	}
	if hook := oauthBeforeClearHook; hook != nil {
		hook(server, b, "dcr_client")
	}
	return db.ClearOAuthClientIfBound(serverKey, server, b, rec.Revision)
}

// ClearClientIfBound is clearClientIfBound for core's refresh and login
// paths.
func ClearClientIfBound(db *storage.BoltDB, serverKey, server string, b commitview.CredentialBinding, rec *storage.OAuthTokenRecord) (bool, error) {
	return clearClientIfBound(db, serverKey, server, b, rec)
}

// Test seams (nil in production; plan.md §Test seams).
var (
	// oauthBeforePersistHook runs in a token store's SaveToken after the
	// network response and before the bound storage write (T2.9, T2.13,
	// T2.17).
	oauthBeforePersistHook func(server string, b commitview.CredentialBinding)
	// oauthBeforeClearHook runs before a bound clear's storage transaction
	// (T2.15); op is "token" or "dcr_client".
	oauthBeforeClearHook func(server string, b commitview.CredentialBinding, op string)
	// oauthBeforeRefreshDoHook runs after a caller's bound read and before
	// RefreshCoordinator.Do (T2.16).
	oauthBeforeRefreshDoHook func(server string, b commitview.CredentialBinding)
)

// BindingTestHooks are the PR2b test seams, settable from other packages'
// tests.
type BindingTestHooks struct {
	BeforePersist   func(server string, b commitview.CredentialBinding)
	BeforeClear     func(server string, b commitview.CredentialBinding, op string)
	BeforeRefreshDo func(server string, b commitview.CredentialBinding)
}

// SetBindingTestHooks installs h and returns a restore func. Test-only: the
// hooks are plain variables, so set them before the code under test runs.
func SetBindingTestHooks(h BindingTestHooks) (restore func()) {
	prevP, prevC, prevR := oauthBeforePersistHook, oauthBeforeClearHook, oauthBeforeRefreshDoHook
	oauthBeforePersistHook, oauthBeforeClearHook, oauthBeforeRefreshDoHook = h.BeforePersist, h.BeforeClear, h.BeforeRefreshDo
	return func() {
		oauthBeforePersistHook, oauthBeforeClearHook, oauthBeforeRefreshDoHook = prevP, prevC, prevR
	}
}

// BeforePersistHook runs the before-persist seam, for core's own bound
// writes (DCR persistence, the stored-credential refresh).
func BeforePersistHook(server string, b commitview.CredentialBinding) {
	if hook := oauthBeforePersistHook; hook != nil {
		hook(server, b)
	}
}

// BeforeClearHook runs the before-clear seam, for core's own bound clears.
func BeforeClearHook(server string, b commitview.CredentialBinding, op string) {
	if hook := oauthBeforeClearHook; hook != nil {
		hook(server, b, op)
	}
}

// BeforeRefreshDoHook runs the before-Do seam, for core's direct refresh.
func BeforeRefreshDoHook(server string, b commitview.CredentialBinding) {
	if hook := oauthBeforeRefreshDoHook; hook != nil {
		hook(server, b)
	}
}
