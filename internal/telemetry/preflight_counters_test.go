package telemetry

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.etcd.io/bbolt"
)

// newTestPreflightDB creates a temporary BBolt DB for preflight counter tests.
func newTestPreflightDB(t *testing.T) (*bbolt.DB, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "preflight_test.db")
	db, err := bbolt.Open(path, 0600, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("bbolt.Open: %v", err)
	}
	return db, func() { _ = db.Close() }
}

// P001: an empty DB snapshots to the zero value (and is therefore omitted from
// the heartbeat).
func TestPreflightCounterStore_Empty(t *testing.T) {
	db, cleanup := newTestPreflightDB(t)
	defer cleanup()

	var s bboltPreflightCounterStore
	snap, err := s.Snapshot(db)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !snap.isZero() {
		t.Fatalf("expected zero snapshot on empty DB, got %+v", snap)
	}
}

// P002: filter-diagnostics emission bumps the response counter once and adds
// the per-reason-class counts.
func TestPreflightCounterStore_FilterDiagnosticsEmitted(t *testing.T) {
	db, cleanup := newTestPreflightDB(t)
	defer cleanup()

	var s bboltPreflightCounterStore
	if err := s.RecordFilterDiagnosticsEmitted(db, 3, 2); err != nil {
		t.Fatalf("RecordFilterDiagnosticsEmitted: %v", err)
	}
	if err := s.RecordFilterDiagnosticsEmitted(db, 1, 0); err != nil {
		t.Fatalf("RecordFilterDiagnosticsEmitted: %v", err)
	}

	snap, err := s.Snapshot(db)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.FilterDiagEmitted24h != 2 {
		t.Errorf("FilterDiagEmitted24h = %d, want 2", snap.FilterDiagEmitted24h)
	}
	if snap.FilterDiagMissingAnnotation24h != 4 {
		t.Errorf("FilterDiagMissingAnnotation24h = %d, want 4", snap.FilterDiagMissingAnnotation24h)
	}
	if snap.FilterDiagExplicit24h != 2 {
		t.Errorf("FilterDiagExplicit24h = %d, want 2", snap.FilterDiagExplicit24h)
	}
}

// P003: a block with zero on one reason class still counts the emission, and
// negative inputs (a caller bug) can never decrement a counter.
func TestPreflightCounterStore_EmittedClampsNegatives(t *testing.T) {
	db, cleanup := newTestPreflightDB(t)
	defer cleanup()

	var s bboltPreflightCounterStore
	_ = s.RecordFilterDiagnosticsEmitted(db, 5, 5)
	_ = s.RecordFilterDiagnosticsEmitted(db, -100, -100)

	snap, _ := s.Snapshot(db)
	if snap.FilterDiagEmitted24h != 2 {
		t.Errorf("FilterDiagEmitted24h = %d, want 2", snap.FilterDiagEmitted24h)
	}
	if snap.FilterDiagMissingAnnotation24h != 5 || snap.FilterDiagExplicit24h != 5 {
		t.Errorf("negative deltas must be dropped, got missing=%d explicit=%d",
			snap.FilterDiagMissingAnnotation24h, snap.FilterDiagExplicit24h)
	}
}

// P004: followed / discovery-omission increment paths.
func TestPreflightCounterStore_FollowedAndOmission(t *testing.T) {
	db, cleanup := newTestPreflightDB(t)
	defer cleanup()

	var s bboltPreflightCounterStore
	for i := 0; i < 4; i++ {
		if err := s.RecordFilterDiagnosticsFollowed(db); err != nil {
			t.Fatalf("RecordFilterDiagnosticsFollowed: %v", err)
		}
	}
	for i := 0; i < 7; i++ {
		if err := s.RecordDiscoveryOmission(db); err != nil {
			t.Fatalf("RecordDiscoveryOmission: %v", err)
		}
	}

	snap, _ := s.Snapshot(db)
	if snap.FilterDiagFollowed24h != 4 {
		t.Errorf("FilterDiagFollowed24h = %d, want 4", snap.FilterDiagFollowed24h)
	}
	if snap.DiscoveryOmission24h != 7 {
		t.Errorf("DiscoveryOmission24h = %d, want 7", snap.DiscoveryOmission24h)
	}
}

