package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
// database no Spec 116 commit has written). For the tool-call and scan writers
// the unbound fallback is limited to a database no commit has written: once a
// commit exists every configured server has an incarnation, so an unbound
// write comes from a caller that looked its server up after the removal, and
// writing would recreate the removed server's history or scan evidence
// (review round 1; FR-014a, FR-014c). It is refused in the same transaction
// that would have written.

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

// writeAllowedTx reports whether a conditional write for name bound to inc
// may proceed: inc > 0 needs the row to carry it; inc == 0 (unbound) is only
// allowed while no config commit has been recorded.
func writeAllowedTx(tx *bbolt.Tx, name string, inc int64) (bool, error) {
	if inc == 0 {
		if meta := tx.Bucket([]byte(MetaBucket)); meta != nil && meta.Get([]byte(ConfigCommitGenerationKey)) != nil {
			return false, nil
		}
		return true, nil
	}
	return rowIncarnationMatchesTx(tx, name, inc)
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
	return m.recordToolCallIfIncarnation(record, inc, "", false)
}

// RecordReplayedToolCallIfIncarnation is RecordToolCallIfIncarnation for an
// activity replay (Spec 116 PR2 review rounds 5-6, FR-014c). The replayed call
// carries the identity of the ORIGINAL call, read from that identity's history
// bucket before the incarnation was captured. A removal purges that identity
// and its history; a same-name replacement then carries a new incarnation the
// replay may have captured. The identity alone does not prove the call still
// belongs to the server: GenerateServerID hashes the configuration and not the
// incarnation, so a same-config replacement registers the SAME identity id
// (review round 6). The write is therefore conditional, in its own
// transaction, on the original call record originalCallID itself still being
// in that identity's history bucket. A removal purged it, and nothing
// recreates it, so a replay never records a removed server's call under the
// replacement nor recreates a purged identity or its history.
func (m *Manager) RecordReplayedToolCallIfIncarnation(record *ToolCallRecord, inc int64, originalCallID string) (bool, error) {
	return m.recordToolCallIfIncarnation(record, inc, originalCallID, true)
}

// ToolCallRecordExists reports whether the history bucket of server identity
// serverID still holds the call callID (Spec 116 PR2 review round 6). Replay
// checks it before dispatch: a removal purged the bucket, and a same-config
// replacement's bucket under the same identity id never holds the removed
// server's calls.
func (m *Manager) ToolCallRecordExists(serverID, callID string) (bool, error) {
	found := false
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		var err error
		found, err = toolCallRecordExistsTx(tx, serverID, callID)
		return err
	})
	return found, err
}

// toolCallRecordExistsTx reports whether serverID's history bucket holds a
// record of callID. Keys are "<unix-nano>_<id>"; the bucket is bounded by
// tool_call_max_records, so the scan is bounded too.
func toolCallRecordExistsTx(tx *bbolt.Tx, serverID, callID string) (bool, error) {
	if serverID == "" || callID == "" {
		return false, nil
	}
	b := tx.Bucket([]byte(toolCallsBucketName(serverID)))
	if b == nil {
		return false, nil
	}
	suffix := []byte("_" + callID)
	c := b.Cursor()
	for k, _ := c.First(); k != nil; k, _ = c.Next() {
		if bytes.HasSuffix(k, suffix) {
			return true, nil
		}
	}
	return false, nil
}

func (m *Manager) recordToolCallIfIncarnation(record *ToolCallRecord, inc int64, originalCallID string, replay bool) (bool, error) {
	if record == nil || record.ServerID == codeExecutionServerID {
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
		ok, err := writeAllowedTx(tx, record.ServerName, inc)
		if err != nil || !ok {
			return err
		}
		if replay {
			present, err := identityNamesServerTx(tx, record.ServerID, record.ServerName)
			if err != nil || !present {
				return err
			}
			present, err = toolCallRecordExistsTx(tx, record.ServerID, originalCallID)
			if err != nil || !present {
				return err
			}
		}
		// The history bucket is keyed by the server identity hash, and the
		// removal purge finds such buckets through server_identities. A call
		// can finish before the connect worker registered the identity
		// (review round 2), so the identity is ensured in this transaction:
		// no history bucket ever exists without an identity naming its server.
		if err := ensureCallIdentityTx(tx, record.ServerName, record.ServerID); err != nil {
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

// ensureCallIdentityTx writes a server identity for serverID naming
// serverName unless one exists. Its attributes come from the stored row when
// the row's configuration hashes to serverID; otherwise it carries only the
// id and the name (enough for the purge and the orphan sweep).
func ensureCallIdentityTx(tx *bbolt.Tx, serverName, serverID string) error {
	if serverID == "" {
		return nil
	}
	ids, err := tx.CreateBucketIfNotExists([]byte(serverIdentitiesBucket))
	if err != nil {
		return err
	}
	if ids.Get([]byte(serverID)) != nil {
		return nil
	}
	var identity *ServerIdentity
	if b := tx.Bucket([]byte(UpstreamsBucket)); b != nil {
		if data := b.Get([]byte(serverName)); data != nil {
			rec := &UpstreamRecord{}
			if rec.UnmarshalBinary(data) == nil {
				if cfg := serverConfigFromRecord(rec); cfg != nil && GenerateServerID(cfg) == serverID {
					identity = NewServerIdentity(cfg, "")
					identity.ConfigPaths = nil
				}
			}
		}
	}
	if identity == nil {
		now := time.Now()
		fp := serverID
		if len(fp) > 12 {
			fp = fp[:12]
		}
		identity = &ServerIdentity{ID: serverID, ServerName: serverName, Fingerprint: fp,
			Attributes: ServerAttributes{Name: serverName}, FirstSeen: now, LastSeen: now,
			Metadata: map[string]string{}}
	}
	data, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	return ids.Put([]byte(serverID), data)
}

// SaveScanJobIfIncarnation saves job only while the scanned server's row
// carries incarnation inc (FR-014a): a scan that outlives a remove and re-add
// leaves no job under the replacement.
func (m *Manager) SaveScanJobIfIncarnation(job *scanner.ScanJob, inc int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	written := false
	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		ok, err := writeAllowedTx(tx, job.ServerName, inc)
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
	m.mu.Lock()
	defer m.mu.Unlock()
	written := false
	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		ok, err := writeAllowedTx(tx, report.ServerName, inc)
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

// identityNamesServerTx reports whether the server identity serverID exists
// and names serverName.
func identityNamesServerTx(tx *bbolt.Tx, serverID, serverName string) (bool, error) {
	if serverID == "" {
		return false, nil
	}
	ids := tx.Bucket([]byte(serverIdentitiesBucket))
	if ids == nil {
		return false, nil
	}
	data := ids.Get([]byte(serverID))
	if data == nil {
		return false, nil
	}
	identity := &ServerIdentity{}
	if err := json.Unmarshal(data, identity); err != nil {
		return false, fmt.Errorf("decode server identity %s: %w", serverID, err)
	}
	return identity.ServerName == serverName, nil
}
