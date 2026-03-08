//go:build teams

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

func TestIntegration_CrossUserIsolation(t *testing.T) {
	// Setup: temp BBolt, user store, workspace manager, router
	// User A adds servers: "server-a1", "server-a2"
	// User B adds servers: "server-b1"
	// Shared servers: "shared-1"
	router, _ := setupRouter(t,
		[]string{"shared-1"},
		map[string][]string{
			"userA": {"server-a1", "server-a2"},
			"userB": {"server-b1"},
		},
	)

	ctxA := auth.WithAuthContext(context.Background(),
		auth.UserContext("userA", "a@test.com", "User A", "google"))
	ctxB := auth.WithAuthContext(context.Background(),
		auth.UserContext("userB", "b@test.com", "User B", "google"))

	// Test 1: User A's accessible servers = shared-1, server-a1, server-a2
	serversA, err := router.GetUserServers(ctxA)
	require.NoError(t, err)
	assert.Len(t, serversA, 3, "User A should see shared-1 + server-a1 + server-a2")

	namesA := serverInfoNames(serversA)
	assert.Contains(t, namesA, "shared-1")
	assert.Contains(t, namesA, "server-a1")
	assert.Contains(t, namesA, "server-a2")

	// Test 2: User B's accessible servers = shared-1, server-b1
	serversB, err := router.GetUserServers(ctxB)
	require.NoError(t, err)
	assert.Len(t, serversB, 2, "User B should see shared-1 + server-b1")

	namesB := serverInfoNames(serversB)
	assert.Contains(t, namesB, "shared-1")
	assert.Contains(t, namesB, "server-b1")

	// Test 3: User A cannot access server-b1
	assert.False(t, router.IsServerAccessible(ctxA, "server-b1"),
		"User A must not access User B's server")

	// Test 4: User B cannot access server-a1
	assert.False(t, router.IsServerAccessible(ctxB, "server-a1"),
		"User B must not access User A's server")
	assert.False(t, router.IsServerAccessible(ctxB, "server-a2"),
		"User B must not access User A's server")

	// Test 5: Both can access shared-1
	assert.True(t, router.IsServerAccessible(ctxA, "shared-1"),
		"User A should access shared server")
	assert.True(t, router.IsServerAccessible(ctxB, "shared-1"),
		"User B should access shared server")

	// Test 6: Tool filtering
	allTools := []ToolInfo{
		{ToolName: "tool1", ServerName: "server-a1", Ownership: OwnershipPersonal},
		{ToolName: "tool2", ServerName: "server-b1", Ownership: OwnershipPersonal},
		{ToolName: "tool3", ServerName: "shared-1", Ownership: OwnershipShared},
		{ToolName: "tool4", ServerName: "server-a2", Ownership: OwnershipPersonal},
		{ToolName: "tool5", ServerName: "unknown-server", Ownership: OwnershipPersonal},
	}

	toolFilter := NewToolFilter(router, testLogger(t))

	// User A sees tool1 (server-a1), tool3 (shared-1), tool4 (server-a2) -- not tool2 or tool5
	filteredA := toolFilter.FilterToolsByUser(ctxA, allTools)
	require.Len(t, filteredA, 3)

	filteredANames := make([]string, len(filteredA))
	for i, ti := range filteredA {
		filteredANames[i] = ti.ToolName
	}
	assert.Contains(t, filteredANames, "tool1")
	assert.Contains(t, filteredANames, "tool3")
	assert.Contains(t, filteredANames, "tool4")
	assert.NotContains(t, filteredANames, "tool2")
	assert.NotContains(t, filteredANames, "tool5")

	// User B sees tool2 (server-b1), tool3 (shared-1) -- not tool1, tool4, or tool5
	filteredB := toolFilter.FilterToolsByUser(ctxB, allTools)
	require.Len(t, filteredB, 2)

	filteredBNames := make([]string, len(filteredB))
	for i, ti := range filteredB {
		filteredBNames[i] = ti.ToolName
	}
	assert.Contains(t, filteredBNames, "tool2")
	assert.Contains(t, filteredBNames, "tool3")
	assert.NotContains(t, filteredBNames, "tool1")
	assert.NotContains(t, filteredBNames, "tool4")
	assert.NotContains(t, filteredBNames, "tool5")

	// Verify ownership is correctly set on filtered tools
	for _, ti := range filteredA {
		switch ti.ServerName {
		case "shared-1":
			assert.Equal(t, OwnershipShared, ti.Ownership)
		default:
			assert.Equal(t, OwnershipPersonal, ti.Ownership)
		}
	}
}

func TestIntegration_CrossUserIsolation_GetServerForUser(t *testing.T) {
	router, _ := setupRouter(t,
		[]string{"shared-1"},
		map[string][]string{
			"userA": {"server-a1"},
			"userB": {"server-b1"},
		},
	)

	ctxA := userCtx("userA")
	ctxB := userCtx("userB")

	// User A can get their own personal server
	infoA, err := router.GetServerForUser(ctxA, "server-a1")
	require.NoError(t, err)
	assert.Equal(t, "server-a1", infoA.Config.Name)
	assert.Equal(t, OwnershipPersonal, infoA.Ownership)

	// User A cannot get User B's personal server
	_, err = router.GetServerForUser(ctxA, "server-b1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found or not accessible")

	// User B can get their own personal server
	infoB, err := router.GetServerForUser(ctxB, "server-b1")
	require.NoError(t, err)
	assert.Equal(t, "server-b1", infoB.Config.Name)
	assert.Equal(t, OwnershipPersonal, infoB.Ownership)

	// User B cannot get User A's personal server
	_, err = router.GetServerForUser(ctxB, "server-a1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found or not accessible")

	// Both can get shared server
	sharedA, err := router.GetServerForUser(ctxA, "shared-1")
	require.NoError(t, err)
	assert.Equal(t, OwnershipShared, sharedA.Ownership)

	sharedB, err := router.GetServerForUser(ctxB, "shared-1")
	require.NoError(t, err)
	assert.Equal(t, OwnershipShared, sharedB.Ownership)
}

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

func TestIntegration_ToolFilterAccessibleServerNames(t *testing.T) {
	router, _ := setupRouter(t,
		[]string{"shared-a", "shared-b"},
		map[string][]string{
			"userA": {"personal-1", "personal-2"},
			"userB": {"personal-3"},
		},
	)

	toolFilter := NewToolFilter(router, testLogger(t))

	// User A should see shared + personal servers, sorted
	ctxA := userCtx("userA")
	namesA, err := toolFilter.GetAccessibleServerNames(ctxA)
	require.NoError(t, err)
	assert.Equal(t, []string{"personal-1", "personal-2", "shared-a", "shared-b"}, namesA)

	// User B should see shared + their personal server, sorted
	ctxB := userCtx("userB")
	namesB, err := toolFilter.GetAccessibleServerNames(ctxB)
	require.NoError(t, err)
	assert.Equal(t, []string{"personal-3", "shared-a", "shared-b"}, namesB)

	// Verify IsToolAccessible respects isolation
	assert.True(t, toolFilter.IsToolAccessible(ctxA, "personal-1"))
	assert.False(t, toolFilter.IsToolAccessible(ctxA, "personal-3"))
	assert.True(t, toolFilter.IsToolAccessible(ctxB, "personal-3"))
	assert.False(t, toolFilter.IsToolAccessible(ctxB, "personal-1"))
}
