package telemetry

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.etcd.io/bbolt"
)

// DiagnosticsCountersBucketName is the BBolt bucket that stores Phase H
// diagnostics counters (spec 044). Keys inside are defined as constants below.
const DiagnosticsCountersBucketName = "diagnostics_counters"

const (
	diagKeyFixAttempted24h = "fix_attempted_24h"
	diagKeyFixSucceeded24h = "fix_succeeded_24h"
	diagKeyUniqueCodesEver = "unique_codes_ever"
	diagKeyCodePrefix      = "code_count_24h_"
)

// maxDiagCodeEntries is the cardinality cap for ErrorCodeCounts24h in
// MarshalJSON. Protects payload size; top-20 by count, ties by code asc.
const maxDiagCodeEntries = 20

// DiagnosticsCounters holds the Phase H counter snapshot. MarshalJSON caps
// ErrorCodeCounts24h to the top-20 entries (by count desc, code asc on ties)
// so the wire payload stays bounded regardless of MCPX_ catalog growth.
type DiagnosticsCounters struct {
	// ErrorCodeCounts24h maps stable MCPX_ code strings to 24h occurrence counts.
	// Safe: only MCPX_* enum constants are stored here, never free text, paths,
	// server names, or user-entered values.
	ErrorCodeCounts24h map[string]int `json:"error_code_counts_24h,omitempty"`
	// FixAttempted24h counts POST /api/v1/diagnostics/fix calls in the last 24h.
	FixAttempted24h int `json:"fix_attempted_24h"`
	// FixSucceeded24h counts fix invocations with outcome="success" in the last 24h.
	FixSucceeded24h int `json:"fix_succeeded_24h"`
	// UniqueCodesEver is the cardinality of the all-time error code set.
	// Bounded by the MCPX_ catalog size (~30 codes), never approaches PII risk.
	UniqueCodesEver int `json:"unique_codes_ever"`
}

// isZero reports whether all counters are zero (used for omitempty on the
// parent struct pointer — the struct itself has no omitempty on int fields).
func (d DiagnosticsCounters) isZero() bool {
	return len(d.ErrorCodeCounts24h) == 0 &&
		d.FixAttempted24h == 0 &&
		d.FixSucceeded24h == 0 &&
		d.UniqueCodesEver == 0
}

// MarshalJSON caps ErrorCodeCounts24h to top-20 entries before serialising.
func (d DiagnosticsCounters) MarshalJSON() ([]byte, error) {
	counts := d.ErrorCodeCounts24h
	if len(counts) > maxDiagCodeEntries {
		type kv struct {
			k string
			v int
		}
		entries := make([]kv, 0, len(counts))
		for k, v := range counts {
			entries = append(entries, kv{k, v})
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].v != entries[j].v {
				return entries[i].v > entries[j].v // higher count first
			}
			return entries[i].k < entries[j].k // tie-break by code asc
		})
		counts = make(map[string]int, maxDiagCodeEntries)
		for _, e := range entries[:maxDiagCodeEntries] {
			counts[e.k] = e.v
		}
	}
	type wire struct {
		ErrorCodeCounts24h map[string]int `json:"error_code_counts_24h,omitempty"`
		FixAttempted24h    int            `json:"fix_attempted_24h"`
		FixSucceeded24h    int            `json:"fix_succeeded_24h"`
		UniqueCodesEver    int            `json:"unique_codes_ever"`
	}
	return json.Marshal(wire{
		ErrorCodeCounts24h: counts,
		FixAttempted24h:    d.FixAttempted24h,
		FixSucceeded24h:    d.FixSucceeded24h,
		UniqueCodesEver:    d.UniqueCodesEver,
	})
}

// DiagnosticsCounterStore is the persistence contract for Phase H counters.
// Implementations back onto BBolt; a fake is used in tests.
// All methods are individually atomic via bbolt transactions.
type DiagnosticsCounterStore interface {
	// RecordErrorCode increments the 24h sliding counter for the given MCPX_
	// code and adds it to the unique_codes_ever set (idempotent on second add).
	// Only values that match the MCPX_ prefix are accepted; others are silently
	// dropped to prevent free-text from leaking into telemetry.
	RecordErrorCode(db *bbolt.DB, code string) error

	// RecordFixAttempt increments fix_attempted_24h. If outcome == "success",
	// also increments fix_succeeded_24h. Unknown outcomes are counted as
	// attempted-only (graceful: new outcome strings from future code won't panic).
	RecordFixAttempt(db *bbolt.DB, outcome string) error

	// Snapshot loads the current counter state, applying 24h decay at now.
	Snapshot(db *bbolt.DB) (DiagnosticsCounters, error)
}

// bboltDiagnosticsCounterStore is the production BBolt-backed implementation.
// Zero-value is ready to use; no initialisation required.
type bboltDiagnosticsCounterStore struct{}

// NewDiagnosticsCounterStore returns a BBolt-backed DiagnosticsCounterStore.
func NewDiagnosticsCounterStore() DiagnosticsCounterStore {
	return bboltDiagnosticsCounterStore{}
}

