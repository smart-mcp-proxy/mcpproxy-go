package storage

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
)

// Spec 116 PR2b (#1607): the storage half of the OAuth credential binding.

const bindKey = "acme_0123456789abcdef"

func bstamp(g, inc, epoch int64) commitview.ServerGen {
	return commitview.ServerGen{Gen: g, Incarnation: inc, OAuthEpoch: epoch}
}

// commitAcme writes acme's row at stamp sg in a commit of generation g.
func commitAcme(t *testing.T, m *Manager, g int64, sg commitview.ServerGen, extra func(b *ServerCommitBatch)) {
	t.Helper()
	b := &ServerCommitBatch{Generation: g, Saves: []ServerSave{{Config: commitEntry("acme"), Stamp: sg}}}
	if extra != nil {
		extra(b)
	}
	require.NoError(t, m.ApplyServerCommit(b))
}

func rawOAuthRecord(t *testing.T, m *Manager, key string) []byte {
	t.Helper()
	var out []byte
	require.NoError(t, m.db.db.View(func(tx *bbolt.Tx) error {
		if v := tx.Bucket([]byte(OAuthTokenBucket)).Get([]byte(key)); v != nil {
			out = append([]byte(nil), v...)
		}
		return nil
	}))
	return out
}

func saveBoundToken(t *testing.T, m *Manager, b commitview.CredentialBinding, access string) bool {
	t.Helper()
	ok, err := m.db.UpdateOAuthTokenIfBound(bindKey, "acme", b, func(rec *OAuthTokenRecord) error {
		rec.AccessToken = access
		rec.RefreshToken = "rt-" + access
		rec.ExpiresAt = time.Now().Add(time.Hour)
		return nil
	})
	require.NoError(t, err)
	return ok
}

// The row stamp carries the OAuth epoch; a stamp above G is an invariant
// error.
func TestApplyServerCommit_StampsOAuthEpoch(t *testing.T) {
	m := newCommitTestManager(t)
	commitAcme(t, m, 4, bstamp(4, 4, 4), nil)
	row, err := m.GetStoredServer("acme")
	require.NoError(t, err)
	assert.Equal(t, bstamp(4, 4, 4), row.Stamp)

	err = m.ApplyServerCommit(&ServerCommitBatch{Generation: 5, Saves: []ServerSave{{Config: commitEntry("acme"), Stamp: bstamp(5, 4, 6)}}})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvariant))
}

// T2.9 / T2.13 at the storage level: a bound write lands only while the row
// carries the binding; a removal, a remove and re-add, and an OAuth clear
// (epoch moved) each refuse an old-binding write.
func TestOAuthBinding_WritesNeedTheRowBinding(t *testing.T) {
	m := newCommitTestManager(t)
	commitAcme(t, m, 3, bstamp(3, 3, 3), nil)
	old := bstamp(3, 3, 3).Binding()
	require.True(t, saveBoundToken(t, m, old, "a1"), "the current binding writes")
	rec, err := m.db.GetOAuthTokenBound(bindKey, old)
	require.NoError(t, err)
	assert.Equal(t, int64(3), rec.OAuthEpoch, "the record carries the binding's epoch")

	// OAuth config change: the commit clears and raises the epoch.
	commitAcme(t, m, 4, bstamp(4, 3, 4), func(b *ServerCommitBatch) { b.OAuthClears = []string{"acme"} })
	assert.Nil(t, rawOAuthRecord(t, m, bindKey), "the OAuth clear deleted the record")
	assert.False(t, saveBoundToken(t, m, old, "late"), "an old-binding write after the OAuth clear is refused")
	assert.Nil(t, rawOAuthRecord(t, m, bindKey), "nothing was re-created")
	id, _, _, _, _, err := m.db.GetOAuthClientCredentialsBound(bindKey, old)
	require.NoError(t, err)
	assert.Empty(t, id, "no client registration survives the clear")
	cur := bstamp(4, 3, 4).Binding()
	require.True(t, saveBoundToken(t, m, cur, "a2"))

	// Removal: the row and the record are purged; an old write is refused.
	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{Generation: 5, Removes: []string{"acme"}}))
	assert.False(t, saveBoundToken(t, m, cur, "late2"), "a write bound to a removed server is refused")
	saved, err := m.db.SaveOAuthClientIfBound(bindKey, "acme", cur, OAuthClientSaveAlways, "cid", "sec", 1234, "http://127.0.0.1:1234/oauth/callback")
	require.NoError(t, err)
	assert.False(t, saved, "a DCR save bound to a removed server is refused")
	assert.Nil(t, rawOAuthRecord(t, m, bindKey))

	// Re-add under the same name and URL: a new binding; the old one stays
	// refused and reads nothing.
	commitAcme(t, m, 6, bstamp(6, 6, 6), nil)
	assert.False(t, saveBoundToken(t, m, cur, "late3"), "an old-incarnation write after a re-add is refused")
	assert.Nil(t, rawOAuthRecord(t, m, bindKey), "the re-added server inherits nothing")
	require.True(t, saveBoundToken(t, m, bstamp(6, 6, 6).Binding(), "fresh"))
	_, err = m.db.GetOAuthTokenBound(bindKey, cur)
	assert.Error(t, err, "an old binding reads nothing of the replacement")
}

