package telemetry

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.etcd.io/bbolt"
)

// ActivationBucketName is the BBolt bucket that stores retention activation
// state (spec 044). Keys inside the bucket are fixed at compile time; missing
// keys default to their zero value.
const ActivationBucketName = "activation"

// Fixed keys inside the activation bucket. Values are encoded per
// data-model.md §ActivationBucket.
const (
	activationKeyFirstConnectedServerEver   = "first_connected_server_ever"
	activationKeyFirstMCPClientEver         = "first_mcp_client_ever"
	activationKeyFirstRetrieveToolsCallEver = "first_retrieve_tools_call_ever"
	activationKeyMCPClientsSeenEver         = "mcp_clients_seen_ever"
	activationKeyRetrieveToolsCalls24h      = "retrieve_tools_calls_24h"
	activationKeyEstimatedTokensSaved24h    = "estimated_tokens_saved_24h"
	activationKeyInstallerHeartbeatPending  = "installer_heartbeat_pending"
)

// MaxMCPClientsSeen bounds the cardinality of the mcp_clients_seen_ever list.
// 17th insertion is dropped.
const MaxMCPClientsSeen = 16

// windowSeconds is the 24h sliding window for retrieve_tools_calls / tokens-saved.
const windowSeconds = 24 * 60 * 60

// ActivationState is the in-memory / on-the-wire representation of the
// activation bucket. Instances are loaded with ActivationStore.Load and saved
// with ActivationStore.Save.
type ActivationState struct {
	FirstConnectedServerEver      bool     `json:"first_connected_server_ever"`
	FirstMCPClientEver            bool     `json:"first_mcp_client_ever"`
	FirstRetrieveToolsCallEver    bool     `json:"first_retrieve_tools_call_ever"`
	MCPClientsSeenEver            []string `json:"mcp_clients_seen_ever"`
	RetrieveToolsCalls24h         int      `json:"retrieve_tools_calls_24h"`
	EstimatedTokensSaved24hBucket string   `json:"estimated_tokens_saved_24h_bucket"`
	ConfiguredIDECount            int      `json:"configured_ide_count"`
}

// ActivationStore is the persistence contract for the activation bucket.
// Implementations back onto a BBolt database; a fake is used in tests.
//
// All methods are safe for concurrent use by the caller's discretion — the
// BBolt implementation uses transactional updates so individual method calls
// are atomic. Callers that need multi-step atomicity should serialize through
// a single goroutine.
type ActivationStore interface {
	// Load reads the full activation state. Missing bucket or keys yield
	// zero values.
	Load(db *bbolt.DB) (ActivationState, error)

	// Save writes the full activation state, enforcing monotonic flags
	// (true cannot revert to false).
	Save(db *bbolt.DB, st ActivationState) error

	// MarkFirstConnectedServer sets first_connected_server_ever=true if not
	// already set. No-op if already true.
	MarkFirstConnectedServer(db *bbolt.DB) error

	// MarkFirstMCPClient sets first_mcp_client_ever=true if not already set.
	MarkFirstMCPClient(db *bbolt.DB) error

	// MarkFirstRetrieveToolsCall sets first_retrieve_tools_call_ever=true
	// if not already set.
	MarkFirstRetrieveToolsCall(db *bbolt.DB) error

	// RecordMCPClient adds sanitized client name to the seen list. Dedups
	// on insert; drops when cap (16) is reached.
	RecordMCPClient(db *bbolt.DB, name string) error

	// IncrementRetrieveToolsCall bumps the 24h window counter by 1,
	// rolling the window if it has expired.
	IncrementRetrieveToolsCall(db *bbolt.DB) error

	// AddTokensSaved adds n to the 24h token-savings estimator counter.
	AddTokensSaved(db *bbolt.DB, n int) error

	// SetInstallerPending writes the installer_heartbeat_pending flag.
	SetInstallerPending(db *bbolt.DB, v bool) error

	// IsInstallerPending reports whether installer_heartbeat_pending is
	// currently set.
	IsInstallerPending(db *bbolt.DB) (bool, error)
}

