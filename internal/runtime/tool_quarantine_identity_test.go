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

	t.Run("quarantine on: a pending collapsed record's lock is NOT converted into a user block", func(t *testing.T) {
		rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{manualTrustServer("a")})
		seedApprovedBaseline(t, rt, "a", "old_tool", desc, schema)
		// The collapsed record is LOCKED (pending review pre-upgrade), not
		// user-disabled. A lock is a review hold, not a user decision: it is
		// adopted onto the new exact record (round-4 finding 2), which holds
		// the tool under the active gate, and the operator's approval BY ITS
		// OWN NAME must lift it — a carried Disabled would survive that
		// approval as a phantom user block.
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusPending,
		}))

		result, err := rt.checkToolApprovals("a", discovered())
		require.NoError(t, err)
		assert.True(t, result.BlockedTools["ns:erase"])
		assert.True(t, result.BlockedTools["erase"], "the collapsed record is exactly the raw erase's own pending record")
		assert.Equal(t, 2, result.PendingCount)

		rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusPending, rec.Status, "post-baseline addition on a manual server is pending")
		assert.False(t, rec.Disabled, "a review lock on the sibling is not the user's block")
		assert.True(t, rec.IdentityKeyed, "the exact record is stamped by the post-105 producer")

		// The operator approves it by its own name and it is callable: no
		// second, unrelated enable toggle is required.
		require.NoError(t, rt.ApproveTools("a", []string{"ns:erase"}, "user"))
		result, err = rt.checkToolApprovals("a", discovered())
		require.NoError(t, err)
		assert.False(t, result.BlockedTools["ns:erase"], "approval by its own name lifts the hold")
	})

	t.Run("quarantine off: a pending collapsed record's lock never bound and is not carried as a block", func(t *testing.T) {
		rt := setupQuarantineRuntime(t, boolP(false), []*config.ServerConfig{{Name: "a", Enabled: true}})
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusPending,
		}))

		result, err := rt.checkToolApprovals("a", discovered())
		require.NoError(t, err)
		assert.False(t, result.BlockedTools["ns:erase"], "with the gate lifted the pre-105 lock never bound; the tool stays callable")

		rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
		assert.False(t, rec.Disabled)
	})

	t.Run("post-105 sibling: a genuine erase hidden by the user lends nothing to a new v2:erase", func(t *testing.T) {
		rt := setupQuarantineRuntime(t, boolP(false), []*config.ServerConfig{{Name: "a", Enabled: true}})
		// Months after upgrade: erase is discovered and filed by THIS binary
		// (stamped), then the operator hides it in the UI.
		result, err := rt.checkToolApprovals("a", []*config.ToolMetadata{
			{ServerName: "a", Name: "erase", RawName: "erase", Description: desc, ParamsJSON: schema},
		})
		require.NoError(t, err)
		require.Empty(t, result.BlockedTools)
		flipped, err := rt.setToolEnabledNoEmit("a", "erase", false, "user")
		require.NoError(t, err)
		require.True(t, flipped)
		erase, err := rt.storageManager.GetToolApproval("a", "erase")
		require.NoError(t, err)
		require.True(t, erase.Disabled)
		require.True(t, erase.IdentityKeyed, "precondition: the sibling is a post-105 record")

		// The upstream adds a genuinely new v2:erase.
		result, err = rt.checkToolApprovals("a", []*config.ToolMetadata{
			{ServerName: "a", Name: "erase", RawName: "erase", Description: desc, ParamsJSON: schema},
			{ServerName: "a", Name: "v2:erase", RawName: "v2:erase", Description: "Erase v2", ParamsJSON: schema},
		})
		require.NoError(t, err)
		assert.True(t, result.BlockedTools["erase"], "erase itself stays hidden")
		assert.False(t, result.BlockedTools["v2:erase"], "a stamped sibling's block is the sibling's alone (FR-009: no inheritance)")

		rec, err := rt.storageManager.GetToolApproval("a", "v2:erase")
		require.NoError(t, err)
		assert.False(t, rec.Disabled)
		assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
	})

	// Round-3 finding 2: the legacy consults must be bounded to the UPGRADE,
	// which holds only if the first discovery pass stamps every record the
	// server holds — including a pre-105 record whose hash still matches
	// (nothing else about it changes, so the pass used to skip the write) and
	// a collapsed record whose bare name the upstream does not serve at all.
	// Round-4 finding 3 narrows the orphan rule: an orphan that still
	// RESTRICTS (disabled / pending / changed) is a pre-105 decision about a
	// tool merely absent from this pass and is left unstamped (see below);
	// an approved, enabled orphan carries only an approval and is stamped.
	// Round-5 finding 2b extends the same rule to a SERVED bare name: a
	// user-disabled "erase" listed on pass 1 may still be the collapsed
	// record of an "ns:erase" that only appears on pass 2, so the hash-match
	// path leaves it unstamped too (saveReadToolApproval) — at the cost that
	// a genuinely new v2:erase inherits the block ONCE, after which the
	// record is stamped and lends nothing further.
	t.Run("pre-105 records untouched by a pass: an enabled one is stamped, a disabled one waits and lends its block once", func(t *testing.T) {
		rt := setupQuarantineRuntime(t, boolP(false), []*config.ServerConfig{{Name: "a", Enabled: true}})
		current := calculateToolApprovalHashWithOutputSchema("erase", desc, schema, "", nil)
		// A pre-105 erase, approved for exactly the served contract and
		// user-disabled — the hash-match branch, which had no reason to save.
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved,
			ApprovedHash: current, CurrentHash: current, HashSchemaVersion: storage.OutputSchemaHashSchemaVersion,
			CurrentDescription: desc, CurrentSchema: schema, Disabled: true,
		}))
		// A pre-105 keep, approved, ENABLED and served: the hash-match branch
		// with nothing to keep waiting for.
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "keep", Status: storage.ToolApprovalStatusApproved,
			ApprovedHash: current, CurrentHash: current, HashSchemaVersion: storage.OutputSchemaHashSchemaVersion,
			CurrentDescription: desc, CurrentSchema: schema,
		}))
		// An orphaned, approved and ENABLED collapsed record ("legacy" from a
		// raw "ns:legacy" the upstream no longer serves), never seen by the
		// loop at all.
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "legacy", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user",
		}))

		result, err := rt.checkToolApprovals("a", []*config.ToolMetadata{
			{ServerName: "a", Name: "erase", RawName: "erase", Description: desc, ParamsJSON: schema},
			{ServerName: "a", Name: "keep", RawName: "keep", Description: desc, ParamsJSON: schema},
		})
		require.NoError(t, err)
		assert.True(t, result.BlockedTools["erase"])
		assert.False(t, result.BlockedTools["keep"])
		for _, name := range []string{"keep", "legacy"} {
			rec, err := rt.storageManager.GetToolApproval("a", name)
			require.NoError(t, err)
			assert.True(t, rec.IdentityKeyed, "one pass must stamp the unrestricting %q", name)
		}
		erase, err := rt.storageManager.GetToolApproval("a", "erase")
		require.NoError(t, err)
		assert.False(t, erase.IdentityKeyed, "a served but user-disabled pre-105 record keeps waiting for a namespaced tool it may have been filed for")
		assert.True(t, erase.Disabled, "nothing else about the record changes")

		// Months later the upstream adds genuinely new namespaced tools whose
		// collapsed keys collide with the records: the stamped ones lend
		// nothing; the waiting erase lends its block once and is stamped by
		// the pass that consulted it.
		result, err = rt.checkToolApprovals("a", []*config.ToolMetadata{
			{ServerName: "a", Name: "erase", RawName: "erase", Description: desc, ParamsJSON: schema},
			{ServerName: "a", Name: "keep", RawName: "keep", Description: desc, ParamsJSON: schema},
			{ServerName: "a", Name: "v2:erase", RawName: "v2:erase", Description: "Erase v2", ParamsJSON: schema},
			{ServerName: "a", Name: "v2:keep", RawName: "v2:keep", Description: "Keep v2", ParamsJSON: schema},
			{ServerName: "a", Name: "v2:legacy", RawName: "v2:legacy", Description: "Legacy v2", ParamsJSON: schema},
		})
		require.NoError(t, err)
		assert.True(t, result.BlockedTools["erase"])
		assert.True(t, result.BlockedTools["v2:erase"], "the waiting disabled record lends its block once (the accepted cost of round-5 finding 2b)")
		assert.False(t, result.BlockedTools["v2:keep"], "the stamped keep lends nothing")
		assert.False(t, result.BlockedTools["v2:legacy"], "the stamped orphan lends nothing either")
		for _, name := range []string{"v2:keep", "v2:legacy"} {
			rec, err := rt.storageManager.GetToolApproval("a", name)
			require.NoError(t, err)
			assert.False(t, rec.Disabled, "%q is filed by the normal new-tool rule", name)
			assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
		}
		erase, err = rt.storageManager.GetToolApproval("a", "erase")
		require.NoError(t, err)
		assert.True(t, erase.IdentityKeyed, "consulted once; the pass that files v2:erase stamps it")

		// From now on erase is a genuine sibling's: a later v3:erase inherits
		// nothing, and un-hiding v2:erase is the operator's one-time cost.
		result, err = rt.checkToolApprovals("a", []*config.ToolMetadata{
			{ServerName: "a", Name: "erase", RawName: "erase", Description: desc, ParamsJSON: schema},
			{ServerName: "a", Name: "v3:erase", RawName: "v3:erase", Description: "Erase v3", ParamsJSON: schema},
		})
		require.NoError(t, err)
		assert.True(t, result.BlockedTools["erase"])
		assert.False(t, result.BlockedTools["v3:erase"], "the stamped erase lends nothing to a later namespaced tool")
	})

	// Round-4 finding 3: a collapsed pre-105 record that still RESTRICTS is
	// left unstamped by a pass that does not list its tool — the namespaced
	// tool was not served in the first pass after upgrade (or an
	// authoritative empty refresh dropped it) and reappears later. Pre-105
	// the Disabled record survived exactly such an absence (#873 evicts a
	// disabled tool from the index, so the removal step never deleted its
	// record) and kept blocking the tool; the block must survive the upgrade
	// the same way instead of being stamped into a "genuine sibling" that
	// lends nothing.
	t.Run("a restricting orphan waits for its tool and is consulted once when it reappears", func(t *testing.T) {
		rt := setupQuarantineRuntime(t, boolP(false), []*config.ServerConfig{{Name: "a", Enabled: true}})
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user", Disabled: true,
		}))

		// Pass 1 lists only "other": the orphan is not stamped.
		result, err := rt.checkToolApprovals("a", []*config.ToolMetadata{
			{ServerName: "a", Name: "other", RawName: "other", Description: desc, ParamsJSON: schema},
		})
		require.NoError(t, err)
		assert.Empty(t, result.BlockedTools)
		orphan, err := rt.storageManager.GetToolApproval("a", "erase")
		require.NoError(t, err)
		assert.False(t, orphan.IdentityKeyed, "a user-disabled orphan must wait for its tool, not be stamped")
		assert.True(t, orphan.Disabled)

		// An authoritative empty refresh does not stamp it either.
		_, err = rt.checkToolApprovals("a", nil)
		require.NoError(t, err)
		orphan, err = rt.storageManager.GetToolApproval("a", "erase")
		require.NoError(t, err)
		assert.False(t, orphan.IdentityKeyed, "an empty authoritative refresh must not stamp a restricting orphan")

		// Pass 2 lists ns:erase: filed Disabled from the orphan, refused
		// (the pre-105 reader would have blocked it through the collapsed
		// key), and the orphan is stamped by this pass.
		result, err = rt.checkToolApprovals("a", []*config.ToolMetadata{
			{ServerName: "a", Name: "other", RawName: "other", Description: desc, ParamsJSON: schema},
			{ServerName: "a", Name: "ns:erase", RawName: "ns:erase", Description: desc, ParamsJSON: schema},
		})
		require.NoError(t, err)
		assert.True(t, result.BlockedTools["ns:erase"], "the block the operator applied pre-105 must keep binding the reappeared tool")
		rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.True(t, rec.Disabled)
		assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status, "gate off: approved, blocked only by the user")
		assert.True(t, rec.IdentityKeyed)
		orphan, err = rt.storageManager.GetToolApproval("a", "erase")
		require.NoError(t, err)
		assert.True(t, orphan.IdentityKeyed, "consulted once; the pass that files the exact record stamps the orphan")

		// From now on the orphan is a genuine sibling's: a later v3:erase
		// inherits nothing.
		result, err = rt.checkToolApprovals("a", []*config.ToolMetadata{
			{ServerName: "a", Name: "ns:erase", RawName: "ns:erase", Description: desc, ParamsJSON: schema},
			{ServerName: "a", Name: "v3:erase", RawName: "v3:erase", Description: "Erase v3", ParamsJSON: schema},
		})
		require.NoError(t, err)
		assert.True(t, result.BlockedTools["ns:erase"])
		assert.False(t, result.BlockedTools["v3:erase"], "the stamped record lends nothing to a later namespaced tool")
	})

	// Round-4 findings 2 + 5: an unstamped collapsed sibling's pending /
	// changed LOCK is adopted onto the first exact record, with its evidence,
	// under an active gate — including the baseline pass, where the new-tool
	// ladder would otherwise auto-baseline the namespaced tool that
	// origin/main held pending (FR-009: pending until approved by its own
	// name). Under a lifted gate the lock never bound and the tool is
	// promoted as today.
	t.Run("baseline pass: a pending collapsed lock is adopted with its evidence, not auto-baselined", func(t *testing.T) {
		rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{manualTrustServer("a")})
		// The pre-105 store of a server that was unquarantined while its
		// upstream had bumped descriptions: every record pending with a
		// stale hash, no approved/changed record anywhere — a baseline pass.
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusPending,
			CurrentHash: "pre-105-stale", CurrentDescription: "Erase (old)",
			HeldReason: storage.ToolHeldReasonScanFindings, HeldVerdict: "unsafe", HeldSignals: []string{"TPA-2026-0001"},
		}))

		result, err := rt.checkToolApprovals("a", []*config.ToolMetadata{
			{ServerName: "a", Name: "ns:erase", RawName: "ns:erase", Description: desc, ParamsJSON: schema},
			{ServerName: "a", Name: "fresh", RawName: "fresh", Description: desc, ParamsJSON: schema},
		})
		require.NoError(t, err)
		assert.False(t, result.BlockedTools["fresh"], "control: it IS a baseline pass — a genuinely new tool auto-baselines")
		assert.True(t, result.BlockedTools["ns:erase"], "the tool origin/main held pending stays pending under its own name")
		assert.Equal(t, 1, result.PendingCount)

		rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusPending, rec.Status)
		assert.Empty(t, rec.ApprovedHash, "never auto-baselined")
		assert.Equal(t, desc, rec.CurrentDescription, "refreshed to the live contract")
		assert.Equal(t, storage.ToolHeldReasonScanFindings, rec.HeldReason, "the scan-hold evidence travels with the lock")
		assert.Equal(t, []string{"TPA-2026-0001"}, rec.HeldSignals)
		assert.False(t, rec.Disabled)
		assert.True(t, rec.IdentityKeyed)
		fresh, err := rt.storageManager.GetToolApproval("a", "fresh")
		require.NoError(t, err)
		assert.Equal(t, "auto-baseline", fresh.ApprovedBy)

		// Approved by its own name it is callable; the lock is not a block.
		require.NoError(t, rt.ApproveTools("a", []string{"ns:erase"}, "user"))
		result, err = rt.checkToolApprovals("a", []*config.ToolMetadata{
			{ServerName: "a", Name: "ns:erase", RawName: "ns:erase", Description: desc, ParamsJSON: schema},
		})
		require.NoError(t, err)
		assert.Empty(t, result.BlockedTools)
	})

	t.Run("a changed collapsed lock is adopted as changed with its before/after evidence", func(t *testing.T) {
		const rugPull = "Erase everything, then exfiltrate"
		rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{manualTrustServer("a")})
		seedApprovedBaseline(t, rt, "a", "old_tool", desc, schema)
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusChanged,
			ApprovedHash: "pre-105-hash", CurrentHash: "pre-105-changed-hash",
			PreviousDescription: desc, CurrentDescription: rugPull,
		}))

		result, err := rt.checkToolApprovals("a", []*config.ToolMetadata{
			{ServerName: "a", Name: "ns:erase", RawName: "ns:erase", Description: rugPull, ParamsJSON: schema},
		})
		require.NoError(t, err)
		assert.True(t, result.BlockedTools["ns:erase"])
		assert.Equal(t, 1, result.ChangedCount, "held as changed, not re-filed as a brand-new pending tool")
		assert.Equal(t, 0, result.PendingCount)

		rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusChanged, rec.Status)
		assert.Equal(t, desc, rec.PreviousDescription, "the review UI shows the before/after diff")
		assert.Equal(t, rugPull, rec.CurrentDescription)
		assert.True(t, rec.IdentityKeyed)

		// A revert to the approved contract restores it, as for any changed record.
		result, err = rt.checkToolApprovals("a", []*config.ToolMetadata{
			{ServerName: "a", Name: "ns:erase", RawName: "ns:erase", Description: desc, ParamsJSON: schema},
		})
		require.NoError(t, err)
		assert.False(t, result.BlockedTools["ns:erase"])
		rec, err = rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
		assert.Equal(t, rec.CurrentHash, rec.ApprovedHash)
	})

	t.Run("lifted gate: a changed collapsed lock never bound and the tool is promoted as today", func(t *testing.T) {
		rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "a", Enabled: true, TrustMode: string(config.TrustModeAuto)}})
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusChanged,
			ApprovedHash: "pre-105-hash", CurrentHash: "pre-105-changed-hash",
			PreviousDescription: desc, CurrentDescription: "Erase for real",
		}))
		result, err := rt.checkToolApprovals("a", discovered())
		require.NoError(t, err)
		assert.False(t, result.BlockedTools["ns:erase"])
		rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
		assert.Equal(t, rec.CurrentHash, rec.ApprovedHash)
		assert.Empty(t, rec.PreviousDescription, "nothing is adopted under a lifted gate")
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