// The zero binding acts on the current state: it writes while the row exists
// (stamping the row's epoch) or while no commit was ever recorded, and is
// refused for a removed server.
func TestOAuthBinding_UnboundWrites(t *testing.T) {
	m := newCommitTestManager(t)
	assert.True(t, saveBoundToken(t, m, commitview.CredentialBinding{}, "fixture"), "no commit recorded: an unbound write lands")
	commitAcme(t, m, 2, bstamp(2, 2, 2), nil)
	assert.True(t, saveBoundToken(t, m, commitview.CredentialBinding{}, "cli"))
	rec, err := m.db.GetOAuthTokenBound(bindKey, bstamp(2, 2, 2).Binding())
	require.NoError(t, err, "an unbound write adopts the row's epoch, so the daemon reads it")
	assert.Equal(t, "cli", rec.AccessToken)
	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{Generation: 3, Removes: []string{"acme"}}))
	assert.False(t, saveBoundToken(t, m, commitview.CredentialBinding{}, "late"), "an unbound write for a removed server is refused")
}

// A bound reader ignores a record of another epoch; a record written before
// PR2b (epoch 0) is read only by a binding with epoch 0.
func TestOAuthBinding_ReadsFilterByEpoch(t *testing.T) {
	m := newCommitTestManager(t)
	require.NoError(t, m.db.SaveOAuthToken(&OAuthTokenRecord{ServerName: bindKey, AccessToken: "legacy"}))
	_, err := m.db.GetOAuthTokenBound(bindKey, commitview.CredentialBinding{Incarnation: 3, OAuthEpoch: 0})
	require.NoError(t, err, "a legacy record is read under a legacy (epoch 0) row")
	_, err = m.db.GetOAuthTokenBound(bindKey, commitview.CredentialBinding{Incarnation: 3, OAuthEpoch: 3})
	assert.Error(t, err, "a legacy record is not read under a PR2b epoch")
	_, err = m.db.GetOAuthTokenBound(bindKey, commitview.CredentialBinding{})
	assert.NoError(t, err, "an unbound reader sees every record")
}

// T048e (review R7.3): every write path of the oauth_tokens bucket takes a
// new revision from the bucket sequence; a deleted and re-created record
// never reuses one.
func TestOAuthRevision_EveryWritePathBumps(t *testing.T) {
	m := newCommitTestManager(t)
	commitAcme(t, m, 2, bstamp(2, 2, 2), nil)
	b := bstamp(2, 2, 2).Binding()
	rev := func() uint64 {
		var r OAuthTokenRecord
		raw := rawOAuthRecord(t, m, bindKey)
		require.NotNil(t, raw)
		require.NoError(t, r.UnmarshalBinary(raw))
		return r.Revision
	}
	var last uint64
	step := func(name string, fn func() error) {
		require.NoError(t, fn(), name)
		got := rev()
		assert.Greater(t, got, last, "%s must bump the revision", name)
		last = got
	}
	step("SaveOAuthToken", func() error {
		return m.db.SaveOAuthToken(&OAuthTokenRecord{ServerName: bindKey, AccessToken: "a", RefreshToken: "r", ClientID: "c"})
	})
	step("UpdateOAuthClientCredentials", func() error {
		return m.db.UpdateOAuthClientCredentials(bindKey, "c", "s", 1, "u")
	})
	step("SaveOAuthClientCredentialsIfUnsetOrSame", func() error {
		_, err := m.db.SaveOAuthClientCredentialsIfUnsetOrSame(bindKey, "c", "s", 2, "u")
		return err
	})
	step("SaveOAuthClientCredentialsIfSameOrEmpty", func() error {
		_, err := m.db.SaveOAuthClientCredentialsIfSameOrEmpty(bindKey, "c", "s", 3, "u")
		return err
	})
	step("UpdateOAuthToken", func() error {
		return m.db.UpdateOAuthToken(bindKey, func(rec *OAuthTokenRecord) error { rec.AccessToken = "a2"; return nil })
	})
	step("ClearOAuthClientCredentialsIf", func() error {
		_, err := m.db.ClearOAuthClientCredentialsIf(bindKey, "c", "r")
		return err
	})
	step("UpdateOAuthClientCredentials again", func() error {
		return m.db.UpdateOAuthClientCredentials(bindKey, "c2", "s", 1, "u")
	})
	step("ClearOAuthClientCredentialsIfClientID", func() error {
		_, err := m.db.ClearOAuthClientCredentialsIfClientID(bindKey, "c2")
		return err
	})
	step("ClearOAuthClientCredentials", func() error { return m.db.ClearOAuthClientCredentials(bindKey) })
	step("UpdateOAuthTokenIfBound", func() error {
		_, err := m.db.UpdateOAuthTokenIfBound(bindKey, "acme", b, func(rec *OAuthTokenRecord) error { rec.AccessToken = "a3"; return nil })
		return err
	})
	step("SaveOAuthClientIfBound", func() error {
		_, err := m.db.SaveOAuthClientIfBound(bindKey, "acme", b, OAuthClientSaveAlways, "c3", "s", 1, "u")
		return err
	})
	step("ClearOAuthClientIfBoundAndClientID", func() error {
		_, err := m.db.ClearOAuthClientIfBoundAndClientID(bindKey, "acme", b, "c3")
		return err
	})
	step("SaveOAuthClientIfBound again", func() error {
		_, err := m.db.SaveOAuthClientIfBound(bindKey, "acme", b, OAuthClientSaveAlways, "c4", "s", 1, "u")
		return err
	})
	step("ClearOAuthClientIfBound", func() error {
		_, err := m.db.ClearOAuthClientIfBound(bindKey, "acme", b, last)
		return err
	})

	require.NoError(t, m.db.DeleteOAuthToken(bindKey))
	step("re-created after a delete", func() error {
		return m.db.SaveOAuthToken(&OAuthTokenRecord{ServerName: bindKey, AccessToken: "z"})
	})
}

