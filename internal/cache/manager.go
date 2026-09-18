package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.etcd.io/bbolt"
	"go.uber.org/zap"
)

const (
	CacheBucket      = "cache"
	CacheStatsBucket = "cache_stats"
	// CacheSnapshotBucket holds each distinct producer authorization snapshot
	// ONCE, keyed by the digest its records' frame headers carry, for
	// ADMINISTRATOR DIAGNOSTICS only: no read path consults it — the gate
	// decides on the header alone (research D16). Written in the same
	// transaction as the record that first references it; unreferenced
	// snapshots are dropped by the cleanup sweep.
	CacheSnapshotBucket = "cache_snapshots"
	DefaultTTL          = 2 * time.Hour
	CleanupInterval     = 10 * time.Minute
)

// Read outcomes a caller can act on with errors.Is. The messages are part of
// the agent-facing contract: the read_cache banner and its consumers key on
// "cache key not found", and the non-disclosing refusal for scoped callers
// (Spec 105 FR-001) reuses ErrKeyNotFound verbatim.
var (
	// ErrKeyNotFound: no entry under the key (or nothing left after an
	// invalidation).
	ErrKeyNotFound = errors.New("cache key not found")
	// ErrKeyExpired: the entry had passed its TTL. The ungated Get evicts it;
	// the gated read refuses it like a miss and leaves it to the cleanup
	// sweep (see getGuarded).
	ErrKeyExpired = errors.New("cache key expired")
)

// Manager handles cached tool responses
type Manager struct {
	db     *bbolt.DB
	logger *zap.Logger
	stats  *Stats
	stopCh chan struct{}
	// writeMu serialises update: the in-memory stats snapshot it takes must
	// be the state the transaction started from, and bbolt's own writer lock
	// is acquired inside db.Update, after that snapshot.
	writeMu sync.Mutex
	// dbUpdate runs a write transaction; db.Update in production. It is a
	// seam so a test can make the COMMIT fail after the closure succeeded
	// (disk full at fsync), a fault no in-process bbolt setup produces.
	dbUpdate func(fn func(tx *bbolt.Tx) error) error
}

