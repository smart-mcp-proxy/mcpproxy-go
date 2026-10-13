package telemetry

import (
	"path/filepath"
	"testing"
	"time"

	"go.etcd.io/bbolt"
)

// openFunnelTestDB creates a throwaway BBolt database for funnel-store tests.
func openFunnelTestDB(t *testing.T) *bbolt.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "funnel_test.db")
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatalf("open bbolt: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func funnelDay(t *testing.T, value string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return ts.UTC()
}

// TestFunnelStore_SnapshotEmpty asserts a missing bucket yields a zero state
// without error (FR-009 nil/empty safety).
func TestFunnelStore_SnapshotEmpty(t *testing.T) {
	db := openFunnelTestDB(t)
	store := NewFunnelStore()

	st, err := store.Snapshot(db, time.Now().UTC())
	if err != nil {
		t.Fatalf("Snapshot on empty DB: %v", err)
	}
	if st.WebUIOpened != 0 || st.HasInstallDay || st.ActiveDays30d != 0 {
		t.Fatalf("expected zero state, got %+v", st)
	}
}

// TestFunnelStore_WebUIOpenedCounter asserts the lifetime counter increments
// and persists (FR-006).
func TestFunnelStore_WebUIOpenedCounter(t *testing.T) {
	db := openFunnelTestDB(t)
	store := NewFunnelStore()

	for i := 0; i < 3; i++ {
		if err := store.IncrementWebUIOpened(db); err != nil {
			t.Fatalf("IncrementWebUIOpened: %v", err)
		}
	}

	st, err := store.Snapshot(db, time.Now().UTC())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if st.WebUIOpened != 3 {
		t.Fatalf("expected web_ui_opened=3, got %d", st.WebUIOpened)
	}
}

// TestFunnelStore_DaysSinceInstall asserts first-install stamping and whole-day
// math (FR-007): stamp on first activity, K whole days later the count is K,
// and the stamp is never overwritten by later activity.
func TestFunnelStore_DaysSinceInstall(t *testing.T) {
	db := openFunnelTestDB(t)
	store := NewFunnelStore()

	install := funnelDay(t, "2026-07-01T15:04:05Z")
	if err := store.RecordActivity(db, install); err != nil {
		t.Fatalf("RecordActivity: %v", err)
	}

	// Same day: 0 whole days.
	st, err := store.Snapshot(db, funnelDay(t, "2026-07-01T23:59:59Z"))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !st.HasInstallDay || st.DaysSinceInstall != 0 {
		t.Fatalf("expected days_since_install=0 on install day, got %+v", st)
	}

	// Day boundary: 00:00 UTC next day is 1 whole day even though <24h elapsed.
	st, err = store.Snapshot(db, funnelDay(t, "2026-07-02T00:00:01Z"))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if st.DaysSinceInstall != 1 {
		t.Fatalf("expected days_since_install=1 across UTC day boundary, got %d", st.DaysSinceInstall)
	}

	// K days later.
	st, err = store.Snapshot(db, funnelDay(t, "2026-07-15T10:00:00Z"))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if st.DaysSinceInstall != 14 {
		t.Fatalf("expected days_since_install=14, got %d", st.DaysSinceInstall)
	}

	// Later activity must not move the stamp.
	if err := store.RecordActivity(db, funnelDay(t, "2026-07-15T10:00:00Z")); err != nil {
		t.Fatalf("RecordActivity: %v", err)
	}
	st, err = store.Snapshot(db, funnelDay(t, "2026-07-15T11:00:00Z"))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if st.DaysSinceInstall != 14 {
		t.Fatalf("install stamp moved: expected 14, got %d", st.DaysSinceInstall)
	}
}

// TestFunnelStore_DaysSinceInstallClockSkewClamp asserts a backwards clock
// never produces a negative day count (Edge Cases: clamp at 0).
func TestFunnelStore_DaysSinceInstallClockSkewClamp(t *testing.T) {
	db := openFunnelTestDB(t)
	store := NewFunnelStore()

	if err := store.RecordActivity(db, funnelDay(t, "2026-07-10T12:00:00Z")); err != nil {
		t.Fatalf("RecordActivity: %v", err)
	}

	st, err := store.Snapshot(db, funnelDay(t, "2026-07-05T12:00:00Z"))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if st.DaysSinceInstall != 0 {
		t.Fatalf("expected clamp to 0 on backwards clock, got %d", st.DaysSinceInstall)
	}
}

