package storage

import (
	"fmt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"go.etcd.io/bbolt"
)

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
		return bucket.Put([]byte(job.ID), data)
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
		bucket := tx.Bucket([]byte(ScanJobsBucket))
		return bucket.Delete([]byte(id))
	})
}

// DeleteServerScanJobs deletes all scan jobs for a server
func (b *BoltDB) DeleteServerScanJobs(serverName string) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ScanJobsBucket))
		var keysToDelete [][]byte
		err := bucket.ForEach(func(k, v []byte) error {
			record := &scanner.ScanJob{}
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