// NewManager creates a new cache manager
func NewManager(db *bbolt.DB, logger *zap.Logger) (*Manager, error) {
	manager := &Manager{
		db:       db,
		logger:   logger,
		stats:    &Stats{},
		stopCh:   make(chan struct{}),
		dbUpdate: db.Update,
	}

	// Initialize buckets
	err := db.Update(func(tx *bbolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists([]byte(CacheBucket)); err != nil {
			return fmt.Errorf("create cache bucket: %w", err)
		}
		if _, err := tx.CreateBucketIfNotExists([]byte(CacheStatsBucket)); err != nil {
			return fmt.Errorf("create cache stats bucket: %w", err)
		}
		if _, err := tx.CreateBucketIfNotExists([]byte(CacheSnapshotBucket)); err != nil {
			return fmt.Errorf("create cache snapshots bucket: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Load existing stats
	if err := manager.loadStats(); err != nil {
		logger.Warn("Failed to load cache stats", zap.Error(err))
	}

	// Start background cleanup
	go manager.startCleanup()

	return manager, nil
}

// GenerateKey generates a cache key from tool name, arguments, and timestamp.
//
// The timestamp is mixed in at nanosecond granularity. The truncator calls
// this with time.Now() once per truncated payload; a single upstream result
// can carry multiple oversized TextContent blocks, and recursive read_cache
// truncation can mint several keys in quick succession. At second granularity
// any of those that landed in the same wall-clock second collided on one key,
// so a later Store silently overwrote an earlier payload and the earlier
// banner resolved to the wrong data. GenerateKey is a PURE function of its
// inputs (asserted by TestGenerateKey); callers that need per-call uniqueness
// must supply distinct timestamps — see NextUniqueTimestamp.
func GenerateKey(toolName string, args map[string]interface{}, timestamp time.Time) string {
	// Create a consistent string representation
	argsJSON, _ := json.Marshal(args)
	input := fmt.Sprintf("%s:%s:%d", toolName, string(argsJSON), timestamp.UnixNano())

	hash := sha256.Sum256([]byte(input))
	return hex.EncodeToString(hash[:])
}

// NextUniqueTimestamp returns a strictly increasing timestamp for cache-key
// generation. time.Now() alone is not collision-safe: Windows wall-clock
// resolution is ~0.5-15.6ms, so two truncated blocks in the same tick got
// identical UnixNano readings and therefore identical keys (flaked
// TestForwardContentResult_MultipleTextBlocksDistinctKeys on windows-latest).
// A process-wide atomic high-water mark bumps same-tick readings by 1ns each,
// preserving GenerateKey's purity while guaranteeing distinct inputs.
func NextUniqueTimestamp() time.Time {
	for {
		now := time.Now().UnixNano()
		last := lastKeyNano.Load()
		if now <= last {
			now = last + 1
		}
		if lastKeyNano.CompareAndSwap(last, now) {
			return time.Unix(0, now)
		}
	}
}

var lastKeyNano atomic.Int64

// Store saves a tool response to cache with no producer authorization. Such an
// entry has legacy provenance (Spec 105 FR-002): the gated read refuses it for
// every caller and invalidates it. It exists for the ungated readers and for
// tests that seed pre-feature records; production callers stamp the producer
// via StoreAs.
func (m *Manager) Store(key, toolName string, args map[string]interface{}, content, recordPath string, totalRecords int) error {
	return m.storeRecord(key, toolName, args, content, recordPath, totalRecords, nil)
}

// StoreAs saves a tool response to cache stamped with the authorization it was
// produced under and the current RecordVersion. GetRecordsAs refuses readers
// that could not have produced it.
func (m *Manager) StoreAs(key, toolName string, args map[string]interface{}, content, recordPath string, totalRecords int, producer Authorization) error {
	return m.storeRecord(key, toolName, args, content, recordPath, totalRecords, &producer)
}

func (m *Manager) storeRecord(key, toolName string, args map[string]interface{}, content, recordPath string, totalRecords int, producer *Authorization) error {
	record := &Record{
		Producer:     producer,
		Version:      RecordVersion,
		Key:          key,
		ToolName:     toolName,
		Args:         args,
		Timestamp:    time.Now(),
		FullContent:  content,
		RecordPath:   recordPath,
		TotalRecords: totalRecords,
		TotalSize:    len(content),
		ExpiresAt:    time.Now().Add(DefaultTTL),
		AccessCount:  0,
		LastAccessed: time.Now(),
		CreatedAt:    time.Now(),
	}

	return m.update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(CacheBucket))
		data, snapshot, err := record.marshalFrame()
		if err != nil {
			return fmt.Errorf("marshal cache record: %w", err)
		}
		// The diagnostic snapshot is persisted in THIS transaction, once per
		// distinct producer digest, so the snapshots bucket never describes
		// an entry the cache bucket does not hold.
		if producer != nil {
			if err := m.putSnapshot(tx, producer.digest(), snapshot); err != nil {
				return err
			}
		}

		if err := bucket.Put([]byte(key), data); err != nil {
			return fmt.Errorf("store cache record: %w", err)
		}

		// Update stats
		m.stats.TotalEntries++
		m.stats.TotalSizeBytes += len(content)

		return m.saveStats(tx)
	})
}

// Get retrieves a cached tool response without a read gate. It is the read
// path of the proxy's own writers (the repository guesser); credentialed
// requests go through GetRecordsAs.
func (m *Manager) Get(key string) (*Record, error) {
	return m.getGuarded(key, nil)
}

// getGuarded is Get with an optional read gate. A non-nil guard marks the
// GATED door (read_cache). On that door EVERY verdict is decided on the
// record's FIXED-SIZE FRAME HEADER (decodeRecordHeader: version, caller kind,
// deny-all bit, tier bits, expiry, size, producer digest — 52 bytes) and
// never on the payload nor on the producer snapshot: a refusal that decoded
// a multi-megabyte FullContent, or a snapshot naming thousands of servers,
// first would take a timing class a nonexistent key does not, and the spec's
// non-disclosing refusal is indistinguishable in status, body AND timing
// class (Spec 105 Definitions; codex rounds 2, 4 and 5; research D16). The
// order is: provenance class first (Spec 105 FR-002) — a value with no
// frame, a frame this binary cannot decode, or a header with legacy or
// unrecognised provenance is refused for every caller and invalidated; then
// an internal entry is refused for every caller WITHOUT eviction, even when
// it has expired (its writers' ungated readers serve expired entries as
// stale until cleanup, and a guessable key must not let a probe evict them
// early); then an expired entry is refused like a miss and left for the
// cleanup sweep; then the guard runs on the header's facts (admits). Only an
// admitted read decodes the record — and only then are the access stats
// updated, so a refused read never counts as a hit or marks the entry as
// accessed.
//
// An admitted read whose body then proves undecodable, or disagrees with the
// header it was admitted on, is invalidated too — but reported as
// ErrEntryUnreadable, an admitted-class outcome, never as a refusal: the
// refusal shape is decided on the header only (codex round 6).
//
// Every refusal COMMITS, as a miss. A refusal that returned its error from the
// Update closure made bbolt roll the transaction back without a disk write,
// while a miss committed a stats write: ~5 µs against ~10 ms, a timing class
// a single probe could read as "a live entry sits behind this key". So the
// guard verdict, like every other outcome, is handed out through `verdict`
// after a committed stats write; the closure returns an error only for a
// storage fault, and m.update then restores the in-memory stats to the
// rolled-back state. The invalidating outcomes (the legacy-provenance
// refusal, the admitted unreadable body) additionally delete the key —
// FR-002 requires the legacy entry durably invalidated by the refusal
// itself, not by a later sweep — and that delete is bounded: bbolt rewrites
// the leaf minus the entry and frees the value's pages by id range, never
// reading the payload (pinned by
// TestGetRecordsAs_EvictingRefusalWritesArePayloadIndependent). It is also
// one-shot per key: the entry is gone, so the second probe is a plain miss.
func (m *Manager) getGuarded(key string, guard func(header recordHeader) error) (*Record, error) {
	var (
		record  *Record
		verdict error
	)

	err := m.update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(CacheBucket))
		// bbolt hands back a view into its page memory: no copy, whatever
		// the value's size.
		data := bucket.Get([]byte(key))
		if data == nil {
			verdict = ErrKeyNotFound
			return m.commitMiss(tx)
		}

		var header recordHeader
		if guard != nil {
			var err error
			header, err = decodeRecordHeader(data)
			if err != nil || !header.HasCurrentProvenance() {
				// Legacy or unrecognised provenance: refuse every caller and
				// invalidate on this first redemption, committed (FR-002),
				// WITHOUT decoding the value. The size folded out of the
				// stats is the header's TotalSize — what the store folded
				// in — and 0 for a pre-frame bare-JSON value or a corrupt
				// frame, whose size is unknown without a payload-sized
				// decode the refusal must not do (codex round 5). Until
				// the next cleanup sweep TotalSizeBytes then over-counts
				// by that entry's payload; the sweep recomputes
				// TotalEntries and TotalSizeBytes exactly from the bucket
				// it walks anyway, so the statistics are eventually
				// consistent while the refusal stays O(1) (research D16).
				m.logger.Info("Invalidated cache entry with legacy provenance on first redemption",
					zap.String("key", key),
					zap.Uint8("version", header.Version),
					zap.Bool("has_producer", header.KindCode != 0),
					zap.String("caller_kind", header.Kind),
					zap.NamedError("frame", err))
				verdict = ErrLegacyProvenance
				return m.evict(tx, bucket, key, header.TotalSize, "invalidate legacy cache record")
			}
			// Internal entry: refused for every caller, kept — expired or not.
			if header.Kind == CallerKindInternal {
				verdict = ErrInternalEntry
				return m.commitMiss(tx)
			}
			// Expired: refused exactly as a miss and LEFT for the cleanup
			// sweep (CleanupInterval), which evicts expired entries anyway.
			// Deleting here would rewrite the entry's leaf — work a miss
			// never does, and proportional to whatever the leaf's other
			// values hold — for no gain: nothing requires the gated door
			// to evict, and the ungated Get keeps doing so for its own
			// readers.
			if header.expired() {
				verdict = ErrKeyExpired
				return m.commitMiss(tx)
			}
			// The guard sees the header and nothing else: kind, deny-all
			// bit, tier bits and the producer digest decide (admits).
			if err := guard(header); err != nil {
				verdict = err
				return m.commitMiss(tx)
			}
		}

		// Admitted (or the ungated door): the payload is decoded from here on.
		record = &Record{}
		if err := record.UnmarshalBinary(data); err != nil {
			record = nil
			if guard == nil {
				return fmt.Errorf("unmarshal cache record: %w", err)
			}
			// A frame the gate admitted around a body this binary cannot
			// decode — or one that DISAGREES with the header the gate
			// admitted on (UnmarshalBinary checks the two agree exactly):
			// invalidate, the way cleanup drops undecodable records, and
			// never return the body. The reader was ADMITTED — entitled to
			// the entry — so the decode it paid for is not a refusal
			// oracle, and its outcome is not a refusal either: the refusal
			// shape is decided on the fixed header only, and an admitted
			// reader gets the admitted-class ErrEntryUnreadable, which the
			// handler renders distinctly for every caller kind (codex round
			// 6). The size folded out is the header's, the one the stats
			// were told at store time.
			m.logger.Info("Invalidated unreadable cache entry on gated read",
				zap.String("key", key),
				zap.Error(err))
			verdict = ErrEntryUnreadable
			return m.evict(tx, bucket, key, header.TotalSize, "invalidate unreadable cache record")
		}

		// Expired on the ungated door (the gated door already refused it on
		// the header): evict in this transaction and COMMIT the eviction.
		if record.IsExpired() {
			size := record.TotalSize
			record = nil
			verdict = ErrKeyExpired
			return m.evict(tx, bucket, key, size, "evict expired cache record")
		}

		// Update access stats
		record.AccessCount++
		record.LastAccessed = time.Now()

		data, err := record.MarshalBinary()
		if err != nil {
			return fmt.Errorf("marshal updated record: %w", err)
		}

		if err := bucket.Put([]byte(key), data); err != nil {
			return fmt.Errorf("update access stats: %w", err)
		}

		m.stats.HitCount++
		return m.saveStats(tx)
	})
	if err != nil {
		return nil, err
	}
	if verdict != nil {
		return nil, verdict
	}
	return record, nil
}

