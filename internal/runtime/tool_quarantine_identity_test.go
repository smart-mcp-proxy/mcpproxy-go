package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 105 FR-009 G1 (task T004): the discovery producer must key every
// approval record by the RAW tool name it will dispatch. Before Spec 105,
// checkToolApprovals keyed records by everything after the first colon (the
// since-deleted extractToolName), so a raw "ns:erase" on server "a" was filed
// under (a, "erase") — the same key as the plain "erase" tool. Two silent
// failures followed on a manual-trust server whose baseline already approved
// "erase":
//
//   - identical description + schema: "ns:erase" hashes identically (the tool
//     name in the hash is the collapsed one and annotations are excluded), so
//     it INHERITS erase's approval — indexed and callable with zero review;
//   - differing description or schema: erase's record is marked "changed"
//     (a false rug-pull), both tools are blocked, and "ns:erase" can never be
//     approved by its own name (ApproveTools reads the exact key).
//
// The FR-009 fixture: real discovery + approval processing on a manual-trust
// server whose baseline exists, then add "ns:erase"; it must be pending under
// its own name, erase must stay approved, and approving "ns:erase" by its own
// name must lift the hold.

// manualTrustServer is a trust_mode: manual server — every post-baseline
// addition or change is held for review.
func manualTrustServer(name string) *config.ServerConfig {
	return &config.ServerConfig{Name: name, Enabled: true, TrustMode: string(config.TrustModeManual)}
}

func TestCheckToolApprovals_NamespacedTool_PendingUnderRawName(t *testing.T) {
	const (
		desc   = "Erase preview"
		schema = `{"type":"object"}`
	)

	t.Run("identical contract: ns:erase must not inherit erase's approval", func(t *testing.T) {
		rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{manualTrustServer("a")})
		seedApprovedBaseline(t, rt, "a", "erase", desc, schema)

		// Post-baseline discovery exposes the namespaced twin with the SAME
		// description and schema, which is exactly the shape that collapses
		// to erase's key and hash.
		discovered := []*config.ToolMetadata{
			{ServerName: "a", Name: "erase", Description: desc, ParamsJSON: schema},
			{ServerName: "a", Name: "ns:erase", Description: desc, ParamsJSON: schema},
		}
		result, err := rt.checkToolApprovals("a", discovered)
		require.NoError(t, err)

		assert.True(t, result.BlockedTools["ns:erase"],
			"a post-baseline addition on a manual-trust server must be held under its RAW name")
		assert.False(t, result.BlockedTools["erase"], "the baselined tool must stay callable")
		assert.Equal(t, 1, result.PendingCount, "exactly the new tool is pending")
		assert.Equal(t, 0, result.ChangedCount, "nothing changed — erase's contract is untouched")

		rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err, "the pending record must be keyed by the raw name ns:erase")
		assert.Equal(t, storage.ToolApprovalStatusPending, rec.Status)
		assert.Equal(t, "ns:erase", rec.ToolName)

		base, err := rt.storageManager.GetToolApproval("a", "erase")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusApproved, base.Status, "erase keeps its own approval")
	})

	t.Run("differing contract: erase must not be marked changed by ns:erase", func(t *testing.T) {
		rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{manualTrustServer("a")})
		seedApprovedBaseline(t, rt, "a", "erase", desc, schema)

		discovered := []*config.ToolMetadata{
			{ServerName: "a", Name: "erase", Description: desc, ParamsJSON: schema},
			{ServerName: "a", Name: "ns:erase", Description: "Erase for real", ParamsJSON: schema},
		}
		result, err := rt.checkToolApprovals("a", discovered)
		require.NoError(t, err)

		assert.Equal(t, 0, result.ChangedCount,
			"a distinct tool's contract must never read as a rug-pull of the suffix tool")
		assert.False(t, result.BlockedTools["erase"], "erase must not be blocked by a false rug-pull")
		assert.True(t, result.BlockedTools["ns:erase"])
		assert.Equal(t, 1, result.PendingCount)

		base, err := rt.storageManager.GetToolApproval("a", "erase")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusApproved, base.Status, "erase must stay approved")
		assert.Empty(t, base.PreviousDescription, "no rug-pull evidence may be attached to erase")

		rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err, "the pending record must be keyed by the raw name ns:erase")
		assert.Equal(t, storage.ToolApprovalStatusPending, rec.Status)
		assert.Equal(t, "Erase for real", rec.CurrentDescription)
	})

	t.Run("approving ns:erase by its own name lifts the hold and both identities survive rediscovery", func(t *testing.T) {
		rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{manualTrustServer("a")})
		seedApprovedBaseline(t, rt, "a", "erase", desc, schema)

		discovered := []*config.ToolMetadata{
			{ServerName: "a", Name: "erase", Description: desc, ParamsJSON: schema},
			{ServerName: "a", Name: "ns:erase", Description: desc, ParamsJSON: schema},
		}
		_, err := rt.checkToolApprovals("a", discovered)
		require.NoError(t, err)

		// The user reviews and approves the namespaced tool by the name the
		// UI shows — its raw name.
		require.NoError(t, rt.ApproveTools("a", []string{"ns:erase"}, "user"))
		rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err, "ApproveTools must find the record under the raw name")
		assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
		assert.Equal(t, "user", rec.ApprovedBy)

		// SC-005 positive control: with both approved, a rediscovery pass
		// holds nothing and the two records stay distinct.
		result, err := rt.checkToolApprovals("a", discovered)
		require.NoError(t, err)
		assert.Empty(t, result.BlockedTools)
		assert.Equal(t, 0, result.PendingCount)
		assert.Equal(t, 0, result.ChangedCount)

		records, err := rt.storageManager.ListToolApprovals("a")
		require.NoError(t, err)
		names := make([]string, 0, len(records))
		for _, r := range records {
			names = append(names, r.ToolName)
		}
		assert.ElementsMatch(t, []string{"erase", "ns:erase"}, names,
			"erase and ns:erase must be two distinct approval records")
	})
}
