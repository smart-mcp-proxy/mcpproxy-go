package cache

import (
	"testing"

	"go.uber.org/zap"
)

// Issue #953 follow-up: an empty (or fully paginated-past) record set must
// yield a non-nil Records slice so read_cache serializes "records": [] —
// never null — for strict MCP clients that iterate the array.
func TestGetRecords_EmptyContentReturnsNonNilRecords(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	manager, err := NewManager(db, zap.NewNop())
	if err != nil {
		t.Fatalf("Failed to create cache manager: %v", err)
	}
	defer manager.Close()

	if err := manager.Store("empty-key", "test_tool", nil, "[]", "", 0); err != nil {
		t.Fatalf("Failed to store record: %v", err)
	}

	resp, err := manager.GetRecords("empty-key", 0, 10)
	if err != nil {
		t.Fatalf("GetRecords failed: %v", err)
	}
	if resp.Records == nil {
		t.Fatal("Records must be a non-nil slice so it serializes as [], not null")
	}
	if len(resp.Records) != 0 {
		t.Fatalf("expected 0 records, got %d", len(resp.Records))
	}
}

// Paginating past the end of a non-empty set must also stay non-nil.
func TestGetRecords_OffsetPastEndReturnsNonNilRecords(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	manager, err := NewManager(db, zap.NewNop())
	if err != nil {
		t.Fatalf("Failed to create cache manager: %v", err)
	}
	defer manager.Close()

	if err := manager.Store("two-key", "test_tool", nil, `["a","b"]`, "", 2); err != nil {
		t.Fatalf("Failed to store record: %v", err)
	}

	resp, err := manager.GetRecords("two-key", 5, 10)
	if err != nil {
		t.Fatalf("GetRecords failed: %v", err)
	}
	if resp.Records == nil {
		t.Fatal("Records must be a non-nil slice so it serializes as [], not null")
	}
}
