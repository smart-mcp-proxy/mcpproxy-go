package storage

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strconv"
	"time"

	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 116: the storage side of Runtime.Commit. The live configuration is the
// only source of server membership and fields; the upstreams bucket is its
// projection, written by the commit in ONE bbolt transaction together with the
// per-row generation stamps and the commit generation.

const (
	// ConfigCommitGenerationKey (meta bucket) holds the generation G of the
	// last config commit, as a decimal string.
	ConfigCommitGenerationKey = "config_commit_generation"
	// QuarantineEnabledMetaKey (meta bucket) holds the global
	// quarantine_enabled value of the last commit ("true"/"false"), so
	// approval decisions resolve their policy from storage (Spec 116 D16).
	QuarantineEnabledMetaKey = "quarantine_enabled"
)

// ErrInvariant marks a commit batch that violates a Spec 116 invariant. It
// indicates a bug in the caller, never a user error.
var ErrInvariant = stderrors.New("config commit invariant violated")

// ServerSave is one row write. Stamp is the ServerGen that the commit's
// Prepare computed for this name. Storage writes it verbatim and never derives
// a stamp from the batch generation or from the row it replaces (review R9.3).
type ServerSave struct {
	Config *config.ServerConfig
	Stamp  commitview.ServerGen
}

// ServerCommitBatch is everything one commit writes to the upstreams bucket.
type ServerCommitBatch struct {
	// Generation is G; it is written to meta.config_commit_generation only.
	Generation int64
	// Saves are the rows the commit creates, changes or repairs.
	Saves []ServerSave
	// Removes are names whose row and tool-call history are deleted.
	Removes []string
	// Replaces are names removed and re-created in this commit (review
	// R8.2): the old row and history are deleted before the new row is saved.
	Replaces []string
	// QuarantineEnabled, when non-nil, is written to
	// meta.quarantine_enabled in the same transaction (D16).
	QuarantineEnabled *bool
}

// StoredServer is one upstreams row as a config entry plus its stamp.
type StoredServer struct {
	Config *config.ServerConfig
	Stamp  commitview.ServerGen
}

