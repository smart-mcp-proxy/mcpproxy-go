package storage

import (
	"encoding/json"
	"fmt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"go.etcd.io/bbolt"
)

// scanJobMetaFrom projects a full ScanJob into its lightweight index entry.
func scanJobMetaFrom(job *scanner.ScanJob) *scanner.ScanJobMeta {
	return &scanner.ScanJobMeta{
		ID:          job.ID,
		ServerName:  job.ServerName,
		Status:      job.Status,
		ScanPass:    job.ScanPass,
		StartedAt:   job.StartedAt,
		CompletedAt: job.CompletedAt,
	}
}

// putScanJobIndex writes the lightweight index entry for a job within tx.
func putScanJobIndex(tx *bbolt.Tx, job *scanner.ScanJob) error {
	data, err := json.Marshal(scanJobMetaFrom(job))
	if err != nil {
		return err
	}
	return tx.Bucket([]byte(ScanJobIndexBucket)).Put([]byte(job.ID), data)
}

// backfillScanJobIndex repopulates the scan-job index from the jobs bucket when
// the index is empty (i.e. a database created before the index existed). It is
// idempotent and a no-op once the index has at least one entry. Runs inside the
// open transaction. See MCP-2205.
func backfillScanJobIndex(tx *bbolt.Tx) error {
	idx := tx.Bucket([]byte(ScanJobIndexBucket))
	if idx.Stats().KeyN > 0 {
		return nil // already populated
	}
	jobs := tx.Bucket([]byte(ScanJobsBucket))
	if jobs == nil || jobs.Stats().KeyN == 0 {
		return nil // nothing to backfill
	}
	return jobs.ForEach(func(k, v []byte) error {
		job := &scanner.ScanJob{}
		if err := job.UnmarshalBinary(v); err != nil {
			return err
		}
		data, err := json.Marshal(scanJobMetaFrom(job))
		if err != nil {
			return err
		}
		return idx.Put(k, data)
	})
}

// Scanner plugin CRUD operations

// SaveScanner saves a scanner plugin record
func (b *BoltDB) SaveScanner(s *scanner.ScannerPlugin) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ScannersBucket))
		data, err := s.MarshalBinary()
		if err != nil {
			return err
		}
		return bucket.Put([]byte(s.ID), data)
	})
}

// GetScanner retrieves a scanner plugin by ID
func (b *BoltDB) GetScanner(id string) (*scanner.ScannerPlugin, error) {
	var record *scanner.ScannerPlugin

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ScannersBucket))
		data := bucket.Get([]byte(id))
		if data == nil {
			return fmt.Errorf("scanner not found: %s", id)
		}

		record = &scanner.ScannerPlugin{}
		return record.UnmarshalBinary(data)
	})

	return record, err
}

// ListScanners returns all scanner plugin records
func (b *BoltDB) ListScanners() ([]*scanner.ScannerPlugin, error) {
	var records []*scanner.ScannerPlugin

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ScannersBucket))
		return bucket.ForEach(func(_, v []byte) error {
			record := &scanner.ScannerPlugin{}
			if err := record.UnmarshalBinary(v); err != nil {
				return err
			}
			records = append(records, record)
			return nil
		})
	})

	return records, err
}

// DeleteScanner deletes a scanner plugin by ID
func (b *BoltDB) DeleteScanner(id string) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ScannersBucket))
		return bucket.Delete([]byte(id))
	})
}

// Scan job CRUD operations

// SaveScanJob saves a scan job record
func (b *BoltDB) SaveScanJob(job *scanner.ScanJob) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ScanJobsBucket))
		data, err := job.MarshalBinary()
		if err != nil {
			return err
		}
		if err := bucket.Put([]byte(job.ID), data); err != nil {
			return err
		}
		// Keep the lightweight index in sync (MCP-2205).
		return putScanJobIndex(tx, job)
	})
}

