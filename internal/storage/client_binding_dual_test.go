package storage

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// bindings returns the (pin, mode) held by the client's raw API-key record and
// by its oauth_client_bindings entry ("" / "" and ok=false when absent).
func bindings(t *testing.T, m *Manager, clientID string) (apiPin, apiMode string, apiOK bool, oPin, oMode string, oOK bool) {
	t.Helper()
	require.NoError(t, m.db.db.View(func(tx *bbolt.Tx) error {
		_, tok, err := findClientTokenRecordLocked(tx, clientID)
		require.NoError(t, err)
		if tok != nil && tok.Kind == auth.KindClient {
			apiPin, apiMode, apiOK = tok.ProfilePin, tok.ProfileMode, true
		}
		b, err := getOAuthBindingTx(tx, clientID)
		require.NoError(t, err)
		if b != nil {
			oPin, oMode, oOK = b.ProfilePin, b.ProfileMode, true
		}
		return nil
	}))
	return
}

func assertEqualBindings(t *testing.T, m *Manager, clientID, pin, mode string) {
	t.Helper()
	apiPin, apiMode, apiOK, oPin, oMode, oOK := bindings(t, m, clientID)
	require.True(t, apiOK && oOK, "both records must exist")
	assert.Equal(t, pin, apiPin, "api pin")
	assert.Equal(t, mode, apiMode, "api mode")
	assert.Equal(t, pin, oPin, "oauth pin")
	assert.Equal(t, mode, oMode, "oauth mode")
}

// clientWithBoth: an API-key client that also got a first browser login.
func clientWithBoth(t *testing.T, m *Manager, clientID, mode, pin string) {
	t.Helper()
	mintTestClient(t, m, clientID, mode, pin)
	now := time.Now().UTC()
	require.NoError(t, m.CreateOAuthRegistration(newTestReg("reg-"+clientID, now)))
	rev, err := m.ClientBindingRevision(clientID)
	require.NoError(t, err)
	_, err = m.CommitOAuthLogin(CommitLoginInput{
		Grant: newTestGrant("grant-"+clientID, "reg-"+clientID, clientID, now), Client: clientID,
		ExpectedBindingRevision: rev, Now: now,
	})
	require.NoError(t, err)
}

func TestWriteClientBindingTx(t *testing.T) {
	t.Run("both records in one tx", func(t *testing.T) {
		m, cleanup := setupTestStorageForAgentTokens(t)
		defer cleanup()
		clientWithBoth(t, m, "cursor", auth.ProfileModeLocked, "ro")
		require.NoError(t, m.db.db.Update(func(tx *bbolt.Tx) error {
			return writeClientBindingTx(tx, "cursor", "full", auth.ProfileModeSwitchable)
		}))
		assertEqualBindings(t, m, "cursor", "full", auth.ProfileModeSwitchable)
	})

	t.Run("OAuth-only client", func(t *testing.T) {
		m, cleanup := setupTestStorageForAgentTokens(t)
		defer cleanup()
		commitNew(t, m, "r", "g1", "webonly", "ro", auth.ProfileModeLocked, time.Now())
		require.NoError(t, m.db.db.Update(func(tx *bbolt.Tx) error {
			return writeClientBindingTx(tx, "webonly", "full", auth.ProfileModeLocked)
		}))
		_, _, apiOK, oPin, oMode, oOK := bindings(t, m, "webonly")
		assert.False(t, apiOK)
		assert.True(t, oOK)
		assert.Equal(t, "full", oPin)
		assert.Equal(t, auth.ProfileModeLocked, oMode)
	})

	t.Run("API-key-only client never gets an OAuth entry", func(t *testing.T) {
		m, cleanup := setupTestStorageForAgentTokens(t)
		defer cleanup()
		mintTestClient(t, m, "cursor", auth.ProfileModeLocked, "ro")
		require.NoError(t, m.db.db.Update(func(tx *bbolt.Tx) error {
			return writeClientBindingTx(tx, "cursor", "full", auth.ProfileModeLocked)
		}))
		_, _, _, _, _, oOK := bindings(t, m, "cursor")
		assert.False(t, oOK)
		assert.Empty(t, oauthBucketNames(t, m))
	})

	t.Run("fails when neither exists", func(t *testing.T) {
		m, cleanup := setupTestStorageForAgentTokens(t)
		defer cleanup()
		err := m.db.db.Update(func(tx *bbolt.Tx) error {
			return writeClientBindingTx(tx, "ghost", "x", auth.ProfileModeLocked)
		})
		assert.ErrorIs(t, err, ErrClientCredentialNotFound)
	})
}

func TestUpdateClientCredentialBinding_KeepsOAuthEntryEqual(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	clientWithBoth(t, m, "cursor", auth.ProfileModeLocked, "ro")
	_, _, err := m.UpdateClientCredentialBinding("cursor", "full", auth.ProfileModeSwitchable)
	require.NoError(t, err)
	assertEqualBindings(t, m, "cursor", "full", auth.ProfileModeSwitchable)

	// A refused update writes neither record.
	_, _, err = m.UpdateClientCredentialBinding("cursor", "", auth.ProfileModeLocked)
	require.Error(t, err)
	assertEqualBindings(t, m, "cursor", "full", auth.ProfileModeSwitchable)
}

