package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.etcd.io/bbolt"
	bbolterrors "go.etcd.io/bbolt/errors"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// db_cmd.go implements the offline `mcpproxy db` maintenance group (GH #1176).
//
// BBolt returns freed pages to its own freelist, never to the OS, so pruning
// tool-call history shrinks the logical database while config.db stays at its
// high-water mark — one report had a 940MB file that pruning could not shrink.
// bbolt.Compact copies the live data into a fresh file, which is the only way
// to hand those pages back. It needs exclusive access, hence "offline".

const (
	// defaultCompactTxMaxSize bounds the destination write transaction so a
	// multi-GB database does not have to fit in memory at once.
	defaultCompactTxMaxSize = int64(64 << 20)

	// defaultStatsTopBuckets is how many buckets `db stats` ranks by key count.
	defaultStatsTopBuckets = 10

	// dbFileName is the fixed name of the BBolt store inside the data dir.
	dbFileName = "config.db"

	// lockedRemediation is the only useful thing to say about a locked
	// database: BBolt's lock is held for the core's whole lifetime, so no
	// amount of waiting or retrying clears it.
	lockedRemediation = "The database is held by a running mcpproxy. Stop it first (quit the tray app, or stop the 'mcpproxy serve' process) and retry."
)

// compactFn is a seam so tests can inject a mid-compaction failure and prove
// the original file survives it. Production always uses bbolt.Compact.
var compactFn = bbolt.Compact

var (
	dbCompactTxMaxSize int64
	dbCompactKeepBkp   bool
	dbStatsTopBuckets  int
)

type compactOptions struct {
	TxMaxSize  int64
	KeepBackup bool
}

type compactResult struct {
	Path           string `json:"path" yaml:"path"`
	BeforeBytes    int64  `json:"before_bytes" yaml:"before_bytes"`
	AfterBytes     int64  `json:"after_bytes" yaml:"after_bytes"`
	ReclaimedBytes int64  `json:"reclaimed_bytes" yaml:"reclaimed_bytes"`
	BackupPath     string `json:"backup_path,omitempty" yaml:"backup_path,omitempty"`
	// BackupBytes is what the retained backup still occupies. ReclaimedBytes
	// describes the FILE; until this backup is deleted the filesystem has not
	// actually got the space back — which matters most to the user who ran out
	// of disk in the first place.
	BackupBytes int64 `json:"backup_bytes,omitempty" yaml:"backup_bytes,omitempty"`
}

type bucketStat struct {
	Name    string `json:"name" yaml:"name"`
	Keys    int    `json:"keys" yaml:"keys"`
	Buckets int    `json:"buckets" yaml:"buckets"`
}

type dbStatsResult struct {
	Path             string       `json:"path" yaml:"path"`
	FileBytes        int64        `json:"file_bytes" yaml:"file_bytes"`
	PageSize         int          `json:"page_size" yaml:"page_size"`
	FreePageN        int          `json:"free_page_count" yaml:"free_page_count"`
	PendingPageN     int          `json:"pending_page_count" yaml:"pending_page_count"`
	ReclaimableBytes int64        `json:"reclaimable_bytes" yaml:"reclaimable_bytes"`
	Buckets          []bucketStat `json:"buckets" yaml:"buckets"`
}

var dbCmd = &cobra.Command{
	Use:   "db",
	Short: "Offline maintenance for the mcpproxy database",
	Long: `Offline maintenance for the mcpproxy BBolt database (config.db).

BBolt never returns freed pages to the operating system, so deleting activity
or tool-call history shrinks the database logically while the file on disk
stays at its high-water mark. 'db compact' rewrites the file to reclaim that
space; 'db stats' reports how much there is to reclaim.`,
}