// EnsureDiagnosticsCountersBucket pre-creates the bucket to avoid write-races
// on first use. Safe to call multiple times.
func EnsureDiagnosticsCountersBucket(db *bbolt.DB) error {
	if db == nil {
		return fmt.Errorf("nil db")
	}
	return db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(DiagnosticsCountersBucketName))
		return err
	})
}

// --- bucket helpers ---

func diagBucket(tx *bbolt.Tx) *bbolt.Bucket {
	return tx.Bucket([]byte(DiagnosticsCountersBucketName))
}

func diagBucketForWrite(tx *bbolt.Tx) (*bbolt.Bucket, error) {
	return tx.CreateBucketIfNotExists([]byte(DiagnosticsCountersBucketName))
}

// --- RecordErrorCode ---

func (bboltDiagnosticsCounterStore) RecordErrorCode(db *bbolt.DB, code string) error {
	// Only store stable MCPX_ enum values. Drop anything else so free text,
	// server names, or paths can never reach the telemetry pipeline.
	if !strings.HasPrefix(code, "MCPX_") {
		return nil
	}
	now := time.Now()
	return db.Update(func(tx *bbolt.Tx) error {
		b, err := diagBucketForWrite(tx)
		if err != nil {
			return err
		}

		// 1. Bump per-code 24h counter.
		codeKey := []byte(diagKeyCodePrefix + code)
		raw := b.Get(codeKey)
		count, windowStart, _ := readCounterWithDecay(raw, now)
		count++
		if err := b.Put(codeKey, encodeCounter(count, windowStart)); err != nil {
			return err
		}

		// 2. Add to unique_codes_ever set (idempotent).
		var seen []string
		if raw := b.Get([]byte(diagKeyUniqueCodesEver)); len(raw) > 0 {
			_ = json.Unmarshal(raw, &seen)
		}
		for _, existing := range seen {
			if existing == code {
				return nil // already in set
			}
		}
		seen = append(seen, code)
		raw2, err := json.Marshal(seen)
		if err != nil {
			return err
		}
		return b.Put([]byte(diagKeyUniqueCodesEver), raw2)
	})
}

// --- RecordFixAttempt ---

func (bboltDiagnosticsCounterStore) RecordFixAttempt(db *bbolt.DB, outcome string) error {
	now := time.Now()
	return db.Update(func(tx *bbolt.Tx) error {
		b, err := diagBucketForWrite(tx)
		if err != nil {
			return err
		}

		// always bump attempted
		raw := b.Get([]byte(diagKeyFixAttempted24h))
		cnt, ws, _ := readCounterWithDecay(raw, now)
		cnt++
		if err := b.Put([]byte(diagKeyFixAttempted24h), encodeCounter(cnt, ws)); err != nil {
			return err
		}

		if outcome == "success" {
			raw2 := b.Get([]byte(diagKeyFixSucceeded24h))
			cnt2, ws2, _ := readCounterWithDecay(raw2, now)
			cnt2++
			return b.Put([]byte(diagKeyFixSucceeded24h), encodeCounter(cnt2, ws2))
		}
		return nil
	})
}

// --- Snapshot ---

func (bboltDiagnosticsCounterStore) Snapshot(db *bbolt.DB) (DiagnosticsCounters, error) {
	return snapshotDiagnosticsAt(db, time.Now())
}

func snapshotDiagnosticsAt(db *bbolt.DB, now time.Time) (DiagnosticsCounters, error) {
	var out DiagnosticsCounters
	err := db.View(func(tx *bbolt.Tx) error {
		b := diagBucket(tx)
		if b == nil {
			return nil // bucket absent → all zero
		}

		// fix_attempted_24h
		if raw := b.Get([]byte(diagKeyFixAttempted24h)); len(raw) >= 16 {
			cnt, _, _ := readCounterWithDecay(raw, now)
			out.FixAttempted24h = int(cnt)
		}

		// fix_succeeded_24h
		if raw := b.Get([]byte(diagKeyFixSucceeded24h)); len(raw) >= 16 {
			cnt, _, _ := readCounterWithDecay(raw, now)
			out.FixSucceeded24h = int(cnt)
		}

		// unique_codes_ever
		if raw := b.Get([]byte(diagKeyUniqueCodesEver)); len(raw) > 0 {
			var codes []string
			if err := json.Unmarshal(raw, &codes); err == nil {
				out.UniqueCodesEver = len(codes)
			}
		}

		// per-code 24h counts
		prefix := []byte(diagKeyCodePrefix)
		c := b.Cursor()
		for k, v := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), diagKeyCodePrefix); k, v = c.Next() {
			code := strings.TrimPrefix(string(k), diagKeyCodePrefix)
			if len(v) >= 16 {
				cnt, _, _ := readCounterWithDecay(v, now)
				if cnt > 0 {
					if out.ErrorCodeCounts24h == nil {
						out.ErrorCodeCounts24h = make(map[string]int)
					}
					out.ErrorCodeCounts24h[code] = int(cnt)
				}
			}
		}
		return nil
	})
	return out, err
}