// ApplyServerCommit writes one commit's batch in a single bbolt Update
// transaction: deletions (Removes and Replaces) first, then the saves with
// their stamps, then the meta keys. Any invariant violation aborts the whole
// transaction and nothing changes.
func (m *Manager) ApplyServerCommit(b *ServerCommitBatch) error {
	if b == nil {
		return fmt.Errorf("%w: nil batch", ErrInvariant)
	}
	if b.Generation <= 0 {
		return fmt.Errorf("%w: generation %d", ErrInvariant, b.Generation)
	}
	deleted := make(map[string]bool, len(b.Removes)+len(b.Replaces))
	replaced := make(map[string]bool, len(b.Replaces))
	for _, n := range b.Replaces {
		replaced[n] = true
		deleted[n] = true
	}
	for _, n := range b.Removes {
		if replaced[n] {
			return fmt.Errorf("%w: %q is both removed and replaced", ErrInvariant, n)
		}
		deleted[n] = true
	}
	saved := make(map[string]bool, len(b.Saves))
	for _, s := range b.Saves {
		if s.Config == nil || s.Config.Name == "" {
			return fmt.Errorf("%w: save without a server name", ErrInvariant)
		}
		name := s.Config.Name
		if saved[name] {
			return fmt.Errorf("%w: %q is saved twice", ErrInvariant, name)
		}
		saved[name] = true
		if deleted[name] && !replaced[name] {
			return fmt.Errorf("%w: %q is both removed and saved", ErrInvariant, name)
		}
		if s.Stamp.Gen <= 0 || s.Stamp.Incarnation <= 0 {
			return fmt.Errorf("%w: %q has a zero stamp %+v", ErrInvariant, name, s.Stamp)
		}
		if s.Stamp.Gen > b.Generation || s.Stamp.Incarnation > b.Generation {
			return fmt.Errorf("%w: %q stamp %+v is above the commit generation %d", ErrInvariant, name, s.Stamp, b.Generation)
		}
	}
	for n := range replaced {
		if !saved[n] {
			return fmt.Errorf("%w: replaced %q has no new row", ErrInvariant, n)
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	return m.db.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(UpstreamsBucket))
		if bucket == nil {
			return fmt.Errorf("upstreams bucket not found")
		}
		for name := range deleted {
			if err := bucket.Delete([]byte(name)); err != nil {
				return err
			}
			if err := deleteToolCallHistoryForNameTx(tx, name); err != nil {
				return fmt.Errorf("delete tool call history for %s: %w", name, err)
			}
		}
		for _, s := range b.Saves {
			record := upstreamRecordFromConfig(s.Config)
			record.Updated = now
			record.Generation = s.Stamp.Gen
			record.Incarnation = s.Stamp.Incarnation
			if !replaced[record.ID] {
				// The SaveUpstreamKeepingQuarantine invariant. The commit's
				// admission step has already put the kept value into the
				// config, so a refusal here is a bug, not a policy decision.
				if prevData := bucket.Get([]byte(record.ID)); prevData != nil && !record.Quarantined && !s.Config.QuarantineExplicitlySet() {
					prev := &UpstreamRecord{}
					if prev.UnmarshalBinary(prevData) == nil && prev.Quarantined {
						return fmt.Errorf("%w: %q would lower a recorded quarantine without an explicit decision", ErrInvariant, record.ID)
					}
				}
			}
			data, err := record.MarshalBinary()
			if err != nil {
				return err
			}
			if err := bucket.Put([]byte(record.ID), data); err != nil {
				return err
			}
		}
		meta, err := tx.CreateBucketIfNotExists([]byte(MetaBucket))
		if err != nil {
			return err
		}
		if err := meta.Put([]byte(ConfigCommitGenerationKey), []byte(strconv.FormatInt(b.Generation, 10))); err != nil {
			return err
		}
		if b.QuarantineEnabled != nil {
			if err := meta.Put([]byte(QuarantineEnabledMetaKey), []byte(strconv.FormatBool(*b.QuarantineEnabled))); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListStoredServers returns every upstreams row with its stamp, keyed by name.
func (m *Manager) ListStoredServers() (map[string]*StoredServer, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	records, err := m.db.ListUpstreams()
	if err != nil {
		return nil, err
	}
	out := make(map[string]*StoredServer, len(records))
	for _, rec := range records {
		out[rec.Name] = &StoredServer{
			Config: serverConfigFromRecord(rec),
			Stamp:  commitview.ServerGen{Gen: rec.Generation, Incarnation: rec.Incarnation},
		}
	}
	return out, nil
}

// GetStoredServer returns one row with its stamp, or ErrUpstreamNotFound.
func (m *Manager) GetStoredServer(name string) (*StoredServer, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec, err := m.db.GetUpstream(name)
	if err != nil {
		return nil, err
	}
	return &StoredServer{
		Config: serverConfigFromRecord(rec),
		Stamp:  commitview.ServerGen{Gen: rec.Generation, Incarnation: rec.Incarnation},
	}, nil
}

// ConfigCommitState reports the generation to seed the config service with
// (the larger of meta.config_commit_generation and every row's Generation)
// and whether a Spec 116 commit has ever written this database.
func (m *Manager) ConfigCommitState() (seed int64, committed bool, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	err = m.db.db.View(func(tx *bbolt.Tx) error {
		if meta := tx.Bucket([]byte(MetaBucket)); meta != nil {
			if v := meta.Get([]byte(ConfigCommitGenerationKey)); v != nil {
				n, perr := strconv.ParseInt(string(v), 10, 64)
				if perr != nil {
					return fmt.Errorf("decode %s: %w", ConfigCommitGenerationKey, perr)
				}
				seed, committed = n, true
			}
		}
		if bucket := tx.Bucket([]byte(UpstreamsBucket)); bucket != nil {
			return bucket.ForEach(func(_, v []byte) error {
				rec := &UpstreamRecord{}
				if rec.UnmarshalBinary(v) == nil && rec.Generation > seed {
					seed = rec.Generation
				}
				return nil
			})
		}
		return nil
	})
	return seed, committed, err
}

// ConfigCommitted reports whether a Spec 116 config commit has written this
// database (meta.config_commit_generation is present). One key read; callers
// on hot paths use it instead of ConfigCommitState.
func (m *Manager) ConfigCommitted() (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	committed := false
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		if meta := tx.Bucket([]byte(MetaBucket)); meta != nil {
			committed = meta.Get([]byte(ConfigCommitGenerationKey)) != nil
		}
		return nil
	})
	return committed, err
}

// StoredQuarantineEnabled returns meta.quarantine_enabled. ok is false when no
// commit has recorded it.
func (m *Manager) StoredQuarantineEnabled() (enabled, ok bool, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	err = m.db.db.View(func(tx *bbolt.Tx) error {
		meta := tx.Bucket([]byte(MetaBucket))
		if meta == nil {
			return nil
		}
		v := meta.Get([]byte(QuarantineEnabledMetaKey))
		if v == nil {
			return nil
		}
		b, perr := strconv.ParseBool(string(v))
		if perr != nil {
			return perr
		}
		enabled, ok = b, true
		return nil
	})
	return enabled, ok, err
}

// ServerRowMatches reports whether a stored row describes the same projection
// of a server as cfg: the persisted fields compared, Created and Updated and
// the stamps ignored. Config-only fields (Shared, AuthBroker, the explicit
// quarantine bit) are not part of the projection.
func ServerRowMatches(cfg, stored *config.ServerConfig) bool {
	if cfg == nil || stored == nil {
		return cfg == stored
	}
	a := upstreamRecordFromConfig(cfg)
	b := upstreamRecordFromConfig(stored)
	for _, r := range []*UpstreamRecord{a, b} {
		r.Created = time.Time{}
		r.Updated = time.Time{}
	}
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}
