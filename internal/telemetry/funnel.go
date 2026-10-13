package telemetry

import (
	"encoding/binary"
	"time"

	"go.etcd.io/bbolt"
)

// FunnelBucketName is the BBolt bucket that stores Spec 080 US2 funnel
// observability state: the lifetime web-UI open counter, the first-install
// day stamp, and the trailing 30-day active-day set. Only derived integers
// (counter value, whole-day age, day-set cardinality) ever leave the machine;
// the raw structures below are local-only (FR-006..FR-008).
const FunnelBucketName = "telemetry_funnel"

// Fixed keys inside the funnel bucket.
const (
	// funnelKeyWebUIOpened: 8-byte big-endian uint64 lifetime counter of
	// embedded Web UI index-document serves.
	funnelKeyWebUIOpened = "web_ui_opened"
	// funnelKeyFirstInstallDay: 8-byte big-endian int64 UTC day ordinal
	// (Unix seconds / 86400) of the first recorded activity for this data
	// dir. Day granularity only — no timestamp is persisted, and the value
	// is independent of anonymous_id (FR-007).
	funnelKeyFirstInstallDay = "first_install_day"
	// funnelKeyActiveDays: concatenated 8-byte big-endian int64 UTC day
	// ordinals — the compact per-day set behind active_days_30d. Pruned to
	// the trailing window on write; never transmitted (FR-008).
	funnelKeyActiveDays = "active_days"
)

// activeDaysWindow is the trailing window, in whole UTC days, for
// active_days_30d. A day d is in the window at day `now` iff
// now-activeDaysWindow < d <= now, so the surfaced count is 1..30.
const activeDaysWindow = 30

// FunnelState is the read-side snapshot of the funnel bucket, already reduced
// to the payload-safe integers (FR-006..FR-008). The per-day set itself is
// intentionally not part of this struct.
type FunnelState struct {
	// WebUIOpened is the lifetime count of embedded Web UI index serves.
	WebUIOpened int64
	// DaysSinceInstall is the non-negative whole-day age of the install,
	// clamped at 0 on clock skew. Only meaningful when HasInstallDay is true.
	DaysSinceInstall int
	// HasInstallDay reports whether a first-install stamp exists yet.
	HasInstallDay bool
	// ActiveDays30d is the number of distinct active UTC days in the
	// trailing 30-day window (0..30; 0 only before any activity is recorded).
	ActiveDays30d int
}

// FunnelStore is the persistence contract for the funnel bucket. The BBolt
// implementation uses transactional updates so individual calls are atomic.
type FunnelStore interface {
	// IncrementWebUIOpened bumps the lifetime index-serve counter by 1.
	IncrementWebUIOpened(db *bbolt.DB) error

	// RecordActivity stamps the first-install day if absent and adds now's
	// UTC day to the active-day set, pruning members that have aged out of
	// the trailing window. Out-of-order days are tolerated: a day is a set
	// member, not a delta.
	RecordActivity(db *bbolt.DB, now time.Time) error

	// Snapshot reads the payload-safe reduction of the bucket at `now`.
	// Aging is applied at read time as well, so a stale on-disk set never
	// inflates the surfaced count. A missing bucket yields a zero state.
	Snapshot(db *bbolt.DB, now time.Time) (FunnelState, error)
}

// bboltFunnelStore is the BBolt-backed FunnelStore implementation.
// Zero-value is ready to use.
type bboltFunnelStore struct{}

// NewFunnelStore returns a BBolt-backed FunnelStore.
func NewFunnelStore() FunnelStore {
	return bboltFunnelStore{}
}

// EnsureFunnelBucket proactively creates the funnel bucket so concurrent
// first writes (index serve + heartbeat) never race on bucket creation.
func EnsureFunnelBucket(db *bbolt.DB) error {
	return db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(FunnelBucketName))
		return err
	})
}

// dayOrdinal converts a time to its UTC day ordinal (whole days since the
// Unix epoch). Day boundaries are UTC midnights (FR-007/FR-008 day math).
func dayOrdinal(t time.Time) int64 {
	const secondsPerDay = 24 * 60 * 60
	secs := t.UTC().Unix()
	// Floor division so pre-epoch times (grossly skewed clocks) still map to
	// a consistent ordinal instead of rounding toward zero.
	d := secs / secondsPerDay
	if secs%secondsPerDay < 0 {
		d--
	}
	return d
}