// bboltActivationStore is the BBolt-backed ActivationStore implementation.
// Zero-value is ready to use; no initialization required.
type bboltActivationStore struct{}

// NewActivationStore returns a BBolt-backed ActivationStore.
func NewActivationStore() ActivationStore {
	return bboltActivationStore{}
}

// --- Encoding helpers ---

func encodeBool(v bool) []byte {
	if v {
		return []byte{0x01}
	}
	return []byte{0x00}
}

func decodeBool(b []byte) bool {
	return len(b) > 0 && b[0] != 0x00
}

// counter record: 8 bytes uint64 count + 8 bytes int64 window_start_unix
func encodeCounter(count uint64, windowStart int64) []byte {
	buf := make([]byte, 16)
	binary.BigEndian.PutUint64(buf[0:8], count)
	binary.BigEndian.PutUint64(buf[8:16], uint64(windowStart))
	return buf
}

func decodeCounter(b []byte) (uint64, int64) {
	if len(b) < 16 {
		return 0, 0
	}
	count := binary.BigEndian.Uint64(b[0:8])
	windowStart := int64(binary.BigEndian.Uint64(b[8:16]))
	return count, windowStart
}

// readCounterWithDecay reads a counter and applies 24h decay at `now`.
// Returns (count, windowStart, didDecay).
func readCounterWithDecay(b []byte, now time.Time) (uint64, int64, bool) {
	count, windowStart := decodeCounter(b)
	if windowStart == 0 {
		return 0, now.Unix(), false
	}
	if now.Unix()-windowStart >= windowSeconds {
		return 0, now.Unix(), true
	}
	return count, windowStart, false
}

// --- Bucket helpers ---

func activationBucket(tx *bbolt.Tx) *bbolt.Bucket {
	return tx.Bucket([]byte(ActivationBucketName))
}

func activationBucketForWrite(tx *bbolt.Tx) (*bbolt.Bucket, error) {
	return tx.CreateBucketIfNotExists([]byte(ActivationBucketName))
}

// --- Load / Save ---

func (bboltActivationStore) Load(db *bbolt.DB) (ActivationState, error) {
	return loadActivationAt(db, time.Now())
}

func loadActivationAt(db *bbolt.DB, now time.Time) (ActivationState, error) {
	st := ActivationState{}
	err := db.View(func(tx *bbolt.Tx) error {
		b := activationBucket(tx)
		if b == nil {
			return nil
		}
		st.FirstConnectedServerEver = decodeBool(b.Get([]byte(activationKeyFirstConnectedServerEver)))
		st.FirstMCPClientEver = decodeBool(b.Get([]byte(activationKeyFirstMCPClientEver)))
		st.FirstRetrieveToolsCallEver = decodeBool(b.Get([]byte(activationKeyFirstRetrieveToolsCallEver)))

		if raw := b.Get([]byte(activationKeyMCPClientsSeenEver)); len(raw) > 0 {
			var list []string
			if err := json.Unmarshal(raw, &list); err != nil {
				// Corrupt value: treat as empty rather than failing the load.
				list = nil
			}
			st.MCPClientsSeenEver = list
		}

		if raw := b.Get([]byte(activationKeyRetrieveToolsCalls24h)); len(raw) >= 16 {
			count, _, _ := readCounterWithDecay(raw, now)
			st.RetrieveToolsCalls24h = int(count)
		}

		if raw := b.Get([]byte(activationKeyEstimatedTokensSaved24h)); len(raw) >= 16 {
			count, _, _ := readCounterWithDecay(raw, now)
			st.EstimatedTokensSaved24hBucket = BucketTokens(int(count))
		} else {
			st.EstimatedTokensSaved24hBucket = BucketTokens(0)
		}
		return nil
	})
	return st, err
}

// LoadRetrieveToolsCalls24hAt returns the 24h counter value at the given time,
// applying decay. Exported for tests; production code should call Load().
func (bboltActivationStore) LoadRetrieveToolsCalls24hAt(db *bbolt.DB, now time.Time) (int, error) {
	var count uint64
	err := db.View(func(tx *bbolt.Tx) error {
		b := activationBucket(tx)
		if b == nil {
			return nil
		}
		raw := b.Get([]byte(activationKeyRetrieveToolsCalls24h))
		if len(raw) >= 16 {
			c, _, _ := readCounterWithDecay(raw, now)
			count = c
		}
		return nil
	})
	return int(count), err
}