func TestMintForOAuthOnlyClient_SnapshotsAndRollbackRestores(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	commitNew(t, m, "r", "g1", "webonly", "ro", auth.ProfileModeLocked, time.Now())

	rec := mintTestClient(t, m, "webonly", auth.ProfileModeSwitchable, "")
	require.NotEmpty(t, rec)
	// One binding: the requested (wider) one on both records.
	assertEqualBindings(t, m, "webonly", "", auth.ProfileModeSwitchable)
	tok, err := m.GetAgentTokenByName("client-webonly")
	require.NoError(t, err)
	assert.Equal(t, "ro", tok.PriorProfilePin, "the OAuth binding was snapshotted")
	assert.Equal(t, auth.ProfileModeLocked, tok.PriorProfileMode)

	// An aborted/undone connect restores the snapshot to the OAuth entry and
	// leaves the grant alone.
	_, err = m.ForgetClientCredentialRestoringPrior("webonly")
	require.NoError(t, err)
	assertEqualBindings(t, m, "webonly", "ro", auth.ProfileModeLocked)
	g, err := m.GetOAuthGrant("g1")
	require.NoError(t, err)
	assert.False(t, g.Revoked)
	b, _ := m.GetOAuthClientBinding("webonly")
	require.NotNil(t, b)
}

func TestReconnectKeepsBothRecordsEqual(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	clientWithBoth(t, m, "cursor", auth.ProfileModeLocked, "ro")

	_, err := m.RevokeClientAPIKey("cursor")
	require.NoError(t, err)
	assertEqualBindings(t, m, "cursor", "ro", auth.ProfileModeLocked)

	// Re-mint over the revoked tombstone with a new binding.
	mintTestClient(t, m, "cursor", auth.ProfileModeLocked, "full")
	assertEqualBindings(t, m, "cursor", "full", auth.ProfileModeLocked)

	// Undo of that reconnect puts the tombstone's old binding back on both.
	_, err = m.ForgetClientCredentialRestoringPrior("cursor")
	require.NoError(t, err)
	assertEqualBindings(t, m, "cursor", "ro", auth.ProfileModeLocked)
}

func TestRepinProfile_MovesOAuthOnlyClientAndRestorePinsRestores(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	commitNew(t, m, "r", "g1", "webonly", "old", auth.ProfileModeLocked, time.Now())
	clientWithBoth(t, m, "cursor", auth.ProfileModeLocked, "old")
	commitNew(t, m, "r2", "g2", "other", "keep", auth.ProfileModeLocked, time.Now())

	before, err := m.RepinProfile("old", "new")
	require.NoError(t, err)
	require.Len(t, before, 2, "the API-key client and the OAuth-only client")

	_, _, apiOK, oPin, oMode, oOK := bindings(t, m, "webonly")
	assert.False(t, apiOK)
	assert.True(t, oOK)
	assert.Equal(t, "new", oPin)
	assert.Equal(t, auth.ProfileModeLocked, oMode)
	assertEqualBindings(t, m, "cursor", "new", auth.ProfileModeLocked)
	_, _, _, oPin, _, _ = bindings(t, m, "other")
	assert.Equal(t, "keep", oPin, "an unrelated client does not move")

	require.NoError(t, m.RestorePins(before))
	_, _, _, oPin, _, _ = bindings(t, m, "webonly")
	assert.Equal(t, "old", oPin)
	assertEqualBindings(t, m, "cursor", "old", auth.ProfileModeLocked)
}

func TestRepinProfile_FailureLeavesOAuthEntriesUnchanged(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	commitNew(t, m, "r", "g1", "webonly", "old", auth.ProfileModeLocked, time.Now())
	clientWithBoth(t, m, "cursor", auth.ProfileModeLocked, "old")

	boom := errors.New("boom")
	_, err := m.repinProfile("old", "new", func(written int) error {
		if written == 1 {
			return boom
		}
		return nil
	})
	require.ErrorIs(t, err, boom)
	assertEqualBindings(t, m, "cursor", "old", auth.ProfileModeLocked)
	_, _, _, oPin, _, _ := bindings(t, m, "webonly")
	assert.Equal(t, "old", oPin)
}

func TestRepairOAuthClientBindings_TakesAPIKeyRecord(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	clientWithBoth(t, m, "cursor", auth.ProfileModeLocked, "ro")
	clientWithBoth(t, m, "zed", auth.ProfileModeSwitchable, "")
	commitNew(t, m, "r", "g1", "webonly", "x", auth.ProfileModeLocked, time.Now())

	// Drift the OAuth entry of cursor behind the writer's back.
	require.NoError(t, m.db.db.Update(func(tx *bbolt.Tx) error {
		return oauthPutJSON(tx, OAuthClientBindingsBucket, "cursor", &OAuthClientBinding{
			ProfilePin: "stale", ProfileMode: auth.ProfileModeSwitchable, UpdatedAt: time.Now(),
		})
	}))
	repaired, err := m.RepairOAuthClientBindings()
	require.NoError(t, err)
	assert.Equal(t, []string{"cursor"}, repaired)
	assertEqualBindings(t, m, "cursor", "ro", auth.ProfileModeLocked)

	repaired, err = m.RepairOAuthClientBindings()
	require.NoError(t, err)
	assert.Empty(t, repaired)
}