// P005: availability blocks increment the total and the per-reason map.
func TestPreflightCounterStore_AvailabilityBlockByReason(t *testing.T) {
	db, cleanup := newTestPreflightDB(t)
	defer cleanup()

	var s bboltPreflightCounterStore
	_ = s.RecordAvailabilityBlock(db, BlockReasonServerQuarantined)
	_ = s.RecordAvailabilityBlock(db, BlockReasonServerQuarantined)
	_ = s.RecordAvailabilityBlock(db, BlockReasonTokenScope)

	snap, _ := s.Snapshot(db)
	if snap.AvailabilityBlock24h != 3 {
		t.Errorf("AvailabilityBlock24h = %d, want 3", snap.AvailabilityBlock24h)
	}
	if got := snap.AvailabilityBlockReasons24h[BlockReasonServerQuarantined]; got != 2 {
		t.Errorf("server_quarantined = %d, want 2", got)
	}
	if got := snap.AvailabilityBlockReasons24h[BlockReasonTokenScope]; got != 1 {
		t.Errorf("token_scope = %d, want 1", got)
	}
}

// P006: a reason OUTSIDE the closed enum — e.g. the operator-facing prose that
// embeds a server name — is folded into "other" and never becomes a key.
func TestPreflightCounterStore_UnknownReasonFoldedIntoOther(t *testing.T) {
	db, cleanup := newTestPreflightDB(t)
	defer cleanup()

	var s bboltPreflightCounterStore
	leaky := []string{
		"",
		"Server 'acme-internal' is not in scope for this agent token",
		"/Users/algis/.mcpproxy/config.db",
		"SERVER_QUARANTINED",
	}
	for _, r := range leaky {
		if err := s.RecordAvailabilityBlock(db, r); err != nil {
			t.Fatalf("RecordAvailabilityBlock(%q): %v", r, err)
		}
	}

	snap, _ := s.Snapshot(db)
	if snap.AvailabilityBlock24h != len(leaky) {
		t.Errorf("AvailabilityBlock24h = %d, want %d", snap.AvailabilityBlock24h, len(leaky))
	}
	if got := snap.AvailabilityBlockReasons24h[BlockReasonOther]; got != len(leaky) {
		t.Errorf("other = %d, want %d", got, len(leaky))
	}
	for key := range snap.AvailabilityBlockReasons24h {
		if !IsAvailabilityBlockReason(key) {
			t.Errorf("non-enum key %q leaked into the snapshot", key)
		}
	}
}

// P007: 24h windowing — a counter whose window opened more than 24h ago reads
// as zero (same decay contract as the Phase H counters).
func TestPreflightCounterStore_24hDecay(t *testing.T) {
	db, cleanup := newTestPreflightDB(t)
	defer cleanup()

	pastStart := time.Now().Add(-25 * time.Hour)
	err := db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(PreflightCountersBucketName))
		if err != nil {
			return err
		}
		if err := b.Put([]byte(preflightKeyFilterDiagEmitted24h), encodeCounter(42, pastStart.Unix())); err != nil {
			return err
		}
		if err := b.Put([]byte(preflightKeyDiscoveryOmission24h), encodeCounter(17, pastStart.Unix())); err != nil {
			return err
		}
		// availability_block_24h has no storage key of its own — it is the sum
		// of the reason keys, so seeding a stale reason covers it.
		return b.Put([]byte(preflightKeyAvailabilityReasonPfx+BlockReasonToolNotCallable),
			encodeCounter(9, pastStart.Unix()))
	})
	if err != nil {
		t.Fatalf("seeding stale counters: %v", err)
	}

	var s bboltPreflightCounterStore
	snap, err := s.Snapshot(db)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !snap.isZero() {
		t.Fatalf("expected everything decayed to zero, got %+v", snap)
	}
	if _, ok := snap.AvailabilityBlockReasons24h[BlockReasonToolNotCallable]; ok {
		t.Errorf("decayed reason key must not appear in the snapshot")
	}
}