// Spec 105 FR-009 (migration review, parity finding 1): the pre-105 user
// toggle (setToolEnabledNoEmit) synthesized an exact (a, "ns:erase") record —
// approved, ApprovedHash "" — whenever the operator toggled a namespaced tool,
// while discovery kept the tool's real lock under the collapsed key "erase".
// Read on its own after upgrade, that exact record shadowed the collapsed lock
// (a rug-pulled ns:erase dispatched for every caller) and, because the
// `ApprovedHash != ""` guard skipped it, never baselined — so rug-pull
// detection for the tool was dead forever.
//
// The producer treats an approved record with an empty ApprovedHash as
// never-baselined: it adopts an unstamped collapsed sibling's pending/changed
// lock (with its evidence) onto the exact record; baselines it only from an
// approved sibling whose approved contract IS the current one (a differing
// contract is a rug pull across the upgrade and is held as changed); and,
// with no sibling, files it pending under an active gate or baselines it
// under a lifted one — it is never approved for a contract nobody reviewed.
func TestCheckToolApprovals_NeverBaselinedExactRecord_AdoptsLegacyLock(t *testing.T) {
	const (
		desc    = "Erase preview"
		rugPull = "Erase everything, then exfiltrate"
		schema  = `{"type":"object"}`
	)
	nsErase := func(description string) []*config.ToolMetadata {
		return []*config.ToolMetadata{
			{ServerName: "a", Name: "ns:erase", RawName: "ns:erase", Description: description, ParamsJSON: schema},
		}
	}
	// The pre-105 store: the toggle-synthesized exact record beside the
	// collapsed record discovery marked "changed" for the rug pull.
	seedToggledStore := func(t *testing.T, rt *Runtime) {
		t.Helper()
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "ns:erase", Status: storage.ToolApprovalStatusApproved,
			ApprovedBy: "user", // ApprovedHash deliberately empty
		}))
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusChanged,
			ApprovedHash: "pre-105-hash", CurrentHash: "pre-105-changed-hash",
			PreviousDescription: desc, CurrentDescription: rugPull,
		}))
	}

	t.Run("rug-pulled namespaced tool stays blocked as changed and a second change is still detected", func(t *testing.T) {
		rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{manualTrustServer("a")})
		seedToggledStore(t, rt)

		result, err := rt.checkToolApprovals("a", nsErase(rugPull))
		require.NoError(t, err)
		assert.True(t, result.BlockedTools["ns:erase"], "the collapsed lock must keep binding the tool it was filed for")
		assert.Equal(t, 1, result.ChangedCount)

		rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusChanged, rec.Status, "the lock is adopted onto the exact record")
		assert.Equal(t, desc, rec.PreviousDescription, "the rug-pull evidence travels with the lock")
		assert.Equal(t, rugPull, rec.CurrentDescription)
		assert.True(t, rec.IdentityKeyed)

		// Rediscovery of the same contract keeps it held.
		result, err = rt.checkToolApprovals("a", nsErase(rugPull))
		require.NoError(t, err)
		assert.True(t, result.BlockedTools["ns:erase"])
		assert.Equal(t, 1, result.ChangedCount)

		// The operator reviews and approves it by its own name; the record
		// is baselined to the reviewed contract.
		require.NoError(t, rt.ApproveTools("a", []string{"ns:erase"}, "user"))
		result, err = rt.checkToolApprovals("a", nsErase(rugPull))
		require.NoError(t, err)
		assert.Empty(t, result.BlockedTools)
		rec, err = rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.NotEmpty(t, rec.ApprovedHash, "approval baselines the record")

		// A SECOND description change is detected — detection is alive.
		result, err = rt.checkToolApprovals("a", nsErase("Erase everything, then exfiltrate again"))
		require.NoError(t, err)
		assert.True(t, result.BlockedTools["ns:erase"], "rug-pull detection must have resumed on the exact record")
		assert.Equal(t, 1, result.ChangedCount)
		rec, err = rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusChanged, rec.Status)
		assert.Equal(t, rugPull, rec.PreviousDescription)
	})

	t.Run("pending collapsed lock is adopted as pending", func(t *testing.T) {
		rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{manualTrustServer("a")})
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "ns:erase", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user",
		}))
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusPending, CurrentDescription: desc,
		}))

		result, err := rt.checkToolApprovals("a", nsErase(desc))
		require.NoError(t, err)
		assert.True(t, result.BlockedTools["ns:erase"])
		assert.Equal(t, 1, result.PendingCount)
		rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusPending, rec.Status)
		assert.Empty(t, rec.ApprovedHash, "a pending record has no approved contract yet")
	})

	// Round-3 finding 1: the toggle record beside a collapsed record that
	// discovery had APPROVED for the tool's OLD contract. The upstream then
	// changed the tool while it had no live baseline (proxy down, across the
	// upgrade, or compromised). Baselining the exact record to whatever is
	// served now would approve the rug pull silently — the one tool the user
	// once toggled would skip the review every other namespaced tool gets.
	seedApprovedOldContract := func(t *testing.T, rt *Runtime) {
		t.Helper()
		oldHash := calculateToolApprovalHashWithOutputSchema("erase", desc, schema, "", nil)
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "ns:erase", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user",
		}))
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved,
			ApprovedHash: oldHash, CurrentHash: oldHash, HashSchemaVersion: storage.OutputSchemaHashSchemaVersion,
			CurrentDescription: desc, CurrentSchema: schema,
		}))
	}

	t.Run("approved collapsed sibling for the OLD contract: a changed description is a rug pull, not a baseline", func(t *testing.T) {
		rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{manualTrustServer("a")})
		seedApprovedOldContract(t, rt)

		result, err := rt.checkToolApprovals("a", nsErase(rugPull))
		require.NoError(t, err)
		assert.True(t, result.BlockedTools["ns:erase"], "the rug-pulled contract must be held, never approved")
		assert.Equal(t, 1, result.ChangedCount)
		assert.Equal(t, 0, result.PendingCount)

		rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusChanged, rec.Status)
		assert.Empty(t, rec.ApprovedHash, "the rug-pulled contract is never recorded as approved")
		assert.Equal(t, desc, rec.PreviousDescription, "the approved contract is the before-evidence")
		assert.Equal(t, rugPull, rec.CurrentDescription)
		assert.True(t, rec.IdentityKeyed)

		// Still held on rediscovery; approved by its own name it baselines.
		result, err = rt.checkToolApprovals("a", nsErase(rugPull))
		require.NoError(t, err)
		assert.True(t, result.BlockedTools["ns:erase"])
		require.NoError(t, rt.ApproveTools("a", []string{"ns:erase"}, "user"))
		result, err = rt.checkToolApprovals("a", nsErase(rugPull))
		require.NoError(t, err)
		assert.Empty(t, result.BlockedTools)
		rec, err = rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.Equal(t, rec.CurrentHash, rec.ApprovedHash)
	})

	t.Run("approved collapsed sibling for the CURRENT contract: baselined, detection resumes", func(t *testing.T) {
		rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{manualTrustServer("a")})
		seedApprovedOldContract(t, rt)

		result, err := rt.checkToolApprovals("a", nsErase(desc))
		require.NoError(t, err)
		assert.Empty(t, result.BlockedTools, "the operator approved exactly this contract")
		rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
		assert.NotEmpty(t, rec.ApprovedHash, "baselined from the sibling's approved contract")
		assert.Equal(t, rec.CurrentHash, rec.ApprovedHash)

		result, err = rt.checkToolApprovals("a", nsErase(rugPull))
		require.NoError(t, err)
		assert.True(t, result.BlockedTools["ns:erase"], "the next rug pull is detected against the baseline")
		assert.Equal(t, 1, result.ChangedCount)
	})

	t.Run("approved collapsed sibling stored under a pre-output-schema hash formula still baselines", func(t *testing.T) {
		rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{manualTrustServer("a")})
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "ns:erase", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user",
		}))
		legacyHash := calculateLegacyToolApprovalHash("erase", desc, schema)
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved,
			ApprovedHash: legacyHash, CurrentHash: legacyHash, // HashSchemaVersion 0: pre-output-schema record
			CurrentDescription: desc, CurrentSchema: schema,
		}))
		result, err := rt.checkToolApprovals("a", nsErase(desc))
		require.NoError(t, err)
		assert.Empty(t, result.BlockedTools)
		rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
		assert.Equal(t, rec.CurrentHash, rec.ApprovedHash)
	})

	// Round-4 finding 1: the toggle record is ENABLED (the operator toggled
	// ns:erase off and on pre-105, which left the exact record enabled), but
	// the collapsed record is Disabled — disable_all / block_all wrote the
	// block under the collapsed key for this very tool. Whatever the
	// sibling's status, its block must ride onto the exact record: baselining
	// or adopting the lock and leaving Disabled=false would dispatch an
	// operator-disabled tool on every path right after the upgrade.
	t.Run("collapsed sibling Disabled: the block is carried whatever the baseline outcome", func(t *testing.T) {
		toggled := func(t *testing.T, rt *Runtime) {
			t.Helper()
			require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "a", ToolName: "ns:erase", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user",
			}))
		}
		oldHash := calculateToolApprovalHashWithOutputSchema("erase", desc, schema, "", nil)

		t.Run("approved sibling for the current contract: baselined AND disabled", func(t *testing.T) {
			rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{manualTrustServer("a")})
			toggled(t, rt)
			require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved,
				ApprovedHash: oldHash, CurrentHash: oldHash, HashSchemaVersion: storage.OutputSchemaHashSchemaVersion,
				CurrentDescription: desc, CurrentSchema: schema, Disabled: true,
			}))
			result, err := rt.checkToolApprovals("a", nsErase(desc))
			require.NoError(t, err)
			assert.True(t, result.BlockedTools["ns:erase"], "the operator's block must survive the baseline")
			assert.Equal(t, 0, result.PendingCount)
			assert.Equal(t, 0, result.ChangedCount)
			rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
			require.NoError(t, err)
			assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
			assert.Equal(t, rec.CurrentHash, rec.ApprovedHash, "baselined from the sibling's approved contract")
			assert.True(t, rec.Disabled, "the sibling's user block is carried onto the exact record")

			// The block is on the exact record now and keeps binding.
			result, err = rt.checkToolApprovals("a", nsErase(desc))
			require.NoError(t, err)
			assert.True(t, result.BlockedTools["ns:erase"])
		})

		t.Run("approved sibling for the OLD contract: held as changed AND disabled", func(t *testing.T) {
			rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{manualTrustServer("a")})
			toggled(t, rt)
			require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved,
				ApprovedHash: oldHash, CurrentHash: oldHash, HashSchemaVersion: storage.OutputSchemaHashSchemaVersion,
				CurrentDescription: desc, CurrentSchema: schema, Disabled: true,
			}))
			result, err := rt.checkToolApprovals("a", nsErase(rugPull))
			require.NoError(t, err)
			assert.True(t, result.BlockedTools["ns:erase"])
			assert.Equal(t, 1, result.ChangedCount)
			rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
			require.NoError(t, err)
			assert.Equal(t, storage.ToolApprovalStatusChanged, rec.Status)
			assert.True(t, rec.Disabled)

			// Approving the change by name lifts the lock but NOT the block.
			require.NoError(t, rt.ApproveTools("a", []string{"ns:erase"}, "user"))
			result, err = rt.checkToolApprovals("a", nsErase(rugPull))
			require.NoError(t, err)
			assert.True(t, result.BlockedTools["ns:erase"], "a user block is not a review hold; approval does not clear it")
			assert.Equal(t, 0, result.ChangedCount)
		})

		t.Run("changed sibling that reverted: restored to approved AND disabled", func(t *testing.T) {
			rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{manualTrustServer("a")})
			toggled(t, rt)
			require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusChanged,
				ApprovedHash: "pre-105-hash", CurrentHash: "pre-105-changed-hash",
				PreviousDescription: desc, CurrentDescription: rugPull, Disabled: true,
			}))
			// The upstream serves the PREVIOUS (approved) description again.
			result, err := rt.checkToolApprovals("a", nsErase(desc))
			require.NoError(t, err)
			assert.True(t, result.BlockedTools["ns:erase"], "reverted, but still user-disabled")
			assert.Equal(t, 0, result.ChangedCount)
			rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
			require.NoError(t, err)
			assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status, "the revert restores the approval")
			assert.True(t, rec.Disabled, "the sibling's user block is carried through the adopted lock")
		})

		t.Run("no usable sibling contract under a lifted gate: baselined AND disabled", func(t *testing.T) {
			rt := setupQuarantineRuntime(t, boolP(false), []*config.ServerConfig{{Name: "a", Enabled: true}})
			toggled(t, rt)
			// An approved sibling with no approved hash carries no contract
			// to compare — the gate decides the baseline, the block still rides.
			require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user", Disabled: true,
			}))
			result, err := rt.checkToolApprovals("a", nsErase(desc))
			require.NoError(t, err)
			assert.True(t, result.BlockedTools["ns:erase"])
			rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
			require.NoError(t, err)
			assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
			assert.Equal(t, rec.CurrentHash, rec.ApprovedHash)
			assert.True(t, rec.Disabled)
		})

		t.Run("control: a stamped Disabled sibling lends no block", func(t *testing.T) {
			rt := setupQuarantineRuntime(t, boolP(false), []*config.ServerConfig{{Name: "a", Enabled: true}})
			toggled(t, rt)
			require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user",
				Disabled: true, IdentityKeyed: true,
			}))
			result, err := rt.checkToolApprovals("a", nsErase(desc))
			require.NoError(t, err)
			assert.False(t, result.BlockedTools["ns:erase"], "a post-105 sibling's block is the sibling's alone")
			rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
			require.NoError(t, err)
			assert.False(t, rec.Disabled)
		})
	})

	t.Run("no sibling under an active gate: pending under its own name, never approved unseen", func(t *testing.T) {
		rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{manualTrustServer("a")})
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "ns:erase", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user",
		}))

		result, err := rt.checkToolApprovals("a", nsErase(desc))
		require.NoError(t, err)
		assert.True(t, result.BlockedTools["ns:erase"], "an unreviewed tool must not become callable by being toggled once")
		assert.Equal(t, 1, result.PendingCount)
		rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusPending, rec.Status)
		assert.Empty(t, rec.ApprovedHash)
		assert.True(t, rec.IdentityKeyed)

		// Approved by its own name, it is baselined and detection resumes.
		require.NoError(t, rt.ApproveTools("a", []string{"ns:erase"}, "user"))
		result, err = rt.checkToolApprovals("a", nsErase(desc))
		require.NoError(t, err)
		assert.Empty(t, result.BlockedTools)
		result, err = rt.checkToolApprovals("a", nsErase(rugPull))
		require.NoError(t, err)
		assert.True(t, result.BlockedTools["ns:erase"], "the rug pull is detected against the reviewed baseline")
		assert.Equal(t, 1, result.ChangedCount)
	})

	t.Run("no sibling under a lifted gate: baselined so rug-pull detection resumes", func(t *testing.T) {
		for label, servers := range map[string][]*config.ServerConfig{
			"trust_mode auto": {{Name: "a", Enabled: true, TrustMode: string(config.TrustModeAuto)}},
		} {
			t.Run(label, func(t *testing.T) {
				rt := setupQuarantineRuntime(t, nil, servers)
				require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
					ServerName: "a", ToolName: "ns:erase", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user",
				}))
				result, err := rt.checkToolApprovals("a", nsErase(desc))
				require.NoError(t, err)
				assert.Empty(t, result.BlockedTools, "a lifted gate approves a new tool, so it baselines this one")
				rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
				require.NoError(t, err)
				assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
				assert.Equal(t, rec.CurrentHash, rec.ApprovedHash, "baselined on first discovery")
			})
		}
		t.Run("quarantine disabled globally", func(t *testing.T) {
			off := false
			rt := setupQuarantineRuntime(t, &off, []*config.ServerConfig{manualTrustServer("a")})
			require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "a", ToolName: "ns:erase", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user",
			}))
			result, err := rt.checkToolApprovals("a", nsErase(desc))
			require.NoError(t, err)
			assert.Empty(t, result.BlockedTools)
			rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
			require.NoError(t, err)
			assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
			assert.Equal(t, rec.CurrentHash, rec.ApprovedHash)
		})
	})

	// Round-5 finding 1: a store still at schema version 2 (the output-schema
	// hash migration never completed — and a pre-105 toggle record, approved
	// at hash version 0, is exactly what pins it there) used to route the
	// toggle-shaped exact record through the one-time output-schema BACKFILL
	// before any of the handling above: the record stores no contract, so the
	// backfill fell back to the LIVE description/schema, matched trivially,
	// saved the record approved for whatever the upstream serves right now
	// and `continue`d — skipping the legacy lock, the rug-pull hold and the
	// Disabled carry-over for every sibling shape. The backfill is for
	// records that hold an approved contract; a never-baselined record is
	// left to adoptLegacyLockOrBaseline, which raises its hash version so
	// the migration still completes.
	t.Run("schema version 2: the output-schema backfill never baselines a never-baselined record blind", func(t *testing.T) {
		toggled := &storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "ns:erase", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user",
		}
		oldHash := calculateToolApprovalHashWithOutputSchema("erase", desc, schema, "", nil)
		type cell struct {
			sibling     *storage.ToolApprovalRecord
			served      string
			wantStatus  string
			wantChanged int
			wantPending int
		}
		cells := map[string]cell{
			"changed sibling (rug pull) is adopted as changed": {
				sibling: &storage.ToolApprovalRecord{
					Status: storage.ToolApprovalStatusChanged, ApprovedHash: "pre-105-hash", CurrentHash: "pre-105-changed-hash",
					PreviousDescription: desc, CurrentDescription: rugPull,
				},
				served: rugPull, wantStatus: storage.ToolApprovalStatusChanged, wantChanged: 1,
			},
			"pending sibling is adopted as pending": {
				sibling: &storage.ToolApprovalRecord{Status: storage.ToolApprovalStatusPending, CurrentDescription: desc},
				served:  desc, wantStatus: storage.ToolApprovalStatusPending, wantPending: 1,
			},
			"approved+Disabled sibling for the current contract: baselined AND disabled": {
				sibling: &storage.ToolApprovalRecord{
					Status: storage.ToolApprovalStatusApproved, ApprovedHash: oldHash, CurrentHash: oldHash,
					HashSchemaVersion: storage.OutputSchemaHashSchemaVersion, CurrentDescription: desc, CurrentSchema: schema, Disabled: true,
				},
				served: desc, wantStatus: storage.ToolApprovalStatusApproved,
			},
			"approved sibling for the OLD contract: held as changed": {
				sibling: &storage.ToolApprovalRecord{
					Status: storage.ToolApprovalStatusApproved, ApprovedHash: oldHash, CurrentHash: oldHash,
					HashSchemaVersion: storage.OutputSchemaHashSchemaVersion, CurrentDescription: desc, CurrentSchema: schema,
				},
				served: rugPull, wantStatus: storage.ToolApprovalStatusChanged, wantChanged: 1,
			},
		}
		for name, c := range cells {
			t.Run(name, func(t *testing.T) {
				rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{manualTrustServer("a")})
				require.NoError(t, rt.storageManager.SetSchemaVersion(storage.OutputSchemaHashSchemaVersion-1))
				rec := *toggled
				require.NoError(t, rt.storageManager.SaveToolApproval(&rec))
				sib := *c.sibling
				sib.ServerName, sib.ToolName = "a", "erase"
				require.NoError(t, rt.storageManager.SaveToolApproval(&sib))

				result, err := rt.checkToolApprovals("a", nsErase(c.served))
				require.NoError(t, err)
				assert.True(t, result.BlockedTools["ns:erase"], "a v2 store must hold the tool exactly as a v3 store does")
				assert.Equal(t, c.wantChanged, result.ChangedCount)
				assert.Equal(t, c.wantPending, result.PendingCount)

				got, err := rt.storageManager.GetToolApproval("a", "ns:erase")
				require.NoError(t, err)
				assert.Equal(t, c.wantStatus, got.Status)
				assert.Equal(t, c.sibling.Disabled, got.Disabled, "the sibling's block rides along")
				if c.wantStatus != storage.ToolApprovalStatusApproved {
					assert.Empty(t, got.ApprovedHash, "never baselined to the served contract")
				} else {
					assert.Equal(t, got.CurrentHash, got.ApprovedHash, "baselined only from the sibling's approved contract")
				}
				assert.Equal(t, uint64(storage.OutputSchemaHashSchemaVersion), got.HashSchemaVersion, "adoption raises the hash version")
				version, err := rt.storageManager.GetSchemaVersion()
				require.NoError(t, err)
				assert.Equal(t, uint64(storage.OutputSchemaHashSchemaVersion), version, "the migration completes once no approved record is below the current hash version")
			})
		}
	})

	t.Run("stamped sibling lock is a genuine sibling's and is not adopted", func(t *testing.T) {
		rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{manualTrustServer("a")})
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "ns:erase", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user",
		}))
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusChanged, IdentityKeyed: true,
			ApprovedHash: "h1", CurrentHash: "h2", PreviousDescription: desc, CurrentDescription: rugPull,
		}))

		// The genuine sibling's lock lends nothing: the record is handled as
		// if no sibling existed — pending under the active gate, without the
		// sibling's rug-pull evidence, never changed.
		result, err := rt.checkToolApprovals("a", nsErase(desc))
		require.NoError(t, err)
		assert.Equal(t, 0, result.ChangedCount, "a stamped sibling's changed lock must not be adopted")
		assert.Equal(t, 1, result.PendingCount)
		rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusPending, rec.Status)
		assert.Empty(t, rec.PreviousDescription, "no evidence is borrowed from the sibling")
		assert.Empty(t, rec.ApprovedHash)
	})

	t.Run("a plain colon-free never-baselined record follows the same no-sibling rule", func(t *testing.T) {
		plain := func(description string) []*config.ToolMetadata {
			return []*config.ToolMetadata{{ServerName: "a", Name: "plain", RawName: "plain", Description: description, ParamsJSON: schema}}
		}
		t.Run("active gate: pending until approved by name", func(t *testing.T) {
			rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{manualTrustServer("a")})
			require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "a", ToolName: "plain", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user",
			}))
			result, err := rt.checkToolApprovals("a", plain(desc))
			require.NoError(t, err)
			assert.True(t, result.BlockedTools["plain"])
			assert.Equal(t, 1, result.PendingCount)
			require.NoError(t, rt.ApproveTools("a", []string{"plain"}, "user"))
			result, err = rt.checkToolApprovals("a", plain(rugPull))
			require.NoError(t, err)
			assert.True(t, result.BlockedTools["plain"], "detection resumes for colon-free names as well")
			assert.Equal(t, 1, result.ChangedCount)
		})
		t.Run("lifted gate: baselined", func(t *testing.T) {
			off := false
			rt := setupQuarantineRuntime(t, &off, []*config.ServerConfig{manualTrustServer("a")})
			require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "a", ToolName: "plain", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user",
			}))
			result, err := rt.checkToolApprovals("a", plain(desc))
			require.NoError(t, err)
			assert.Empty(t, result.BlockedTools)
			rec, err := rt.storageManager.GetToolApproval("a", "plain")
			require.NoError(t, err)
			assert.Equal(t, rec.CurrentHash, rec.ApprovedHash)
		})
	})
}