var dbCompactCmd = &cobra.Command{
	Use:   "compact",
	Short: "Rewrite config.db to return freed pages to the filesystem",
	Long: `Rewrite config.db into a fresh file, returning freed pages to the filesystem.

This requires EXCLUSIVE access to the database, so mcpproxy must be stopped
first. The rewrite goes to a temporary file in the same directory and is moved
into place atomically, so an interrupted compaction leaves the original intact.

Run 'mcpproxy db stats' first to see whether there is anything worth reclaiming.

Examples:
  mcpproxy db compact
  mcpproxy db compact --keep-backup=false
  mcpproxy db compact -o json`,
	RunE: runDBCompact,
}

var dbStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Report database size, reclaimable space and the largest buckets",
	Long: `Report the on-disk size of config.db, how much of it is reclaimable free
pages, and the buckets holding the most keys (every per-server tool-call bucket
is always listed).

This opens the database READ-ONLY. That is a shared lock, so several 'db stats'
runs coexist, but a running mcpproxy holds an EXCLUSIVE lock on config.db and
will still shut this out — stop mcpproxy first, same as for 'db compact'.

Examples:
  mcpproxy db stats
  mcpproxy db stats --top 25 -o json`,
	RunE: runDBStats,
}

func init() {
	dbCompactCmd.Flags().Int64Var(&dbCompactTxMaxSize, "tx-max-size", defaultCompactTxMaxSize,
		"Maximum size in bytes of a single write transaction during compaction (0 = unbounded)")
	dbCompactCmd.Flags().BoolVar(&dbCompactKeepBkp, "keep-backup", true,
		"Keep the pre-compaction database as config.db.bak")
	dbStatsCmd.Flags().IntVar(&dbStatsTopBuckets, "top", defaultStatsTopBuckets,
		"Number of buckets to report, ranked by key count")

	dbCmd.AddCommand(dbCompactCmd)
	dbCmd.AddCommand(dbStatsCmd)
}

// resolveDBPath locates config.db from the global --data-dir flag, falling back
// to the config's data dir and then ~/.mcpproxy, mirroring the other CLI
// commands so `-d` points every subcommand at the same instance (GH #908).
func resolveDBPath() (string, error) {
	dir := dataDir
	if dir == "" {
		if cfg, err := loadCLIConfig(configFile); err == nil && cfg.DataDir != "" {
			dir = cfg.DataDir
		}
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to determine data directory: %w", err)
		}
		dir = filepath.Join(home, ".mcpproxy")
	}
	return filepath.Join(dir, dbFileName), nil
}

func runDBCompact(cmd *cobra.Command, _ []string) error {
	// Past argument parsing every failure is operational, not a usage
	// error; cobra's usage dump would push the remediation line off
	// screen (same pattern as auth_cmd.go).
	cmd.SilenceUsage = true

	dbPath, err := resolveDBPath()
	if err != nil {
		return err
	}

	// Human banner goes to stderr so machine formats keep stdout parseable
	// (see docs/cli-output-formatting.md).
	fmt.Fprintf(os.Stderr, "Compacting %s ...\n", dbPath)

	res, err := compactDatabase(dbPath, compactOptions{
		TxMaxSize:  dbCompactTxMaxSize,
		KeepBackup: dbCompactKeepBkp,
	})
	if err != nil {
		var lockErr *storage.DatabaseLockedError
		if errors.As(err, &lockErr) {
			// classifyError turns this into exit code 3; the remediation is the
			// part the user actually needs.
			fmt.Fprintln(os.Stderr, lockedRemediation)
		}
		return err
	}

	formatter, err := GetOutputFormatter()
	if err != nil {
		return fmt.Errorf("failed to get output formatter: %w", err)
	}

	if format := ResolveOutputFormat(); format == "json" || format == "yaml" {
		out, err := formatter.Format(res)
		if err != nil {
			return fmt.Errorf("failed to format output: %w", err)
		}
		fmt.Println(out)
		return nil
	}

	rows := [][]string{
		{"before", humanBytes(res.BeforeBytes)},
		{"after", humanBytes(res.AfterBytes)},
		{"reclaimed", humanBytes(res.ReclaimedBytes)},
	}
	if res.BackupPath != "" {
		// Spell out that the retained backup still holds the original inode.
		// "reclaimed" above describes the FILE; the filesystem gets nothing
		// back until this is deleted, which is the whole point for the user
		// who ran out of disk in the first place.
		rows = append(rows, []string{"backup", res.BackupPath})
		rows = append(rows, []string{"backup size", humanBytes(res.BackupBytes)})
		rows = append(rows, []string{"disk freed now", "0 B (the backup still holds the old file)"})
		rows = append(rows, []string{"to reclaim", "verify mcpproxy starts, then delete the backup"})
	}
	out, err := formatter.FormatTable([]string{"METRIC", "VALUE"}, rows)
	if err != nil {
		return fmt.Errorf("failed to format output: %w", err)
	}
	fmt.Print(out)
	return nil
}

