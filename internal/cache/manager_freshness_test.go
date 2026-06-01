package cache

import (
	"testing"
	"time"

	"go.etcd.io/bbolt"
	"go.uber.org/zap"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	db := setupTestDB(t)
	t.Cleanup(func() { db.Close() })
	m, err := NewManager(db, zap.NewNop())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(m.Close)
	return m
}

// putRaw writes a record directly so tests can craft timestamps (e.g. a stale
// entry) that the public Store API would not produce.
func putRaw(t *testing.T, m *Manager, rec *Record) {
	t.Helper()
	err := m.db.Update(func(tx *bbolt.Tx) error {
		data, err := rec.MarshalBinary()
		if err != nil {
			return err
		}
		return tx.Bucket([]byte(CacheBucket)).Put([]byte(rec.Key), data)
	})
	if err != nil {
		t.Fatalf("putRaw: %v", err)
	}
}

func TestInvalidate_RemovesKey(t *testing.T) {
	m := newTestManager(t)
	if err := m.Store("k1", "tool", nil, `[]`, "", 0); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if _, err := m.Get("k1"); err != nil {
		t.Fatalf("precondition Get: %v", err)
	}
	if err := m.Invalidate("k1"); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if _, err := m.Get("k1"); err == nil {
		t.Fatal("expected key to be gone after Invalidate")
	}
}

func TestRefresh_ForcesReFetch(t *testing.T) {
	m := newTestManager(t)
	if err := m.Store("k1", "tool", nil, `[]`, "", 0); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if err := m.Refresh("k1"); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if _, err := m.Get("k1"); err == nil {
		t.Fatal("expected key to be gone after Refresh (next access re-fetches)")
	}
}

func TestInvalidatePrefix_OnlyMatching(t *testing.T) {
	m := newTestManager(t)
	for _, k := range []string{"reg:a", "reg:b", "other:c"} {
		if err := m.Store(k, "tool", nil, `[]`, "", 0); err != nil {
			t.Fatalf("Store %s: %v", k, err)
		}
	}
	n, err := m.InvalidatePrefix("reg:")
	if err != nil {
		t.Fatalf("InvalidatePrefix: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 keys invalidated, got %d", n)
	}
	if _, err := m.Get("other:c"); err != nil {
		t.Errorf("non-matching key should survive: %v", err)
	}
}

func TestPeek_FreshEntry(t *testing.T) {
	m := newTestManager(t)
	if err := m.Store("k1", "tool", nil, `[]`, "", 0); err != nil {
		t.Fatalf("Store: %v", err)
	}
	rec, ok := m.Peek("k1")
	if !ok {
		t.Fatal("Peek should find a fresh entry")
	}
	if time.Since(rec.CreatedAt) > time.Minute {
		t.Errorf("fresh entry age unexpectedly large: %v", time.Since(rec.CreatedAt))
	}
	if rec.IsExpired() {
		t.Error("fresh entry should not be stale")
	}
}

func TestPeek_StaleEntryNotEvicted(t *testing.T) {
	m := newTestManager(t)
	putRaw(t, m, &Record{
		Key:       "stale",
		ToolName:  "tool",
		CreatedAt: time.Now().Add(-3 * time.Hour),
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	})
	rec, ok := m.Peek("stale")
	if !ok {
		t.Fatal("Peek should return a stale entry, not drop it")
	}
	if !rec.IsExpired() {
		t.Error("entry should be reported stale")
	}
	// Peek must NOT evict — a second Peek still finds it.
	if _, ok := m.Peek("stale"); !ok {
		t.Error("Peek must not evict stale entries")
	}
}

func TestPeek_Missing(t *testing.T) {
	m := newTestManager(t)
	if _, ok := m.Peek("nope"); ok {
		t.Error("Peek should report missing key as not found")
	}
}
