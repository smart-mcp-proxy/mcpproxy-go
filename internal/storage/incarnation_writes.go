package storage

import (
	"encoding/json"
	"errors"
	"fmt"

	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// Spec 116 PR2 (research D12, FR-014a, FR-014c): incarnation-conditional
// writes of purge-set state by asynchronous writers that can outlive a
// removal — a connect worker registering the server identity, a tool call
// recording its history, a scan reporting its result.
//
// Each method runs ONE bbolt Update that reads the upstreams row and writes
// only when it exists and carries the expected incarnation. The removal purge
// deletes the row and the whole purge set in one transaction as well, so the
// two serialize in bbolt: either the write committed first (and the purge
// deleted it) or it sees the row missing or re-incarnated and writes nothing.
// No lock beyond bbolt's own is taken, so this adds no lock-order edge.
//
// inc == 0 means "unbound" (a caller that has no committed view, or a
// database no Spec 116 commit has written): the write is unconditional, as
// before.

// rowIncarnationMatchesTx reports whether upstreams[name] exists and carries
// incarnation inc. A row without a stamp (written before Spec 116, or by a
// fixture that bypasses the commit) is not authoritative and matches: every
// row a commit writes is stamped (ApplyServerCommit refuses a zero stamp).
func rowIncarnationMatchesTx(tx *bbolt.Tx, name string, inc int64) (bool, error) {
	b := tx.Bucket([]byte(UpstreamsBucket))
	if b == nil {
		return false, nil
	}
	data := b.Get([]byte(name))
	if data == nil {
		return false, nil
	}
	rec := &UpstreamRecord{}
	if err := rec.UnmarshalBinary(data); err != nil {
		return false, fmt.Errorf("decode upstream %s: %w", name, err)
	}
	return rec.Incarnation == 0 || rec.Incarnation == inc, nil
}

// RegisterServerIdentityIfIncarnation is RegisterServerIdentity conditional on
// the server's row carrying incarnation inc. written is false (and nothing is
// stored) when the server was removed or replaced.
func (m *Manager) RegisterServerIdentityIfIncarnation(server *config.ServerConfig, configPath string, inc int64) (identity *ServerIdentity, written bool, err error) {
	if inc == 0 {
		identity, err = m.RegisterServerIdentity(server, configPath)
		return identity, err == nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	serverID := GenerateServerID(server)
	err = m.db.db.Update(func(tx *bbolt.Tx) error {
		ok, err := rowIncarnationMatchesTx(tx, server.Name, inc)
		if err != nil || !ok {
			return err
		}
		bucket, err := tx.CreateBucketIfNotExists([]byte(serverIdentitiesBucket))
		if err != nil {
			return err
		}
		if data := bucket.Get([]byte(serverID)); data != nil {
			existing := &ServerIdentity{}
			if err := json.Unmarshal(data, existing); err != nil {
				return fmt.Errorf("decode server identity %s: %w", serverID, err)
			}
			existing.UpdateLastSeen(configPath)
			identity = existing
		} else {
			identity = NewServerIdentity(server, configPath)
		}
		data, err := json.Marshal(identity)
		if err != nil {
			return err
		}
		if err := bucket.Put([]byte(identity.ID), data); err != nil {
			return err
		}
		written = true
		return nil
	})
	if err != nil || !written {
		return nil, false, err
	}
	return identity, true, nil
}

// RecordToolCallIfIncarnation is RecordToolCall conditional on the row of
// record.ServerName carrying incarnation inc (code_execution parent records
// are exempt: they belong to no upstream and are recorded unconditionally).
func (m *Manager) RecordToolCallIfIncarnation(record *ToolCallRecord, inc int64) (bool, error) {
	if inc == 0 || record == nil || record.ServerID == codeExecutionServerID {
		if err := m.RecordToolCall(record); err != nil {
			return false, err
		}
		return true, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	maxResponseBytes, maxRecords := m.toolCallLimits()
	bounded := boundToolCallResponse(record, maxResponseBytes)
	key := fmt.Sprintf("%d_%s", record.Timestamp.UnixNano(), record.ID)
	written := false
	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		ok, err := rowIncarnationMatchesTx(tx, record.ServerName, inc)
		if err != nil || !ok {
			return err
		}
		bucket, err := tx.CreateBucketIfNotExists([]byte(toolCallsBucketName(record.ServerID)))
		if err != nil {
			return err
		}
		data, err := json.Marshal(bounded)
		if err != nil {
			return err
		}
		if err := bucket.Put([]byte(key), data); err != nil {
			return err
		}
		written = true
		return pruneToolCallBucket(bucket, maxRecords)
	})
	return written && err == nil, err
}

// SaveScanJobIfIncarnation saves job only while the scanned server's row
// carries incarnation inc (FR-014a): a scan that outlives a remove and re-add
// leaves no job under the replacement.
func (m *Manager) SaveScanJobIfIncarnation(job *scanner.ScanJob, inc int64) (bool, error) {
	if inc == 0 {
		return true, m.SaveScanJob(job)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	written := false
	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		ok, err := rowIncarnationMatchesTx(tx, job.ServerName, inc)
		if err != nil || !ok {
			return err
		}
		data, err := job.MarshalBinary()
		if err != nil {
			return err
		}
		if err := tx.Bucket([]byte(ScanJobsBucket)).Put([]byte(job.ID), data); err != nil {
			return err
		}
		if err := putScanJobIndex(tx, job); err != nil {
			return err
		}
		written = true
		return nil
	})
	return written && err == nil, err
}

// SaveScanReportIfIncarnation saves report only while the scanned server's row
// carries incarnation inc (FR-014a).
func (m *Manager) SaveScanReportIfIncarnation(report *scanner.ScanReport, inc int64) (bool, error) {
	if inc == 0 {
		return true, m.SaveScanReport(report)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	written := false
	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		ok, err := rowIncarnationMatchesTx(tx, report.ServerName, inc)
		if err != nil || !ok {
			return err
		}
		data, err := report.MarshalBinary()
		if err != nil {
			return err
		}
		if err := tx.Bucket([]byte(ScanReportsBucket)).Put([]byte(report.ID), data); err != nil {
			return err
		}
		written = true
		return nil
	})
	return written && err == nil, err
}

// ServerIncarnation returns the incarnation stamped on name's row. ok is false
// when no row exists.
func (m *Manager) ServerIncarnation(name string) (inc int64, ok bool, err error) {
	row, err := m.GetStoredServer(name)
	if err != nil {
		if errors.Is(err, ErrUpstreamNotFound) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return row.Stamp.Incarnation, true, nil
}
