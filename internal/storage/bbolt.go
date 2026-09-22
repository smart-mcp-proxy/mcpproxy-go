package storage

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"go.etcd.io/bbolt"
	"go.etcd.io/bbolt/errors"
	"go.uber.org/zap"
)

// DatabaseLockedError indicates that the database is locked by another process
type DatabaseLockedError struct {
	Path string
	Err  error
}

func (e *DatabaseLockedError) Error() string {
	return fmt.Sprintf("database %s is locked by another process", e.Path)
}

func (e *DatabaseLockedError) Unwrap() error {
	return e.Err
}

// BoltDB wraps bolt database operations
type BoltDB struct {
	db     *bbolt.DB
	logger *zap.SugaredLogger
}

// NewBoltDB creates a new BoltDB instance
func NewBoltDB(dataDir string, logger *zap.SugaredLogger) (*BoltDB, error) {
	dbPath := filepath.Join(dataDir, "config.db")

	// Try to open with timeout, if it fails, immediately return database locked error.
	// The database holds OAuth tokens and DCR client secrets, so it is created
	// owner-only (0600).
	db, err := bbolt.Open(dbPath, 0600, &bbolt.Options{
		Timeout: 10 * time.Second,
	})
	if err != nil {
		logger.Warnf("Failed to open database on first attempt: %v", err)

		// Check if it's a timeout or lock issue - return immediately without recovery attempts
		if err == errors.ErrTimeout {
			logger.Info("Database timeout detected, another mcpproxy instance may be running")
			return nil, &DatabaseLockedError{
				Path: dbPath,
				Err:  err,
			}
		}

		// For other errors, return wrapped error
		return nil, fmt.Errorf("failed to open bolt database: %w", err)
	}

	// The mode passed to bbolt.Open only applies when the file is created, so
	// databases created by older builds keep their 0644 mode. Tighten them here.
	tightenFilePermissions(dbPath, logger)

	boltDB := &BoltDB{
		db:     db,
		logger: logger,
	}

	// Initialize buckets and schema
	if err := boltDB.initBuckets(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize buckets: %w", err)
	}

	return boltDB, nil
}

// tightenFilePermissions clears group and other permission bits from path,
// leaving owner bits untouched so a deliberately stricter mode is never widened.
// It is best-effort: any failure is logged at debug level and ignored, because a
// mode that cannot be tightened (read-only mount, exotic filesystem, Windows)
// must never prevent the proxy from starting.
func tightenFilePermissions(path string, logger *zap.SugaredLogger) {
	info, err := os.Stat(path)
	if err != nil {
		logger.Debugf("Could not stat %s to check file permissions: %v", path, err)
		return
	}

	perm := info.Mode().Perm()
	if perm&0o077 == 0 {
		return
	}

	if err := os.Chmod(path, perm&^0o077); err != nil {
		logger.Debugf("Could not tighten permissions on %s: %v", path, err)
		return
	}

	logger.Debugf("Tightened permissions on %s from %#o to %#o", path, perm, perm&^0o077)
}

// Close closes the database
func (b *BoltDB) Close() error {
	return b.db.Close()
}

