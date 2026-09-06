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
	// CurrentErrorCodes is the STANDING state at heartbeat time: stable MCPX_
	// code -> number of configured servers currently in that state. It is not
	// a counter and is never persisted — it is recomputed each heartbeat from
	// the supervisor's live stateview.
	//
	// Schema v11 (MCP-2967). It exists because error_code_counts_24h became
	// edge-triggered in the same change: previously the supervisor re-counted
	// every standing failure on its 30s reconcile ticker, so a parked install
	// inflated the counter by ~2880/day/server. Edge-triggering alone would
	// have swung the error to the other side — the 24h window decays, this
	// struct is omitempty via isZero(), so a permanently broken install would
	// emit once and then disappear from the payload entirely, and absent reads
	// as zero. The two fields answer different questions and are only correct
	// together: "how often did something newly break" and "how many servers
	// are broken right now".
	//
	// Safe: only MCPX_* enum constants as keys, never server names, URLs,
	// commands, or free text; values are non-negative server counts.
	CurrentErrorCodes map[string]int `json:"current_error_codes,omitempty"`
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
		len(d.CurrentErrorCodes) == 0 &&
		d.FixAttempted24h == 0 &&
		d.FixSucceeded24h == 0 &&
		d.UniqueCodesEver == 0
}

// sanitizeMCPXCodeMap re-asserts the anonymity contract on a code->count map
// supplied by a provider outside this package (MCP-2967). Only keys matching
// the stable MCPX_* enum shape and strictly positive counts survive, so a
// server name, URL, or any other free text can never reach the wire even if a
// future caller passes one. Returns nil when nothing survives, so omitempty
// drops the field rather than emitting an empty object.
func sanitizeMCPXCodeMap(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for code, n := range in {
		if n <= 0 || len(code) > maxLastErrorCodeLen || !mcpxCodePattern.MatchString(code) {
			continue
		}
		out[code] = n
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// capDiagCodeMap bounds a code->count map to maxDiagCodeEntries entries,
// keeping the highest counts (ties broken by code ascending so the wire form
// is deterministic). Returns the input untouched when it already fits.
func capDiagCodeMap(counts map[string]int) map[string]int {
	if len(counts) <= maxDiagCodeEntries {
		return counts
	}
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
	capped := make(map[string]int, maxDiagCodeEntries)
	for _, e := range entries[:maxDiagCodeEntries] {
		capped[e.k] = e.v
	}
	return capped
}

// MarshalJSON caps ErrorCodeCounts24h and CurrentErrorCodes to top-20 entries
// each before serialising.
func (d DiagnosticsCounters) MarshalJSON() ([]byte, error) {
	type wire struct {
		ErrorCodeCounts24h map[string]int `json:"error_code_counts_24h,omitempty"`
		CurrentErrorCodes  map[string]int `json:"current_error_codes,omitempty"`
		FixAttempted24h    int            `json:"fix_attempted_24h"`
		FixSucceeded24h    int            `json:"fix_succeeded_24h"`
		UniqueCodesEver    int            `json:"unique_codes_ever"`
	}
	return json.Marshal(wire{
		ErrorCodeCounts24h: capDiagCodeMap(d.ErrorCodeCounts24h),
		CurrentErrorCodes:  capDiagCodeMap(d.CurrentErrorCodes),
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
