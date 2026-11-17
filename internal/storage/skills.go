package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.etcd.io/bbolt"
)

// Spec 125 (skills over MCP) storage: two server-scoped buckets.
//
// Key safety: both keys are "server:<rest>". Server names cannot contain ':'
// (config validation, see purgeServerTx), so a "server:" prefix is exact for
// valid names. These buckets do not rely on that alone:
//   - skill_approvals: after the prefix scan the record is decoded and its
//     ServerName compared exactly (the skill URI itself contains ':').
//   - skill_files: the rest of the key must be exactly 64 hex digits, so
//     "a:b:<hex>" never belongs to server "a".

// Skill statuses (spec 125).
const (
	SkillStatusPending  = "pending"
	SkillStatusApproved = "approved"
	SkillStatusChanged  = "changed"
	SkillStatusUnusable = "unusable"
)

var (
	// ErrSkillApprovalNotFound is returned by GetSkillApproval when no record exists.
	ErrSkillApprovalNotFound = errors.New("skill approval not found")
	// ErrSkillFileNotFound is returned by GetSkillFile when no blob exists.
	ErrSkillFileNotFound = errors.New("skill file not found")
)

// SkillFile is one file of a skill as listed by the upstream. Only Text files
// with Stored set have a row in the skill_files bucket.
type SkillFile struct {
	RelPath string `json:"rel_path"`
	URI     string `json:"uri"`
	Digest  string `json:"digest"` // "sha256:" + 64 hex
	Size    int64  `json:"size"`
	Text    bool   `json:"text"`
	Stored  bool   `json:"stored"`
}

// SkillContent is the reviewable content of a skill: frontmatter and files.
type SkillContent struct {
	Frontmatter json.RawMessage `json:"frontmatter,omitempty"`
	Files       []SkillFile     `json:"files,omitempty"`
}