// TestFunnelStore_ActiveDays30d asserts distinct-day set semantics (FR-008):
// same-day repeats count once, distinct days accumulate.
func TestFunnelStore_ActiveDays30d(t *testing.T) {
	db := openFunnelTestDB(t)
	store := NewFunnelStore()

	// Two activities on the same UTC day → one active day.
	if err := store.RecordActivity(db, funnelDay(t, "2026-07-01T01:00:00Z")); err != nil {
		t.Fatalf("RecordActivity: %v", err)
	}
	if err := store.RecordActivity(db, funnelDay(t, "2026-07-01T23:00:00Z")); err != nil {
		t.Fatalf("RecordActivity: %v", err)
	}
	st, err := store.Snapshot(db, funnelDay(t, "2026-07-01T23:30:00Z"))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if st.ActiveDays30d != 1 {
		t.Fatalf("expected active_days_30d=1 for same-day repeats, got %d", st.ActiveDays30d)
	}

	// Three distinct days → 3.
	if err := store.RecordActivity(db, funnelDay(t, "2026-07-03T12:00:00Z")); err != nil {
		t.Fatalf("RecordActivity: %v", err)
	}
	if err := store.RecordActivity(db, funnelDay(t, "2026-07-07T12:00:00Z")); err != nil {
		t.Fatalf("RecordActivity: %v", err)
	}
	st, err = store.Snapshot(db, funnelDay(t, "2026-07-07T13:00:00Z"))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if st.ActiveDays30d != 3 {
		t.Fatalf("expected active_days_30d=3, got %d", st.ActiveDays30d)
	}
}

// TestFunnelStore_ActiveDays30dWindowAging asserts days age out of the
// trailing 30-day window (FR-008, acceptance scenario 4).
func TestFunnelStore_ActiveDays30dWindowAging(t *testing.T) {
	db := openFunnelTestDB(t)
	store := NewFunnelStore()

	first := funnelDay(t, "2026-06-01T12:00:00Z")
	if err := store.RecordActivity(db, first); err != nil {
		t.Fatalf("RecordActivity: %v", err)
	}
	// Fourth run 31 days after the first: the first day has aged out.
	later := funnelDay(t, "2026-07-02T12:00:00Z")
	if err := store.RecordActivity(db, later); err != nil {
		t.Fatalf("RecordActivity: %v", err)
	}

	st, err := store.Snapshot(db, later)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if st.ActiveDays30d != 1 {
		t.Fatalf("expected first day aged out (count=1), got %d", st.ActiveDays30d)
	}

	// Aging also applies at read time, without an intervening write.
	st, err = store.Snapshot(db, funnelDay(t, "2026-09-01T12:00:00Z"))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if st.ActiveDays30d != 0 {
		t.Fatalf("expected all days aged out at read time, got %d", st.ActiveDays30d)
	}
}

// TestFunnelStore_ActiveDays30dOutOfOrder asserts the window tolerates
// out-of-order days (Edge Cases): a day is a set member, not a delta.
func TestFunnelStore_ActiveDays30dOutOfOrder(t *testing.T) {
	db := openFunnelTestDB(t)
	store := NewFunnelStore()

	if err := store.RecordActivity(db, funnelDay(t, "2026-07-05T12:00:00Z")); err != nil {
		t.Fatalf("RecordActivity: %v", err)
	}
	// Clock jumped backwards: an earlier day arrives after a later one.
	if err := store.RecordActivity(db, funnelDay(t, "2026-07-03T12:00:00Z")); err != nil {
		t.Fatalf("RecordActivity: %v", err)
	}

	st, err := store.Snapshot(db, funnelDay(t, "2026-07-05T13:00:00Z"))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if st.ActiveDays30d != 2 {
		t.Fatalf("expected 2 distinct days despite out-of-order recording, got %d", st.ActiveDays30d)
	}

	// A future-dated member (relative to snapshot time) is not counted:
	// snapshot taken at the earlier day sees only that day.
	st, err = store.Snapshot(db, funnelDay(t, "2026-07-03T13:00:00Z"))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if st.ActiveDays30d != 1 {
		t.Fatalf("expected future days excluded from the trailing window, got %d", st.ActiveDays30d)
	}
}

// TestFunnelStore_ActiveDays30dCap asserts the surfaced count never exceeds 30
// (FR-008: 1–30).
func TestFunnelStore_ActiveDays30dCap(t *testing.T) {
	db := openFunnelTestDB(t)
	store := NewFunnelStore()

	base := funnelDay(t, "2026-06-01T12:00:00Z")
	for i := 0; i < 45; i++ {
		if err := store.RecordActivity(db, base.AddDate(0, 0, i)); err != nil {
			t.Fatalf("RecordActivity day %d: %v", i, err)
		}
	}
	last := base.AddDate(0, 0, 44)
	st, err := store.Snapshot(db, last)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if st.ActiveDays30d != 30 {
		t.Fatalf("expected active_days_30d capped at 30, got %d", st.ActiveDays30d)
	}
}
