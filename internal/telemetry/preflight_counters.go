package telemetry

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.etcd.io/bbolt"
)

// PreflightCountersBucketName is the BBolt bucket that stores the
// required-tools-preflight BASELINE counters (issue #969, Phase 0). Keys inside
// are defined as constants below and follow the Phase-H counter encoding
// (encodeCounter / readCounterWithDecay), so every counter here is a 24h
// sliding window that decays at read time.
//
// These counters ship ONE RELEASE AHEAD of the preflight feature on purpose:
// without a live pre-feature window there is nothing to compare the post-feature
// numbers against, and "did preflight help?" degrades into an argument about
// anecdotes.
const PreflightCountersBucketName = "preflight_counters"

const (
	preflightKeyFilterDiagEmitted24h  = "filter_diag_emitted_24h"
	preflightKeyFilterDiagMissing24h  = "filter_diag_missing_annotation_24h"
	preflightKeyFilterDiagExplicit24h = "filter_diag_explicit_24h"
	preflightKeyFilterDiagFollowed24h = "filter_diag_followed_24h"
	preflightKeyAvailabilityBlock24h  = "availability_block_24h"
	preflightKeyDiscoveryOmission24h  = "discovery_omission_24h"
	preflightKeyAvailabilityReasonPfx = "availability_block_reason_24h_"
)

// Availability block reason keys (issue #969). This is a CLOSED enum mirroring
// the structured policy-block sites in internal/server — it is duplicated here
// rather than imported for the same reason tpaSeverityKeys is: the telemetry
// package must not depend on the server package, and the enum IS the anonymity
// contract. Anything outside this list is folded into BlockReasonOther rather
// than transmitted, so a reason STRING (which carries server/tool names) can
// never become a map key.
const (
	BlockReasonIntentInvalid       = "intent_invalid"
	BlockReasonIntentRejected      = "intent_rejected"
	BlockReasonProfileScope        = "profile_scope"
	BlockReasonTokenScope          = "token_scope"
	BlockReasonTokenPermission     = "token_permission"
	BlockReasonServerQuarantined   = "server_quarantined"
	BlockReasonToolPendingApproval = "tool_pending_approval"
	BlockReasonToolChanged         = "tool_changed_approval"
	BlockReasonToolNotCallable     = "tool_not_callable"
	BlockReasonOutputSanitisation  = "output_sanitisation"
	BlockReasonOutputSchema        = "output_schema"
	BlockReasonOther               = "other"
)

// availabilityBlockReasonKeys is the fixed enum emitted under
// preflight.availability_block_reasons_24h.
var availabilityBlockReasonKeys = []string{
	BlockReasonIntentInvalid,
	BlockReasonIntentRejected,
	BlockReasonProfileScope,
	BlockReasonTokenScope,
	BlockReasonTokenPermission,
	BlockReasonServerQuarantined,
	BlockReasonToolPendingApproval,
	BlockReasonToolChanged,
	BlockReasonToolNotCallable,
	BlockReasonOutputSanitisation,
	BlockReasonOutputSchema,
	BlockReasonOther,
}

// availabilityBlockReasonAllowList is the set form of availabilityBlockReasonKeys.
var availabilityBlockReasonAllowList = func() map[string]struct{} {
	m := make(map[string]struct{}, len(availabilityBlockReasonKeys))
	for _, k := range availabilityBlockReasonKeys {
		m[k] = struct{}{}
	}
	return m
}()

// IsAvailabilityBlockReason reports whether key is a member of the closed
// availability-block reason enum.
func IsAvailabilityBlockReason(key string) bool {
	_, ok := availabilityBlockReasonAllowList[key]
	return ok
}

// NormalizeAvailabilityBlockReason maps any input onto the closed enum:
// members pass through, everything else becomes BlockReasonOther. Callers on
// the hot path use this so a future block site that forgets to declare a key
// still produces a countable — and non-identifying — bucket.
func NormalizeAvailabilityBlockReason(key string) string {
	if IsAvailabilityBlockReason(key) {
		return key
	}
	return BlockReasonOther
}

// maxAvailabilityReasonKeys bounds how many DISTINCT reason keys the bucket
// will ever hold. The allow-list already bounds cardinality at len(enum); this
// is the write-side backstop that keeps a producer regression (or a stale
// bucket written by a newer build) from growing the key set without bound.
// BlockReasonOther is exempt — it is the overflow bucket itself.
const maxAvailabilityReasonKeys = 16

