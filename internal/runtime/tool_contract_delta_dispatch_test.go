package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// TestHashEqualDispatchDrop_Bites (spec 120 FR-008, written first): a tool
// approved with explicit {readOnlyHint:false, destructiveHint:true} whose live
// definition drops its annotations keeps its approval hash (the hash folds in
// the EFFECTIVE hints, and these are the MCP defaults) while DeriveCallWith
// routes it as read instead of destructive. The gate must hold it (FR-009).
func TestHashEqualDispatchDrop_Bites(t *testing.T) {
	f := false
	tr := true
	explicit := &config.ToolAnnotations{ReadOnlyHint: &f, DestructiveHint: &tr}
	schema := `{"type":"object"}`

	// The premise: equal hash, different dispatch variant.
	withHints := calculateToolApprovalHashWithOutputSchema("drop_db", "Drops the database", schema, "", explicit)
	without := calculateToolApprovalHashWithOutputSchema("drop_db", "Drops the database", schema, "", nil)
	require.Equal(t, withHints, without, "premise: the approval hash does not see explicit-default hints")
	require.Equal(t, contracts.ToolVariantDestructive, contracts.DeriveCallWith(explicit))
	require.Equal(t, contracts.ToolVariantRead, contracts.DeriveCallWith(nil), "premise: dropping the hints drops the dispatch tier")

	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{scanServer("db")})
	approved := []*config.ToolMetadata{{ServerName: "db", Name: "drop_db", Description: "Drops the database", ParamsJSON: schema, Annotations: explicit}}
	_, err := rt.checkToolApprovals("db", approved)
	require.NoError(t, err)
	seeded, err := rt.storageManager.GetToolApproval("db", "drop_db")
	require.NoError(t, err)
	require.Equal(t, storage.ToolApprovalStatusApproved, seeded.Status)

	dropped := []*config.ToolMetadata{{ServerName: "db", Name: "drop_db", Description: "Drops the database", ParamsJSON: schema}}
	for pass := 0; pass < 2; pass++ {
		result, err := rt.checkToolApprovals("db", dropped)
		require.NoError(t, err)
		assert.True(t, result.BlockedTools["drop_db"], "pass %d: a hash-equal dispatch-tier drop is held", pass)
		rec, err := rt.storageManager.GetToolApproval("db", "drop_db")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusChanged, rec.Status, "pass %d", pass)
		assert.Equal(t, seeded.ApprovedHash, rec.ApprovedHash, "pass %d: the approved hash is unmoved", pass)
		assert.Equal(t, storage.ToolHeldReasonScanFindings, rec.HeldReason, "pass %d", pass)
		assert.Equal(t, "clean", rec.HeldVerdict, "pass %d", pass)
		assert.Contains(t, rec.HeldSignals, "GATE-HINT-CHANGE:dispatch:destructive>read", "pass %d", pass)
	}

	// The server restores the explicit hints: that is the approved contract.
	result, err := rt.checkToolApprovals("db", approved)
	require.NoError(t, err)
	assert.False(t, result.BlockedTools["drop_db"], "restoring the hints restores the approval")
	rec, err := rt.storageManager.GetToolApproval("db", "drop_db")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
}

// TestHashEqualDispatchDrop_ManualHeldAutoApproved: under manual the same
// change is held like any other; under auto it is approved.
func TestHashEqualDispatchDrop_ManualHeldAutoApproved(t *testing.T) {
	f := false
	tr := true
	explicit := &config.ToolAnnotations{ReadOnlyHint: &f, DestructiveHint: &tr}
	schema := `{"type":"object"}`
	for _, tc := range []struct {
		name   string
		server *config.ServerConfig
		held   bool
	}{
		{"manual", manualServer("db"), true},
		{"auto", &config.ServerConfig{Name: "db", Enabled: true, TrustMode: string(config.TrustModeAuto)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{tc.server})
			approved := []*config.ToolMetadata{{ServerName: "db", Name: "drop_db", Description: "Drops the database", ParamsJSON: schema, Annotations: explicit}}
			_, err := rt.checkToolApprovals("db", approved)
			require.NoError(t, err)
			dropped := []*config.ToolMetadata{{ServerName: "db", Name: "drop_db", Description: "Drops the database", ParamsJSON: schema}}
			result, err := rt.checkToolApprovals("db", dropped)
			require.NoError(t, err)
			assert.Equal(t, tc.held, result.BlockedTools["drop_db"])
			rec, err := rt.storageManager.GetToolApproval("db", "drop_db")
			require.NoError(t, err)
			if tc.held {
				assert.Equal(t, storage.ToolApprovalStatusChanged, rec.Status)
			} else {
				assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
				assert.Nil(t, rec.CurrentAnnotations)
			}
		})
	}
}
