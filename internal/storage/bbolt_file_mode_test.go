package storage

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// skipOnWindows guards the permission assertions below: os.Chmod on Windows only
// toggles the read-only bit and os.Stat reports 0666, so POSIX mode bits are
// meaningless there. CI runs the unit tests on Windows too.
func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file mode bits are not meaningful on Windows")
	}
}

// TestNewBoltDBCreatesDatabaseOwnerOnly pins that a freshly created config.db is
// owner-only: it holds OAuth access/refresh tokens and DCR client secrets.
func TestNewBoltDBCreatesDatabaseOwnerOnly(t *testing.T) {
	skipOnWindows(t)

	dir := t.TempDir()
	db, err := NewBoltDB(dir, zap.NewNop().Sugar())
	require.NoError(t, err)
	defer db.Close()

	info, err := os.Stat(filepath.Join(dir, "config.db"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"a freshly created config.db must not be group/world readable")
}

// TestNewBoltDBTightensWorldReadableDatabaseOnOpen covers existing installs:
// bbolt.Open's mode argument applies only at creation, so an already-created
// 0644 database must be chmod-migrated on open.
func TestNewBoltDBTightensWorldReadableDatabaseOnOpen(t *testing.T) {
	skipOnWindows(t)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "config.db")

	db, err := NewBoltDB(dir, zap.NewNop().Sugar())
	require.NoError(t, err)
	require.NoError(t, db.Close())

	// Simulate a database created by an older build.
	require.NoError(t, os.Chmod(dbPath, 0o644))

	db2, err := NewBoltDB(dir, zap.NewNop().Sugar())
	require.NoError(t, err)
	require.NoError(t, db2.Close())

	info, err := os.Stat(dbPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"an existing world-readable config.db must be tightened on open")
}

// TestNewBoltDBPreservesOwnerBitsWhenTightening makes sure the migration only
// clears group/other bits instead of assigning 0600 outright, so it can never
// widen a mode the owner chose.
func TestNewBoltDBPreservesOwnerBitsWhenTightening(t *testing.T) {
	skipOnWindows(t)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "config.db")

	db, err := NewBoltDB(dir, zap.NewNop().Sugar())
	require.NoError(t, err)
	require.NoError(t, db.Close())

	require.NoError(t, os.Chmod(dbPath, 0o744))

	db2, err := NewBoltDB(dir, zap.NewNop().Sugar())
	require.NoError(t, err)
	require.NoError(t, db2.Close())

	info, err := os.Stat(dbPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm(),
		"only group/other bits should be cleared; owner bits must be preserved")
}

// TestBackupWritesOwnerOnlyCopy pins the backup copy: the destination is an
// operator-chosen path that may sit outside the 0700 data directory, where a
// 0644 copy really would be world-readable.
func TestBackupWritesOwnerOnlyCopy(t *testing.T) {
	skipOnWindows(t)

	db, err := NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	defer db.Close()

	backupPath := filepath.Join(t.TempDir(), "backup.db")
	require.NoError(t, db.Backup(backupPath))

	info, err := os.Stat(backupPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"database backups must not be group/world readable")

	// The copy must still be a usable database, not just a well-permissioned file.
	restored, err := bbolt.Open(backupPath, 0600, &bbolt.Options{
		ReadOnly: true,
		Timeout:  5 * time.Second,
	})
	require.NoError(t, err)
	require.NoError(t, restored.Close())
}

// TestBackupTightensExistingDestination covers overwriting an earlier backup:
// bbolt's CopyFile opens the destination with O_CREATE|O_TRUNC, so its mode
// argument applies only when the destination does not already exist. The backup
// must not be written into a pre-existing world-readable file at all, so it is
// staged in an owner-only temporary file and renamed into place - which means
// the old destination inode is replaced rather than truncated and rewritten.
func TestBackupTightensExistingDestination(t *testing.T) {
	skipOnWindows(t)

	db, err := NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	defer db.Close()

	destDir := t.TempDir()
	backupPath := filepath.Join(destDir, "backup.db")
	// A backup left behind by an older build.
	require.NoError(t, os.WriteFile(backupPath, []byte("stale"), 0o644))

	before, err := os.Stat(backupPath)
	require.NoError(t, err)

	require.NoError(t, db.Backup(backupPath))

	after, err := os.Stat(backupPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), after.Mode().Perm(),
		"overwriting an existing backup must also tighten its permissions")
	require.False(t, os.SameFile(before, after),
		"database bytes must never be written into the pre-existing, possibly world-readable file; "+
			"stage in an owner-only temp file and rename over the destination")

	entries, err := os.ReadDir(destDir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "backup must not leave temporary files behind: %v", entries)
}

// TestTightenFilePermissionsLogsStatFailureAtWarn covers SEC-03 follow-up
// review: the database holds OAuth tokens and DCR client secrets, so an
// operator running at the project's default log level (Info) must be able to
// see that the permission-tightening migration did not run, instead of the
// failure disappearing into Debug output nobody has enabled.
func TestTightenFilePermissionsLogsStatFailureAtWarn(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core).Sugar()

	tightenFilePermissions(filepath.Join(t.TempDir(), "does-not-exist.db"), logger)

	entries := logs.FilterMessageSnippet("Could not stat").All()
	require.Len(t, entries, 1, "a stat failure must be logged")
	require.Equal(t, zapcore.WarnLevel, entries[0].Level,
		"a stat failure must be visible at the project's default (Info) log level")
}

// TestTightenFilePermissionsLogsChmodFailureAtWarn reproduces one of the
// review's named real-world causes (macOS uchg/schg flag) for a chmod that
// cannot succeed even though the process owns the file: darwin's immutable
// flag makes chmod fail with EPERM regardless of ownership.
func TestTightenFilePermissionsLogsChmodFailureAtWarn(t *testing.T) {
	skipOnWindows(t)
	if runtime.GOOS != "darwin" {
		t.Skip("uses chflags uchg to force an owner-proof chmod failure; darwin-only")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.db")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))

	require.NoError(t, exec.Command("chflags", "uchg", path).Run())
	defer func() {
		_ = exec.Command("chflags", "nouchg", path).Run()
	}()

	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core).Sugar()

	tightenFilePermissions(path, logger)

	entries := logs.FilterMessageSnippet("Could not tighten permissions").All()
	require.Len(t, entries, 1, "a chmod failure must be logged")
	require.Equal(t, zapcore.WarnLevel, entries[0].Level,
		"a chmod failure must be visible at the project's default (Info) log level, "+
			"since it means the DB is left silently world-readable")
}