// maxPreflightReasonEntries caps availability_block_reasons_24h in
// MarshalJSON: top-N by count desc, key asc on ties. Same posture as
// maxDiagCodeEntries — the wire payload stays bounded regardless of what the
// bucket accumulated.
const maxPreflightReasonEntries = 16

// PreflightCounters is the baseline counter snapshot for the required-tools
// preflight roadmap (issue #969, Phase 0).
//
// Privacy contract: counts only. Every field is a non-negative integer, and the
// only map keys that can appear are members of the closed
// availabilityBlockReasonKeys enum. No tool name, server name, query, filter
// value, session id, or free text ever reaches this struct — the Record*
// methods do not accept them.
type PreflightCounters struct {
	// FilterDiagEmitted24h counts retrieve_tools responses that carried a
	// spec-094 filter_diagnostics block in the last 24h. Spec 094 shipped with
	// zero usage measurement; this is the denominator everything else divides by.
	FilterDiagEmitted24h int `json:"filter_diag_emitted_24h"`
	// FilterDiagMissingAnnotation24h is the sum of the per-filter
	// missing_annotation reason counts across every emitted block ("fix the
	// upstream server" class).
	FilterDiagMissingAnnotation24h int `json:"filter_diag_missing_annotation_24h"`
	// FilterDiagExplicit24h is the sum of the per-filter explicit reason counts
	// ("the filter is working as intended" class).
	FilterDiagExplicit24h int `json:"filter_diag_explicit_24h"`
	// FilterDiagFollowed24h counts diagnostics blocks the agent ACTED ON: a
	// later retrieve_tools call in the same MCP session dropped or relaxed at
	// least one of the filters the block blamed. This is the engagement signal —
	// emitted-but-never-followed means the block is being ignored.
	FilterDiagFollowed24h int `json:"filter_diag_followed_24h"`
	// AvailabilityBlock24h counts policy blocks (the "blocked" decision on the
	// single emitActivityPolicyDecision funnel) in the last 24h.
	AvailabilityBlock24h int `json:"availability_block_24h"`
	// AvailabilityBlockReasons24h splits AvailabilityBlock24h by the closed
	// reason enum. Sparse: reasons with a zero count are omitted.
	AvailabilityBlockReasons24h map[string]int `json:"availability_block_reasons_24h,omitempty"`
	// DiscoveryOmission24h counts retrieve_tools responses that silently
	// withheld locked/quarantined matches from the caller (include_disabled
	// unset). This is the substrate for the preflight silent-unavailability
	// metric: how often an agent was told "no such tool" when the tool exists.
	DiscoveryOmission24h int `json:"discovery_omission_24h"`
}

// isZero reports whether nothing at all was recorded, in which case the
// heartbeat omits the whole sub-object (same posture as DiagnosticsCounters).
func (p PreflightCounters) isZero() bool {
	if p.FilterDiagEmitted24h != 0 ||
		p.FilterDiagMissingAnnotation24h != 0 ||
		p.FilterDiagExplicit24h != 0 ||
		p.FilterDiagFollowed24h != 0 ||
		p.AvailabilityBlock24h != 0 ||
		p.DiscoveryOmission24h != 0 {
		return false
	}
	for _, n := range p.AvailabilityBlockReasons24h {
		if n != 0 {
			return false
		}
	}
	return true
}

// MarshalJSON drops any reason key outside the closed enum and caps the map to
// maxPreflightReasonEntries before serialising. Both guards are wire-form
// backstops for the producer-side filtering in RecordAvailabilityBlock.
func (p PreflightCounters) MarshalJSON() ([]byte, error) {
	counts := p.AvailabilityBlockReasons24h
	if len(counts) > 0 {
		filtered := make(map[string]int, len(counts))
		for k, v := range counts {
			if IsAvailabilityBlockReason(k) {
				filtered[k] = v
			}
		}
		counts = filtered
	}
	if len(counts) > maxPreflightReasonEntries {
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
			return entries[i].k < entries[j].k // tie-break by key asc
		})
		counts = make(map[string]int, maxPreflightReasonEntries)
		for _, e := range entries[:maxPreflightReasonEntries] {
			counts[e.k] = e.v
		}
	}
	if len(counts) == 0 {
		counts = nil
	}
	type wire struct {
		FilterDiagEmitted24h           int            `json:"filter_diag_emitted_24h"`
		FilterDiagMissingAnnotation24h int            `json:"filter_diag_missing_annotation_24h"`
		FilterDiagExplicit24h          int            `json:"filter_diag_explicit_24h"`
		FilterDiagFollowed24h          int            `json:"filter_diag_followed_24h"`
		AvailabilityBlock24h           int            `json:"availability_block_24h"`
		AvailabilityBlockReasons24h    map[string]int `json:"availability_block_reasons_24h,omitempty"`
		DiscoveryOmission24h           int            `json:"discovery_omission_24h"`
	}
	return json.Marshal(wire{
		FilterDiagEmitted24h:           p.FilterDiagEmitted24h,
		FilterDiagMissingAnnotation24h: p.FilterDiagMissingAnnotation24h,
		FilterDiagExplicit24h:          p.FilterDiagExplicit24h,
		FilterDiagFollowed24h:          p.FilterDiagFollowed24h,
		AvailabilityBlock24h:           p.AvailabilityBlock24h,
		AvailabilityBlockReasons24h:    counts,
		DiscoveryOmission24h:           p.DiscoveryOmission24h,
	})
}