func (bboltActivationStore) Save(db *bbolt.DB, st ActivationState) error {
	return db.Update(func(tx *bbolt.Tx) error {
		b, err := activationBucketForWrite(tx)
		if err != nil {
			return err
		}

		// Monotonic flags: OR with existing value (never flip true->false).
		existing := decodeBool(b.Get([]byte(activationKeyFirstConnectedServerEver))) || st.FirstConnectedServerEver
		if err := b.Put([]byte(activationKeyFirstConnectedServerEver), encodeBool(existing)); err != nil {
			return err
		}
		existing = decodeBool(b.Get([]byte(activationKeyFirstMCPClientEver))) || st.FirstMCPClientEver
		if err := b.Put([]byte(activationKeyFirstMCPClientEver), encodeBool(existing)); err != nil {
			return err
		}
		existing = decodeBool(b.Get([]byte(activationKeyFirstRetrieveToolsCallEver))) || st.FirstRetrieveToolsCallEver
		if err := b.Put([]byte(activationKeyFirstRetrieveToolsCallEver), encodeBool(existing)); err != nil {
			return err
		}

		// Clients list: write as-is (trimmed to cap).
		list := st.MCPClientsSeenEver
		if len(list) > MaxMCPClientsSeen {
			list = list[:MaxMCPClientsSeen]
		}
		if list == nil {
			list = []string{}
		}
		raw, err := json.Marshal(list)
		if err != nil {
			return err
		}
		if err := b.Put([]byte(activationKeyMCPClientsSeenEver), raw); err != nil {
			return err
		}

		return nil
	})
}

// --- Monotonic flag setters ---

func (bboltActivationStore) markFlag(db *bbolt.DB, key string) error {
	return db.Update(func(tx *bbolt.Tx) error {
		b, err := activationBucketForWrite(tx)
		if err != nil {
			return err
		}
		if decodeBool(b.Get([]byte(key))) {
			return nil // already set
		}
		return b.Put([]byte(key), encodeBool(true))
	})
}

func (s bboltActivationStore) MarkFirstConnectedServer(db *bbolt.DB) error {
	return s.markFlag(db, activationKeyFirstConnectedServerEver)
}

func (s bboltActivationStore) MarkFirstMCPClient(db *bbolt.DB) error {
	return s.markFlag(db, activationKeyFirstMCPClientEver)
}

func (s bboltActivationStore) MarkFirstRetrieveToolsCall(db *bbolt.DB) error {
	return s.markFlag(db, activationKeyFirstRetrieveToolsCallEver)
}

// --- MCP client list ---

func (bboltActivationStore) RecordMCPClient(db *bbolt.DB, name string) error {
	sanitized := sanitizeClientName(name)
	return db.Update(func(tx *bbolt.Tx) error {
		b, err := activationBucketForWrite(tx)
		if err != nil {
			return err
		}
		var list []string
		if raw := b.Get([]byte(activationKeyMCPClientsSeenEver)); len(raw) > 0 {
			if err := json.Unmarshal(raw, &list); err != nil {
				list = nil
			}
		}
		// Dedup.
		for _, existing := range list {
			if existing == sanitized {
				return nil
			}
		}
		// Cap.
		if len(list) >= MaxMCPClientsSeen {
			return nil
		}
		list = append(list, sanitized)
		raw, err := json.Marshal(list)
		if err != nil {
			return err
		}
		return b.Put([]byte(activationKeyMCPClientsSeenEver), raw)
	})
}

// --- 24h counters ---

func (bboltActivationStore) IncrementRetrieveToolsCall(db *bbolt.DB) error {
	now := time.Now()
	return db.Update(func(tx *bbolt.Tx) error {
		b, err := activationBucketForWrite(tx)
		if err != nil {
			return err
		}
		raw := b.Get([]byte(activationKeyRetrieveToolsCalls24h))
		count, windowStart, _ := readCounterWithDecay(raw, now)
		count++
		return b.Put([]byte(activationKeyRetrieveToolsCalls24h), encodeCounter(count, windowStart))
	})
}

