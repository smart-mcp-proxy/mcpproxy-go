package cache

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.etcd.io/bbolt"
	"go.uber.org/zap"
)

// Spec 105 FR-002 (gap FR001-G1): an entry with absent, legacy or unrecognised
// provenance — every entry persisted before producer stamping existed, and
// anything still written through Store — is refused for EVERY caller kind,
// administrators and the administrator-shaped anonymous caller included
// (research D2, SC-004), and is durably invalidated on that first refused
// redemption: gone from Peek, gone after the bbolt file is closed and
// reopened. Before this feature such an entry was readable by any
// unrestricted kind and survived every refusal.

// openManagerAt opens (or reopens) a cache manager on the bbolt file at path.
// Tests that prove durability close the first handle and reopen the same file
// — the only way to tell a committed delete from one bbolt rolled back.
func openManagerAt(t *testing.T, path string) (*Manager, *bbolt.DB) {
	t.Helper()
	db, err := bbolt.Open(path, 0644, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatalf("open bbolt %s: %v", path, err)
	}
	m, err := NewManager(db, zap.NewNop())
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	return m, db
}

// onDiskEntryCount counts the records physically present in the cache bucket
// in a fresh read transaction — the committed state, independent of whatever
// the in-memory stats believe.
func onDiskEntryCount(t *testing.T, db *bbolt.DB) int {
	t.Helper()
	n := 0
	err := db.View(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(CacheBucket)).ForEach(func(_, _ []byte) error {
			n++
			return nil
		})
	})
	if err != nil {
		t.Fatalf("count cache bucket: %v", err)
	}
	return n
}

// putRawRecord writes an arbitrary JSON document under key straight into the
// cache bucket, bypassing every Store path — the shape a pre-feature binary
// left on disk (no producer field, no version field) or a record a later
// schema wrote (a version this binary does not recognise).
func putRawRecord(t *testing.T, db *bbolt.DB, key string, doc map[string]interface{}) {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(CacheBucket)).Put([]byte(key), data)
	}); err != nil {
		t.Fatalf("put raw record: %v", err)
	}
}

// legacyReaders enumerates every caller kind the gate can see. FR-002 names
// them all: "every caller" includes administrators and anonymous callers.
func legacyReaders() []struct {
	name   string
	reader Authorization
} {
	return []struct {
		name   string
		reader Authorization
	}{
		{"admin", Authorization{CallerKind: CallerKindAdmin}},
		{"admin_user", Authorization{CallerKind: CallerKindAdminUser, Principal: "u9"}},
		{"anonymous", Authorization{CallerKind: CallerKindAnonymous}},
		{"wildcard agent", Authorization{CallerKind: CallerKindAgent, Principal: "star",
			AllowedServers: []string{"*"}, Permissions: []string{"read", "write", "destructive"}}},
		{"user", Authorization{CallerKind: CallerKindUser, Principal: "u1"}},
	}
}

func TestGetRecordsAs_LegacyEntryRefusedForEveryCallerAndInvalidated(t *testing.T) {
	for _, tc := range legacyReaders() {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cache.db")
			m, db := openManagerAt(t, path)

			// Store (no producer) is exactly what a pre-stamping binary wrote.
			if err := m.Store("legacy", "github:list", nil, `{"items":[1,2,3]}`, "items", 3); err != nil {
				t.Fatal(err)
			}
			if _, ok := m.Peek("legacy"); !ok {
				t.Fatal("premise: the legacy entry is present before the first redemption")
			}

			resp, err := m.GetRecordsAs("legacy", 0, 10, tc.reader)
			if !errors.Is(err, ErrUnauthorizedRead) {
				t.Fatalf("%s reading a legacy (nil-producer) entry: got err=%v resp=%v, want ErrUnauthorizedRead", tc.name, err, resp)
			}
			if !errors.Is(err, ErrLegacyProvenance) {
				t.Fatalf("%s: got %v, want the ErrLegacyProvenance sentinel the handler renders for administrators", tc.name, err)
			}
			if resp != nil {
				t.Fatalf("%s: a refused legacy read must return no content, got %+v", tc.name, resp)
			}

			// Invalidated on first redemption: the refusal evicts the entry.
			if rec, ok := m.Peek("legacy"); ok {
				t.Fatalf("%s: legacy entry still present after the refused redemption: %+v", tc.name, rec)
			}
			if got := onDiskEntryCount(t, db); got != 0 {
				t.Fatalf("%s: legacy entry still on disk after refusal (count=%d); the delete was rolled back", tc.name, got)
			}
			// Stats mutate only on the committed path and must agree with the
			// bucket: the eviction is one fewer entry, one more eviction.
			stats := *m.GetStats()
			if stats.TotalEntries != 0 || stats.EvictedCount != 1 {
				t.Fatalf("%s: stats after committed eviction = %+v, want TotalEntries=0 EvictedCount=1", tc.name, stats)
			}

			// Durably: still absent after the bbolt file is closed and reopened.
			m.Close()
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			m2, db2 := openManagerAt(t, path)
			defer db2.Close()
			defer m2.Close()
			if rec, ok := m2.Peek("legacy"); ok {
				t.Fatalf("%s: legacy entry survived a reopen: %+v", tc.name, rec)
			}
			if got := onDiskEntryCount(t, db2); got != 0 {
				t.Fatalf("%s: on-disk count after reopen = %d, want 0", tc.name, got)
			}
			// A second redemption of the same key is a plain miss, for every
			// caller — nothing left to disclose.
			if _, err := m2.GetRecordsAs("legacy", 0, 10, tc.reader); err == nil || errors.Is(err, ErrUnauthorizedRead) {
				t.Fatalf("%s: after invalidation the key must be a plain miss, got %v", tc.name, err)
			}
		})
	}
}