func runDBStats(cmd *cobra.Command, _ []string) error {
	// Past argument parsing every failure is operational, not a usage
	// error; cobra's usage dump would push the remediation line off
	// screen (same pattern as auth_cmd.go).
	cmd.SilenceUsage = true

	dbPath, err := resolveDBPath()
	if err != nil {
		return err
	}

	top := dbStatsTopBuckets
	if top <= 0 {
		top = defaultStatsTopBuckets
	}

	stats, err := databaseStats(dbPath, top)
	if err != nil {
		var lockErr *storage.DatabaseLockedError
		if errors.As(err, &lockErr) {
			fmt.Fprintln(os.Stderr, lockedRemediation)
		}
		return err
	}

	formatter, err := GetOutputFormatter()
	if err != nil {
		return fmt.Errorf("failed to get output formatter: %w", err)
	}

	if format := ResolveOutputFormat(); format == "json" || format == "yaml" {
		out, err := formatter.Format(stats)
		if err != nil {
			return fmt.Errorf("failed to format output: %w", err)
		}
		fmt.Println(out)
		return nil
	}

	// Table mode has no machine consumer — the json/yaml formats returned
	// above — and this header is the answer to "should I compact?", so it goes
	// to stdout with the table rather than to the stderr banner channel.
	fmt.Printf("Database: %s\n", stats.Path)
	fmt.Printf("Size on disk: %s | free pages: %d (%s reclaimable) | page size: %d\n",
		humanBytes(stats.FileBytes), stats.FreePageN, humanBytes(stats.ReclaimableBytes), stats.PageSize)
	if stats.ReclaimableBytes > 0 {
		fmt.Printf("Run 'mcpproxy db compact' (with mcpproxy stopped) to return that space to the filesystem.\n")
	}
	fmt.Println()

	rows := make([][]string, 0, len(stats.Buckets))
	for _, b := range stats.Buckets {
		rows = append(rows, []string{b.Name, fmt.Sprintf("%d", b.Keys), fmt.Sprintf("%d", b.Buckets)})
	}
	out, err := formatter.FormatTable([]string{"BUCKET", "KEYS", "SUB-BUCKETS"}, rows)
	if err != nil {
		return fmt.Errorf("failed to format output: %w", err)
	}
	fmt.Print(out)
	return nil
}

