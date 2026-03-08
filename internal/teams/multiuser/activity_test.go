//go:build teams

package multiuser

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// mockActivityStorageProvider implements ActivityStorageProvider for testing.
type mockActivityStorageProvider struct {
	records []*storage.ActivityRecord
}

func (m *mockActivityStorageProvider) ListActivities(filter storage.ActivityFilter) ([]*storage.ActivityRecord, int, error) {
	filter.Validate()

	var matched []*storage.ActivityRecord
	for _, r := range m.records {
		if filter.Matches(r) {
			matched = append(matched, r)
		}
	}

	total := len(matched)

	// Apply pagination
	start := filter.Offset
	if start > len(matched) {
		start = len(matched)
	}
	end := start + filter.Limit
	if end > len(matched) {
		end = len(matched)
	}

	return matched[start:end], total, nil
}

func (m *mockActivityStorageProvider) GetActivity(id string) (*storage.ActivityRecord, error) {
	for _, r := range m.records {
		if r.ID == id {
			return r, nil
		}
	}
	return nil, fmt.Errorf("not found")
}

func TestActivityFilter_EnrichRecord(t *testing.T) {
	af := NewActivityFilter(&mockActivityStorageProvider{})

	record := &storage.ActivityRecord{
		ID:     "test-001",
		Type:   storage.ActivityTypeToolCall,
		Status: "success",
	}

	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:   auth.AuthTypeUser,
		UserID: "user-123",
		Email:  "alice@example.com",
	})

	af.EnrichRecord(ctx, record)

	if record.UserID != "user-123" {
		t.Errorf("expected UserID 'user-123', got %q", record.UserID)
	}
	if record.UserEmail != "alice@example.com" {
		t.Errorf("expected UserEmail 'alice@example.com', got %q", record.UserEmail)
	}
}

func TestActivityFilter_EnrichRecord_NoAuth(t *testing.T) {
	af := NewActivityFilter(&mockActivityStorageProvider{})

	record := &storage.ActivityRecord{
		ID:     "test-002",
		Type:   storage.ActivityTypeToolCall,
		Status: "success",
	}

	// Context without auth
	ctx := context.Background()

	af.EnrichRecord(ctx, record)

	if record.UserID != "" {
		t.Errorf("expected empty UserID, got %q", record.UserID)
	}
	if record.UserEmail != "" {
		t.Errorf("expected empty UserEmail, got %q", record.UserEmail)
	}
}

func TestActivityFilter_EnrichRecord_NilRecord(t *testing.T) {
	af := NewActivityFilter(&mockActivityStorageProvider{})

	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:   auth.AuthTypeUser,
		UserID: "user-123",
		Email:  "alice@example.com",
	})

	// Should not panic
	af.EnrichRecord(ctx, nil)
}

func TestActivityFilter_GetUserActivity(t *testing.T) {
	now := time.Now().UTC()
	provider := &mockActivityStorageProvider{
		records: []*storage.ActivityRecord{
			{
				ID:        "rec-1",
				Type:      storage.ActivityTypeToolCall,
				Status:    "success",
				UserID:    "user-alice",
				UserEmail: "alice@example.com",
				Timestamp: now,
			},
			{
				ID:        "rec-2",
				Type:      storage.ActivityTypeToolCall,
				Status:    "success",
				UserID:    "user-bob",
				UserEmail: "bob@example.com",
				Timestamp: now.Add(-1 * time.Minute),
			},
			{
				ID:        "rec-3",
				Type:      storage.ActivityTypeToolCall,
				Status:    "error",
				UserID:    "user-alice",
				UserEmail: "alice@example.com",
				Timestamp: now.Add(-2 * time.Minute),
			},
			{
				ID:        "rec-4",
				Type:      storage.ActivityTypeToolCall,
				Status:    "success",
				UserID:    "user-bob",
				UserEmail: "bob@example.com",
				Timestamp: now.Add(-3 * time.Minute),
			},
		},
	}

	af := NewActivityFilter(provider)

	// Regular user Alice should only see her own records
	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:   auth.AuthTypeUser,
		UserID: "user-alice",
		Email:  "alice@example.com",
		Role:   "user",
	})

	records, total, err := af.GetUserActivity(ctx, 50, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if total != 2 {
		t.Errorf("expected total 2, got %d", total)
	}

	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	for _, r := range records {
		if r.UserID != "user-alice" {
			t.Errorf("expected all records to belong to user-alice, got UserID %q for record %s", r.UserID, r.ID)
		}
	}
}