// Upgrade fixture (FR-002, SC-004, gap FR001-G7a): a record exactly as a
// pre-feature binary serialised it — no producer field at all, no version
// field (or version 0) — is refused for administrator and agent callers,
// returns no content, and is absent after a restart of the store. A record
// carrying a version this binary does not recognise is treated the same way:
// unknown provenance is legacy provenance.
func TestGetRecordsAs_UpgradeFixturePreFeatureRecordRefusedAndAbsentAfterReopen(t *testing.T) {
	now := time.Now()
	preFeature := map[string]interface{}{
		"key":           "pre-feature",
		"tool_name":     "github:list_issues",
		"args":          map[string]interface{}{"repo": "acme/widgets"},
		"timestamp":     now,
		"full_content":  `{"issues":[{"id":1,"title":"SENTINEL-PRE-FEATURE"},{"id":2,"title":"second"}]}`,
		"record_path":   "issues",
		"total_records": 2,
		"total_size":    80,
		"expires_at":    now.Add(time.Hour),
		"access_count":  0,
		"last_accessed": now,
		"created_at":    now,
		// no "producer", no "version": the pre-feature wire shape
	}
	variant := func(key string, extra map[string]interface{}) map[string]interface{} {
		doc := map[string]interface{}{}
		for k, v := range preFeature {
			doc[k] = v
		}
		doc["key"] = key
		for k, v := range extra {
			doc[k] = v
		}
		return doc
	}

	fixtures := []struct {
		name string
		doc  map[string]interface{}
	}{
		{"no producer, no version", preFeature},
		{"no producer, version 0", variant("version-zero", map[string]interface{}{"version": 0})},
		{"no producer, unrecognised version", variant("unknown-version", map[string]interface{}{"version": 99})},
		// Stamped with a producer but no version: a record from a binary that
		// stamped producers before versions existed is still legacy provenance.
		{"producer stamped, no version", variant("stamped-no-version", map[string]interface{}{
			"producer": map[string]interface{}{"caller_kind": CallerKindAdmin}})},
		// Critique round 1, finding 2: "unrecognised provenance" is decided on
		// the CALLER KIND as well as the version. A kind this binary does not
		// know — an empty stamp, or one a later binary added without bumping
		// RecordVersion and a rollback left behind — is not one an
		// administrator reader may be handed (CouldHaveProduced answers true
		// for any non-internal kind once the reader is an administrator, so
		// an unknown kind failed OPEN for the anonymous and admin readers).
		{"current version, empty caller kind", variant("empty-kind", map[string]interface{}{
			"version": RecordVersion, "producer": map[string]interface{}{"caller_kind": ""}})},
		{"current version, unknown caller kind", variant("unknown-kind", map[string]interface{}{
			"version": RecordVersion, "producer": map[string]interface{}{"caller_kind": "superadmin"}})},
		{"current version, versioned-looking caller kind", variant("future-kind", map[string]interface{}{
			"version": RecordVersion, "producer": map[string]interface{}{"caller_kind": "agent-v2",
				"allowed_servers": []string{"*"}}})},
		// Critique round 1 finding 6 / round 2 finding 6: a record this binary
		// cannot even decode (here: a version that overflows the uint8 field —
		// a downgrade after a future schema bump) is provenance it does not
		// recognise. Before this round it surfaced as a distinct "unmarshal
		// cache record" body for every caller and was never invalidated.
		{"undecodable version", variant("undecodable", map[string]interface{}{"version": 300})},
	}
	readers := []struct {
		name   string
		reader Authorization
	}{
		{"admin", Authorization{CallerKind: CallerKindAdmin}},
		{"anonymous", Authorization{CallerKind: CallerKindAnonymous}},
		{"agent", Authorization{CallerKind: CallerKindAgent, Principal: "bot",
			AllowedServers: []string{"*"}, Permissions: []string{"read", "write", "destructive"}}},
	}

	for _, fx := range fixtures {
		for _, rd := range readers {
			t.Run(fx.name+"/"+rd.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "cache.db")
				m, db := openManagerAt(t, path)
				key := fx.doc["key"].(string)
				putRawRecord(t, db, key, fx.doc)
				if got, want := onDiskEntryCount(t, db), 1; got != want {
					t.Fatalf("premise: the raw record is on disk (count=%d)", got)
				}

				resp, err := m.GetRecordsAs(key, 0, 10, rd.reader)
				if !errors.Is(err, ErrUnauthorizedRead) {
					t.Fatalf("%s reading a pre-feature record: got err=%v, want ErrUnauthorizedRead", rd.name, err)
				}
				// The specific sentinel, not just the parent: the handler keys
				// the administrator's "predates provenance" body on it.
				if !errors.Is(err, ErrLegacyProvenance) {
					t.Fatalf("%s: got %v, want ErrLegacyProvenance", rd.name, err)
				}
				if resp != nil {
					t.Fatalf("refused read returned content: %+v", resp)
				}
				if rec, ok := m.Peek(key); ok {
					t.Fatalf("pre-feature record still present after the refused redemption: %+v", rec)
				}

				m.Close()
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				m2, db2 := openManagerAt(t, path)
				defer db2.Close()
				defer m2.Close()
				if rec, ok := m2.Peek(key); ok {
					t.Fatalf("pre-feature record survived a restart: %+v", rec)
				}
				if got := onDiskEntryCount(t, db2); got != 0 {
					t.Fatalf("on-disk count after restart = %d, want 0", got)
				}
			})
		}
	}
}