func seedT220(t *testing.T, m *Manager, epoch int64) *OAuthTokenRecord {
	t.Helper()
	require.NoError(t, m.db.DeleteOAuthToken(bindKey))
	require.NoError(t, m.db.SaveOAuthToken(&OAuthTokenRecord{
		ServerName: bindKey, DisplayName: "acme",
		AccessToken: "at", RefreshToken: "rt", TokenType: "Bearer",
		ExpiresAt: time.Unix(1_900_000_000, 0).UTC(),
		ClientID:  "cid", ClientSecret: "sec", CallbackPort: 4321, RedirectURI: "http://127.0.0.1:4321/oauth/callback",
		OAuthEpoch: epoch,
	}))
	rec, err := m.db.GetOAuthTokenBound(bindKey, commitview.CredentialBinding{})
	require.NoError(t, err)
	return rec
}

// T2.20 (review R9.4): ClearOAuthClientIfBound empties only the four DCR
// fields of exactly the inspected revision; anything else leaves the record
// byte-identical.
func TestClearOAuthClientIfBound_T2_20(t *testing.T) {
	m := newCommitTestManager(t)
	commitAcme(t, m, 2, bstamp(2, 2, 2), nil)
	b := bstamp(2, 2, 2).Binding()

	seed := seedT220(t, m, 2)
	ok, err := m.db.ClearOAuthClientIfBound(bindKey, "acme", b, seed.Revision)
	require.NoError(t, err)
	require.True(t, ok)
	got, err := m.db.GetOAuthTokenBound(bindKey, b)
	require.NoError(t, err)
	assert.Equal(t, "at", got.AccessToken)
	assert.Equal(t, "rt", got.RefreshToken)
	assert.True(t, seed.ExpiresAt.Equal(got.ExpiresAt))
	assert.Equal(t, int64(2), got.OAuthEpoch)
	assert.Empty(t, got.ClientID)
	assert.Empty(t, got.ClientSecret)
	assert.Zero(t, got.CallbackPort)
	assert.Empty(t, got.RedirectURI)
	assert.Greater(t, got.Revision, seed.Revision)

	for _, tc := range []struct {
		name    string
		binding commitview.CredentialBinding
		epoch   int64
		stale   bool
	}{
		{"wrong binding", commitview.CredentialBinding{Incarnation: 9, OAuthEpoch: 2}, 2, false},
		{"wrong record epoch", b, 1, false},
		{"stale revision", b, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := seedT220(t, m, tc.epoch)
			before := rawOAuthRecord(t, m, bindKey)
			rev := s.Revision
			if tc.stale {
				rev--
			}
			ok, err := m.db.ClearOAuthClientIfBound(bindKey, "acme", tc.binding, rev)
			require.NoError(t, err)
			assert.False(t, ok)
			assert.Equal(t, before, rawOAuthRecord(t, m, bindKey), "a refused clear leaves the record byte-identical")
			ok, err = m.db.DeleteOAuthTokenIfBound(bindKey, "acme", tc.binding, rev)
			require.NoError(t, err)
			assert.False(t, ok, "the token delete applies the same checks")
			assert.Equal(t, before, rawOAuthRecord(t, m, bindKey))
		})
	}

	require.NoError(t, m.db.DeleteOAuthToken(bindKey))
	ok, err = m.db.ClearOAuthClientIfBound(bindKey, "acme", b, 0)
	require.NoError(t, err)
	assert.False(t, ok, "an absent record returns false")
}