// P008: 24h windowing — a stale window ROLLS on the next write instead of
// accumulating on top of the expired count.
func TestPreflightCounterStore_StaleWindowRolls(t *testing.T) {
	db, cleanup := newTestPreflightDB(t)
	defer cleanup()

	pastStart := time.Now().Add(-30 * time.Hour)
	err := db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(PreflightCountersBucketName))
		if err != nil {
			return err
		}
		return b.Put([]byte(preflightKeyFilterDiagFollowed24h), encodeCounter(1000, pastStart.Unix()))
	})
	if err != nil {
		t.Fatalf("seeding stale counter: %v", err)
	}

	var s bboltPreflightCounterStore
	if err := s.RecordFilterDiagnosticsFollowed(db); err != nil {
		t.Fatalf("RecordFilterDiagnosticsFollowed: %v", err)
	}
	snap, _ := s.Snapshot(db)
	if snap.FilterDiagFollowed24h != 1 {
		t.Errorf("FilterDiagFollowed24h = %d, want 1 (window must roll, not accumulate)", snap.FilterDiagFollowed24h)
	}
}

// P009: a counter written INSIDE the window survives.
func TestPreflightCounterStore_WithinWindowSurvives(t *testing.T) {
	db, cleanup := newTestPreflightDB(t)
	defer cleanup()

	recentStart := time.Now().Add(-23 * time.Hour)
	err := db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(PreflightCountersBucketName))
		if err != nil {
			return err
		}
		return b.Put([]byte(preflightKeyDiscoveryOmission24h), encodeCounter(5, recentStart.Unix()))
	})
	if err != nil {
		t.Fatalf("seeding counter: %v", err)
	}

	var s bboltPreflightCounterStore
	if err := s.RecordDiscoveryOmission(db); err != nil {
		t.Fatalf("RecordDiscoveryOmission: %v", err)
	}
	snap, _ := s.Snapshot(db)
	if snap.DiscoveryOmission24h != 6 {
		t.Errorf("DiscoveryOmission24h = %d, want 6", snap.DiscoveryOmission24h)
	}
}

// P010: write-side key cap — once the bucket holds maxAvailabilityReasonKeys
// distinct reason keys, a further NEW key folds into "other" instead of growing
// the key set. (The allow-list already bounds this; the cap is the backstop for
// a bucket written by a future build with a wider enum.)
func TestPreflightCounterStore_ReasonKeyCapEnforced(t *testing.T) {
	db, cleanup := newTestPreflightDB(t)
	defer cleanup()

	// Seed the bucket to the cap with synthetic keys (as a newer build might).
	err := db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(PreflightCountersBucketName))
		if err != nil {
			return err
		}
		for i := 0; i < maxAvailabilityReasonKeys; i++ {
			key := fmt.Sprintf("%sfuture_reason_%02d", preflightKeyAvailabilityReasonPfx, i)
			if err := b.Put([]byte(key), encodeCounter(1, time.Now().Unix())); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seeding reason keys: %v", err)
	}

	var s bboltPreflightCounterStore
	if err := s.RecordAvailabilityBlock(db, BlockReasonProfileScope); err != nil {
		t.Fatalf("RecordAvailabilityBlock: %v", err)
	}

	// The new key must NOT have been created; the count went to "other".
	err = db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(PreflightCountersBucketName))
		if raw := b.Get([]byte(preflightKeyAvailabilityReasonPfx + BlockReasonProfileScope)); raw != nil {
			t.Errorf("key set grew past the cap: profile_scope key was created")
		}
		if raw := b.Get([]byte(preflightKeyAvailabilityReasonPfx + BlockReasonOther)); raw == nil {
			t.Errorf("overflow bucket 'other' was not written")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	snap, _ := s.Snapshot(db)
	if snap.AvailabilityBlock24h != 1 {
		t.Errorf("AvailabilityBlock24h = %d, want 1", snap.AvailabilityBlock24h)
	}
	if got := snap.AvailabilityBlockReasons24h[BlockReasonOther]; got != 1 {
		t.Errorf("other = %d, want 1", got)
	}
	// The synthetic (non-enum) keys are filtered out on the read side too.
	for key := range snap.AvailabilityBlockReasons24h {
		if strings.HasPrefix(key, "future_reason_") {
			t.Errorf("non-enum key %q leaked into the snapshot", key)
		}
	}
}