// initBuckets creates required buckets and sets up schema
func (b *BoltDB) initBuckets() error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		// Create buckets
		buckets := []string{
			UpstreamsBucket,
			ToolStatsBucket,
			ToolHashBucket,
			ToolApprovalBucket,
			PromptApprovalBucket,
			OAuthTokenBucket,
			MetaBucket,
			ActivityRecordsBucket,
			ActivityStatsBucket,
			ScannersBucket,
			ScanJobsBucket,
			ScanJobIndexBucket,
			ScanReportsBucket,
			IntegrityBaselinesBucket,
			OnboardingBucket,
			SessionsBucket,
		}

		for _, bucket := range buckets {
			if _, err := tx.CreateBucketIfNotExists([]byte(bucket)); err != nil {
				return fmt.Errorf("failed to create bucket %s: %w", bucket, err)
			}
		}

		// Move MCP session records out of the shared "sessions" bucket, which the
		// server edition also used for USER LOGIN sessions. Each side swept the
		// bucket believing it owned every key, deleting the other's data.
		// Idempotent; leaves auth sessions untouched, so nobody is logged out.
		if err := migrateLegacySessions(tx); err != nil {
			return fmt.Errorf("failed to migrate legacy sessions bucket: %w", err)
		}

		// Migration is a write path into the sessions bucket like any other, and
		// it can move in an arbitrary number of records. Enforce the retention
		// cap here so it is an invariant of an open database rather than
		// something only CreateSession happens to maintain.
		if err := enforceSessionRetentionOnOpen(tx, b.logger); err != nil {
			return fmt.Errorf("failed to enforce session retention: %w", err)
		}

		// Backfill the scan-job index for databases created before MCP-2205.
		// Idempotent: only runs when the index is empty but jobs exist.
		if err := backfillScanJobIndex(tx); err != nil {
			return fmt.Errorf("failed to backfill scan job index: %w", err)
		}

		// Set schema version only for new databases. Existing databases keep their
		// stored version so migrations can observe and upgrade them.
		metaBucket := tx.Bucket([]byte(MetaBucket))
		if metaBucket.Get([]byte(SchemaVersionKey)) == nil {
			versionBytes := make([]byte, 8)
			binary.LittleEndian.PutUint64(versionBytes, CurrentSchemaVersion)
			return metaBucket.Put([]byte(SchemaVersionKey), versionBytes)
		}
		return nil
	})
}

// GetSchemaVersion returns the current schema version
func (b *BoltDB) GetSchemaVersion() (uint64, error) {
	var version uint64
	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(MetaBucket))
		if bucket == nil {
			return fmt.Errorf("meta bucket not found")
		}

		versionBytes := bucket.Get([]byte(SchemaVersionKey))
		if versionBytes == nil {
			version = 0
			return nil
		}

		version = binary.LittleEndian.Uint64(versionBytes)
		return nil
	})

	return version, err
}

// SetSchemaVersion stores the current migration schema version.
func (b *BoltDB) SetSchemaVersion(version uint64) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(MetaBucket))
		if bucket == nil {
			return fmt.Errorf("meta bucket not found")
		}

		versionBytes := make([]byte, 8)
		binary.LittleEndian.PutUint64(versionBytes, version)
		return bucket.Put([]byte(SchemaVersionKey), versionBytes)
	})
}

// Upstream operations

// SaveUpstream saves an upstream server record
func (b *BoltDB) SaveUpstream(record *UpstreamRecord) error {
	record.Updated = time.Now()

	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(UpstreamsBucket))
		data, err := record.MarshalBinary()
		if err != nil {
			return err
		}
		return bucket.Put([]byte(record.ID), data)
	})
}

// GetUpstream retrieves an upstream server record by ID
func (b *BoltDB) GetUpstream(id string) (*UpstreamRecord, error) {
	var record *UpstreamRecord

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(UpstreamsBucket))
		data := bucket.Get([]byte(id))
		if data == nil {
			return ErrUpstreamNotFound
		}

		record = &UpstreamRecord{}
		return record.UnmarshalBinary(data)
	})

	return record, err
}

// ListUpstreams returns all upstream server records
func (b *BoltDB) ListUpstreams() ([]*UpstreamRecord, error) {
	var records []*UpstreamRecord

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(UpstreamsBucket))
		return bucket.ForEach(func(_, v []byte) error {
			record := &UpstreamRecord{}
			if err := record.UnmarshalBinary(v); err != nil {
				return err
			}
			records = append(records, record)
			return nil
		})
	})

	return records, err
}

// DeleteUpstream deletes an upstream server record
func (b *BoltDB) DeleteUpstream(id string) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(UpstreamsBucket))
		return bucket.Delete([]byte(id))
	})
}

// Tool statistics operations