// PreflightCounterStore is the persistence contract for the Phase-0 baseline
// counters. Implementations back onto BBolt; every method is individually
// atomic via a bbolt transaction.
type PreflightCounterStore interface {
	// RecordFilterDiagnosticsEmitted increments filter_diag_emitted_24h once
	// and adds the block's per-reason-class counts (both must be >= 0; negative
	// values are clamped to 0).
	RecordFilterDiagnosticsEmitted(db *bbolt.DB, missingAnnotation, explicit int) error

	// RecordFilterDiagnosticsFollowed increments filter_diag_followed_24h — a
	// later call in the same session relaxed a filter the block blamed.
	RecordFilterDiagnosticsFollowed(db *bbolt.DB) error

	// RecordAvailabilityBlock increments availability_block_24h and the
	// per-reason counter. Reasons outside the closed enum are folded into
	// BlockReasonOther, so free text can never become a key.
	RecordAvailabilityBlock(db *bbolt.DB, reason string) error

	// RecordDiscoveryOmission increments discovery_omission_24h.
	RecordDiscoveryOmission(db *bbolt.DB) error

	// Snapshot loads the current counter state, applying 24h decay at now.
	Snapshot(db *bbolt.DB) (PreflightCounters, error)
}

// bboltPreflightCounterStore is the production BBolt-backed implementation.
// Zero-value is ready to use; no initialisation required.
type bboltPreflightCounterStore struct{}

// NewPreflightCounterStore returns a BBolt-backed PreflightCounterStore.
func NewPreflightCounterStore() PreflightCounterStore {
	return bboltPreflightCounterStore{}
}

// EnsurePreflightCountersBucket pre-creates the bucket to avoid write-races on
// first use. Safe to call multiple times.
func EnsurePreflightCountersBucket(db *bbolt.DB) error {
	if db == nil {
		return fmt.Errorf("nil db")
	}
	return db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(PreflightCountersBucketName))
		return err
	})
}

// --- bucket helpers ---

func preflightBucket(tx *bbolt.Tx) *bbolt.Bucket {
	return tx.Bucket([]byte(PreflightCountersBucketName))
}

func preflightBucketForWrite(tx *bbolt.Tx) (*bbolt.Bucket, error) {
	return tx.CreateBucketIfNotExists([]byte(PreflightCountersBucketName))
}

// bumpPreflightCounter adds n (clamped at >= 0) to the 24h counter at key,
// rolling the window when it has expired.
func bumpPreflightCounter(b *bbolt.Bucket, key string, n int, now time.Time) error {
	if n <= 0 {
		return nil
	}
	count, windowStart, _ := readCounterWithDecay(b.Get([]byte(key)), now)
	count += uint64(n)
	return b.Put([]byte(key), encodeCounter(count, windowStart))
}

// --- RecordFilterDiagnosticsEmitted ---

func (bboltPreflightCounterStore) RecordFilterDiagnosticsEmitted(db *bbolt.DB, missingAnnotation, explicit int) error {
	if db == nil {
		return nil
	}
	now := time.Now()
	return db.Update(func(tx *bbolt.Tx) error {
		b, err := preflightBucketForWrite(tx)
		if err != nil {
			return err
		}
		if err := bumpPreflightCounter(b, preflightKeyFilterDiagEmitted24h, 1, now); err != nil {
			return err
		}
		if err := bumpPreflightCounter(b, preflightKeyFilterDiagMissing24h, missingAnnotation, now); err != nil {
			return err
		}
		return bumpPreflightCounter(b, preflightKeyFilterDiagExplicit24h, explicit, now)
	})
}

// --- RecordFilterDiagnosticsFollowed ---

