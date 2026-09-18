//go:build server

package multiuser

import (
	"context"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegration_ActivityIsolation(t *testing.T) {
	now := time.Now().UTC()

	// Create mock activity records with different UserIDs
	records := []*storage.ActivityRecord{
		{
			ID:         "rec-a1",
			Type:       storage.ActivityTypeToolCall,
			Status:     "success",
			UserID:     "user-alice",
			UserEmail:  "alice@example.com",
			ServerName: "github",
			ToolName:   "list_repos",
			Timestamp:  now,
		},
		{
			ID:         "rec-a2",
			Type:       storage.ActivityTypeToolCall,
			Status:     "success",
			UserID:     "user-alice",
			UserEmail:  "alice@example.com",
			ServerName: "github",
			ToolName:   "create_issue",
			Timestamp:  now.Add(-1 * time.Minute),
		},
		{
			ID:         "rec-b1",
			Type:       storage.ActivityTypeToolCall,
			Status:     "success",
			UserID:     "user-bob",
			UserEmail:  "bob@example.com",
			ServerName: "gitlab",
			ToolName:   "create_mr",
			Timestamp:  now.Add(-2 * time.Minute),
		},
		{
			ID:         "rec-a3",
			Type:       storage.ActivityTypeToolCall,
			Status:     "error",
			UserID:     "user-alice",
			UserEmail:  "alice@example.com",
			ServerName: "github",
			ToolName:   "delete_repo",
			Timestamp:  now.Add(-3 * time.Minute),
		},
		{
			ID:         "rec-b2",
			Type:       storage.ActivityTypeToolCall,
			Status:     "success",
			UserID:     "user-bob",
			UserEmail:  "bob@example.com",
			ServerName: "gitlab",
			ToolName:   "list_pipelines",
			Timestamp:  now.Add(-4 * time.Minute),
		},
	}

	provider := &mockActivityStorageProvider{records: records}
	af := NewActivityFilter(provider)

	// Verify user Alice only sees her records (3 records)
	ctxAlice := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:   auth.AuthTypeUser,
		UserID: "user-alice",
		Email:  "alice@example.com",
		Role:   "user",
	})

	aliceRecords, aliceTotal, err := af.GetUserActivity(ctxAlice, 50, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, aliceTotal)
	assert.Len(t, aliceRecords, 3)
	for _, r := range aliceRecords {
		assert.Equal(t, "user-alice", r.UserID, "Alice should only see her own records")
	}

	// Verify user Bob only sees his records (2 records)
	ctxBob := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:   auth.AuthTypeUser,
		UserID: "user-bob",
		Email:  "bob@example.com",
		Role:   "user",
	})

	bobRecords, bobTotal, err := af.GetUserActivity(ctxBob, 50, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, bobTotal)
	assert.Len(t, bobRecords, 2)
	for _, r := range bobRecords {
		assert.Equal(t, "user-bob", r.UserID, "Bob should only see his own records")
	}

	// Verify admin sees all records (5 records)
	ctxAdmin := auth.WithAuthContext(context.Background(),
		auth.AdminUserContext("admin-001", "admin@example.com", "Admin", "google"))

	adminRecords, adminTotal, err := af.GetUserActivity(ctxAdmin, 50, 0)
	require.NoError(t, err)
	assert.Equal(t, 5, adminTotal)
	assert.Len(t, adminRecords, 5)

	// Verify admin can also filter by specific user
	filteredRecords, filteredTotal, err := af.GetFilteredActivity(ctxAdmin, "user-bob", 50, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, filteredTotal)
	assert.Len(t, filteredRecords, 2)
	for _, r := range filteredRecords {
		assert.Equal(t, "user-bob", r.UserID)
	}

	// Non-admin cannot use GetFilteredActivity
	_, _, err = af.GetFilteredActivity(ctxAlice, "user-bob", 50, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "admin access required")
}

func TestIntegration_ActivityIsolation_EnrichAndFilter(t *testing.T) {
	// Test the full cycle: enrich a record with user context, then filter
	provider := &mockActivityStorageProvider{}
	af := NewActivityFilter(provider)

	// Simulate creating records with EnrichRecord
	recordA := &storage.ActivityRecord{
		ID:     "new-rec-a",
		Type:   storage.ActivityTypeToolCall,
		Status: "success",
	}
	recordB := &storage.ActivityRecord{
		ID:     "new-rec-b",
		Type:   storage.ActivityTypeToolCall,
		Status: "success",
	}

	ctxA := auth.WithAuthContext(context.Background(),
		auth.UserContext("userA", "a@test.com", "User A", "google"))
	ctxB := auth.WithAuthContext(context.Background(),
		auth.UserContext("userB", "b@test.com", "User B", "google"))

	af.EnrichRecord(ctxA, recordA)
	af.EnrichRecord(ctxB, recordB)

	// Verify enrichment
	assert.Equal(t, "userA", recordA.UserID)
	assert.Equal(t, "a@test.com", recordA.UserEmail)
	assert.Equal(t, "userB", recordB.UserID)
	assert.Equal(t, "b@test.com", recordB.UserEmail)

	// Now create a new provider with these enriched records and verify isolation
	now := time.Now().UTC()
	recordA.Timestamp = now
	recordB.Timestamp = now.Add(-1 * time.Minute)

	provider2 := &mockActivityStorageProvider{records: []*storage.ActivityRecord{recordA, recordB}}
	af2 := NewActivityFilter(provider2)

	// User A sees only their record
	recsA, totalA, err := af2.GetUserActivity(ctxA, 50, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, totalA)
	require.Len(t, recsA, 1)
	assert.Equal(t, "new-rec-a", recsA[0].ID)

	// User B sees only their record
	recsB, totalB, err := af2.GetUserActivity(ctxB, 50, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, totalB)
	require.Len(t, recsB, 1)
	assert.Equal(t, "new-rec-b", recsB[0].ID)
}