// A refused legacy redemption must not disturb its neighbours: only the
// redeemed key is invalidated, and the stats stay consistent with the bucket.
func TestGetRecordsAs_LegacyInvalidationIsPerKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	m, db := openManagerAt(t, path)
	defer db.Close()
	defer m.Close()

	if err := m.Store("legacy-a", "t", nil, `[1]`, "", 1); err != nil {
		t.Fatal(err)
	}
	if err := m.Store("legacy-b", "t", nil, `[2]`, "", 1); err != nil {
		t.Fatal(err)
	}
	admin := Authorization{CallerKind: CallerKindAdmin}
	if err := m.StoreAs("stamped", "t", nil, `[3]`, "", 1, admin); err != nil {
		t.Fatal(err)
	}

	if _, err := m.GetRecordsAs("legacy-a", 0, 10, admin); !errors.Is(err, ErrUnauthorizedRead) {
		t.Fatalf("got %v, want ErrUnauthorizedRead", err)
	}
	if _, ok := m.Peek("legacy-a"); ok {
		t.Fatal("redeemed legacy key must be gone")
	}
	if _, ok := m.Peek("legacy-b"); !ok {
		t.Fatal("an unredeemed legacy key must be untouched")
	}
	if _, ok := m.Peek("stamped"); !ok {
		t.Fatal("a stamped key must be untouched")
	}
	if got, want := onDiskEntryCount(t, db), 2; got != want {
		t.Fatalf("on-disk count = %d, want %d", got, want)
	}
	if got := m.GetStats().TotalEntries; got != 2 {
		t.Fatalf("stats.TotalEntries = %d, want 2 (must match the bucket)", got)
	}
	if _, err := m.GetRecordsAs("stamped", 0, 10, admin); err != nil {
		t.Fatalf("the stamped neighbour must still read: %v", err)
	}
}