// update runs fn inside a bbolt write transaction. The in-memory stats are
// mutated inside fn but only STAY mutated once the transaction has committed:
// whether fn returned an error or the commit itself failed afterwards (page
// write, file grow, fsync — disk full), bbolt rolled the transaction back, and
// the counters are restored to what they were when it began. So the counters
// never record a mutation the bucket did not commit, and GetStats agrees with
// the bucket on every path, not only the happy one. writeMu serialises the
// snapshot with the transaction: it is taken before bbolt's writer lock is
// acquired, so without it a concurrent writer's committed delta could be
// snapshotted away by this one's restore.
func (m *Manager) update(fn func(tx *bbolt.Tx) error) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	prev := *m.stats
	if err := m.dbUpdate(fn); err != nil {
		*m.stats = prev
		return err
	}
	return nil
}

// commitMiss records a miss and persists the stats — the one committing
// branch every refusal shares with an absent key.
func (m *Manager) commitMiss(tx *bbolt.Tx) error {
	m.stats.MissCount++
	return m.saveStats(tx)
}

// evict deletes key inside tx, folds the eviction into the stats and persists
// them. size is what the store folded in for the entry — the header's
// TotalSize — or 0 when the value has no decodable header (a pre-frame or
// corrupt record): never an estimate read from the payload. The cleanup
// sweep reconciles TotalSizeBytes from the bucket. what names the operation
// in the storage error.
func (m *Manager) evict(tx *bbolt.Tx, bucket *bbolt.Bucket, key string, size int, what string) error {
	if err := bucket.Delete([]byte(key)); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	m.stats.EvictedCount++
	m.stats.TotalEntries--
	m.stats.TotalSizeBytes -= size
	return m.saveStats(tx)
}

