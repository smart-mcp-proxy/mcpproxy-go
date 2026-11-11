package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
)

// Spec 123 PR-3 T052 (FR-016, US1.3): `db compact` and the compaction-advised
// marker. Only --keep-backup=false clears it; the default keeps the
// pre-compaction file, which still holds the freed plaintext pages, so the
// marker stays and the command says so.

const (
	compactTokenMarker   = "COMPACT-ACCESS-MARKER-7f3a"
	compactRefreshMarker = "COMPACT-REFRESH-MARKER-7f3a"
)

func compactTestKey(t *testing.T) *tokenseal.Key {
	t.Helper()
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i) + 11
	}
	k, err := tokenseal.ParseKey(base64.StdEncoding.EncodeToString(raw))
	require.NoError(t, err)
	return k
}

// seedSealedDB writes plaintext rows with known markers, then seals them under
// a key (which sets the marker), and returns the database path.
func seedSealedDB(t *testing.T, dir string) string {
	t.Helper()
	seed, err := storage.NewManager(dir, zap.NewNop().Sugar(), storage.WithOAuthTokenKey(nil))
	require.NoError(t, err)
	require.NoError(t, seed.GetBoltDB().SaveOAuthToken(&storage.OAuthTokenRecord{
		ServerName: "alpha_0123456789abcdef", AccessToken: compactTokenMarker, RefreshToken: compactRefreshMarker,
	}))
	require.NoError(t, seed.Close())

	m, err := storage.NewManager(dir, zap.NewNop().Sugar(), storage.WithOAuthTokenKey(compactTestKey(t)))
	require.NoError(t, err)
	n, err := m.SealPlaintextOAuthTokens(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	advised, err := m.OAuthCompactAdvised()
	require.NoError(t, err)
	require.True(t, advised)
	require.NoError(t, m.Close())
	return filepath.Join(dir, "config.db")
}

func markerPresent(t *testing.T, dbPath string) bool {
	t.Helper()
	db, err := bbolt.Open(dbPath, 0o600, &bbolt.Options{ReadOnly: true})
	require.NoError(t, err)
	defer db.Close()
	present := false
	require.NoError(t, db.View(func(tx *bbolt.Tx) error {
		if b := tx.Bucket([]byte(storage.MetaBucket)); b != nil {
			present = b.Get([]byte(storage.OAuthSealCompactAdvisedKey)) != nil
		}
		return nil
	}))
	return present
}

func TestCompactKeepsTheMarkerWhileABackupIsKept(t *testing.T) {
	dir := t.TempDir()
	dbPath := seedSealedDB(t, dir)

	res, err := compactDatabase(dbPath, compactOptions{TxMaxSize: defaultCompactTxMaxSize, KeepBackup: true})
	require.NoError(t, err)

	assert.True(t, markerPresent(t, dbPath), "the default compaction keeps the marker")
	assert.True(t, res.OAuthCompactionAdvised)
	assert.False(t, res.OAuthCompactionCleared)
	require.NotEmpty(t, res.BackupPath)

	notes := strings.Join(compactNotes(res), "\n")
	assert.Contains(t, notes, "config.db.bak")
	assert.Contains(t, notes, "--keep-backup=false")
	assert.NotContains(t, notes, compactTokenMarker)

	// the backup is the pre-compaction file: the old plaintext may still be in it
	bak, err := os.ReadFile(res.BackupPath)
	require.NoError(t, err)
	assert.True(t, bytes.Contains(bak, []byte(compactTokenMarker)), "premise: the retained backup holds the freed plaintext page")
}

func TestCompactWithoutBackupClearsTheMarkerAndLeavesNoPlaintext(t *testing.T) {
	dir := t.TempDir()
	dbPath := seedSealedDB(t, dir)

	res, err := compactDatabase(dbPath, compactOptions{TxMaxSize: defaultCompactTxMaxSize, KeepBackup: false})
	require.NoError(t, err)

	assert.False(t, markerPresent(t, dbPath), "--keep-backup=false clears the marker")
	assert.True(t, res.OAuthCompactionAdvised)
	assert.True(t, res.OAuthCompactionCleared)
	assert.Empty(t, res.BackupPath)
	_, statErr := os.Stat(dbPath + ".bak")
	assert.True(t, os.IsNotExist(statErr), "no backup is created")

	raw, err := os.ReadFile(dbPath)
	require.NoError(t, err)
	assert.False(t, bytes.Contains(raw, []byte(compactTokenMarker)), "no access-token text in the compacted file")
	assert.False(t, bytes.Contains(raw, []byte(compactRefreshMarker)), "no refresh-token text in the compacted file")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".compact-", "no temp copy is left behind")
	}

	// the rows survive and still open with the key
	m, err := storage.NewManager(dir, zap.NewNop().Sugar(), storage.WithOAuthTokenKey(compactTestKey(t)))
	require.NoError(t, err)
	defer m.Close()
	rows, err := m.ListOAuthTokenRows()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, storage.OAuthRowSealed, rows[0].Class)
	advised, err := m.OAuthCompactAdvised()
	require.NoError(t, err)
	assert.False(t, advised)

	assert.Contains(t, strings.Join(compactNotes(res), "\n"), "advice cleared")
}

func TestCompactWithoutBackupWarnsAboutAStaleBackup(t *testing.T) {
	dir := t.TempDir()
	dbPath := seedSealedDB(t, dir)
	stale := dbPath + ".bak"
	require.NoError(t, os.WriteFile(stale, []byte("old plaintext copy"), 0o600))

	res, err := compactDatabase(dbPath, compactOptions{TxMaxSize: defaultCompactTxMaxSize, KeepBackup: false})
	require.NoError(t, err)

	assert.Equal(t, stale, res.StaleBackupPath)
	_, statErr := os.Stat(stale)
	assert.NoError(t, statErr, "compaction never deletes a backup it did not make")
	assert.Contains(t, strings.Join(compactNotes(res), "\n"), "Delete it")
}

func TestCompactWithNoOAuthSealingChangesNothingNew(t *testing.T) {
	dir := t.TempDir()
	dbPath := seedDB(t, dir)
	res, err := compactDatabase(dbPath, compactOptions{TxMaxSize: defaultCompactTxMaxSize, KeepBackup: false})
	require.NoError(t, err)
	assert.False(t, res.OAuthCompactionAdvised)
	assert.False(t, res.OAuthCompactionCleared)
	assert.Empty(t, res.StaleBackupPath)
	assert.Empty(t, compactNotes(res), "a database that never sealed anything gets no extra output")
}