// P011: MarshalJSON caps the reason map to maxPreflightReasonEntries and drops
// any key outside the closed enum — the wire-form backstop.
func TestPreflightCounters_MarshalJSON_CapsAndFilters(t *testing.T) {
	counts := make(map[string]int, len(availabilityBlockReasonKeys)+5)
	for i, k := range availabilityBlockReasonKeys {
		counts[k] = 100 - i
	}
	// Keys that must never reach the wire.
	counts["Server 'acme' is not in scope"] = 999
	counts["/Users/algis/secret"] = 998

	p := PreflightCounters{AvailabilityBlockReasons24h: counts}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var out struct {
		Reasons map[string]int `json:"availability_block_reasons_24h"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(out.Reasons) > maxPreflightReasonEntries {
		t.Errorf("map has %d entries, cap is %d", len(out.Reasons), maxPreflightReasonEntries)
	}
	for key := range out.Reasons {
		if !IsAvailabilityBlockReason(key) {
			t.Errorf("non-enum key %q reached the wire", key)
		}
	}
	if strings.Contains(string(raw), "acme") || strings.Contains(string(raw), "/Users/") {
		t.Errorf("PII leaked into the wire form: %s", raw)
	}
}

// P012: MarshalJSON caps deterministically (top-N by count desc, key asc).
func TestPreflightCounters_MarshalJSON_Deterministic(t *testing.T) {
	counts := make(map[string]int, len(availabilityBlockReasonKeys))
	for _, k := range availabilityBlockReasonKeys {
		counts[k] = 10 // pure alphabetic tie-break
	}
	p := PreflightCounters{AvailabilityBlock24h: 120, AvailabilityBlockReasons24h: counts}

	var results [3][]byte
	for i := range results {
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("MarshalJSON run %d: %v", i, err)
		}
		results[i] = raw
	}
	for i := 1; i < len(results); i++ {
		if string(results[i]) != string(results[0]) {
			t.Errorf("MarshalJSON is non-deterministic: run 0 != run %d", i)
		}
	}
}

// P013: the sub-object is omitted from the wire when nothing was recorded, and
// carries every documented key when it is present.
func TestPreflightCounters_IsZeroAndWireKeys(t *testing.T) {
	var zero PreflightCounters
	if !zero.isZero() {
		t.Fatalf("zero value must report isZero")
	}
	// A reason key with a zero count is still "nothing recorded".
	zero.AvailabilityBlockReasons24h = map[string]int{BlockReasonOther: 0}
	if !zero.isZero() {
		t.Fatalf("all-zero reason map must still report isZero")
	}

	full := PreflightCounters{
		FilterDiagEmitted24h:           1,
		FilterDiagMissingAnnotation24h: 2,
		FilterDiagExplicit24h:          3,
		FilterDiagFollowed24h:          4,
		AvailabilityBlock24h:           5,
		AvailabilityBlockReasons24h:    map[string]int{BlockReasonToolChanged: 5},
		DiscoveryOmission24h:           6,
	}
	if full.isZero() {
		t.Fatalf("populated counters must not report isZero")
	}
	raw, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, key := range []string{
		"filter_diag_emitted_24h",
		"filter_diag_missing_annotation_24h",
		"filter_diag_explicit_24h",
		"filter_diag_followed_24h",
		"availability_block_24h",
		"availability_block_reasons_24h",
		"discovery_omission_24h",
	} {
		if _, ok := out[key]; !ok {
			t.Errorf("wire form is missing %q", key)
		}
	}
}

// P014: the store never persists anything that looks like an identity, even
// when every enum member is exercised.
func TestPreflightCounters_NoLeakPII(t *testing.T) {
	db, cleanup := newTestPreflightDB(t)
	defer cleanup()

	var s bboltPreflightCounterStore
	for _, reason := range availabilityBlockReasonKeys {
		_ = s.RecordAvailabilityBlock(db, reason)
	}
	_ = s.RecordFilterDiagnosticsEmitted(db, 2, 3)
	_ = s.RecordFilterDiagnosticsFollowed(db)
	_ = s.RecordDiscoveryOmission(db)

	snap, _ := s.Snapshot(db)
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	js := string(raw)
	for _, forbidden := range []string{
		"/home/", "/Users/", "C:\\",
		"localhost", "127.0.0.1",
		"password", "secret", "token:",
	} {
		if strings.Contains(js, forbidden) {
			t.Errorf("PII leak: JSON contains %q\nJSON: %s", forbidden, js)
		}
	}
	envelope := []byte(`{"preflight":` + js + `}`)
	if err := ScanForPII(envelope); err != nil {
		t.Errorf("preflight sub-object must pass the anonymity scanner: %v", err)
	}
}

// P015: the anonymity scanner rejects a preflight sub-object whose reason map
// carries a key outside the closed enum — the wire-form backstop for a producer
// regression that let operator prose (server/tool names) become a key.
func TestScanForPII_RejectsNonEnumPreflightReasonKey(t *testing.T) {
	payload := []byte(`{"preflight":{"availability_block_24h":1,` +
		`"availability_block_reasons_24h":{"server acme-internal is quarantined":1}}}`)
	err := ScanForPII(payload)
	if err == nil {
		t.Fatalf("expected an anonymity violation for a non-enum reason key")
	}
	if !strings.Contains(err.Error(), "availability_block_reasons_24h") {
		t.Errorf("violation should name the offending field, got: %v", err)
	}
	if strings.Contains(err.Error(), "acme-internal") {
		t.Errorf("violation must never echo the offending key: %v", err)
	}
}

// P016: the scanner also rejects non-integer / negative preflight scalars.
func TestScanForPII_RejectsMalformedPreflightScalars(t *testing.T) {
	cases := []string{
		`{"preflight":{"filter_diag_emitted_24h":-1}}`,
		`{"preflight":{"discovery_omission_24h":"3"}}`,
		`{"preflight":{"availability_block_24h":1.5}}`,
		`{"preflight":null}`,
	}
	for _, payload := range cases {
		if err := ScanForPII([]byte(payload)); err == nil {
			t.Errorf("expected a violation for %s", payload)
		}
	}
}

// P016b: the preflight sub-object is CLOSED — a key outside the documented
// counter set is rejected before transmit, whatever it carries. This is the
// backstop for a future field that smuggles free text (a server name, a query,
// an error message) into a sub-object whose whole contract is "counts only".
func TestScanForPII_RejectsUnknownPreflightKey(t *testing.T) {
	cases := []struct {
		payload string
		// mustNotEcho is content the violation message may never repeat. The
		// generic prefix/regex rules run first and legitimately name their own
		// literal pattern, so only rule-7-specific cases assert this.
		mustNotEcho string
	}{
		{`{"preflight":{"availability_block_24h":1,"last_blocked_server":"acme-internal"}}`, "acme-internal"},
		{`{"preflight":{"availability_block_24h":1,"top_query":"list the deploy keys"}}`, "deploy keys"},
		{`{"preflight":{"filter_diag_emitted_24h":1,"filter_diag_emitted_48h":2}}`, ""},
	}
	for _, tc := range cases {
		err := ScanForPII([]byte(tc.payload))
		if err == nil {
			t.Errorf("expected a violation for an unknown preflight key: %s", tc.payload)
			continue
		}
		if tc.mustNotEcho != "" && strings.Contains(err.Error(), tc.mustNotEcho) {
			t.Errorf("violation must never echo the offending content: %v", err)
		}
	}
}

// P016c: the closed-key set is exactly what MarshalJSON emits, so a populated
// payload can never trip the rule it is guarded by.
func TestScanForPII_PreflightAllowedKeysMatchWireForm(t *testing.T) {
	full := PreflightCounters{
		FilterDiagEmitted24h:           1,
		FilterDiagMissingAnnotation24h: 2,
		FilterDiagExplicit24h:          3,
		FilterDiagFollowed24h:          4,
		AvailabilityBlock24h:           5,
		AvailabilityBlockReasons24h:    map[string]int{BlockReasonOther: 5},
		DiscoveryOmission24h:           6,
	}
	raw, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for key := range out {
		if !isPreflightAllowedKey(key) {
			t.Errorf("wire form emits %q, which the scanner would reject", key)
		}
	}
	if len(out) != len(preflightAllowedKeys) {
		t.Errorf("wire form has %d keys, allow-list has %d — they must stay in sync",
			len(out), len(preflightAllowedKeys))
	}
	if err := ScanForPII([]byte(`{"preflight":` + string(raw) + `}`)); err != nil {
		t.Errorf("fully populated preflight must pass the scanner: %v", err)
	}
}

// P017: a well-formed preflight sub-object passes the scanner untouched.
func TestScanForPII_AcceptsWellFormedPreflight(t *testing.T) {
	payload := []byte(`{"preflight":{"filter_diag_emitted_24h":3,` +
		`"filter_diag_missing_annotation_24h":7,"filter_diag_explicit_24h":2,` +
		`"filter_diag_followed_24h":1,"availability_block_24h":4,` +
		`"availability_block_reasons_24h":{"server_quarantined":3,"token_scope":1},` +
		`"discovery_omission_24h":9}}`)
	if err := ScanForPII(payload); err != nil {
		t.Errorf("well-formed preflight must pass, got: %v", err)
	}
}

// The total and its per-reason split must never disagree. Each counter key
// carries its OWN window start, so a total whose window opened earlier can
// decay to zero while a reason key that started later survives — producing a
// payload whose reason counts sum to more than the total they supposedly split
// (opencode review, round 4, finding 3).
func TestPreflightSnapshot_TotalAlwaysMatchesReasonSplit(t *testing.T) {
	db, cleanup := newTestPreflightDB(t)
	defer cleanup()

	now := time.Now()
	// reason A's window opened 25h ago (expired); reason B's opened 2h ago.
	expired := now.Add(-25 * time.Hour)
	recent := now.Add(-2 * time.Hour)

	err := db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(PreflightCountersBucketName))
		if err != nil {
			return err
		}
		if err := b.Put([]byte(preflightKeyAvailabilityReasonPfx+BlockReasonServerQuarantined),
			encodeCounter(1, expired.Unix())); err != nil {
			return err
		}
		return b.Put([]byte(preflightKeyAvailabilityReasonPfx+BlockReasonTokenScope),
			encodeCounter(1, recent.Unix()))
	})
	if err != nil {
		t.Fatalf("seeding counters: %v", err)
	}

	var s bboltPreflightCounterStore
	snap, err := s.Snapshot(db)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	sum := 0
	for _, n := range snap.AvailabilityBlockReasons24h {
		sum += n
	}
	if snap.AvailabilityBlock24h != sum {
		t.Errorf("availability_block_24h = %d but its reason split sums to %d; "+
			"the total and the split must be the same number",
			snap.AvailabilityBlock24h, sum)
	}
	// The expired reason is gone, the recent one survives.
	if sum != 1 {
		t.Errorf("reason split sums to %d, want 1 (the expired reason must decay out)", sum)
	}
}