// ListScanJobMetas returns lightweight scan-job metadata, optionally filtered by
// server name. Unlike ListScanJobs it reads from a dedicated index bucket and
// never deserializes the large per-job stdout/stderr payloads, so its cost is
// independent of scan-output size. See MCP-2205.
func (b *BoltDB) ListScanJobMetas(serverName string) ([]*scanner.ScanJobMeta, error) {
	var records []*scanner.ScanJobMeta

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ScanJobIndexBucket))
		return bucket.ForEach(func(_, v []byte) error {
			meta := &scanner.ScanJobMeta{}
			if err := json.Unmarshal(v, meta); err != nil {
				return err
			}
			if serverName != "" && meta.ServerName != serverName {
				return nil
			}
			records = append(records, meta)
			return nil
		})
	})

	return records, err
}

// dropScanJobIndexForTest deletes every entry in the scan-job index bucket.
// Used by tests to emulate a pre-index database for backfill verification.
func (b *BoltDB) dropScanJobIndexForTest() error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ScanJobIndexBucket))
		var keys [][]byte
		if err := bucket.ForEach(func(k, _ []byte) error {
			keys = append(keys, append([]byte(nil), k...))
			return nil
		}); err != nil {
			return err
		}
		for _, k := range keys {
			if err := bucket.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
}

// GetScanJob retrieves a scan job by ID
func (b *BoltDB) GetScanJob(id string) (*scanner.ScanJob, error) {
	var record *scanner.ScanJob

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ScanJobsBucket))
		data := bucket.Get([]byte(id))
		if data == nil {
			return fmt.Errorf("scan job not found: %s", id)
		}

		record = &scanner.ScanJob{}
		return record.UnmarshalBinary(data)
	})

	return record, err
}

// ListScanJobs returns scan jobs, optionally filtered by server name.
// If serverName is empty, returns all jobs.
func (b *BoltDB) ListScanJobs(serverName string) ([]*scanner.ScanJob, error) {
	var records []*scanner.ScanJob

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ScanJobsBucket))
		return bucket.ForEach(func(_, v []byte) error {
			record := &scanner.ScanJob{}
			if err := record.UnmarshalBinary(v); err != nil {
				return err
			}
			if serverName != "" && record.ServerName != serverName {
				return nil
			}
			records = append(records, record)
			return nil
		})
	})

	return records, err
}

// GetLatestScanJob returns the most recent scan job for a server
func (b *BoltDB) GetLatestScanJob(serverName string) (*scanner.ScanJob, error) {
	var latest *scanner.ScanJob

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ScanJobsBucket))
		return bucket.ForEach(func(_, v []byte) error {
			record := &scanner.ScanJob{}
			if err := record.UnmarshalBinary(v); err != nil {
				return err
			}
			if record.ServerName != serverName {
				return nil
			}
			if latest == nil || record.StartedAt.After(latest.StartedAt) {
				latest = record
			}
			return nil
		})
	})

	if err != nil {
		return nil, err
	}
	if latest == nil {
		return nil, fmt.Errorf("no scan jobs found for server: %s", serverName)
	}
	return latest, nil
}

// DeleteScanJob deletes a scan job by ID
func (b *BoltDB) DeleteScanJob(id string) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		if err := tx.Bucket([]byte(ScanJobsBucket)).Delete([]byte(id)); err != nil {
			return err
		}
		return tx.Bucket([]byte(ScanJobIndexBucket)).Delete([]byte(id))
	})
}

// DeleteServerScanJobs deletes all scan jobs for a server
func (b *BoltDB) DeleteServerScanJobs(serverName string) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ScanJobsBucket))
		idx := tx.Bucket([]byte(ScanJobIndexBucket))
		var keysToDelete [][]byte
		err := bucket.ForEach(func(k, v []byte) error {
			record := &scanner.ScanJob{}
			if err := record.UnmarshalBinary(v); err != nil {
				return err
			}
			if record.ServerName == serverName {
				keysToDelete = append(keysToDelete, append([]byte(nil), k...))
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
			if err := idx.Delete(key); err != nil {
				return err
			}
		}
		return nil
	})
}

// Scan report CRUD operations