// TestDowngradeView_APIKeyRecordStaysCurrent (FR-028): a binary that predates
// browser logins reads only the agent_tokens record, so after a reassign, a
// rename and a reconnect the raw record must carry the current pin and mode.
func TestDowngradeView_APIKeyRecordStaysCurrent(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	clientWithBoth(t, m, "cursor", auth.ProfileModeLocked, "p1")

	raw := func() (pin, mode string) {
		var rec auth.AgentToken
		require.NoError(t, m.db.db.View(func(tx *bbolt.Tx) error {
			return tx.Bucket([]byte(AgentTokensBucket)).ForEach(func(_, v []byte) error {
				var t auth.AgentToken
				if json.Unmarshal(v, &t) == nil && t.Name == "client-cursor" {
					rec = t
				}
				return nil
			})
		}))
		return rec.ProfilePin, rec.ProfileMode
	}

	// reassign
	_, _, err := m.UpdateClientCredentialBinding("cursor", "p2", auth.ProfileModeLocked)
	require.NoError(t, err)
	pin, mode := raw()
	assert.Equal(t, "p2", pin)
	assert.Equal(t, auth.ProfileModeLocked, mode)

	// rename
	_, err = m.RepinProfile("p2", "p3")
	require.NoError(t, err)
	pin, _ = raw()
	assert.Equal(t, "p3", pin)

	// reconnect (revoke key, mint again with another binding)
	_, err = m.RevokeClientAPIKey("cursor")
	require.NoError(t, err)
	mintTestClient(t, m, "cursor", auth.ProfileModeSwitchable, "")
	pin, mode = raw()
	assert.Equal(t, "", pin)
	assert.Equal(t, auth.ProfileModeSwitchable, mode)
	assertEqualBindings(t, m, "cursor", "", auth.ProfileModeSwitchable)
}

// TestClientBindingFieldsWrittenOnlyByAllowListedFunctions is the source test of
// research R4: an assignment to, or a composite-literal key of, ProfilePin or
// ProfileMode in this package's non-test code may appear only in the functions
// below. A new writer must go through writeClientBindingTx instead.
func TestClientBindingFieldsWrittenOnlyByAllowListedFunctions(t *testing.T) {
	allowed := map[string]string{
		// THE writer of a client's pin/mode in both stores.
		"writeClientBindingOptsTx": "the one binding writer",
		// Builds the brand-new record literal; the OAuth entry is then synced
		// through writeClientBindingTx in the same transaction.
		"MintClientCredentialWith": "new client record literal",
		// Assigns pin/mode on a COPY only to validate invariants and to return
		// the after-snapshot; the stored write is writeClientBindingOptsTx.
		"UpdateClientCredentialBinding": "validation copy and returned snapshot",
		// Sets the restored binding on the returned (already stored) record;
		// the stored restore is writeClientBindingTx.
		"forgetClientCredential": "returned snapshot of the restored binding",
		// Regular (non-client) agent tokens are rewritten directly; client
		// records and OAuth-only entries go through writeClientBindingTx. The
		// pseudo-record for an OAuth-only entry is a literal here.
		"repinProfile": "plain pin rewrite of regular tokens, pseudo-record literal",
		"RestorePins":  "plain pin restore of regular tokens",
		// Creates the OAuth-side entry at a client's first login (the only
		// creator of oauth_client_bindings entries).
		"CommitOAuthLogin": "creates the oauth_client_bindings entry",
	}

	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	fset := token.NewFileSet()
	isBindingField := func(name string) bool { return name == "ProfilePin" || name == "ProfileMode" }
	var violations []string
	used := map[string]bool{}

	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		file, err := parser.ParseFile(fset, f, src, 0)
		require.NoError(t, err)
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				var pos token.Pos
				hit := false
				switch x := n.(type) {
				case *ast.AssignStmt:
					for _, lhs := range x.Lhs {
						if sel, ok := lhs.(*ast.SelectorExpr); ok && isBindingField(sel.Sel.Name) {
							hit, pos = true, sel.Pos()
						}
					}
				case *ast.KeyValueExpr:
					if id, ok := x.Key.(*ast.Ident); ok && isBindingField(id.Name) {
						hit, pos = true, id.Pos()
					}
				}
				if hit {
					if _, ok := allowed[fn.Name.Name]; ok {
						used[fn.Name.Name] = true
					} else {
						violations = append(violations, fset.Position(pos).String()+" in "+fn.Name.Name)
					}
				}
				return true
			})
		}
	}
	sort.Strings(violations)
	assert.Empty(t, violations, "route the write through writeClientBindingTx or justify a new allow-list entry")
	for name := range allowed {
		assert.True(t, used[name], "allow-list entry %s is stale (no such write any more); remove it", name)
	}
}