// putSnapshot persists the diagnostic snapshot under the producer's digest
// unless the bucket already holds one (first writer wins; every snapshot
// under a digest is the same effective authorization).
func (m *Manager) putSnapshot(tx *bbolt.Tx, digest [sha256.Size]byte, snapshot []byte) error {
	bucket := tx.Bucket([]byte(CacheSnapshotBucket))
	if bucket == nil {
		return fmt.Errorf("cache snapshots bucket %q is missing", CacheSnapshotBucket)
	}
	if bucket.Get(digest[:]) != nil {
		return nil
	}
	if err := bucket.Put(digest[:], snapshot); err != nil {
		return fmt.Errorf("store producer snapshot: %w", err)
	}
	return nil
}

// GetRecords retrieves paginated records from a cached response without a
// read gate. Callers serving a credentialed request use GetRecordsAs.
func (m *Manager) GetRecords(key string, offset, limit int) (*ReadCacheResponse, error) {
	return m.getRecords(key, offset, limit, nil)
}

// GetRecordsAs retrieves paginated records from a cached response, refusing
// with ErrUnauthorizedRead when reader could not have produced the entry
// (Spec 104 FR-016a). The gate runs on every page.
//
// Two classes of entry are refused for EVERY caller kind, administrators
// included (Spec 105 FR-002):
//   - legacy provenance (no producer, no version, or a version this binary
//     does not recognise — every entry persisted before stamping existed):
//     refused with ErrLegacyProvenance and durably invalidated by the refusal;
//   - internal entries (CallerKindInternal — the registry and guesser caches):
//     refused with ErrInternalEntry WITHOUT eviction, since their keys are
//     guessable and their writers' ungated readers depend on them.
//
// Every refusal is decided on the record's fixed header. A reader the header
// admits whose entry then proves unreadable (undecodable body, or one that
// disagrees with the header) gets ErrEntryUnreadable — not an
// ErrUnauthorizedRead — and the entry is invalidated.
func (m *Manager) GetRecordsAs(key string, offset, limit int, reader Authorization) (*ReadCacheResponse, error) {
	// The reader's side of the verdict is computed ONCE, here, before the
	// transaction: its digest, deny-all and tier bits are a function of the
	// caller's own credential, and computing them outside the door means a
	// miss and a refusal do the same work (research D16).
	r := newReaderFacts(reader)
	return m.getRecords(key, offset, limit, func(header recordHeader) error {
		// getGuarded has already refused legacy provenance (invalidated)
		// and internal entries (kept), so the producer is of a request
		// kind here. The verdict is admits over the header's facts — kind
		// first (FR-001), the deny-all bits, digest equality, then the
		// unrestricted-agent rule — in O(1), with no snapshot loaded and
		// no payload seen. CouldHaveProduced is the same function over the
		// same facts, so the door and the predicate cannot disagree.
		if !admits(header.facts(), r) {
			return ErrUnauthorizedRead
		}
		return nil
	})
}

