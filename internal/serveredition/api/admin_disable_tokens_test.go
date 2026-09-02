//go:build server

package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	teamsauth "github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// adminTokenTestSetup builds AdminHandlers with a REAL agent-token store wired
// through SetTokenRevoker, which is what internal/serveredition/setup.go does.
func adminTokenTestSetup(t *testing.T) (*AdminHandlers, *users.UserStore, *storage.Manager) {
	t.Helper()

	db, err := bbolt.Open(filepath.Join(t.TempDir(), "users.db"), 0600, &bbolt.Options{Timeout: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	userStore := users.NewUserStore(db)
	require.NoError(t, userStore.EnsureBuckets())

	logger := zap.NewNop().Sugar()
	tokens, err := storage.NewManager(t.TempDir(), logger)
	require.NoError(t, err)
	t.Cleanup(func() { tokens.Close() })

	sessionManager := teamsauth.NewSessionManager(userStore, 24*time.Hour, false)
	handlers := NewAdminHandlers(userStore, nil, sessionManager, []string{"admin@example.com"}, nil, nil, "", nil, logger)
	handlers.SetTokenRevoker(tokens)

	return handlers, userStore, tokens
}

// seedAgentToken writes a token owned by userID and returns its raw value.
func seedAgentToken(t *testing.T, mgr *storage.Manager, userID, name string) string {
	t.Helper()
	raw, err := auth.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, mgr.CreateAgentToken(auth.AgentToken{
		Name:           name,
		UserID:         userID,
		AllowedServers: []string{"shared-ok"},
		Permissions:    []string{auth.PermRead},
	}, raw, tokenTestHMACKey))
	return raw
}

// TestDisableUser_RevokesTheUsersAgentTokens is the persistence half of the
// disabled-owner fix, at the door an operator actually uses.
//
// Disabling an account is the documented remediation for a compromise. It
// revoked sessions and stopped JWTs, but agent tokens are separate records that
// nothing touched — so the credential most likely to be in an attacker's hands,
// the long-lived non-interactive one, survived the remediation entirely.
//
// The authentication-path owner gate
// (storage.Manager.SetAgentTokenOwnerGate, tested in
// internal/storage/agent_token_lifecycle_test.go) makes the block immediate.
// This handler ALSO revokes, so that re-enabling the account later does not hand
// the attacker their token back.
//
// Oracle discipline:
//
//  1. every token authenticates BEFORE the disable, on the same store — so a
//     later refusal is the revocation and not a bad fixture or a mis-hashed
//     token;
//  2. another user's token is present throughout and must SURVIVE — "did it
//     revoke?" is only half the property, and a bulk revoke that swept every
//     tenant would pass a one-sided assertion;
//  3. the disable response status is pinned to 200, so the assertions below
//     cannot be passing because the request never reached the handler; and
//  4. the end state is read back through storage, not inferred from the
//     response body.
//
// BITES: comment out the h.tokenRevoker block in disableUser (leave the field
// and the setter, so the package still builds) and the two "must not
// authenticate" assertions fail.
func TestDisableUser_RevokesTheUsersAgentTokens(t *testing.T) {
	handlers, store, tokens := adminTokenTestSetup(t)

	victim := createTestUser(t, store, "user-compromised", "victim@example.com", "Victim", "google")
	bystander := createTestUser(t, store, "user-bystander", "bystander@example.com", "Bystander", "google")

	victimCI := seedAgentToken(t, tokens, victim.ID, "ci")
	victimDeploy := seedAgentToken(t, tokens, victim.ID, "deploy")
	bystanderCI := seedAgentToken(t, tokens, bystander.ID, "ci")

	// (1) Positive control: all three authenticate before the disable.
	for label, raw := range map[string]string{
		"victim ci":     victimCI,
		"victim deploy": victimDeploy,
		"bystander ci":  bystanderCI,
	} {
		got, err := tokens.ValidateAgentToken(raw, tokenTestHMACKey)
		require.NoError(t, err, "positive control: %s must authenticate before the disable", label)
		require.NotNil(t, got)
	}

	router := adminTestRouter(handlers, adminAuthContext())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/"+victim.ID+"/disable", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "the disable must succeed (%s)", w.Body.String())

	// (3) The account really is disabled — the fixture landed.
	updated, err := store.GetUser(victim.ID)
	require.NoError(t, err)
	require.NotNil(t, updated)
	require.True(t, updated.Disabled, "the user must be disabled, or nothing below is about tokens")

	// (4) Their tokens no longer authenticate...
	for label, raw := range map[string]string{
		"victim ci":     victimCI,
		"victim deploy": victimDeploy,
	} {
		got, err := tokens.ValidateAgentToken(raw, tokenTestHMACKey)
		assert.Nil(t, got, "%s: a disabled user's token must not authenticate", label)
		assert.Error(t, err, "%s: a disabled user's token must be refused", label)
	}

	// ...and the refusal is durable, not a live check that a re-enable undoes.
	for _, name := range []string{"ci", "deploy"} {
		stored, err := tokens.GetAgentTokenByOwnerAndName(victim.ID, name)
		require.NoError(t, err)
		require.NotNil(t, stored, "revoke is a soft delete: the record must remain visible to the operator")
		assert.True(t, stored.Revoked, "token %q must be revoked on disk", name)
	}

	// (2) The bystander is untouched.
	survivor, err := tokens.ValidateAgentToken(bystanderCI, tokenTestHMACKey)
	require.NoError(t, err, "another user's token must survive the disable")
	require.NotNil(t, survivor)
	assert.Equal(t, bystander.ID, survivor.UserID)
}

// TestEnableUser_DoesNotResurrectRevokedTokens pins the deliberate asymmetry.
//
// Re-enabling an account restores the person's access. It must NOT restore the
// credentials that were burned when the account was disabled — those may be the
// reason it was disabled. The owner mints new ones.
//
// Oracle discipline: the enable is pinned to 200 and the user record is read
// back as enabled, so "the token still fails" cannot be passing because the
// enable never happened.
//
// BITES: add an un-revoke loop to enableUser and this fails.
func TestEnableUser_DoesNotResurrectRevokedTokens(t *testing.T) {
	handlers, store, tokens := adminTokenTestSetup(t)

	user := createTestUser(t, store, "user-rehired", "rehired@example.com", "Rehired", "google")
	raw := seedAgentToken(t, tokens, user.ID, "ci")

	got, err := tokens.ValidateAgentToken(raw, tokenTestHMACKey)
	require.NoError(t, err, "positive control: the token must authenticate before the disable")
	require.NotNil(t, got)

	router := adminTestRouter(handlers, adminAuthContext())

	disable := httptest.NewRecorder()
	router.ServeHTTP(disable, httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/"+user.ID+"/disable", nil))
	require.Equal(t, http.StatusOK, disable.Code, "the disable must succeed (%s)", disable.Body.String())

	enable := httptest.NewRecorder()
	router.ServeHTTP(enable, httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/"+user.ID+"/enable", nil))
	require.Equal(t, http.StatusOK, enable.Code, "the enable must succeed (%s)", enable.Body.String())

	back, err := store.GetUser(user.ID)
	require.NoError(t, err)
	require.NotNil(t, back)
	require.False(t, back.Disabled, "the user must be enabled again, or the assertion below is vacuous")

	revived, err := tokens.ValidateAgentToken(raw, tokenTestHMACKey)
	assert.Nil(t, revived, "a token burned by a disable must not come back when the account does")
	assert.Error(t, err)
}
