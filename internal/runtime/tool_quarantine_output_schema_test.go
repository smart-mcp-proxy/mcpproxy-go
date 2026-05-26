package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func TestOutputSchemaHashMigration_ApprovedToolStaysApproved(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "github", Enabled: true}})
	require.NoError(t, rt.storageManager.SetSchemaVersion(storage.OutputSchemaHashSchemaVersion-1))

	legacyHash := calculateLegacyToolApprovalHash("create_issue", "Creates a GitHub issue", `{"type":"object"}`)
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName:         "github",
		ToolName:           "create_issue",
		ApprovedHash:       legacyHash,
		CurrentHash:        legacyHash,
		Status:             storage.ToolApprovalStatusApproved,
		CurrentDescription: "Creates a GitHub issue",
		CurrentSchema:      `{"type":"object"}`,
	}))

	result, err := rt.checkToolApprovals("github", []*config.ToolMetadata{{
		ServerName:       "github",
		Name:             "create_issue",
		Description:      "Creates a GitHub issue",
		ParamsJSON:       `{"type":"object"}`,
		OutputSchemaJSON: `{"type":"object","properties":{"url":{"type":"string"}}}`,
	}})
	require.NoError(t, err)
	assert.Empty(t, result.BlockedTools)
	assert.Zero(t, result.ChangedCount)

	record, err := rt.storageManager.GetToolApproval("github", "create_issue")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusApproved, record.Status)
	assert.NotEqual(t, legacyHash, record.ApprovedHash)
	assert.Equal(t, record.ApprovedHash, record.CurrentHash)
	assert.Equal(t, uint64(storage.OutputSchemaHashSchemaVersion), record.HashSchemaVersion)
	assert.Equal(t, `{"properties":{"url":{"type":"string"}},"type":"object"}`, record.CurrentOutputSchema)

	version, err := rt.storageManager.GetSchemaVersion()
	require.NoError(t, err)
	assert.Equal(t, uint64(storage.OutputSchemaHashSchemaVersion), version)
}

func TestOutputSchemaHashMigration_DescriptionDriftRoutesToChanged(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "github", Enabled: true}})
	require.NoError(t, rt.storageManager.SetSchemaVersion(storage.OutputSchemaHashSchemaVersion-1))

	legacyHash := calculateLegacyToolApprovalHash("create_issue", "Creates a GitHub issue", `{"type":"object"}`)
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName:         "github",
		ToolName:           "create_issue",
		ApprovedHash:       legacyHash,
		CurrentHash:        legacyHash,
		Status:             storage.ToolApprovalStatusApproved,
		CurrentDescription: "Creates a GitHub issue",
		CurrentSchema:      `{"type":"object"}`,
	}))

	result, err := rt.checkToolApprovals("github", []*config.ToolMetadata{{
		ServerName:       "github",
		Name:             "create_issue",
		Description:      "Creates a GitHub issue and edits labels",
		ParamsJSON:       `{"type":"object"}`,
		OutputSchemaJSON: `{"type":"object","properties":{"url":{"type":"string"}}}`,
	}})
	require.NoError(t, err)
	assert.True(t, result.BlockedTools["create_issue"])
	assert.Equal(t, 1, result.ChangedCount)

	record, err := rt.storageManager.GetToolApproval("github", "create_issue")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusChanged, record.Status)
	assert.Equal(t, legacyHash, record.ApprovedHash)
	assert.Equal(t, "Creates a GitHub issue", record.PreviousDescription)
	assert.Equal(t, "Creates a GitHub issue and edits labels", record.CurrentDescription)
}

func TestOutputSchemaHashMigration_PendingAndChangedRemainUntouched(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "github", Enabled: true}})
	require.NoError(t, rt.storageManager.SetSchemaVersion(storage.OutputSchemaHashSchemaVersion-1))

	pendingHash := calculateLegacyToolApprovalHash("pending_tool", "Pending", `{"type":"object"}`)
	changedHash := calculateLegacyToolApprovalHash("changed_tool", "Changed", `{"type":"object"}`)
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName:         "github",
		ToolName:           "pending_tool",
		CurrentHash:        pendingHash,
		Status:             storage.ToolApprovalStatusPending,
		CurrentDescription: "Pending",
		CurrentSchema:      `{"type":"object"}`,
	}))
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName:          "github",
		ToolName:            "changed_tool",
		ApprovedHash:        changedHash,
		CurrentHash:         "different",
		Status:              storage.ToolApprovalStatusChanged,
		PreviousDescription: "Changed",
		CurrentDescription:  "Changed mutated",
		CurrentSchema:       `{"type":"object"}`,
	}))

	result, err := rt.checkToolApprovals("github", []*config.ToolMetadata{
		{
			ServerName:       "github",
			Name:             "pending_tool",
			Description:      "Pending",
			ParamsJSON:       `{"type":"object"}`,
			OutputSchemaJSON: `{"type":"object","properties":{"ok":{"type":"boolean"}}}`,
		},
		{
			ServerName:       "github",
			Name:             "changed_tool",
			Description:      "Changed mutated",
			ParamsJSON:       `{"type":"object"}`,
			OutputSchemaJSON: `{"type":"object","properties":{"ok":{"type":"boolean"}}}`,
		},
	})
	require.NoError(t, err)
	assert.True(t, result.BlockedTools["changed_tool"])

	pending, err := rt.storageManager.GetToolApproval("github", "pending_tool")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusPending, pending.Status)
	assert.Empty(t, pending.ApprovedHash)

	changed, err := rt.storageManager.GetToolApproval("github", "changed_tool")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusChanged, changed.Status)
	assert.Equal(t, changedHash, changed.ApprovedHash)
}

func TestOutputSchemaHashChange_MarksApprovedToolChangedAfterMigration(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "github", Enabled: true}})

	inputSchema := `{"type":"object"}`
	approvedOutputSchema := `{"type":"object","properties":{"url":{"type":"string"}}}`
	approvedHash := calculateToolApprovalHashWithOutputSchema("create_issue", "Creates a GitHub issue", inputSchema, approvedOutputSchema, nil)
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName:          "github",
		ToolName:            "create_issue",
		ApprovedHash:        approvedHash,
		CurrentHash:         approvedHash,
		HashSchemaVersion:   storage.OutputSchemaHashSchemaVersion,
		Status:              storage.ToolApprovalStatusApproved,
		CurrentDescription:  "Creates a GitHub issue",
		CurrentSchema:       inputSchema,
		CurrentOutputSchema: approvedOutputSchema,
	}))
	require.NoError(t, rt.storageManager.SetSchemaVersion(storage.OutputSchemaHashSchemaVersion))

	result, err := rt.checkToolApprovals("github", []*config.ToolMetadata{{
		ServerName:       "github",
		Name:             "create_issue",
		Description:      "Creates a GitHub issue",
		ParamsJSON:       inputSchema,
		OutputSchemaJSON: `{"type":"object","properties":{"id":{"type":"integer"}}}`,
	}})
	require.NoError(t, err)
	assert.True(t, result.BlockedTools["create_issue"])
	assert.Equal(t, 1, result.ChangedCount)

	record, err := rt.storageManager.GetToolApproval("github", "create_issue")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusChanged, record.Status)
	assert.Equal(t, approvedOutputSchema, record.PreviousOutputSchema)
	assert.Equal(t, `{"properties":{"id":{"type":"integer"}},"type":"object"}`, record.CurrentOutputSchema)
}