func (m *Manager) getRecords(key string, offset, limit int, guard func(header recordHeader) error) (*ReadCacheResponse, error) {
	record, err := m.getGuarded(key, guard)
	if err != nil {
		return nil, err
	}

	// Parse the full content as JSON
	var fullData interface{}
	if err := json.Unmarshal([]byte(record.FullContent), &fullData); err != nil {
		return nil, fmt.Errorf("failed to parse cached content as JSON: %w", err)
	}

	// Extract records array
	records, err := extractRecordsArray(fullData, record.RecordPath)
	if err != nil {
		return nil, fmt.Errorf("failed to extract records: %w", err)
	}

	// Apply pagination
	totalRecords := len(records)
	if offset >= totalRecords {
		offset = totalRecords
	}

	end := offset + limit
	if end > totalRecords {
		end = totalRecords
	}

	// Non-nil so an empty page serializes as "records": [] — never null
	// (issue #953: strict MCP clients crash iterating a null array).
	paginatedRecords := make([]interface{}, 0)
	if offset < totalRecords {
		paginatedRecords = records[offset:end]
	}

	response := &ReadCacheResponse{
		Records:  paginatedRecords,
		Producer: record.Producer,
		Meta: Meta{
			Key:          key,
			TotalRecords: totalRecords,
			Limit:        limit,
			Offset:       offset,
			TotalSize:    record.TotalSize,
			RecordPath:   record.RecordPath,
		},
	}

	return response, nil
}