func encodeInt64(v int64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(v))
	return buf
}

func decodeInt64(b []byte) (int64, bool) {
	if len(b) < 8 {
		return 0, false
	}
	return int64(binary.BigEndian.Uint64(b[:8])), true
}

// decodeDaySet decodes the concatenated 8-byte day ordinals. Trailing partial
// records (corruption) are ignored.
func decodeDaySet(b []byte) []int64 {
	n := len(b) / 8
	days := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		days = append(days, int64(binary.BigEndian.Uint64(b[i*8:(i+1)*8])))
	}
	return days
}

func encodeDaySet(days []int64) []byte {
	buf := make([]byte, 0, len(days)*8)
	for _, d := range days {
		buf = append(buf, encodeInt64(d)...)
	}
	return buf
}

// inWindow reports whether day d falls in the trailing window ending at
// nowDay. Future days (backwards clock skew) are excluded so a skewed set
// never inflates the count past the days actually observable at `now`.
func inWindow(d, nowDay int64) bool {
	return d > nowDay-activeDaysWindow && d <= nowDay
}

func funnelBucket(tx *bbolt.Tx) *bbolt.Bucket {
	return tx.Bucket([]byte(FunnelBucketName))
}

func (bboltFunnelStore) IncrementWebUIOpened(db *bbolt.DB) error {
	return db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(FunnelBucketName))
		if err != nil {
			return err
		}
		var count int64
		if v, ok := decodeInt64(b.Get([]byte(funnelKeyWebUIOpened))); ok {
			count = v
		}
		return b.Put([]byte(funnelKeyWebUIOpened), encodeInt64(count+1))
	})
}

func (bboltFunnelStore) RecordActivity(db *bbolt.DB, now time.Time) error {
	nowDay := dayOrdinal(now)
	return db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(FunnelBucketName))
		if err != nil {
			return err
		}

		// First-install stamp: write-once, never moved by later activity.
		if _, ok := decodeInt64(b.Get([]byte(funnelKeyFirstInstallDay))); !ok {
			if err := b.Put([]byte(funnelKeyFirstInstallDay), encodeInt64(nowDay)); err != nil {
				return err
			}
		}

		// Active-day set: add today's ordinal, prune aged-out members.
		// Membership check plus prune keeps the value bounded (≤30 entries
		// plus any not-yet-aged future days from clock skew).
		existing := decodeDaySet(b.Get([]byte(funnelKeyActiveDays)))
		kept := make([]int64, 0, len(existing)+1)
		seen := false
		for _, d := range existing {
			if d == nowDay {
				seen = true
			}
			// Prune only days strictly older than the window; keep future
			// days (clock went backwards) so they are not lost — Snapshot
			// excludes them from the count until the clock catches up.
			if d > nowDay-activeDaysWindow {
				kept = append(kept, d)
			}
		}
		if !seen {
			kept = append(kept, nowDay)
		}
		return b.Put([]byte(funnelKeyActiveDays), encodeDaySet(kept))
	})
}

func (bboltFunnelStore) Snapshot(db *bbolt.DB, now time.Time) (FunnelState, error) {
	nowDay := dayOrdinal(now)
	st := FunnelState{}
	err := db.View(func(tx *bbolt.Tx) error {
		b := funnelBucket(tx)
		if b == nil {
			return nil
		}

		if v, ok := decodeInt64(b.Get([]byte(funnelKeyWebUIOpened))); ok {
			st.WebUIOpened = v
		}

		if firstDay, ok := decodeInt64(b.Get([]byte(funnelKeyFirstInstallDay))); ok {
			st.HasInstallDay = true
			age := nowDay - firstDay
			if age < 0 {
				// Clock skew: never surface a negative day count (FR-007).
				age = 0
			}
			st.DaysSinceInstall = int(age)
		}

		count := 0
		for _, d := range decodeDaySet(b.Get([]byte(funnelKeyActiveDays))) {
			if inWindow(d, nowDay) {
				count++
			}
		}
		if count > activeDaysWindow {
			count = activeDaysWindow
		}
		st.ActiveDays30d = count
		return nil
	})
	return st, err
}