// compactDatabase rewrites srcPath in place via a temp file + atomic rename.
//
// The rename is what makes this safe to interrupt: until it succeeds the
// original file is the only one anything reads, and every failure path removes
// the temp file rather than leaving a half-written database beside the real one.
func compactDatabase(srcPath string, opts compactOptions) (result *compactResult, err error) {
	info, err := os.Stat(srcPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat database %s: %w", srcPath, err)
	}
	beforeBytes := info.Size()

	// A short timeout and no retry: a locked database means a running core owns
	// it, and waiting cannot fix that — only stopping mcpproxy can.
	//
	// ReadOnly is deliberate. bbolt.Compact only reads the source, and a
	// read-write open commits a freelist-flush transaction when the previous
	// close was unclean — exactly the state a user reaching for this command
	// may be in. Opening read-only makes "the original is untouched" absolute
	// rather than conditional.
	src, err := bbolt.Open(srcPath, 0600, &bbolt.Options{
		Timeout:         500 * time.Millisecond,
		ReadOnly:        true,
		PreLoadFreelist: true,
	})
	if err != nil {
		if errors.Is(err, bbolterrors.ErrTimeout) {
			return nil, &storage.DatabaseLockedError{Path: srcPath, Err: err}
		}
		return nil, fmt.Errorf("failed to open database %s: %w", srcPath, err)
	}
	defer func() {
		if src != nil {
			_ = src.Close()
		}
	}()

	dir := filepath.Dir(srcPath)

	// The temp file must live in the same directory: os.Rename is only atomic
	// within a filesystem.
	tmpFile, err := os.CreateTemp(dir, dbFileName+".compact-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary file in %s: %w", dir, err)
	}
	tmpPath := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("failed to close temporary file: %w", err)
	}
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			os.Remove(tmpPath)
		}
	}()

	dst, err := bbolt.Open(tmpPath, info.Mode().Perm(), &bbolt.Options{Timeout: 500 * time.Millisecond})
	if err != nil {
		return nil, fmt.Errorf("failed to create destination database: %w", err)
	}
	dstClosed := false
	defer func() {
		if !dstClosed {
			_ = dst.Close()
		}
	}()

	if err := compactFn(dst, src, opts.TxMaxSize); err != nil {
		return nil, fmt.Errorf("compaction failed: %w", err)
	}

	// Close both handles before touching the files: on Windows a rename over an
	// open file fails outright.
	if err := dst.Close(); err != nil {
		return nil, fmt.Errorf("failed to close destination database: %w", err)
	}
	dstClosed = true
	if err := src.Close(); err != nil {
		src = nil
		return nil, fmt.Errorf("failed to close source database: %w", err)
	}
	src = nil

	if err := syncPath(tmpPath); err != nil {
		return nil, fmt.Errorf("failed to sync compacted database: %w", err)
	}

	afterInfo, err := os.Stat(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat compacted database: %w", err)
	}

	// bbolt.Open does not chmod a file that already exists, so carry the
	// original permissions across rather than leaking os.CreateTemp's 0600.
	if err := os.Chmod(tmpPath, info.Mode().Perm()); err != nil {
		return nil, fmt.Errorf("failed to set permissions on compacted database: %w", err)
	}

	res := &compactResult{
		Path:           srcPath,
		BeforeBytes:    beforeBytes,
		AfterBytes:     afterInfo.Size(),
		ReclaimedBytes: beforeBytes - afterInfo.Size(),
	}

	// The backup is a hard link, not a move: config.db therefore never stops
	// existing, and the rename below still replaces it in one step.
	if opts.KeepBackup {
		backupPath := srcPath + ".bak"
		if err := os.Remove(backupPath); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to remove stale backup %s: %w", backupPath, err)
		}
		if err := os.Link(srcPath, backupPath); err != nil {
			if err := copyFile(srcPath, backupPath, info.Mode().Perm()); err != nil {
				return nil, fmt.Errorf("failed to create backup %s: %w", backupPath, err)
			}
		}
		res.BackupPath = backupPath
		res.BackupBytes = beforeBytes
	}

	if err := os.Rename(tmpPath, srcPath); err != nil {
		// The backup was linked just above and is now meaningless — the
		// original is still in place, so leaving a .bak behind would only
		// confuse the next run.
		if res.BackupPath != "" {
			os.Remove(res.BackupPath)
		}
		return nil, fmt.Errorf("failed to replace %s: %w", srcPath, err)
	}
	cleanupTmp = false

	// Fsync the directory so the rename itself survives a crash. Best-effort:
	// Windows cannot fsync a directory handle, and the data is already durable.
	_ = syncPath(dir)

	return res, nil
}

