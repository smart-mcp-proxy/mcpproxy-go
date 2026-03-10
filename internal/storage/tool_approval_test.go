package storage

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func setupTestStorageForToolApproval(t *testing.T) (*Manager, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "tool_approval_test_*")
	require.NoError(t, err)

	logger := zap.NewNop().Sugar()
	manager, err := NewManager(tmpDir, logger)
	require.NoError(t, err)

	cleanup := func() {
		manager.Close()
		os.RemoveAll(tmpDir)
	}

	return manager, cleanup
}

func TestToolApprovalRecord_SaveAndGet(t *testing.T) {
	manager, cleanup := setupTestStorageForToolApproval(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Millisecond)

	record := &ToolApprovalRecord{
		ServerName:         "github",
		ToolName:           "create_issue",
		ApprovedHash:       "abc123",
		CurrentHash:        "abc123",
		Status:             ToolApprovalStatusApproved,
		ApprovedAt:         now,
		ApprovedBy:         "admin",
		CurrentDescription: "Creates a new GitHub issue",
		CurrentSchema:      `{"type":"object","properties":{"title":{"type":"string"}}}`,
	}

	// Save
	err := manager.SaveToolApproval(record)
	require.NoError(t, err)

	// Get
	retrieved, err := manager.GetToolApproval("github", "create_issue")
	require.NoError(t, err)

	assert.Equal(t, "github", retrieved.ServerName)
	assert.Equal(t, "create_issue", retrieved.ToolName)
	assert.Equal(t, "abc123", retrieved.ApprovedHash)
	assert.Equal(t, "abc123", retrieved.CurrentHash)
	assert.Equal(t, ToolApprovalStatusApproved, retrieved.Status)
	assert.Equal(t, "admin", retrieved.ApprovedBy)
	assert.Equal(t, "Creates a new GitHub issue", retrieved.CurrentDescription)
	assert.Equal(t, `{"type":"object","properties":{"title":{"type":"string"}}}`, retrieved.CurrentSchema)
}

func TestToolApprovalRecord_GetNotFound(t *testing.T) {
	manager, cleanup := setupTestStorageForToolApproval(t)
	defer cleanup()

	_, err := manager.GetToolApproval("nonexistent", "tool")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "tool approval not found")
}

func TestToolApprovalRecord_ListByServer(t *testing.T) {
	manager, cleanup := setupTestStorageForToolApproval(t)
	defer cleanup()

	// Add tools for two different servers
	records := []*ToolApprovalRecord{
		{ServerName: "github", ToolName: "create_issue", Status: ToolApprovalStatusApproved, CurrentHash: "h1", ApprovedHash: "h1"},
		{ServerName: "github", ToolName: "list_repos", Status: ToolApprovalStatusPending, CurrentHash: "h2"},
		{ServerName: "gitlab", ToolName: "create_mr", Status: ToolApprovalStatusChanged, CurrentHash: "h3", ApprovedHash: "h0"},
	}

	for _, r := range records {
		err := manager.SaveToolApproval(r)
		require.NoError(t, err)
	}

	// List github tools
	githubTools, err := manager.ListToolApprovals("github")
	require.NoError(t, err)
	assert.Len(t, githubTools, 2)

	// List gitlab tools
	gitlabTools, err := manager.ListToolApprovals("gitlab")
	require.NoError(t, err)
	assert.Len(t, gitlabTools, 1)
	assert.Equal(t, "create_mr", gitlabTools[0].ToolName)

	// List all tools (empty server name)
	allTools, err := manager.ListToolApprovals("")
	require.NoError(t, err)
	assert.Len(t, allTools, 3)
}

func TestToolApprovalRecord_Delete(t *testing.T) {
	manager, cleanup := setupTestStorageForToolApproval(t)
	defer cleanup()

	record := &ToolApprovalRecord{
		ServerName:  "github",
		ToolName:    "create_issue",
		Status:      ToolApprovalStatusPending,
		CurrentHash: "h1",
	}

	err := manager.SaveToolApproval(record)
	require.NoError(t, err)

	// Verify it exists
	_, err = manager.GetToolApproval("github", "create_issue")
	require.NoError(t, err)

	// Delete
	err = manager.DeleteToolApproval("github", "create_issue")
	require.NoError(t, err)

	// Verify it's gone
	_, err = manager.GetToolApproval("github", "create_issue")
	assert.Error(t, err)
}

func TestToolApprovalRecord_DeleteServerToolApprovals(t *testing.T) {
	manager, cleanup := setupTestStorageForToolApproval(t)
	defer cleanup()

	// Add tools for two servers
	records := []*ToolApprovalRecord{
		{ServerName: "github", ToolName: "create_issue", Status: ToolApprovalStatusApproved, CurrentHash: "h1", ApprovedHash: "h1"},
		{ServerName: "github", ToolName: "list_repos", Status: ToolApprovalStatusPending, CurrentHash: "h2"},
		{ServerName: "gitlab", ToolName: "create_mr", Status: ToolApprovalStatusApproved, CurrentHash: "h3", ApprovedHash: "h3"},
	}

	for _, r := range records {
		err := manager.SaveToolApproval(r)
		require.NoError(t, err)
	}

	// Delete all github tools
	err := manager.DeleteServerToolApprovals("github")
	require.NoError(t, err)

	// Verify github tools are gone
	githubTools, err := manager.ListToolApprovals("github")
	require.NoError(t, err)
	assert.Len(t, githubTools, 0)

	// Verify gitlab tools remain
	gitlabTools, err := manager.ListToolApprovals("gitlab")
	require.NoError(t, err)
	assert.Len(t, gitlabTools, 1)
}