// IncrementToolStats increments the usage count for a tool
func (b *BoltDB) IncrementToolStats(toolName string) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ToolStatsBucket))

		// Get existing record
		var record ToolStatRecord
		data := bucket.Get([]byte(toolName))
		if data != nil {
			if err := record.UnmarshalBinary(data); err != nil {
				return err
			}
		} else {
			record.ToolName = toolName
		}

		// Increment count and update timestamp
		record.Count++
		record.LastUsed = time.Now()

		// Save back
		newData, err := record.MarshalBinary()
		if err != nil {
			return err
		}

		return bucket.Put([]byte(toolName), newData)
	})
}

// GetToolStats retrieves tool statistics
func (b *BoltDB) GetToolStats(toolName string) (*ToolStatRecord, error) {
	var record *ToolStatRecord

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ToolStatsBucket))
		data := bucket.Get([]byte(toolName))
		if data == nil {
			return fmt.Errorf("tool stats not found")
		}

		record = &ToolStatRecord{}
		return record.UnmarshalBinary(data)
	})

	return record, err
}

// ListToolStats returns all tool statistics
func (b *BoltDB) ListToolStats() ([]*ToolStatRecord, error) {
	var records []*ToolStatRecord

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ToolStatsBucket))
		return bucket.ForEach(func(_, v []byte) error {
			record := &ToolStatRecord{}
			if err := record.UnmarshalBinary(v); err != nil {
				return err
			}
			records = append(records, record)
			return nil
		})
	})

	return records, err
}

// Tool hash operations

// SaveToolHash saves a tool hash for change detection
func (b *BoltDB) SaveToolHash(toolName, hash string) error {
	record := &ToolHashRecord{
		ToolName: toolName,
		Hash:     hash,
		Updated:  time.Now(),
	}

	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ToolHashBucket))
		data, err := record.MarshalBinary()
		if err != nil {
			return err
		}
		return bucket.Put([]byte(toolName), data)
	})
}

// GetToolHash retrieves a tool hash
func (b *BoltDB) GetToolHash(toolName string) (string, error) {
	var hash string

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ToolHashBucket))
		data := bucket.Get([]byte(toolName))
		if data == nil {
			return fmt.Errorf("tool hash not found")
		}

		record := &ToolHashRecord{}
		if err := record.UnmarshalBinary(data); err != nil {
			return err
		}

		hash = record.Hash
		return nil
	})

	return hash, err
}

// DeleteToolHash deletes a tool hash
func (b *BoltDB) DeleteToolHash(toolName string) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ToolHashBucket))
		return bucket.Delete([]byte(toolName))
	})
}

// Tool approval operations (tool-level quarantine)

// SaveToolApproval saves a tool approval record
func (b *BoltDB) SaveToolApproval(record *ToolApprovalRecord) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ToolApprovalBucket))
		data, err := record.MarshalBinary()
		if err != nil {
			return err
		}
		return bucket.Put([]byte(record.Key()), data)
	})
}

// StampToolApprovalsIdentityKeyed marks the named records of one server
// identity-keyed (Spec 105 FR-009) in ONE update transaction, re-reading each
// record INSIDE the transaction and stamping it only if it is still unstamped
// and still does not Restricts() at write time. Used by the discovery
// producer to end the legacy consults for a server's remaining pre-105
// records after its first pass. The in-transaction re-read is what makes the
// sweep safe against an operator write (SetToolEnabled / BlockTools) that
// lands between the caller's listing and this stamp (astra r1 P4): a stale
// listed copy is never written back, so the operator's Disabled=true is
// neither discarded nor stamped over — only the IdentityKeyed bit is ever
// touched. Returns the names actually stamped.
func (b *BoltDB) StampToolApprovalsIdentityKeyed(serverName string, toolNames []string) ([]string, error) {
	if len(toolNames) == 0 {
		return nil, nil
	}
	var stamped []string
	err := b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ToolApprovalBucket))
		for _, name := range toolNames {
			key := []byte(ToolApprovalKey(serverName, name))
			data := bucket.Get(key)
			if data == nil {
				continue
			}
			record := &ToolApprovalRecord{}
			if err := record.UnmarshalBinary(data); err != nil {
				return err
			}
			if record.IdentityKeyed || record.Restricts() {
				continue
			}
			record.IdentityKeyed = true
			out, err := record.MarshalBinary()
			if err != nil {
				return err
			}
			if err := bucket.Put(key, out); err != nil {
				return err
			}
			stamped = append(stamped, name)
		}
		return nil
	})
	return stamped, err
}