// GetStats returns current cache statistics
func (m *Manager) GetStats() *Stats {
	return m.stats
}

// Invalidate removes a single cache entry, forcing the next access to miss.
// It is a no-op (nil error) if the key is absent. Used by the registry refresh
// path (FR-007) to drop cached server lists on demand.
func (m *Manager) Invalidate(key string) error {
	return m.update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(CacheBucket))
		data := bucket.Get([]byte(key))
		if data == nil {
			return nil
		}
		var record Record
		if err := record.UnmarshalBinary(data); err == nil {
			m.stats.TotalEntries--
			m.stats.TotalSizeBytes -= record.TotalSize
		}
		if err := bucket.Delete([]byte(key)); err != nil {
			return fmt.Errorf("invalidate cache key: %w", err)
		}
		return m.saveStats(tx)
	})
}

// Refresh forces the next access to re-fetch by invalidating the cached entry.
// The cache manager has no knowledge of the upstream source, so "refresh" is a
// lazy operation: it drops the stale value and the caller re-populates it on
// the next Store. Provided alongside Invalidate to match the data model (FR-007).
func (m *Manager) Refresh(key string) error {
	return m.Invalidate(key)
}

// InvalidatePrefix removes every cache entry whose key starts with prefix and
// returns how many were deleted. Registry caches are keyed by a stable prefix
// (e.g. "registry-servers:<id>:") so a single refresh can drop all variants of
// a registry's cached results regardless of tag/query/limit (FR-007).
func (m *Manager) InvalidatePrefix(prefix string) (int, error) {
	deleted := 0
	err := m.update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(CacheBucket))
		cursor := bucket.Cursor()

		var keysToDelete [][]byte
		var sizeReduced int
		for key, value := cursor.First(); key != nil; key, value = cursor.Next() {
			if !strings.HasPrefix(string(key), prefix) {
				continue
			}
			keyCopy := make([]byte, len(key))
			copy(keyCopy, key)
			keysToDelete = append(keysToDelete, keyCopy)
			var record Record
			if err := record.UnmarshalBinary(value); err == nil {
				sizeReduced += record.TotalSize
			}
		}

		for _, key := range keysToDelete {
			if err := bucket.Delete(key); err != nil {
				return fmt.Errorf("invalidate prefix key: %w", err)
			}
		}
		deleted = len(keysToDelete)
		m.stats.TotalEntries -= deleted
		m.stats.TotalSizeBytes -= sizeReduced
		return m.saveStats(tx)
	})
	return deleted, err
}

// Peek returns a cached record WITHOUT evicting it or mutating access stats,
// even when the entry has expired. Unlike Get (which deletes expired entries
// and is the read path for fresh data), Peek lets the registry layer serve a
// stale value while still flagging its age — callers derive freshness from
// time.Since(record.CreatedAt) and record.IsExpired() (FR-007). The boolean is
// false only when the key is absent.
func (m *Manager) Peek(key string) (*Record, bool) {
	var record *Record
	_ = m.db.View(func(tx *bbolt.Tx) error {
		data := tx.Bucket([]byte(CacheBucket)).Get([]byte(key))
		if data == nil {
			return nil
		}
		rec := &Record{}
		if err := rec.UnmarshalBinary(data); err != nil {
			return nil
		}
		record = rec
		return nil
	})
	return record, record != nil
}

// startCleanup runs periodic cleanup of expired cache entries
func (m *Manager) startCleanup() {
	ticker := time.NewTicker(CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := m.cleanup(); err != nil {
				m.logger.Error("Cache cleanup failed", zap.Error(err))
			}
		case <-m.stopCh:
			return
		}
	}
}