func (bboltActivationStore) AddTokensSaved(db *bbolt.DB, n int) error {
	if n <= 0 {
		return nil
	}
	now := time.Now()
	return db.Update(func(tx *bbolt.Tx) error {
		b, err := activationBucketForWrite(tx)
		if err != nil {
			return err
		}
		raw := b.Get([]byte(activationKeyEstimatedTokensSaved24h))
		count, windowStart, _ := readCounterWithDecay(raw, now)
		count += uint64(n)
		return b.Put([]byte(activationKeyEstimatedTokensSaved24h), encodeCounter(count, windowStart))
	})
}

// writeRetrieveCounter is a test helper (unexported) for seeding the window
// counter with a specific start time.
func writeRetrieveCounter(db *bbolt.DB, count uint64, windowStart time.Time) error {
	return db.Update(func(tx *bbolt.Tx) error {
		b, err := activationBucketForWrite(tx)
		if err != nil {
			return err
		}
		return b.Put([]byte(activationKeyRetrieveToolsCalls24h), encodeCounter(count, windowStart.Unix()))
	})
}

// --- Installer pending flag ---

func (bboltActivationStore) SetInstallerPending(db *bbolt.DB, v bool) error {
	return db.Update(func(tx *bbolt.Tx) error {
		b, err := activationBucketForWrite(tx)
		if err != nil {
			return err
		}
		return b.Put([]byte(activationKeyInstallerHeartbeatPending), encodeBool(v))
	})
}

func (bboltActivationStore) IsInstallerPending(db *bbolt.DB) (bool, error) {
	var v bool
	err := db.View(func(tx *bbolt.Tx) error {
		b := activationBucket(tx)
		if b == nil {
			return nil
		}
		v = decodeBool(b.Get([]byte(activationKeyInstallerHeartbeatPending)))
		return nil
	})
	return v, err
}

// --- Bucketing (FR-009) ---

// BucketTokens maps a raw token count to the fixed bucket enum.
// Buckets: "0", "1_100", "100_1k", "1k_10k", "10k_100k", "100k_plus".
//
// Boundaries (inclusive upper edge matches the bucket label):
//   - 0              -> "0"
//   - 1..100         -> "1_100"
//   - 101..1000      -> "100_1k"
//   - 1001..10000    -> "1k_10k"
//   - 10001..100000  -> "10k_100k"
//   - >100000        -> "100k_plus"
func BucketTokens(n int) string {
	switch {
	case n <= 0:
		return "0"
	case n <= 100:
		return "1_100"
	case n <= 1000:
		return "100_1k"
	case n <= 10000:
		return "1k_10k"
	case n <= 100000:
		return "10k_100k"
	default:
		return "100k_plus"
	}
}

// --- Client name sanitizer (T035) ---

var clientNameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// sanitizeClientName normalizes and validates an MCP clientInfo.name.
// Returns "unknown" for any input that:
//   - contains a path separator ('/' or '\\')
//   - contains '..', '@', or whitespace
//   - is empty or longer than 64 chars
//   - contains characters outside [a-z0-9._-] after lowercasing
//
// Valid names are lowercased and returned unchanged.
func sanitizeClientName(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "unknown"
	}
	if strings.ContainsAny(trimmed, "/\\@ \t\r\n") {
		return "unknown"
	}
	if strings.Contains(trimmed, "..") {
		return "unknown"
	}
	lower := strings.ToLower(trimmed)
	if len(lower) > 64 {
		return "unknown"
	}
	if !clientNameRegex.MatchString(lower) {
		return "unknown"
	}
	return lower
}

// ensureBucket ensures the activation bucket exists (useful for migration).
// Exported so callers that wire up storage can pre-create it to avoid races
// on first write. Safe to call multiple times.
func EnsureActivationBucket(db *bbolt.DB) error {
	if db == nil {
		return fmt.Errorf("nil db")
	}
	return db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(ActivationBucketName))
		return err
	})
}