// databaseStats opens the database read-only and reports what compaction would
// reclaim.
//
// A read-only open takes a SHARED lock, so several db stats runs coexist — but
// a running mcpproxy holds the EXCLUSIVE lock, which shuts every reader out.
// This is offline-only, like db compact.
func databaseStats(srcPath string, top int) (*dbStatsResult, error) {
	info, err := os.Stat(srcPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat database %s: %w", srcPath, err)
	}

	// PreLoadFreelist is required: a read-only open otherwise never loads the
	// freelist, and Stats().FreePageN silently reads back as 0.
	db, err := bbolt.Open(srcPath, 0600, &bbolt.Options{
		Timeout:         200 * time.Millisecond,
		ReadOnly:        true,
		PreLoadFreelist: true,
	})
	if err != nil {
		if errors.Is(err, bbolterrors.ErrTimeout) {
			return nil, &storage.DatabaseLockedError{Path: srcPath, Err: err}
		}
		return nil, fmt.Errorf("failed to open database %s: %w", srcPath, err)
	}
	defer db.Close()

	stats := db.Stats()
	pageSize := db.Info().PageSize

	var all []bucketStat
	if err := db.View(func(tx *bbolt.Tx) error {
		return tx.ForEach(func(name []byte, b *bbolt.Bucket) error {
			s := b.Stats()
			all = append(all, bucketStat{Name: string(name), Keys: s.KeyN, Buckets: s.BucketN - 1})
			return nil
		})
	}); err != nil {
		return nil, fmt.Errorf("failed to read bucket statistics: %w", err)
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].Keys != all[j].Keys {
			return all[i].Keys > all[j].Keys
		}
		return all[i].Name < all[j].Name
	})

	return &dbStatsResult{
		Path:             srcPath,
		FileBytes:        info.Size(),
		PageSize:         pageSize,
		FreePageN:        stats.FreePageN,
		PendingPageN:     stats.PendingPageN,
		ReclaimableBytes: int64(stats.FreePageN) * int64(pageSize),
		Buckets:          selectReportedBuckets(all, top),
	}, nil
}

// selectReportedBuckets keeps the top-N by key count plus every per-server
// tool-call bucket, because those are the ones that grow unboundedly and so are
// the reason a user is reading this output at all (#1176).
func selectReportedBuckets(sorted []bucketStat, top int) []bucketStat {
	if top < 0 {
		top = 0
	}
	if top > len(sorted) {
		top = len(sorted)
	}
	selected := make([]bucketStat, 0, len(sorted))
	included := make(map[string]bool, len(sorted))
	for _, b := range sorted[:top] {
		selected = append(selected, b)
		included[b.Name] = true
	}
	for _, b := range sorted[top:] {
		if !included[b.Name] && isToolCallBucket(b.Name) {
			selected = append(selected, b)
		}
	}
	return selected
}

func isToolCallBucket(name string) bool {
	return strings.HasPrefix(name, "server_") && strings.HasSuffix(name, "_tool_calls")
}

// syncPath fsyncs a file or directory so a rename/write survives a power loss.
// syncPath fsyncs a file or directory.
//
// The file case opens O_RDWR on purpose: Windows implements File.Sync as
// FlushFileBuffers, which requires a handle with GENERIC_WRITE, so an
// os.Open'd read-only handle fails there and would abort every compaction.
// Directories cannot be opened O_RDWR, so that case falls back to read-only —
// and the caller treats a directory sync as best-effort anyway.
func syncPath(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec // path is derived from the data dir
	if err != nil {
		if f, err = os.Open(path); err != nil { //nolint:gosec // same path
			return err
		}
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// copyFile is the fallback for filesystems that reject hard links.
func copyFile(srcPath, dstPath string, perm os.FileMode) error {
	data, err := os.ReadFile(srcPath) //nolint:gosec // path is derived from the data dir
	if err != nil {
		return err
	}
	return os.WriteFile(dstPath, data, perm)
}

// humanBytes renders a byte count for the table view; JSON/YAML keep the raw
// integers so scripts do not have to parse a unit suffix.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