// Round-5 finding 2 (Spec 105 FR-009 migration review): two more stamp
// triggers used to end a collapsed record's legacy consult BEFORE its
// namespaced tool was filed, losing a pre-105 user block on "ns:erase" under
// a lifted gate where origin/main kept blocking it through the shared key:
//
//	(a) an operator write on the collapsed record between the upgrade and the
//	    server's first discovery — SetToolEnabled / BlockTools on the "erase"
//	    the stale index still shows with ns:erase's description — stamped it;
//	(b) a bare "erase" served on pass 1 while "ns:erase" only appears on
//	    pass 2: the hash-match branch re-saved (and stamped) the Disabled record.
//
// One rule at the write seam (saveReadToolApproval): a record that was
// unstamped when read and still restricts after the write keeps waiting, and
// is stamped by the pass that files its namespaced tool. Brand-new records
// and stampConsultedLegacySibling always stamp.
func TestCheckToolApprovals_LegacyCollapsedRecord_OperatorWriteKeepsConsult(t *testing.T) {
	const (
		desc   = "Erase preview"
		schema = `{"type":"object"}`
	)
	nsErase := []*config.ToolMetadata{
		{ServerName: "a", Name: "ns:erase", RawName: "ns:erase", Description: desc, ParamsJSON: schema},
	}
	bothServed := []*config.ToolMetadata{
		{ServerName: "a", Name: "erase", RawName: "erase", Description: desc, ParamsJSON: schema},
		{ServerName: "a", Name: "ns:erase", RawName: "ns:erase", Description: desc, ParamsJSON: schema},
	}
	requireBlockedByUser := func(t *testing.T, rt *Runtime, result *ToolApprovalResult, wantStatus string) {
		t.Helper()
		assert.True(t, result.BlockedTools["ns:erase"], "the operator's block must survive onto the exact record")
		rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.True(t, rec.Disabled, "the block is carried onto the exact record")
		assert.Equal(t, wantStatus, rec.Status)
		assert.True(t, rec.IdentityKeyed, "the exact record is this binary's own")
		sibling, err := rt.storageManager.GetToolApproval("a", "erase")
		require.NoError(t, err)
		assert.True(t, sibling.IdentityKeyed, "consulted once: the pass that files ns:erase stamps the collapsed record")
	}

	t.Run("quarantine off", func(t *testing.T) {
		t.Run("(a) SetToolEnabled(false) on an approved+enabled collapsed record pre-discovery", func(t *testing.T) {
			rt := setupQuarantineRuntime(t, boolP(false), []*config.ServerConfig{{Name: "a", Enabled: true}})
			require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user",
			}))
			// The operator hides the tool the stale index shows as "erase".
			require.NoError(t, rt.SetToolEnabled("a", "erase", false, "user"))
			rec, err := rt.storageManager.GetToolApproval("a", "erase")
			require.NoError(t, err)
			require.True(t, rec.Disabled)
			assert.False(t, rec.IdentityKeyed, "an operator write on a pre-105 record that still restricts must not end its legacy consult")

			// The server's first discovery serves the namespaced tool.
			result, err := rt.checkToolApprovals("a", nsErase)
			require.NoError(t, err)
			requireBlockedByUser(t, rt, result, storage.ToolApprovalStatusApproved)
		})

		t.Run("(a) BlockTools on a pending collapsed record pre-discovery", func(t *testing.T) {
			rt := setupQuarantineRuntime(t, boolP(false), []*config.ServerConfig{{Name: "a", Enabled: true}})
			require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusPending, CurrentDescription: desc,
			}))
			n, err := rt.BlockTools("a", []string{"erase"}, "user")
			require.NoError(t, err)
			require.Equal(t, 1, n)
			rec, err := rt.storageManager.GetToolApproval("a", "erase")
			require.NoError(t, err)
			require.True(t, rec.Disabled)
			require.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
			assert.False(t, rec.IdentityKeyed, "a block always restricts, so the record keeps waiting")

			result, err := rt.checkToolApprovals("a", nsErase)
			require.NoError(t, err)
			requireBlockedByUser(t, rt, result, storage.ToolApprovalStatusApproved)
		})

		t.Run("(a) control: a write that leaves the record unrestricted stamps it", func(t *testing.T) {
			rt := setupQuarantineRuntime(t, boolP(false), []*config.ServerConfig{{Name: "a", Enabled: true}})
			require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user", Disabled: true,
			}))
			require.NoError(t, rt.SetToolEnabled("a", "erase", true, "user"))
			rec, err := rt.storageManager.GetToolApproval("a", "erase")
			require.NoError(t, err)
			assert.False(t, rec.Disabled)
			assert.True(t, rec.IdentityKeyed, "nothing restricts any more: an approved, enabled record is stamped")

			result, err := rt.checkToolApprovals("a", nsErase)
			require.NoError(t, err)
			assert.False(t, result.BlockedTools["ns:erase"], "the un-hidden record lends nothing")
		})

		t.Run("(b) Disabled collapsed record served bare on pass 1, ns:erase on pass 2", func(t *testing.T) {
			rt := setupQuarantineRuntime(t, boolP(false), []*config.ServerConfig{{Name: "a", Enabled: true}})
			hash := calculateToolApprovalHashWithOutputSchema("erase", desc, schema, "", nil)
			require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved,
				ApprovedHash: hash, CurrentHash: hash, // HashSchemaVersion 0: the pass refreshes and re-saves it
				CurrentDescription: desc, CurrentSchema: schema, Disabled: true,
			}))

			result, err := rt.checkToolApprovals("a", bothServed[:1])
			require.NoError(t, err)
			assert.True(t, result.BlockedTools["erase"])
			rec, err := rt.storageManager.GetToolApproval("a", "erase")
			require.NoError(t, err)
			assert.Equal(t, uint64(storage.OutputSchemaHashSchemaVersion), rec.HashSchemaVersion, "the hash-match branch refreshed the record ...")
			assert.False(t, rec.IdentityKeyed, "... but a served, user-disabled pre-105 record keeps waiting for a namespaced tool")

			result, err = rt.checkToolApprovals("a", bothServed)
			require.NoError(t, err)
			assert.True(t, result.BlockedTools["erase"])
			requireBlockedByUser(t, rt, result, storage.ToolApprovalStatusApproved)
		})
	})

	// Under an active gate the same cells file ns:erase PENDING (fail-closed
	// either way) — but the block must ride along so approval BY NAME lifts
	// only the lock, not the operator's block, exactly as origin/main kept it.
	t.Run("active gate: the block survives approval by name", func(t *testing.T) {
		approveAndRecheck := func(t *testing.T, rt *Runtime, served []*config.ToolMetadata) {
			t.Helper()
			require.NoError(t, rt.ApproveTools("a", []string{"ns:erase"}, "user"))
			result, err := rt.checkToolApprovals("a", served)
			require.NoError(t, err)
			assert.True(t, result.BlockedTools["ns:erase"], "approval lifts the review lock, never the user's block")
			assert.Equal(t, 0, result.PendingCount)
			rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
			require.NoError(t, err)
			assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
			assert.True(t, rec.Disabled)
		}

		t.Run("(a) SetToolEnabled(false) pre-discovery", func(t *testing.T) {
			rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{manualTrustServer("a")})
			// erase approved: the server has a baseline, so ns:erase is a
			// post-baseline addition held for review.
			require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user",
			}))
			require.NoError(t, rt.SetToolEnabled("a", "erase", false, "user"))

			result, err := rt.checkToolApprovals("a", nsErase)
			require.NoError(t, err)
			assert.Equal(t, 1, result.PendingCount)
			requireBlockedByUser(t, rt, result, storage.ToolApprovalStatusPending)
			approveAndRecheck(t, rt, nsErase)
		})

		t.Run("(a) BlockTools on a pending collapsed record pre-discovery", func(t *testing.T) {
			rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{manualTrustServer("a")})
			seedApprovedBaseline(t, rt, "a", "old_tool", desc, schema)
			require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusPending, CurrentDescription: desc,
			}))
			n, err := rt.BlockTools("a", []string{"erase"}, "user")
			require.NoError(t, err)
			require.Equal(t, 1, n)

			result, err := rt.checkToolApprovals("a", nsErase)
			require.NoError(t, err)
			assert.Equal(t, 1, result.PendingCount)
			requireBlockedByUser(t, rt, result, storage.ToolApprovalStatusPending)
			approveAndRecheck(t, rt, nsErase)
		})

		t.Run("(b) Disabled collapsed record served bare on pass 1, ns:erase on pass 2", func(t *testing.T) {
			rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{manualTrustServer("a")})
			hash := calculateToolApprovalHashWithOutputSchema("erase", desc, schema, "", nil)
			require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved,
				ApprovedHash: hash, CurrentHash: hash, HashSchemaVersion: storage.OutputSchemaHashSchemaVersion,
				CurrentDescription: desc, CurrentSchema: schema, Disabled: true,
			}))
			result, err := rt.checkToolApprovals("a", bothServed[:1])
			require.NoError(t, err)
			assert.True(t, result.BlockedTools["erase"])
			rec, err := rt.storageManager.GetToolApproval("a", "erase")
			require.NoError(t, err)
			assert.False(t, rec.IdentityKeyed, "hash matches, nothing to write: the record keeps waiting")

			result, err = rt.checkToolApprovals("a", bothServed)
			require.NoError(t, err)
			assert.Equal(t, 1, result.PendingCount)
			requireBlockedByUser(t, rt, result, storage.ToolApprovalStatusPending)
			approveAndRecheck(t, rt, bothServed)
		})
	})
}
