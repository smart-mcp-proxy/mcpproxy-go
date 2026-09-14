package runtime

import (
	"context"
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

// Spec 105 FR-009, legacy carry-over (migration review finding): a pre-105
// binary filed the raw "ns:erase" under the COLLAPSED key "erase", so the
// operator who hid ns:erase in the UI toggled Disabled on (a, "erase") — one
// record that, read through the collapsed key, blocked BOTH names. The exact
// keying makes ns:erase miss on the first discovery after upgrade and take the
// new-tool branch; with the gate lifted (quarantine off, skip_quarantine,
// auto-approve changes, green scan) that branch used to save an approved,
// ENABLED exact record, and the reader — which consults the collapsed record
// only while no exact one exists — would never see the block again: the
// operator's decision silently evaporated within seconds of the upgrade.
//
// The producer therefore reads the collapsed sibling when the exact key
// misses and carries a restricting record's block onto the new exact record.
func TestCheckToolApprovals_NamespacedTool_InheritsLegacyCollapsedBlock(t *testing.T) {
	const (
		desc   = "Erase preview"
		schema = `{"type":"object"}`
	)
	discovered := func() []*config.ToolMetadata {
		return []*config.ToolMetadata{
			{ServerName: "a", Name: "erase", RawName: "erase", Description: desc, ParamsJSON: schema},
			{ServerName: "a", Name: "ns:erase", RawName: "ns:erase", Description: desc, ParamsJSON: schema},
		}
	}

	t.Run("quarantine off: Disabled collapsed record blocks the new exact record", func(t *testing.T) {
		rt := setupQuarantineRuntime(t, boolP(false), []*config.ServerConfig{{Name: "a", Enabled: true}})
		// What the pre-105 store holds: erase approved and user-disabled —
		// the toggle the operator applied to hide ns:erase.
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved,
			ApprovedBy: "user", Disabled: true,
		}))
		_, err := rt.storageManager.GetToolApproval("a", "ns:erase")
		require.ErrorIs(t, err, storage.ErrToolApprovalNotFound, "precondition: no exact record for ns:erase")

		result, err := rt.checkToolApprovals("a", discovered())
		require.NoError(t, err)

		assert.True(t, result.BlockedTools["ns:erase"], "the carried-over block must keep ns:erase out of the index")
		assert.True(t, result.BlockedTools["erase"], "erase itself is user-disabled")
		assert.Equal(t, 0, result.PendingCount, "the gate is off: nothing is pending, the block is the user's")

		rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err, "the exact record is filed on the first discovery")
		assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status, "with the gate off the record auto-approves ...")
		assert.True(t, rec.Disabled, "... but carries the operator's block from the collapsed record")
		assert.Equal(t, "auto", rec.ApprovedBy)

		// End to end through the differential: not indexed, and the block
		// survives a rediscovery (the exact record is now the one read).
		require.NoError(t, rt.applyDifferentialToolUpdate(context.Background(), "a", discovered()))
		indexed, err := rt.indexManager.GetToolsByServer("a")
		require.NoError(t, err)
		assert.Empty(t, indexed, "neither the user-disabled erase nor the blocked ns:erase may be indexed")

		result, err = rt.checkToolApprovals("a", discovered())
		require.NoError(t, err)
		assert.True(t, result.BlockedTools["ns:erase"], "the block is on the exact record now and keeps binding")
		rec, err = rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.True(t, rec.Disabled)
	})

	t.Run("quarantine on: pending collapsed record's lock is carried as a block onto the pending exact record", func(t *testing.T) {
		rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{manualTrustServer("a")})
		seedApprovedBaseline(t, rt, "a", "old_tool", desc, schema)
		// The collapsed record is LOCKED (pending review pre-upgrade), not
		// user-disabled: a lock restricts too, and is carried as a block so
		// the namespaced tool stays out of reach until an operator looks.
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusPending,
		}))

		result, err := rt.checkToolApprovals("a", discovered())
		require.NoError(t, err)
		assert.True(t, result.BlockedTools["ns:erase"])

		rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusPending, rec.Status, "post-baseline addition on a manual server is pending")
		assert.True(t, rec.Disabled, "the collapsed record's lock is carried over as a block")
	})

	t.Run("control: an approved, enabled collapsed record lends nothing", func(t *testing.T) {
		rt := setupQuarantineRuntime(t, boolP(false), []*config.ServerConfig{{Name: "a", Enabled: true}})
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user",
		}))

		result, err := rt.checkToolApprovals("a", discovered())
		require.NoError(t, err)
		assert.False(t, result.BlockedTools["ns:erase"], "an approval belongs to the exact raw name alone; nothing restricts ns:erase")

		rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.False(t, rec.Disabled)
		assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
	})

	t.Run("control: a raw name without a colon has no collapsed sibling", func(t *testing.T) {
		rt := setupQuarantineRuntime(t, boolP(false), []*config.ServerConfig{{Name: "a", Enabled: true}})
		result, err := rt.checkToolApprovals("a", []*config.ToolMetadata{
			{ServerName: "a", Name: "plain", RawName: "plain", Description: desc, ParamsJSON: schema},
		})
		require.NoError(t, err)
		assert.Empty(t, result.BlockedTools)
	})
}