// cleanup removes expired cache entries (and records it cannot decode), prunes
// the snapshots no surviving entry references, and RECOMPUTES TotalEntries
// and TotalSizeBytes from the entries that survive — the sweep walks and
// decodes every record anyway, so the exact figures are free here, and the
// gated read's legacy invalidation, which must not decode the value it
// deletes, leaves the size to this sweep (research D16). Between an
// invalidation and the next sweep the statistics may over-count by that
// entry's payload; they are never wrong for longer than CleanupInterval.
func (m *Manager) cleanup() error {
	now := time.Now()
	cleanupCount := 0
	totalSizeReduced := 0

	err := m.update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(CacheBucket))
		cursor := bucket.Cursor()

		var keysToDelete [][]byte
		// Snapshot digests the surviving entries reference; the rest of the
		// snapshots bucket is garbage once the expired entries are gone.
		referenced := map[[sha256.Size]byte]struct{}{}
		survivors, survivingSize := 0, 0

		for key, value := cursor.First(); key != nil; key, value = cursor.Next() {
			var record Record
			if err := record.UnmarshalBinary(value); err != nil {
				m.logger.Warn("Failed to unmarshal cache record during cleanup",
					zap.String("key", string(key)), zap.Error(err))
				keysToDelete = append(keysToDelete, key)
				continue
			}

			if now.After(record.ExpiresAt) {
				keysToDelete = append(keysToDelete, key)
				cleanupCount++
				totalSizeReduced += record.TotalSize
				continue
			}
			survivors++
			survivingSize += record.TotalSize
			if header, err := decodeRecordHeader(value); err == nil && header.KindCode != 0 {
				referenced[header.Digest] = struct{}{}
			}
		}

		// Delete expired keys
		for _, key := range keysToDelete {
			if err := bucket.Delete(key); err != nil {
				return fmt.Errorf("delete expired key: %w", err)
			}
		}

		if err := m.pruneSnapshots(tx, referenced); err != nil {
			return err
		}

		// Update stats: the counters are set from the bucket, not
		// decremented, so any drift a header-only eviction left behind is
		// reconciled here.
		m.stats.CleanupCount += cleanupCount
		m.stats.TotalEntries = survivors
		m.stats.TotalSizeBytes = survivingSize

		return m.saveStats(tx)
	})

	if err != nil {
		return err
	}

	if cleanupCount > 0 {
		m.logger.Info("Cache cleanup completed",
			zap.Int("expired_entries", cleanupCount),
			zap.Int("size_reduced_bytes", totalSizeReduced))
	}

	return nil
}

// pruneSnapshots deletes every snapshot no surviving entry references; a
// later entry under the same digest re-persists it.
func (m *Manager) pruneSnapshots(tx *bbolt.Tx, referenced map[[sha256.Size]byte]struct{}) error {
	bucket := tx.Bucket([]byte(CacheSnapshotBucket))
	if bucket == nil {
		return nil
	}
	var stale [][]byte
	cursor := bucket.Cursor()
	for key, _ := cursor.First(); key != nil; key, _ = cursor.Next() {
		var hash [sha256.Size]byte
		if len(key) != sha256.Size {
			stale = append(stale, append([]byte(nil), key...))
			continue
		}
		copy(hash[:], key)
		if _, ok := referenced[hash]; !ok {
			stale = append(stale, append([]byte(nil), key...))
		}
	}
	for _, key := range stale {
		if err := bucket.Delete(key); err != nil {
			return fmt.Errorf("prune producer snapshot: %w", err)
		}
	}
	return nil
}

// loadStats loads cache statistics from database
func (m *Manager) loadStats() error {
	return m.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(CacheStatsBucket))
		data := bucket.Get([]byte("stats"))
		if data == nil {
			return nil // No existing stats
		}

		return m.stats.UnmarshalBinary(data)
	})
}

// saveStats saves cache statistics to database
func (m *Manager) saveStats(tx *bbolt.Tx) error {
	bucket := tx.Bucket([]byte(CacheStatsBucket))
	if bucket == nil {
		return fmt.Errorf("cache stats bucket %q is missing", CacheStatsBucket)
	}
	data, err := m.stats.MarshalBinary()
	if err != nil {
		return fmt.Errorf("marshal stats: %w", err)
	}

	return bucket.Put([]byte("stats"), data)
}

