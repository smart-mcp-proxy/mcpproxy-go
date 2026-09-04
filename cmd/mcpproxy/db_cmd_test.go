package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// seedDB builds a config.db-shaped database of a few MB and then deletes most
// of it, which is exactly the shape issue #1176 describes: BBolt hands the
// pages back to its own freelist but never to the OS, so the file stays big.
func seedDB(t *testing.T, dir string) string {
	t.Helper()

	dbPath := filepath.Join(dir, "config.db")
	db, err := bbolt.Open(dbPath, 0600, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}

	value := bytes.Repeat([]byte("x"), 4096)
	buckets := []string{"server_alpha_tool_calls", "server_beta_tool_calls", "tools"}

	if err := db.Update(func(tx *bbolt.Tx) error {
		for _, name := range buckets {
			b, err := tx.CreateBucketIfNotExists([]byte(name))
			if err != nil {
				return err
			}
			for i := 0; i < 400; i++ {
				if err := b.Put([]byte(fmt.Sprintf("key-%04d", i)), value); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed write: %v", err)
	}

	// Delete everything except key-0000 in each bucket. key-0000 is the survivor
	// the compaction test reads back to prove data was not silently dropped.
	if err := db.Update(func(tx *bbolt.Tx) error {
		for _, name := range buckets {
			b := tx.Bucket([]byte(name))
			for i := 1; i < 400; i++ {
				if err := b.Delete([]byte(fmt.Sprintf("key-%04d", i))); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed delete: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}
	return dbPath
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}

func fileDigest(t *testing.T, path string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return sha256.Sum256(data)
}

func TestCompactDatabaseReclaimsSpace(t *testing.T) {
	dir := t.TempDir()
	dbPath := seedDB(t, dir)
	before := fileSize(t, dbPath)

	res, err := compactDatabase(dbPath, compactOptions{TxMaxSize: defaultCompactTxMaxSize})
	if err != nil {
		t.Fatalf("compactDatabase: %v", err)
	}

	if res.BeforeBytes != before {
		t.Errorf("BeforeBytes = %d, want %d", res.BeforeBytes, before)
	}
	after := fileSize(t, dbPath)
	if res.AfterBytes != after {
		t.Errorf("AfterBytes = %d, want on-disk %d", res.AfterBytes, after)
	}
	if res.ReclaimedBytes != before-after {
		t.Errorf("ReclaimedBytes = %d, want %d", res.ReclaimedBytes, before-after)
	}
	// "Materially smaller": the seed deletes 399/400 of a multi-MB database, so
	// anything short of a large majority means compaction did not happen.
	if after > before/2 {
		t.Fatalf("file not materially smaller: before=%d after=%d", before, after)
	}

	// The atomic-rename contract: the ORIGINAL path must still be a valid,
	// openable bbolt DB with the surviving key intact.
	db, err := bbolt.Open(dbPath, 0600, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("reopen compacted db: %v", err)
	}
	defer db.Close()

	if err := db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("server_alpha_tool_calls"))
		if b == nil {
			return errors.New("bucket server_alpha_tool_calls missing after compaction")
		}
		got := b.Get([]byte("key-0000"))
		if len(got) != 4096 {
			return fmt.Errorf("surviving key length = %d, want 4096", len(got))
		}
		return nil
	}); err != nil {
		t.Fatalf("verify compacted db: %v", err)
	}
}

func TestCompactDatabaseKeepBackup(t *testing.T) {
	dir := t.TempDir()
	dbPath := seedDB(t, dir)
	originalDigest := fileDigest(t, dbPath)

	res, err := compactDatabase(dbPath, compactOptions{TxMaxSize: defaultCompactTxMaxSize, KeepBackup: true})
	if err != nil {
		t.Fatalf("compactDatabase: %v", err)
	}

	if res.BackupPath == "" {
		t.Fatal("BackupPath is empty with KeepBackup=true")
	}
	if got := fileDigest(t, res.BackupPath); got != originalDigest {
		t.Error("backup is not a byte-for-byte copy of the pre-compaction database")
	}
}

func TestCompactDatabaseLockedReturnsLockError(t *testing.T) {
	dir := t.TempDir()
	dbPath := seedDB(t, dir)

	// Hold the exclusive flock the way a running core does.
	held, err := bbolt.Open(dbPath, 0600, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("hold db lock: %v", err)
	}
	defer held.Close()

	start := time.Now()
	_, err = compactDatabase(dbPath, compactOptions{TxMaxSize: defaultCompactTxMaxSize})
	if err == nil {
		t.Fatal("compactDatabase succeeded against a locked database")
	}

	var lockErr *storage.DatabaseLockedError
	if !errors.As(err, &lockErr) {
		t.Fatalf("error is %T (%v), want *storage.DatabaseLockedError", err, err)
	}
	if classifyError(err) != ExitCodeDBLocked {
		t.Errorf("classifyError = %d, want %d", classifyError(err), ExitCodeDBLocked)
	}
	// It must fail fast rather than hang: no retry loop behind the 500ms timeout.
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("took %s to report a locked database", elapsed)
	}
}

func TestCompactDatabaseFailureLeavesOriginalIntact(t *testing.T) {
	dir := t.TempDir()
	dbPath := seedDB(t, dir)
	originalDigest := fileDigest(t, dbPath)

	wantErr := errors.New("injected compaction failure")
	restore := compactFn
	compactFn = func(_, _ *bbolt.DB, _ int64) error { return wantErr }
	defer func() { compactFn = restore }()

	if _, err := compactDatabase(dbPath, compactOptions{TxMaxSize: defaultCompactTxMaxSize, KeepBackup: true}); !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}

	if got := fileDigest(t, dbPath); got != originalDigest {
		t.Error("original database changed after a failed compaction")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "config.db" {
			t.Errorf("failed compaction left behind %q", e.Name())
		}
	}
}

func TestDatabaseStatsReportsReclaimableSpace(t *testing.T) {
	dir := t.TempDir()
	dbPath := seedDB(t, dir)

	stats, err := databaseStats(dbPath, defaultStatsTopBuckets)
	if err != nil {
		t.Fatalf("databaseStats: %v", err)
	}

	if stats.FileBytes != fileSize(t, dbPath) {
		t.Errorf("FileBytes = %d, want %d", stats.FileBytes, fileSize(t, dbPath))
	}
	if stats.PageSize <= 0 {
		t.Fatalf("PageSize = %d, want > 0", stats.PageSize)
	}
	// Guards the PreLoadFreelist option: without it a read-only open never
	// loads the freelist and FreePageN silently reads back as 0.
	if stats.FreePageN <= 0 {
		t.Fatalf("FreePageN = %d, want > 0 after deleting most of the database", stats.FreePageN)
	}
	if stats.ReclaimableBytes != int64(stats.FreePageN)*int64(stats.PageSize) {
		t.Errorf("ReclaimableBytes = %d, want %d", stats.ReclaimableBytes, int64(stats.FreePageN)*int64(stats.PageSize))
	}

	var sawToolCalls int
	for _, b := range stats.Buckets {
		if strings.HasPrefix(b.Name, "server_") && strings.HasSuffix(b.Name, "_tool_calls") {
			sawToolCalls++
		}
	}
	if sawToolCalls != 2 {
		t.Errorf("reported %d server_*_tool_calls buckets, want 2 (%+v)", sawToolCalls, stats.Buckets)
	}
}

// TestDatabaseStatsSharedLock pins what "read-only" actually buys: concurrent
// readers coexist, but a WRITER (a running mcpproxy) still shuts stats out.
// The help text and the remediation message both depend on this being true.
func TestDatabaseStatsSharedLock(t *testing.T) {
	dir := t.TempDir()
	dbPath := seedDB(t, dir)

	reader, err := bbolt.Open(dbPath, 0600, &bbolt.Options{Timeout: 2 * time.Second, ReadOnly: true})
	if err != nil {
		t.Fatalf("hold shared lock: %v", err)
	}
	if _, err := databaseStats(dbPath, defaultStatsTopBuckets); err != nil {
		t.Errorf("databaseStats failed alongside another reader: %v", err)
	}
	reader.Close()

	writer, err := bbolt.Open(dbPath, 0600, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("hold exclusive lock: %v", err)
	}
	_, err = databaseStats(dbPath, defaultStatsTopBuckets)
	writer.Close()

	var lockErr *storage.DatabaseLockedError
	if !errors.As(err, &lockErr) {
		t.Fatalf("error is %T (%v), want *storage.DatabaseLockedError", err, err)
	}
	if classifyError(err) != ExitCodeDBLocked {
		t.Errorf("classifyError = %d, want %d", classifyError(err), ExitCodeDBLocked)
	}
}

func TestDatabaseStatsMissingFile(t *testing.T) {
	if _, err := databaseStats(filepath.Join(t.TempDir(), "config.db"), defaultStatsTopBuckets); err == nil {
		t.Fatal("databaseStats succeeded on a missing file")
	}
}

// The whole crash-safety story rests on os.Rename being atomic, and it is only
// atomic within one filesystem — so the temp file must be created beside the
// target, not in the system temp dir. An adversarial review moved that
// os.CreateTemp to "" and every test still passed, so pin it directly.
func TestCompactDatabaseWritesItsTempFileBesideTheTarget(t *testing.T) {
	dir := t.TempDir()
	dbPath := seedDB(t, dir)

	// Fail after the temp file exists but before the rename, then look at what
	// was created and where.
	var seen []string
	orig := compactFn
	compactFn = func(_, _ *bbolt.DB, _ int64) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			seen = append(seen, e.Name())
		}
		return errors.New("injected failure")
	}
	defer func() { compactFn = orig }()

	if _, err := compactDatabase(dbPath, compactOptions{TxMaxSize: defaultCompactTxMaxSize}); err == nil {
		t.Fatal("expected the injected failure")
	}

	var found bool
	for _, name := range seen {
		if strings.HasPrefix(name, dbFileName+".compact-") {
			found = true
		}
	}
	if !found {
		t.Errorf("no temp file in the database's own directory %s; saw %v.\n"+
			"A temp file elsewhere makes the final os.Rename cross-filesystem, which is neither atomic nor (on Linux) even permitted",
			dir, seen)
	}
}

