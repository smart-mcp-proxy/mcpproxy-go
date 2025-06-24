package storage

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"go.etcd.io/bbolt"
	"go.etcd.io/bbolt/errors"
	"go.uber.org/zap"
)

// BoltDB wraps bolt database operations
type BoltDB struct {
	db     *bbolt.DB
	logger *zap.SugaredLogger
}

// NewBoltDB creates a new BoltDB instance
func NewBoltDB(dataDir string, logger *zap.SugaredLogger) (*BoltDB, error) {
	dbPath := filepath.Join(dataDir, "config.db")

	// Try to open with timeout, if it fails, attempt recovery
	db, err := bbolt.Open(dbPath, 0644, &bbolt.Options{
		Timeout: 10 * time.Second,
	})
	if err != nil {
		logger.Warnf("Failed to open database on first attempt: %v", err)

		// Check if it's a timeout or lock issue
		if err == errors.ErrTimeout {
			logger.Info("Database timeout detected, attempting recovery...")

			// Try to backup and recreate if file exists
			if _, statErr := filepath.Glob(dbPath); statErr == nil {
				backupPath := dbPath + ".backup." + time.Now().Format("20060102-150405")
				logger.Infof("Creating backup at %s", backupPath)

				// Attempt to copy the file
				if cpErr := copyFile(dbPath, backupPath); cpErr != nil {
					logger.Warnf("Failed to create backup: %v", cpErr)
				}

				// Remove the original file to clear any locks
				if rmErr := removeFile(dbPath); rmErr != nil {
					logger.Warnf("Failed to remove locked database file: %v", rmErr)
				}
			}

			// Try to open again
			db, err = bbolt.Open(dbPath, 0644, &bbolt.Options{
				Timeout: 5 * time.Second,
			})
		}

		if err != nil {
			return nil, fmt.Errorf("failed to open bolt database after recovery attempt: %w", err)
		}
	}

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
			MetaBucket,
		}

		for _, bucket := range buckets {
			if _, err := tx.CreateBucketIfNotExists([]byte(bucket)); err != nil {
				return fmt.Errorf("failed to create bucket %s: %w", bucket, err)
			}
		}

		// Set schema version
		metaBucket := tx.Bucket([]byte(MetaBucket))
		versionBytes := make([]byte, 8)
		binary.LittleEndian.PutUint64(versionBytes, CurrentSchemaVersion)
		return metaBucket.Put([]byte(SchemaVersionKey), versionBytes)
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
			return fmt.Errorf("upstream not found")
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

// Generic operations

// Backup creates a backup of the database
func (b *BoltDB) Backup(destPath string) error {
	return b.db.View(func(tx *bbolt.Tx) error {
		return tx.CopyFile(destPath, 0644)
	})
}

// Stats returns database statistics
func (b *BoltDB) Stats() (*bbolt.Stats, error) {
	stats := b.db.Stats()
	return &stats, nil
}

// copyFile copies a file from src to dst
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
func removeFile(path string) error {
	return os.Remove(path)
}