func TestToolApprovalRecord_StatusTransitions(t *testing.T) {
	manager, cleanup := setupTestStorageForToolApproval(t)
	defer cleanup()

	// Start as pending (new tool discovered)
	record := &ToolApprovalRecord{
		ServerName:         "github",
		ToolName:           "create_issue",
		CurrentHash:        "hash_v1",
		Status:             ToolApprovalStatusPending,
		CurrentDescription: "Creates issues",
	}
	err := manager.SaveToolApproval(record)
	require.NoError(t, err)

	retrieved, err := manager.GetToolApproval("github", "create_issue")
	require.NoError(t, err)
	assert.Equal(t, ToolApprovalStatusPending, retrieved.Status)

	// Transition: pending -> approved
	retrieved.Status = ToolApprovalStatusApproved
	retrieved.ApprovedHash = retrieved.CurrentHash
	retrieved.ApprovedAt = time.Now().UTC()
	retrieved.ApprovedBy = "admin"
	err = manager.SaveToolApproval(retrieved)
	require.NoError(t, err)

	retrieved2, err := manager.GetToolApproval("github", "create_issue")
	require.NoError(t, err)
	assert.Equal(t, ToolApprovalStatusApproved, retrieved2.Status)
	assert.Equal(t, "hash_v1", retrieved2.ApprovedHash)
	assert.Equal(t, "admin", retrieved2.ApprovedBy)

	// Transition: approved -> changed (tool description was modified)
	retrieved2.Status = ToolApprovalStatusChanged
	retrieved2.PreviousDescription = retrieved2.CurrentDescription
	retrieved2.CurrentDescription = "Creates issues with labels"
	retrieved2.PreviousSchema = retrieved2.CurrentSchema
	retrieved2.CurrentSchema = `{"type":"object","properties":{"title":{"type":"string"},"labels":{"type":"array"}}}`
	retrieved2.CurrentHash = "hash_v2"
	err = manager.SaveToolApproval(retrieved2)
	require.NoError(t, err)

	retrieved3, err := manager.GetToolApproval("github", "create_issue")
	require.NoError(t, err)
	assert.Equal(t, ToolApprovalStatusChanged, retrieved3.Status)
	assert.Equal(t, "hash_v1", retrieved3.ApprovedHash)
	assert.Equal(t, "hash_v2", retrieved3.CurrentHash)
	assert.Equal(t, "Creates issues", retrieved3.PreviousDescription)
	assert.Equal(t, "Creates issues with labels", retrieved3.CurrentDescription)

	// Transition: changed -> approved (admin re-approves after review)
	retrieved3.Status = ToolApprovalStatusApproved
	retrieved3.ApprovedHash = retrieved3.CurrentHash
	retrieved3.ApprovedAt = time.Now().UTC()
	retrieved3.PreviousDescription = ""
	retrieved3.PreviousSchema = ""
	err = manager.SaveToolApproval(retrieved3)
	require.NoError(t, err)

	retrieved4, err := manager.GetToolApproval("github", "create_issue")
	require.NoError(t, err)
	assert.Equal(t, ToolApprovalStatusApproved, retrieved4.Status)
	assert.Equal(t, "hash_v2", retrieved4.ApprovedHash)
}

func TestToolApprovalKey(t *testing.T) {
	assert.Equal(t, "github:create_issue", ToolApprovalKey("github", "create_issue"))
	assert.Equal(t, "my-server:my-tool", ToolApprovalKey("my-server", "my-tool"))

	record := &ToolApprovalRecord{ServerName: "github", ToolName: "list_repos"}
	assert.Equal(t, "github:list_repos", record.Key())
}

func TestToolApprovalRecord_MarshalUnmarshal(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	record := &ToolApprovalRecord{
		ServerName:          "github",
		ToolName:            "create_issue",
		ApprovedHash:        "abc123",
		CurrentHash:         "def456",
		Status:              ToolApprovalStatusChanged,
		ApprovedAt:          now,
		ApprovedBy:          "admin",
		PreviousDescription: "Old description",
		CurrentDescription:  "New description",
		PreviousSchema:      `{"old": true}`,
		CurrentSchema:       `{"new": true}`,
	}

	data, err := record.MarshalBinary()
	require.NoError(t, err)
	require.NotEmpty(t, data)

	var result ToolApprovalRecord
	err = result.UnmarshalBinary(data)
	require.NoError(t, err)

	assert.Equal(t, record.ServerName, result.ServerName)
	assert.Equal(t, record.ToolName, result.ToolName)
	assert.Equal(t, record.ApprovedHash, result.ApprovedHash)
	assert.Equal(t, record.CurrentHash, result.CurrentHash)
	assert.Equal(t, record.Status, result.Status)
	assert.Equal(t, record.ApprovedBy, result.ApprovedBy)
	assert.Equal(t, record.PreviousDescription, result.PreviousDescription)
	assert.Equal(t, record.CurrentDescription, result.CurrentDescription)
	assert.Equal(t, record.PreviousSchema, result.PreviousSchema)
	assert.Equal(t, record.CurrentSchema, result.CurrentSchema)
}