func (bboltPreflightCounterStore) RecordFilterDiagnosticsFollowed(db *bbolt.DB) error {
	if db == nil {
		return nil
	}
	now := time.Now()
	return db.Update(func(tx *bbolt.Tx) error {
		b, err := preflightBucketForWrite(tx)
		if err != nil {
			return err
		}
		return bumpPreflightCounter(b, preflightKeyFilterDiagFollowed24h, 1, now)
	})
}

// --- RecordAvailabilityBlock ---

func (bboltPreflightCounterStore) RecordAvailabilityBlock(db *bbolt.DB, reason string) error {
	if db == nil {
		return nil
	}
	key := NormalizeAvailabilityBlockReason(reason)
	now := time.Now()
	return db.Update(func(tx *bbolt.Tx) error {
		b, err := preflightBucketForWrite(tx)
		if err != nil {
			return err
		}
		if err := bumpPreflightCounter(b, preflightKeyAvailabilityBlock24h, 1, now); err != nil {
			return err
		}
		// Write-side cardinality backstop: a key the bucket has never seen is
		// only admitted while the distinct-key budget holds; past it, the count
		// still lands (in the overflow bucket) but the key set cannot grow.
		if key != BlockReasonOther {
			reasonKey := preflightKeyAvailabilityReasonPfx + key
			if b.Get([]byte(reasonKey)) == nil && countPreflightReasonKeys(b) >= maxAvailabilityReasonKeys {
				key = BlockReasonOther
			}
		}
		return bumpPreflightCounter(b, preflightKeyAvailabilityReasonPfx+key, 1, now)
	})
}

// countPreflightReasonKeys returns how many distinct reason keys the bucket
// currently holds (decayed-but-not-yet-deleted keys included — the budget is
// about key-set growth, not live counts).
func countPreflightReasonKeys(b *bbolt.Bucket) int {
	n := 0
	prefix := []byte(preflightKeyAvailabilityReasonPfx)
	c := b.Cursor()
	for k, _ := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), preflightKeyAvailabilityReasonPfx); k, _ = c.Next() {
		n++
	}
	return n
}

// --- RecordDiscoveryOmission ---

func (bboltPreflightCounterStore) RecordDiscoveryOmission(db *bbolt.DB) error {
	if db == nil {
		return nil
	}
	now := time.Now()
	return db.Update(func(tx *bbolt.Tx) error {
		b, err := preflightBucketForWrite(tx)
		if err != nil {
			return err
		}
		return bumpPreflightCounter(b, preflightKeyDiscoveryOmission24h, 1, now)
	})
}

// --- Snapshot ---

func (bboltPreflightCounterStore) Snapshot(db *bbolt.DB) (PreflightCounters, error) {
	return snapshotPreflightAt(db, time.Now())
}

func snapshotPreflightAt(db *bbolt.DB, now time.Time) (PreflightCounters, error) {
	var out PreflightCounters
	if db == nil {
		return out, nil
	}
	err := db.View(func(tx *bbolt.Tx) error {
		b := preflightBucket(tx)
		if b == nil {
			return nil // bucket absent → all zero
		}

		read := func(key string) int {
			raw := b.Get([]byte(key))
			if len(raw) < 16 {
				return 0
			}
			cnt, _, _ := readCounterWithDecay(raw, now)
			return int(cnt)
		}

		out.FilterDiagEmitted24h = read(preflightKeyFilterDiagEmitted24h)
		out.FilterDiagMissingAnnotation24h = read(preflightKeyFilterDiagMissing24h)
		out.FilterDiagExplicit24h = read(preflightKeyFilterDiagExplicit24h)
		out.FilterDiagFollowed24h = read(preflightKeyFilterDiagFollowed24h)
		out.AvailabilityBlock24h = read(preflightKeyAvailabilityBlock24h)
		out.DiscoveryOmission24h = read(preflightKeyDiscoveryOmission24h)

		// per-reason 24h counts
		c := b.Cursor()
		prefix := []byte(preflightKeyAvailabilityReasonPfx)
		for k, v := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), preflightKeyAvailabilityReasonPfx); k, v = c.Next() {
			reason := strings.TrimPrefix(string(k), preflightKeyAvailabilityReasonPfx)
			// Read-side twin of the producer guard: a key that is not a member
			// of the closed enum never becomes part of the snapshot.
			if !IsAvailabilityBlockReason(reason) {
				continue
			}
			if len(v) < 16 {
				continue
			}
			cnt, _, _ := readCounterWithDecay(v, now)
			if cnt > 0 {
				if out.AvailabilityBlockReasons24h == nil {
					out.AvailabilityBlockReasons24h = make(map[string]int)
				}
				out.AvailabilityBlockReasons24h[reason] = int(cnt)
			}
		}
		return nil
	})
	return out, err
}