// Codex round 2, finding 1: a current-version agent snapshot that could have
// authorized no tool — an empty server grant, or an empty effective profile
// (an empty profile, or the scope a stale pin resolves to) — is not one any
// request legitimately produced an entry under. The reader-side deny-all
// guards (codex round 1) left the PRODUCER side open: containment against an
// empty set is vacuously true, so any broader agent redeemed such a record.
// At the door it must be refused for every agent reader with the
// non-disclosing verdict (a miss to the handler), kept (it is a stamped
// record, not legacy provenance), and still readable by an administrator
// (FR-001, kind first).
func TestGetRecordsAs_DenyAllProducerSnapshotRefusedForAgentReaders(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	m, err := NewManager(db, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	producers := map[string]Authorization{
		"nil-grant":   {CallerKind: CallerKindAgent, Principal: "rotated", AllowedServers: nil, Permissions: []string{"read"}},
		"empty-grant": {CallerKind: CallerKindAgent, Principal: "rotated", AllowedServers: []string{}, Permissions: []string{"read"}},
		"empty-profile": {CallerKind: CallerKindAgent, Principal: "star", AllowedServers: []string{"*"},
			Permissions: []string{"read", "write", "destructive"}, Profile: "empty", ProfileScoped: true, ProfileServers: []string{}},
		"stale-pin": {CallerKind: CallerKindAgent, Principal: "star-pinned", AllowedServers: []string{"*"},
			Permissions: []string{"read", "write", "destructive"}, ProfilePin: "research", Profile: "research",
			ProfileScoped: true, ProfileServers: nil},
	}
	readers := []struct {
		name   string
		reader Authorization
	}{
		{"broad agent", Authorization{CallerKind: CallerKindAgent, Principal: "broad",
			AllowedServers: []string{"github", "weather"}, Permissions: []string{"read", "write"}}},
		{"wildcard agent", Authorization{CallerKind: CallerKindAgent, Principal: "star",
			AllowedServers: []string{"*"}, Permissions: []string{"read", "write", "destructive"}}},
		{"pinned wildcard agent", Authorization{CallerKind: CallerKindAgent, Principal: "star-pinned",
			AllowedServers: []string{"*"}, Permissions: []string{"read", "write", "destructive"},
			ProfilePin: "research", Profile: "research", ProfileScoped: true, ProfileServers: []string{"github"}}},
	}
	for name, producer := range producers {
		if err := m.StoreAs(name, "t", nil, `[{"name":"SENTINEL_DENY_ALL_PRODUCER"}]`, "", 1, producer); err != nil {
			t.Fatal(err)
		}
		for _, rd := range readers {
			t.Run(name+"/"+rd.name, func(t *testing.T) {
				resp, err := m.GetRecordsAs(name, 0, 10, rd.reader)
				if !errors.Is(err, ErrUnauthorizedRead) || resp != nil {
					t.Fatalf("%s redeemed a record stamped with a deny-all producer %+v: resp=%+v err=%v", rd.name, producer, resp, err)
				}
				if _, ok := m.Peek(name); !ok {
					t.Fatal("a refused stamped record must not be evicted")
				}
			})
		}
		t.Run(name+"/admin control", func(t *testing.T) {
			resp, err := m.GetRecordsAs(name, 0, 10, Authorization{CallerKind: CallerKindAdmin})
			if err != nil || len(resp.Records) != 1 {
				t.Fatalf("an administrator qualifies for any snapshot: resp=%+v err=%v", resp, err)
			}
		})
	}
}

// putFramedRecord writes a value in the framed layout with an arbitrary
// header and body — the shapes a later or corrupted binary could leave: a
// header the gate must classify WITHOUT reading the body.
func putFramedRecord(t *testing.T, db *bbolt.DB, key string, header, body []byte) {
	t.Helper()
	if err := db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(CacheBucket)).Put([]byte(key), encodeRecordFrame(header, body))
	}); err != nil {
		t.Fatalf("put framed record: %v", err)
	}
}