// GetToolApproval retrieves a tool approval record by server and tool name.
// Returns ErrToolApprovalNotFound (wrapped so callers can use errors.Is) when
// no record exists. Any other error indicates a real read failure (decode
// error, closed DB, etc.) and MUST NOT be treated as "missing" by callers.
func (b *BoltDB) GetToolApproval(serverName, toolName string) (*ToolApprovalRecord, error) {
	var record *ToolApprovalRecord

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ToolApprovalBucket))
		key := ToolApprovalKey(serverName, toolName)
		data := bucket.Get([]byte(key))
		if data == nil {
			return fmt.Errorf("%w: %s", ErrToolApprovalNotFound, key)
		}

		record = &ToolApprovalRecord{}
		return record.UnmarshalBinary(data)
	})

	return record, err
}

// GetToolApprovals reads the approval records for several tools of ONE server
// inside a single read transaction, so the result is a consistent snapshot: no
// write can land between the individual key reads the way it can between
// consecutive GetToolApproval calls. Tools without a record are simply absent
// from the returned map (there is no ErrToolApprovalNotFound for a partial
// miss); a decode failure on any key fails the whole read.
func (b *BoltDB) GetToolApprovals(serverName string, toolNames ...string) (map[string]*ToolApprovalRecord, error) {
	records := make(map[string]*ToolApprovalRecord, len(toolNames))

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ToolApprovalBucket))
		for _, toolName := range toolNames {
			data := bucket.Get([]byte(ToolApprovalKey(serverName, toolName)))
			if data == nil {
				continue
			}
			record := &ToolApprovalRecord{}
			if err := record.UnmarshalBinary(data); err != nil {
				return err
			}
			records[toolName] = record
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return records, nil
}

// ListToolApprovals returns all tool approval records for a server.
// If serverName is empty, returns all records across all servers.
func (b *BoltDB) ListToolApprovals(serverName string) ([]*ToolApprovalRecord, error) {
	var records []*ToolApprovalRecord

	prefix := ""
	if serverName != "" {
		prefix = serverName + ":"
	}

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ToolApprovalBucket))
		return bucket.ForEach(func(k, v []byte) error {
			if prefix != "" && !bytes.HasPrefix(k, []byte(prefix)) {
				return nil
			}

			record := &ToolApprovalRecord{}
			if err := record.UnmarshalBinary(v); err != nil {
				return err
			}
			records = append(records, record)
			return nil
		})
	})

	return records, err
}

// DeleteToolApproval deletes a tool approval record
func (b *BoltDB) DeleteToolApproval(serverName, toolName string) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ToolApprovalBucket))
		key := ToolApprovalKey(serverName, toolName)
		return bucket.Delete([]byte(key))
	})
}

// DeleteServerToolApprovals deletes all tool approval records for a server
func (b *BoltDB) DeleteServerToolApprovals(serverName string) error {
	prefix := serverName + ":"
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ToolApprovalBucket))
		var keysToDelete [][]byte
		err := bucket.ForEach(func(k, _ []byte) error {
			if bytes.HasPrefix(k, []byte(prefix)) {
				keysToDelete = append(keysToDelete, k)
			}
			return nil
		})
		if err != nil {
			return err
		}
		for _, key := range keysToDelete {
			if err := bucket.Delete(key); err != nil {
				return err
			}
		}
		return nil
	})
}