// DeleteOAuthTokenIfBound deletes exactly the inspected revision.
func TestDeleteOAuthTokenIfBound(t *testing.T) {
	m := newCommitTestManager(t)
	commitAcme(t, m, 2, bstamp(2, 2, 2), nil)
	b := bstamp(2, 2, 2).Binding()
	s := seedT220(t, m, 2)
	ok, err := m.db.DeleteOAuthTokenIfBound(bindKey, "acme", b, s.Revision)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Nil(t, rawOAuthRecord(t, m, bindKey))
}

// T2.18 (review R8.3): the standalone logout clear deletes exactly the one
// key; acme_bar's and other's records stay byte-identical.
func TestClearOAuthTokenForStandaloneLogout_ExactKey(t *testing.T) {
	m := newCommitTestManager(t)
	keys := []string{"acme_0123456789abcdef", "acme_bar_0123456789abcdef", "other_0123456789abcdef"}
	for _, k := range keys {
		require.NoError(t, m.db.SaveOAuthToken(&OAuthTokenRecord{ServerName: k, AccessToken: "t-" + k}))
	}
	bar := rawOAuthRecord(t, m, keys[1])
	other := rawOAuthRecord(t, m, keys[2])
	require.NoError(t, m.db.ClearOAuthTokenForStandaloneLogout(keys[0]))
	assert.Nil(t, rawOAuthRecord(t, m, keys[0]))
	assert.Equal(t, bar, rawOAuthRecord(t, m, keys[1]))
	assert.Equal(t, other, rawOAuthRecord(t, m, keys[2]))
}

// Review R6.1: the construction-time check requires the row to equal the
// committed view exactly.
func TestCheckCommittedView(t *testing.T) {
	m := newCommitTestManager(t)
	assert.ErrorIs(t, m.CheckCommittedView("acme", bstamp(2, 2, 2)), commitview.ErrStaleGeneration, "no row")
	commitAcme(t, m, 2, bstamp(2, 2, 2), nil)
	assert.NoError(t, m.CheckCommittedView("acme", bstamp(2, 2, 2)))
	commitAcme(t, m, 3, bstamp(3, 2, 3), nil)
	assert.ErrorIs(t, m.CheckCommittedView("acme", bstamp(2, 2, 2)), commitview.ErrStaleGeneration, "generation and epoch moved")
	assert.ErrorIs(t, m.CheckCommittedView("acme", bstamp(3, 2, 2)), commitview.ErrStaleGeneration, "epoch moved")
	assert.NoError(t, m.CheckCommittedView("acme", bstamp(3, 2, 3)))
}

// T055c: a completion event is recorded only under the row's binding and
// carries it.
func TestSaveOAuthCompletionEventIfBound(t *testing.T) {
	m := newCommitTestManager(t)
	commitAcme(t, m, 2, bstamp(2, 2, 2), nil)
	ok, err := m.db.SaveOAuthCompletionEventIfBound(&OAuthCompletionEvent{ServerName: "acme", CompletedAt: time.Now()}, commitview.CredentialBinding{Incarnation: 1, OAuthEpoch: 1})
	require.NoError(t, err)
	assert.False(t, ok, "a stale binding records no event")
	ok, err = m.db.SaveOAuthCompletionEventIfBound(&OAuthCompletionEvent{ServerName: "acme", CompletedAt: time.Now().Add(time.Second)}, bstamp(2, 2, 2).Binding())
	require.NoError(t, err)
	assert.True(t, ok)
	events, err := m.db.GetUnprocessedOAuthCompletionEvents()
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, int64(2), events[0].Incarnation)
	assert.Equal(t, int64(2), events[0].OAuthEpoch)
}

// Sol r4 (#1621): on a write, the record UpdateOAuthTokenIfBound passed to
// mutate carries the written revision afterwards; a refresh flight reports it
// as the revision it serves.
func TestUpdateOAuthTokenIfBound_MutateRecordCarriesWrittenRevision(t *testing.T) {
	m := newCommitTestManager(t)
	var seen *OAuthTokenRecord
	ok, err := m.db.UpdateOAuthTokenIfBound(bindKey, "acme", commitview.CredentialBinding{}, func(rec *OAuthTokenRecord) error {
		seen = rec
		rec.AccessToken = "at"
		return nil
	})
	require.NoError(t, err)
	require.True(t, ok)
	stored, err := m.db.GetOAuthToken(bindKey)
	require.NoError(t, err)
	require.NotZero(t, stored.Revision)
	assert.Equal(t, stored.Revision, seen.Revision)
}