// SkillScanFinding is one scanner finding on a skill file.
type SkillScanFinding struct {
	File     string `json:"file"`
	Offset   int64  `json:"offset"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Evidence string `json:"evidence"`
}

// SkillScan is the scan result recorded for a skill's current content.
type SkillScan struct {
	Verdict  string             `json:"verdict"`
	Complete bool               `json:"complete"`
	Findings []SkillScanFinding `json:"findings,omitempty"`
}

// SkillApprovalRecord is the per-(server, skill URI) review record.
type SkillApprovalRecord struct {
	ServerName        string        `json:"server_name"`
	SkillURI          string        `json:"skill_uri"`
	SkillPath         string        `json:"skill_path"`
	Name              string        `json:"name"`
	Description       string        `json:"description"`
	Status            string        `json:"status"`
	Blocked           bool          `json:"blocked,omitempty"`
	Listed            bool          `json:"listed"`
	UnusableReason    string        `json:"unusable_reason,omitempty"`
	Current           SkillContent  `json:"current"`
	Approved          *SkillContent `json:"approved,omitempty"`
	CurrentHash       string        `json:"current_hash"`
	ApprovedHash      string        `json:"approved_hash,omitempty"`
	HashSchemaVersion int           `json:"hash_schema_version"`
	Incarnation       int64         `json:"incarnation"`
	ApprovedAt        time.Time     `json:"approved_at"`
	ApprovedBy        string        `json:"approved_by,omitempty"`
	Scan              *SkillScan    `json:"scan,omitempty"`
	FirstSeen         time.Time     `json:"first_seen"`
	UpdatedAt         time.Time     `json:"updated_at"`
	CaptureSeq        uint64        `json:"capture_seq"`
}

// SkillApprovalKey returns the skill_approvals key: server + ":" + skill URI.
func SkillApprovalKey(serverName, skillURI string) string {
	return serverName + ":" + skillURI
}

// Key returns the storage key for this record.
func (r *SkillApprovalRecord) Key() string { return SkillApprovalKey(r.ServerName, r.SkillURI) }

// MarshalBinary implements encoding.BinaryMarshaler.
func (r *SkillApprovalRecord) MarshalBinary() ([]byte, error) { return json.Marshal(r) }

// UnmarshalBinary implements encoding.BinaryUnmarshaler.
func (r *SkillApprovalRecord) UnmarshalBinary(data []byte) error { return json.Unmarshal(data, r) }

// NormalizeSkillDigest accepts a digest with or without the "sha256:" prefix
// and returns the 64 lower-case hex characters.
func NormalizeSkillDigest(digest string) (string, error) {
	h := strings.TrimPrefix(digest, "sha256:")
	if !isSkillDigestHex(h) {
		return "", fmt.Errorf("invalid skill file digest %q", digest)
	}
	return h, nil
}

func isSkillDigestHex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// SkillFileKey returns the skill_files key: server + ":" + 64 hex digits.
func SkillFileKey(serverName, digest string) (string, error) {
	h, err := NormalizeSkillDigest(digest)
	if err != nil {
		return "", err
	}
	return serverName + ":" + h, nil
}

// skillFileKeyDigest returns the digest of a skill_files key when it belongs
// to exactly this server (see the key-safety note above).
func skillFileKeyDigest(key []byte, serverName string) (string, bool) {
	rest, ok := bytes.CutPrefix(key, []byte(serverName+":"))
	if !ok || !isSkillDigestHex(string(rest)) {
		return "", false
	}
	return string(rest), true
}

// skillRecordBelongsTo reports whether the skill_approvals row (key, value)
// is a record of exactly this server.
func skillRecordBelongsTo(k, v []byte, serverName string) bool {
	if !bytes.HasPrefix(k, []byte(serverName+":")) {
		return false
	}
	var r struct {
		ServerName string `json:"server_name"`
	}
	return json.Unmarshal(v, &r) == nil && r.ServerName == serverName
}

// --- skill approvals ---

// SaveSkillApproval upserts a skill approval record.
func (b *BoltDB) SaveSkillApproval(record *SkillApprovalRecord) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		return putSkillApprovalTx(tx, record)
	})
}

func putSkillApprovalTx(tx *bbolt.Tx, record *SkillApprovalRecord) error {
	data, err := record.MarshalBinary()
	if err != nil {
		return err
	}
	return tx.Bucket([]byte(SkillApprovalBucket)).Put([]byte(record.Key()), data)
}

// GetSkillApproval returns the record, or ErrSkillApprovalNotFound (wrapped).
func (b *BoltDB) GetSkillApproval(serverName, skillURI string) (*SkillApprovalRecord, error) {
	var record *SkillApprovalRecord
	err := b.db.View(func(tx *bbolt.Tx) error {
		key := SkillApprovalKey(serverName, skillURI)
		data := tx.Bucket([]byte(SkillApprovalBucket)).Get([]byte(key))
		if data == nil {
			return fmt.Errorf("%w: %s", ErrSkillApprovalNotFound, key)
		}
		record = &SkillApprovalRecord{}
		return record.UnmarshalBinary(data)
	})
	return record, err
}

// ListSkillApprovals returns the records of one server, or of all servers
// when serverName is empty.
func (b *BoltDB) ListSkillApprovals(serverName string) ([]*SkillApprovalRecord, error) {
	var records []*SkillApprovalRecord
	err := b.db.View(func(tx *bbolt.Tx) error {
		var err error
		records, err = listSkillApprovalsTx(tx, serverName)
		return err
	})
	return records, err
}

func listSkillApprovalsTx(tx *bbolt.Tx, serverName string) ([]*SkillApprovalRecord, error) {
	var records []*SkillApprovalRecord
	err := tx.Bucket([]byte(SkillApprovalBucket)).ForEach(func(k, v []byte) error {
		if serverName != "" && !bytes.HasPrefix(k, []byte(serverName+":")) {
			return nil
		}
		r := &SkillApprovalRecord{}
		if err := r.UnmarshalBinary(v); err != nil {
			return err
		}
		if serverName != "" && r.ServerName != serverName {
			return nil
		}
		records = append(records, r)
		return nil
	})
	return records, err
}

// DeleteSkillApproval deletes one record (absent is not an error).
func (b *BoltDB) DeleteSkillApproval(serverName, skillURI string) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(SkillApprovalBucket)).Delete([]byte(SkillApprovalKey(serverName, skillURI)))
	})
}

// --- skill files ---

// PutSkillFile stores one text file under its digest for the server.
func (b *BoltDB) PutSkillFile(serverName, digest string, content []byte) error {
	key, err := SkillFileKey(serverName, digest)
	if err != nil {
		return err
	}
	return b.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(SkillFileBucket)).Put([]byte(key), content)
	})
}

// GetSkillFile returns a copy of the stored bytes, or ErrSkillFileNotFound.
func (b *BoltDB) GetSkillFile(serverName, digest string) ([]byte, error) {
	key, err := SkillFileKey(serverName, digest)
	if err != nil {
		return nil, err
	}
	var out []byte
	err = b.db.View(func(tx *bbolt.Tx) error {
		v := tx.Bucket([]byte(SkillFileBucket)).Get([]byte(key))
		if v == nil {
			return fmt.Errorf("%w: %s", ErrSkillFileNotFound, key)
		}
		// bbolt values are only valid inside the transaction.
		out = append([]byte(nil), v...)
		return nil
	})
	return out, err
}

// HasSkillFile reports whether the server has a stored blob for the digest.
func (b *BoltDB) HasSkillFile(serverName, digest string) (bool, error) {
	key, err := SkillFileKey(serverName, digest)
	if err != nil {
		return false, err
	}
	var ok bool
	err = b.db.View(func(tx *bbolt.Tx) error {
		ok = tx.Bucket([]byte(SkillFileBucket)).Get([]byte(key)) != nil
		return nil
	})
	return ok, err
}

// ListSkillFileDigests returns digest hex -> size for the server's blobs.
func (b *BoltDB) ListSkillFileDigests(serverName string) (map[string]int64, error) {
	out := map[string]int64{}
	err := b.db.View(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(SkillFileBucket)).ForEach(func(k, v []byte) error {
			if d, ok := skillFileKeyDigest(k, serverName); ok {
				out[d] = int64(len(v))
			}
			return nil
		})
	})
	return out, err
}

// skillReferencedDigests collects the digests of stored text files referenced
// by Current or Approved of any record of the server.
func skillReferencedDigests(tx *bbolt.Tx, serverName string) (map[string]bool, error) {
	records, err := listSkillApprovalsTx(tx, serverName)
	if err != nil {
		return nil, err
	}
	refs := map[string]bool{}
	add := func(files []SkillFile) {
		for _, f := range files {
			if !f.Text || !f.Stored {
				continue
			}
			if h, err := NormalizeSkillDigest(f.Digest); err == nil {
				refs[h] = true
			}
		}
	}
	for _, r := range records {
		add(r.Current.Files)
		if r.Approved != nil {
			add(r.Approved.Files)
		}
	}
	return refs, nil
}

// collectSkillFilesTx deletes the server's unreferenced blobs and returns the
// count removed and the bytes still stored.
func collectSkillFilesTx(tx *bbolt.Tx, serverName string) (int, int64, error) {
	refs, err := skillReferencedDigests(tx, serverName)
	if err != nil {
		return 0, 0, err
	}
	bucket := tx.Bucket([]byte(SkillFileBucket))
	var drop [][]byte
	var retained int64
	err = bucket.ForEach(func(k, v []byte) error {
		d, ok := skillFileKeyDigest(k, serverName)
		if !ok {
			return nil
		}
		if refs[d] {
			retained += int64(len(v))
		} else {
			drop = append(drop, append([]byte(nil), k...))
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	for _, k := range drop {
		if err := bucket.Delete(k); err != nil {
			return 0, 0, err
		}
	}
	return len(drop), retained, nil
}

// CollectUnreferencedSkillFiles deletes the server's blobs no record
// references (leftovers of staged but never applied refreshes) and returns
// how many were removed and the bytes still stored.
func (b *BoltDB) CollectUnreferencedSkillFiles(serverName string) (int, int64, error) {
	var removed int
	var retained int64
	err := b.db.Update(func(tx *bbolt.Tx) error {
		var err error
		removed, retained, err = collectSkillFilesTx(tx, serverName)
		return err
	})
	return removed, retained, err
}

// ApplySkillRefresh applies one refresh in a single transaction: write the new
// blobs (digest hex -> bytes), upsert records, mark unlistedURIs as not listed
// (unknown URIs are ignored), then delete the server's unreferenced blobs.
func (b *BoltDB) ApplySkillRefresh(serverName string, upserts []*SkillApprovalRecord, unlistedURIs []string, newFiles map[string][]byte) (int, error) {
	var removed int
	err := b.db.Update(func(tx *bbolt.Tx) error {
		files := tx.Bucket([]byte(SkillFileBucket))
		for digest, content := range newFiles {
			key, err := SkillFileKey(serverName, digest)
			if err != nil {
				return err
			}
			if err := files.Put([]byte(key), content); err != nil {
				return err
			}
		}
		for _, r := range upserts {
			if r.ServerName != serverName {
				return fmt.Errorf("skill record %q belongs to server %q, not %q", r.SkillURI, r.ServerName, serverName)
			}
			if err := putSkillApprovalTx(tx, r); err != nil {
				return err
			}
		}
		approvals := tx.Bucket([]byte(SkillApprovalBucket))
		for _, uri := range unlistedURIs {
			key := []byte(SkillApprovalKey(serverName, uri))
			data := approvals.Get(key)
			if data == nil {
				continue
			}
			r := &SkillApprovalRecord{}
			if err := r.UnmarshalBinary(data); err != nil {
				return err
			}
			r.Listed = false
			if err := putSkillApprovalTx(tx, r); err != nil {
				return err
			}
		}
		var err error
		removed, _, err = collectSkillFilesTx(tx, serverName)
		return err
	})
	return removed, err
}

// --- Manager mirrors ---

// SaveSkillApproval upserts a skill approval record.
func (m *Manager) SaveSkillApproval(record *SkillApprovalRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.db.SaveSkillApproval(record)
}

// GetSkillApproval retrieves a skill approval record.
func (m *Manager) GetSkillApproval(serverName, skillURI string) (*SkillApprovalRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.db.GetSkillApproval(serverName, skillURI)
}

// ListSkillApprovals returns a server's records (all when empty).
func (m *Manager) ListSkillApprovals(serverName string) ([]*SkillApprovalRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.db.ListSkillApprovals(serverName)
}

// DeleteSkillApproval deletes one record.
func (m *Manager) DeleteSkillApproval(serverName, skillURI string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.db.DeleteSkillApproval(serverName, skillURI)
}

// PutSkillFile stores one text file blob.
func (m *Manager) PutSkillFile(serverName, digest string, content []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.db.PutSkillFile(serverName, digest, content)
}

// GetSkillFile returns a copy of a stored blob.
func (m *Manager) GetSkillFile(serverName, digest string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.db.GetSkillFile(serverName, digest)
}

// HasSkillFile reports whether a blob is stored.
func (m *Manager) HasSkillFile(serverName, digest string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.db.HasSkillFile(serverName, digest)
}

// ListSkillFileDigests returns digest hex -> size for a server.
func (m *Manager) ListSkillFileDigests(serverName string) (map[string]int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.db.ListSkillFileDigests(serverName)
}

// CollectUnreferencedSkillFiles removes unreferenced blobs of a server.
func (m *Manager) CollectUnreferencedSkillFiles(serverName string) (int, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.db.CollectUnreferencedSkillFiles(serverName)
}

// ApplySkillRefresh applies a skill refresh in one transaction.
func (m *Manager) ApplySkillRefresh(serverName string, upserts []*SkillApprovalRecord, unlistedURIs []string, newFiles map[string][]byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.db.ApplySkillRefresh(serverName, upserts, unlistedURIs, newFiles)
}