// PruneToolApprovalsNotIn deletes tool-approval records whose ServerName is not
// present in keep, returning the number removed. This GCs orphaned approvals
// for servers that left the config without an explicit delete (e.g. the config
// file was hand-edited, or an old migration). Records for configured servers —
// including disabled ones — are kept so re-enabling a server doesn't re-trigger
// quarantine of its previously-approved tools (MCP-1002).
func (b *BoltDB) PruneToolApprovalsNotIn(keep map[string]bool) (int, error) {
	removed := 0
	err := b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ToolApprovalBucket))
		if bucket == nil {
			return nil
		}
		var keysToDelete [][]byte
		scanErr := bucket.ForEach(func(k, v []byte) error {
			var record ToolApprovalRecord
			if err := json.Unmarshal(v, &record); err != nil {
				// Unparseable record: leave it alone rather than risk dropping data.
				return nil
			}
			if !keep[record.ServerName] {
				keysToDelete = append(keysToDelete, append([]byte(nil), k...))
			}
			return nil
		})
		if scanErr != nil {
			return scanErr
		}
		for _, key := range keysToDelete {
			if err := bucket.Delete(key); err != nil {
				return err
			}
			removed++
		}
		return nil
	})
	return removed, err
}

// --- Prompt approval CRUD (spec 100, mirrors the tool approval ops 1:1) ---

// SavePromptApproval upserts a prompt approval record.
func (b *BoltDB) SavePromptApproval(record *PromptApprovalRecord) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(PromptApprovalBucket))
		data, err := record.MarshalBinary()
		if err != nil {
			return err
		}
		return bucket.Put([]byte(record.Key()), data)
	})
}

// GetPromptApproval retrieves a prompt approval record by server and prompt
// name. Returns ErrPromptApprovalNotFound (wrapped) when no record exists; any
// other error is a real read failure and MUST NOT be treated as "missing".
func (b *BoltDB) GetPromptApproval(serverName, promptName string) (*PromptApprovalRecord, error) {
	var record *PromptApprovalRecord
	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(PromptApprovalBucket))
		key := PromptApprovalKey(serverName, promptName)
		data := bucket.Get([]byte(key))
		if data == nil {
			return fmt.Errorf("%w: %s", ErrPromptApprovalNotFound, key)
		}
		record = &PromptApprovalRecord{}
		return record.UnmarshalBinary(data)
	})
	return record, err
}

// ListPromptApprovals returns all prompt approval records for a server. If
// serverName is empty, returns all records across all servers.
func (b *BoltDB) ListPromptApprovals(serverName string) ([]*PromptApprovalRecord, error) {
	var records []*PromptApprovalRecord
	prefix := ""
	if serverName != "" {
		prefix = serverName + ":"
	}
	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(PromptApprovalBucket))
		return bucket.ForEach(func(k, v []byte) error {
			if prefix != "" && !bytes.HasPrefix(k, []byte(prefix)) {
				return nil
			}
			record := &PromptApprovalRecord{}
			if err := record.UnmarshalBinary(v); err != nil {
				return err
			}
			records = append(records, record)
			return nil
		})
	})
	return records, err
}

// DeletePromptApproval deletes a prompt approval record.
func (b *BoltDB) DeletePromptApproval(serverName, promptName string) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(PromptApprovalBucket))
		return bucket.Delete([]byte(PromptApprovalKey(serverName, promptName)))
	})
}

// DeleteServerPromptApprovals deletes all prompt approval records for a server.
func (b *BoltDB) DeleteServerPromptApprovals(serverName string) error {
	prefix := serverName + ":"
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(PromptApprovalBucket))
		var keysToDelete [][]byte
		err := bucket.ForEach(func(k, _ []byte) error {
			if bytes.HasPrefix(k, []byte(prefix)) {
				keysToDelete = append(keysToDelete, k)
			}
			return nil
		})
		if err != nil {
			return err
		}
		for _, key := range keysToDelete {
			if err := bucket.Delete(key); err != nil {
				return err
			}
		}
		return nil
	})
}