// Close stops the cache manager
func (m *Manager) Close() {
	close(m.stopCh)
}

// extractRecordsArray extracts an array from JSON data using a path
func extractRecordsArray(data interface{}, path string) ([]interface{}, error) {
	if path == "" {
		// Try to find records array automatically
		if arr, ok := data.([]interface{}); ok {
			return arr, nil
		}

		if obj, ok := data.(map[string]interface{}); ok {
			// Look for common array field names
			commonNames := []string{"records", "data", "items", "results", "list", "array"}
			for _, name := range commonNames {
				if val, exists := obj[name]; exists {
					if arr, ok := val.([]interface{}); ok {
						return arr, nil
					}
				}
			}
		}

		return nil, fmt.Errorf("no records array found")
	}

	// Parse complex paths like "[0].text(parsed).totalDataChart"
	current := data
	pathSegments := parsePathSegments(path)

	for _, segment := range pathSegments {
		switch segment.Type {
		case "object":
			if obj, ok := current.(map[string]interface{}); ok {
				if val, exists := obj[segment.Key]; exists {
					current = val
				} else {
					return nil, fmt.Errorf("invalid record path: key '%s' not found", segment.Key)
				}
			} else {
				return nil, fmt.Errorf("invalid record path: expected object for key '%s'", segment.Key)
			}
		case "array":
			if arr, ok := current.([]interface{}); ok {
				if segment.Index >= 0 && segment.Index < len(arr) {
					current = arr[segment.Index]
				} else {
					return nil, fmt.Errorf("invalid record path: array index %d out of bounds", segment.Index)
				}
			} else {
				return nil, fmt.Errorf("invalid record path: expected array for index %d", segment.Index)
			}
		case "parsed":
			// Handle (parsed) JSON strings
			if strVal, ok := current.(string); ok {
				var parsedData interface{}
				if err := json.Unmarshal([]byte(strVal), &parsedData); err != nil {
					return nil, fmt.Errorf("invalid record path: failed to parse JSON string: %w", err)
				}
				current = parsedData
			} else {
				return nil, fmt.Errorf("invalid record path: expected string for (parsed) segment")
			}
		}
	}

	// Final result should be an array
	if arr, ok := current.([]interface{}); ok {
		return arr, nil
	}

	return nil, fmt.Errorf("invalid record path: final result is not an array")
}

// PathSegment represents a segment of a JSON path
type PathSegment struct {
	Type  string // "object", "array", or "parsed"
	Key   string // for object access
	Index int    // for array access
}

// parsePathSegments parses a path string into segments
// Handles paths like: "[0].text(parsed).totalDataChart"
func parsePathSegments(path string) []PathSegment {
	var segments []PathSegment
	i := 0

	for i < len(path) {
		if path[i] == '[' {
			// Array index
			j := strings.Index(path[i:], "]")
			if j == -1 {
				break
			}
			indexStr := path[i+1 : i+j]
			if index := parseIndex(indexStr); index >= 0 {
				segments = append(segments, PathSegment{
					Type:  "array",
					Index: index,
				})
			}
			i += j + 1
			// Skip dot after ]
			if i < len(path) && path[i] == '.' {
				i++
			}
		} else if strings.HasPrefix(path[i:], "(parsed)") {
			// Parsed JSON segment
			segments = append(segments, PathSegment{
				Type: "parsed",
			})
			i += 8 // length of "(parsed)"
			// Skip dot after (parsed)
			if i < len(path) && path[i] == '.' {
				i++
			}
		} else {
			// Object key
			start := i
			for i < len(path) && path[i] != '.' && path[i] != '[' && !strings.HasPrefix(path[i:], "(parsed)") {
				i++
			}
			if i > start {
				key := path[start:i]
				segments = append(segments, PathSegment{
					Type: "object",
					Key:  key,
				})
			}
			// Skip dot
			if i < len(path) && path[i] == '.' {
				i++
			}
		}
	}

	return segments
}

// parseIndex safely parses a string to an integer index
func parseIndex(s string) int {
	if index, err := strconv.Atoi(s); err == nil && index >= 0 {
		return index
	}
	return -1
}