// SaveScanReport saves a scan report record
func (b *BoltDB) SaveScanReport(report *scanner.ScanReport) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ScanReportsBucket))
		data, err := report.MarshalBinary()
		if err != nil {
			return err
		}
		return bucket.Put([]byte(report.ID), data)
	})
}

// GetScanReport retrieves a scan report by ID
func (b *BoltDB) GetScanReport(id string) (*scanner.ScanReport, error) {
	var record *scanner.ScanReport

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ScanReportsBucket))
		data := bucket.Get([]byte(id))
		if data == nil {
			return fmt.Errorf("scan report not found: %s", id)
		}

		record = &scanner.ScanReport{}
		return record.UnmarshalBinary(data)
	})

	return record, err
}

// ListScanReports returns scan reports, optionally filtered by server name.
// If serverName is empty, returns all reports.
func (b *BoltDB) ListScanReports(serverName string) ([]*scanner.ScanReport, error) {
	var records []*scanner.ScanReport

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ScanReportsBucket))
		return bucket.ForEach(func(_, v []byte) error {
			record := &scanner.ScanReport{}
			if err := record.UnmarshalBinary(v); err != nil {
				return err
			}
			if serverName != "" && record.ServerName != serverName {
				return nil
			}
			records = append(records, record)
			return nil
		})
	})

	return records, err
}

// ListScanReportsByJob returns all scan reports for a specific scan job
func (b *BoltDB) ListScanReportsByJob(jobID string) ([]*scanner.ScanReport, error) {
	var records []*scanner.ScanReport

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ScanReportsBucket))
		return bucket.ForEach(func(_, v []byte) error {
			record := &scanner.ScanReport{}
			if err := record.UnmarshalBinary(v); err != nil {
				return err
			}
			if record.JobID != jobID {
				return nil
			}
			records = append(records, record)
			return nil
		})
	})

	return records, err
}

// DeleteScanReport deletes a scan report by ID
func (b *BoltDB) DeleteScanReport(id string) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ScanReportsBucket))
		return bucket.Delete([]byte(id))
	})
}

// DeleteServerScanReports deletes all scan reports for a server
func (b *BoltDB) DeleteServerScanReports(serverName string) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ScanReportsBucket))
		var keysToDelete [][]byte
		err := bucket.ForEach(func(k, v []byte) error {
			record := &scanner.ScanReport{}
			if err := record.UnmarshalBinary(v); err != nil {
				return err
			}
			if record.ServerName == serverName {
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

// Integrity baseline CRUD operations

// SaveIntegrityBaseline saves an integrity baseline record
func (b *BoltDB) SaveIntegrityBaseline(baseline *scanner.IntegrityBaseline) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(IntegrityBaselinesBucket))
		data, err := baseline.MarshalBinary()
		if err != nil {
			return err
		}
		return bucket.Put([]byte(baseline.ServerName), data)
	})
}

// GetIntegrityBaseline retrieves an integrity baseline by server name
func (b *BoltDB) GetIntegrityBaseline(serverName string) (*scanner.IntegrityBaseline, error) {
	var record *scanner.IntegrityBaseline

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(IntegrityBaselinesBucket))
		data := bucket.Get([]byte(serverName))
		if data == nil {
			return fmt.Errorf("integrity baseline not found: %s", serverName)
		}

		record = &scanner.IntegrityBaseline{}
		return record.UnmarshalBinary(data)
	})

	return record, err
}

// DeleteIntegrityBaseline deletes an integrity baseline by server name
func (b *BoltDB) DeleteIntegrityBaseline(serverName string) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(IntegrityBaselinesBucket))
		return bucket.Delete([]byte(serverName))
	})
}

// ListIntegrityBaselines returns all integrity baseline records
func (b *BoltDB) ListIntegrityBaselines() ([]*scanner.IntegrityBaseline, error) {
	var records []*scanner.IntegrityBaseline

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(IntegrityBaselinesBucket))
		return bucket.ForEach(func(_, v []byte) error {
			record := &scanner.IntegrityBaseline{}
			if err := record.UnmarshalBinary(v); err != nil {
				return err
			}
			records = append(records, record)
			return nil
		})
	})

	return records, err
}