// Generic operations

// Backup creates a backup of the database.
// The copy carries the same secrets as the live database and the destination is
// caller-chosen (potentially outside the 0700 data directory), so it is written
// owner-only.
func (b *BoltDB) Backup(destPath string) error {
	// bbolt's Tx.CopyFile opens the destination with O_CREATE|O_TRUNC, so its
	// mode argument is ignored when the file already exists: writing straight to
	// destPath would pour the database into whatever permissions a stale backup
	// happened to carry, and a chmod afterwards comes too late. Stage the copy in
	// an owner-only temporary file (os.CreateTemp creates it 0600) next to the
	// destination, then rename it into place - the rename keeps the temp file's
	// inode and so carries the 0600 mode with it (atomically on POSIX; Go makes
	// no atomicity promise on Windows).
	tmpFile, err := os.CreateTemp(filepath.Dir(destPath), ".config.db.backup-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary backup file: %w", err)
	}

	tmpPath := tmpFile.Name()
	renamed := false
	defer func() {
		tmpFile.Close() //nolint:errcheck // best-effort cleanup; Close below is the checked one
		if !renamed {
			os.Remove(tmpPath) //nolint:errcheck // best-effort cleanup
		}
	}()

	if err := b.db.View(func(tx *bbolt.Tx) error {
		_, writeErr := tx.WriteTo(tmpFile)
		return writeErr
	}); err != nil {
		return fmt.Errorf("failed to write backup: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("failed to flush backup: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close backup: %w", err)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("failed to move backup into place: %w", err)
	}
	renamed = true

	return nil
}

// Stats returns database statistics
func (b *BoltDB) Stats() (*bbolt.Stats, error) {
	stats := b.db.Stats()
	return &stats, nil
}

// copyFile copies a file from src to dst
//
//nolint:unused // Reserved for future backup functionality
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

// removeFile safely removes a file
//
//nolint:unused // Reserved for future cleanup functionality
func removeFile(path string) error {
	return os.Remove(path)
}

// OAuth token operations

// SaveOAuthToken saves an OAuth token record
func (b *BoltDB) SaveOAuthToken(record *OAuthTokenRecord) error {
	record.Updated = time.Now()

	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(OAuthTokenBucket))
		data, err := record.MarshalBinary()
		if err != nil {
			return err
		}
		return bucket.Put([]byte(record.ServerName), data)
	})
}

// GetOAuthToken retrieves an OAuth token record by server name
func (b *BoltDB) GetOAuthToken(serverName string) (*OAuthTokenRecord, error) {
	var record *OAuthTokenRecord

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(OAuthTokenBucket))
		data := bucket.Get([]byte(serverName))
		if data == nil {
			return fmt.Errorf("oauth token not found")
		}

		record = &OAuthTokenRecord{}
		return record.UnmarshalBinary(data)
	})

	return record, err
}

// DeleteOAuthToken deletes an OAuth token record
func (b *BoltDB) DeleteOAuthToken(serverName string) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(OAuthTokenBucket))
		return bucket.Delete([]byte(serverName))
	})
}

// UpdateOAuthClientCredentials updates the client credentials (from DCR), callback port and the
// exact redirect URI used, on an existing token record. This is called after successful Dynamic
// Client Registration to persist the obtained client_id/secret, the callback port used for the
// redirect_uri (Spec 022: OAuth Redirect URI Port Persistence), and the redirect URI itself so a
// later change to `oauth.redirect_uri` that keeps the same port but changes the path (issue #1304)
// can still be detected as stale (comparing port alone would miss it).
func (b *BoltDB) UpdateOAuthClientCredentials(serverKey, clientID, clientSecret string, callbackPort int, redirectURI string) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(OAuthTokenBucket))
		data := bucket.Get([]byte(serverKey))

		var record *OAuthTokenRecord
		if data != nil {
			// Update existing record
			record = &OAuthTokenRecord{}
			if err := record.UnmarshalBinary(data); err != nil {
				return err
			}
			record.ClientID = clientID
			record.ClientSecret = clientSecret
			record.CallbackPort = callbackPort
			record.RedirectURI = redirectURI
			record.Updated = time.Now()
		} else {
			// Create minimal record with just client credentials
			// Full token will be saved later during OAuth completion
			record = &OAuthTokenRecord{
				ServerName:   serverKey,
				ClientID:     clientID,
				ClientSecret: clientSecret,
				CallbackPort: callbackPort,
				RedirectURI:  redirectURI,
				Created:      time.Now(),
				Updated:      time.Now(),
			}
		}

		newData, err := record.MarshalBinary()
		if err != nil {
			return err
		}
		return bucket.Put([]byte(serverKey), newData)
	})
}