// Codex round 2, finding 3: the gate now decides provenance on the frame
// header alone, so the unrecognised-provenance rules the raw-JSON fixtures
// above pin (unknown version, unknown or empty caller kind, undecodable)
// must hold on the HEADER too — a raw fixture is legacy simply for having no
// frame, which would let a header-level check go vacuous. Each shape is
// refused for every caller with ErrLegacyProvenance and durably invalidated;
// the body is never consulted (a body the gate would have admitted sits
// behind every bad header here).
func TestGetRecordsAs_FramedRecordWithUnrecognisedHeaderIsLegacy(t *testing.T) {
	now := time.Now()
	goodBody := func(key string) []byte {
		rec := &Record{Key: key, ToolName: "t", FullContent: `[{"name":"SENTINEL-FRAMED"}]`, TotalSize: 27,
			Timestamp: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now, LastAccessed: now,
			Version: RecordVersion, Producer: &Authorization{CallerKind: CallerKindAdmin}}
		body, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	headerJSON := func(doc map[string]interface{}) []byte {
		data, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	current := map[string]interface{}{"version": RecordVersion, "expires_at": now.Add(time.Hour), "total_size": 27}
	with := func(extra map[string]interface{}) map[string]interface{} {
		doc := map[string]interface{}{}
		for k, v := range current {
			doc[k] = v
		}
		for k, v := range extra {
			doc[k] = v
		}
		return doc
	}

	type fixture struct {
		name   string
		header []byte
		body   func(key string) []byte
		// admittedOnly restricts the readers to the kinds the header admits
		// (the body is reached only after admission).
		admittedOnly bool
	}
	fixtures := []fixture{
		{name: "header: no producer", header: headerJSON(current), body: goodBody},
		{name: "header: unknown version", header: headerJSON(with(map[string]interface{}{"version": 99, "producer": map[string]interface{}{"caller_kind": CallerKindAdmin}})), body: goodBody},
		{name: "header: empty caller kind", header: headerJSON(with(map[string]interface{}{"producer": map[string]interface{}{"caller_kind": ""}})), body: goodBody},
		{name: "header: unknown caller kind", header: headerJSON(with(map[string]interface{}{"producer": map[string]interface{}{"caller_kind": "superadmin"}})), body: goodBody},
		{name: "header: undecodable version", header: headerJSON(with(map[string]interface{}{"version": 300, "producer": map[string]interface{}{"caller_kind": CallerKindAdmin}})), body: goodBody},
		{name: "header: not JSON", header: []byte("not a header"), body: goodBody},
		{name: "header: oversize", header: headerJSON(with(map[string]interface{}{"producer": map[string]interface{}{"caller_kind": CallerKindAdmin},
			"pad": strings.Repeat("x", maxRecordHeaderLen+1)})), body: goodBody},
		{name: "header: length beyond the value"},
		// The one shape the gate admits on the header and only then finds
		// undecodable: still legacy, still invalidated (after admission, so
		// the payload-sized decode is the admitted reader's, not a probe's).
		// Readers the header does NOT admit are refused on the header, with
		// the non-disclosing verdict, and never reach the body.
		{name: "body: undecodable behind an admitted header",
			header:       headerJSON(with(map[string]interface{}{"producer": map[string]interface{}{"caller_kind": CallerKindAdmin}})),
			body:         func(string) []byte { return []byte("{not json") },
			admittedOnly: true},
	}
	for _, fx := range fixtures {
		for _, rd := range legacyReaders() {
			if fx.admittedOnly && !rd.reader.IsAdministrator() {
				continue
			}
			if fx.admittedOnly && rd.reader.CallerKind == CallerKindAnonymous {
				continue // ranks below an authenticated administrator's snapshot
			}
			t.Run(fx.name+"/"+rd.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "cache.db")
				m, db := openManagerAt(t, path)
				const key = "framed"
				if fx.name == "header: length beyond the value" {
					// A frame whose length field promises more header than
					// the value holds.
					if err := db.Update(func(tx *bbolt.Tx) error {
						value := encodeRecordFrame([]byte("{}"), nil)
						value[len(recordFrameMagic)+recordFrameLenSize-1] = 0xff
						return tx.Bucket([]byte(CacheBucket)).Put([]byte(key), value)
					}); err != nil {
						t.Fatal(err)
					}
				} else {
					putFramedRecord(t, db, key, fx.header, fx.body(key))
				}

				resp, err := m.GetRecordsAs(key, 0, 10, rd.reader)
				if !errors.Is(err, ErrLegacyProvenance) {
					t.Fatalf("%s: got err=%v resp=%v, want ErrLegacyProvenance", rd.name, err, resp)
				}
				if resp != nil {
					t.Fatalf("refused read returned content: %+v", resp)
				}
				if _, ok := m.Peek(key); ok {
					t.Fatal("record still present after the refused redemption")
				}
				m.Close()
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				m2, db2 := openManagerAt(t, path)
				defer db2.Close()
				defer m2.Close()
				if got := onDiskEntryCount(t, db2); got != 0 {
					t.Fatalf("on-disk count after restart = %d, want 0", got)
				}
			})
		}
	}
}