// ReclaimedBytes describes the FILE. With the default --keep-backup the .bak is
// a hard link to the original inode, so the filesystem gets nothing back until
// the user deletes it. The result has to say so, or the user in #1176 — who ran
// out of disk — runs this and sees no improvement.
func TestCompactDatabaseReportsWhatTheBackupStillCosts(t *testing.T) {
	dir := t.TempDir()
	dbPath := seedDB(t, dir)
	before := fileSize(t, dbPath)

	res, err := compactDatabase(dbPath, compactOptions{TxMaxSize: defaultCompactTxMaxSize, KeepBackup: true})
	if err != nil {
		t.Fatalf("compactDatabase: %v", err)
	}

	if res.BackupBytes != before {
		t.Errorf("BackupBytes = %d, want the pre-compaction size %d", res.BackupBytes, before)
	}
	if res.ReclaimedBytes-res.BackupBytes >= 0 {
		t.Errorf("with a retained backup the disk actually freed must be negative or zero, got reclaimed=%d backup=%d",
			res.ReclaimedBytes, res.BackupBytes)
	}

	res2, err := compactDatabase(dbPath, compactOptions{TxMaxSize: defaultCompactTxMaxSize, KeepBackup: false})
	if err != nil {
		t.Fatalf("compactDatabase without backup: %v", err)
	}
	if res2.BackupBytes != 0 || res2.BackupPath != "" {
		t.Errorf("--keep-backup=false must report no backup, got %q / %d", res2.BackupPath, res2.BackupBytes)
	}
}