// GetOAuthClientCredentials retrieves the client credentials, callback port and redirect URI for
// token refresh. callbackPort returns 0 and redirectURI returns "" if not stored (legacy records
// or fresh records without DCR).
func (b *BoltDB) GetOAuthClientCredentials(serverKey string) (clientID, clientSecret string, callbackPort int, redirectURI string, err error) {
	err = b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(OAuthTokenBucket))
		data := bucket.Get([]byte(serverKey))
		if data == nil {
			return nil // No credentials stored
		}

		record := &OAuthTokenRecord{}
		if err := record.UnmarshalBinary(data); err != nil {
			return err
		}
		clientID = record.ClientID
		clientSecret = record.ClientSecret
		callbackPort = record.CallbackPort
		redirectURI = record.RedirectURI
		return nil
	})
	return
}

// ClearOAuthClientCredentials clears only the DCR-related fields (ClientID, ClientSecret, CallbackPort)
// while preserving any existing token data. This is called when the callback port conflicts and
// fresh DCR is required (Spec 022: OAuth Redirect URI Port Persistence)
func (b *BoltDB) ClearOAuthClientCredentials(serverKey string) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(OAuthTokenBucket))
		data := bucket.Get([]byte(serverKey))
		if data == nil {
			return nil // Nothing to clear
		}

		record := &OAuthTokenRecord{}
		if err := record.UnmarshalBinary(data); err != nil {
			return err
		}

		// Clear DCR-related fields
		record.ClientID = ""
		record.ClientSecret = ""
		record.CallbackPort = 0
		record.RedirectURI = ""
		record.Updated = time.Now()

		newData, err := record.MarshalBinary()
		if err != nil {
			return err
		}
		return bucket.Put([]byte(serverKey), newData)
	})
}

// ListOAuthTokens returns all OAuth token records
func (b *BoltDB) ListOAuthTokens() ([]*OAuthTokenRecord, error) {
	var records []*OAuthTokenRecord

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(OAuthTokenBucket))
		return bucket.ForEach(func(_, v []byte) error {
			record := &OAuthTokenRecord{}
			if err := record.UnmarshalBinary(v); err != nil {
				return err
			}
			records = append(records, record)
			return nil
		})
	})

	return records, err
}

// Onboarding wizard operations (Spec 046)

// GetOnboardingState returns the current onboarding state.
// If no state has been recorded, returns a zero-value OnboardingState
// (i.e. Engaged=false) with nil error.
func (b *BoltDB) GetOnboardingState() (*OnboardingState, error) {
	state := &OnboardingState{}

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(OnboardingBucket))
		if bucket == nil {
			return nil
		}

		data := bucket.Get([]byte(OnboardingStateKey))
		if data == nil {
			return nil
		}

		return json.Unmarshal(data, state)
	})

	return state, err
}

// SaveOnboardingState persists the wizard state.
func (b *BoltDB) SaveOnboardingState(state *OnboardingState) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(OnboardingBucket))
		if bucket == nil {
			return fmt.Errorf("onboarding bucket not found")
		}

		data, err := json.Marshal(state)
		if err != nil {
			return err
		}

		return bucket.Put([]byte(OnboardingStateKey), data)
	})
}