func TestActivityFilter_GetUserActivity_Admin(t *testing.T) {
	now := time.Now().UTC()
	provider := &mockActivityStorageProvider{
		records: []*storage.ActivityRecord{
			{
				ID:        "rec-1",
				Type:      storage.ActivityTypeToolCall,
				Status:    "success",
				UserID:    "user-alice",
				Timestamp: now,
			},
			{
				ID:        "rec-2",
				Type:      storage.ActivityTypeToolCall,
				Status:    "success",
				UserID:    "user-bob",
				Timestamp: now.Add(-1 * time.Minute),
			},
			{
				ID:        "rec-3",
				Type:      storage.ActivityTypeToolCall,
				Status:    "success",
				UserID:    "", // No user (personal edition record)
				Timestamp: now.Add(-2 * time.Minute),
			},
		},
	}

	af := NewActivityFilter(provider)

	// Admin should see all records
	ctx := auth.WithAuthContext(context.Background(), auth.AdminUserContext(
		"admin-001", "admin@example.com", "Admin", "google",
	))

	records, total, err := af.GetUserActivity(ctx, 50, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if total != 3 {
		t.Errorf("expected total 3, got %d", total)
	}

	if len(records) != 3 {
		t.Errorf("expected 3 records, got %d", len(records))
	}
}

func TestActivityFilter_GetUserActivity_NoAuth(t *testing.T) {
	af := NewActivityFilter(&mockActivityStorageProvider{})

	// No auth context
	ctx := context.Background()

	_, _, err := af.GetUserActivity(ctx, 50, 0)
	if err == nil {
		t.Fatal("expected error for unauthenticated request, got nil")
	}
	if err.Error() != "authentication required" {
		t.Errorf("expected 'authentication required' error, got: %v", err)
	}
}

func TestActivityFilter_GetFilteredActivity(t *testing.T) {
	now := time.Now().UTC()
	provider := &mockActivityStorageProvider{
		records: []*storage.ActivityRecord{
			{
				ID:        "rec-1",
				Type:      storage.ActivityTypeToolCall,
				Status:    "success",
				UserID:    "user-alice",
				Timestamp: now,
			},
			{
				ID:        "rec-2",
				Type:      storage.ActivityTypeToolCall,
				Status:    "success",
				UserID:    "user-bob",
				Timestamp: now.Add(-1 * time.Minute),
			},
			{
				ID:        "rec-3",
				Type:      storage.ActivityTypeToolCall,
				Status:    "error",
				UserID:    "user-alice",
				Timestamp: now.Add(-2 * time.Minute),
			},
		},
	}

	af := NewActivityFilter(provider)

	// Admin filters by specific user
	ctx := auth.WithAuthContext(context.Background(), auth.AdminUserContext(
		"admin-001", "admin@example.com", "Admin", "google",
	))

	records, total, err := af.GetFilteredActivity(ctx, "user-bob", 50, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if total != 1 {
		t.Errorf("expected total 1, got %d", total)
	}

	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	if records[0].UserID != "user-bob" {
		t.Errorf("expected record for user-bob, got UserID %q", records[0].UserID)
	}
}

func TestActivityFilter_GetFilteredActivity_NonAdmin(t *testing.T) {
	provider := &mockActivityStorageProvider{
		records: []*storage.ActivityRecord{
			{
				ID:     "rec-1",
				Type:   storage.ActivityTypeToolCall,
				Status: "success",
				UserID: "user-alice",
			},
		},
	}

	af := NewActivityFilter(provider)

	// Non-admin user tries to filter by another user
	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:   auth.AuthTypeUser,
		UserID: "user-alice",
		Email:  "alice@example.com",
		Role:   "user",
	})

	_, _, err := af.GetFilteredActivity(ctx, "user-bob", 50, 0)
	if err == nil {
		t.Fatal("expected error for non-admin access, got nil")
	}
	if err.Error() != "admin access required" {
		t.Errorf("expected 'admin access required' error, got: %v", err)
	}
}

func TestActivityFilter_GetFilteredActivity_APIKeyAdmin(t *testing.T) {
	now := time.Now().UTC()
	provider := &mockActivityStorageProvider{
		records: []*storage.ActivityRecord{
			{
				ID:        "rec-1",
				Type:      storage.ActivityTypeToolCall,
				Status:    "success",
				UserID:    "user-alice",
				Timestamp: now,
			},
		},
	}

	af := NewActivityFilter(provider)

	// API key admin (AuthTypeAdmin) should also be allowed
	ctx := auth.WithAuthContext(context.Background(), auth.AdminContext())

	records, total, err := af.GetFilteredActivity(ctx, "user-alice", 50, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if total != 1 {
		t.Errorf("expected total 1, got %d", total)
	}

	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
}

func TestActivityFilter_GetUserActivity_Pagination(t *testing.T) {
	now := time.Now().UTC()
	var records []*storage.ActivityRecord
	for i := 0; i < 10; i++ {
		records = append(records, &storage.ActivityRecord{
			ID:        fmt.Sprintf("rec-%02d", i),
			Type:      storage.ActivityTypeToolCall,
			Status:    "success",
			UserID:    "user-alice",
			Timestamp: now.Add(-time.Duration(i) * time.Minute),
		})
	}

	provider := &mockActivityStorageProvider{records: records}
	af := NewActivityFilter(provider)

	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:   auth.AuthTypeUser,
		UserID: "user-alice",
		Role:   "user",
	})

	// Get first page (3 records)
	page1, total, err := af.GetUserActivity(ctx, 3, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if total != 10 {
		t.Errorf("expected total 10, got %d", total)
	}

	if len(page1) != 3 {
		t.Fatalf("expected 3 records on page 1, got %d", len(page1))
	}

	// Get second page (3 records, offset 3)
	page2, total2, err := af.GetUserActivity(ctx, 3, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if total2 != 10 {
		t.Errorf("expected total 10 on page 2, got %d", total2)
	}

	if len(page2) != 3 {
		t.Fatalf("expected 3 records on page 2, got %d", len(page2))
	}

	// Ensure no overlap between pages
	for _, r1 := range page1 {
		for _, r2 := range page2 {
			if r1.ID == r2.ID {
				t.Errorf("record %s appears on both page 1 and page 2", r1.ID)
			}
		}
	}
}
