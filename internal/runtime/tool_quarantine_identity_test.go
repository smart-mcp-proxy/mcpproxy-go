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
		// user-disabled. A lock is a review hold, not a user decision: the
		// new exact record's own pending state holds the tool under the
		// active gate, and the operator's approval BY ITS OWN NAME must lift
		// it — a carried Disabled would survive that approval as a phantom
		// user block.
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
// lock (with its evidence) onto the exact record, or, absent such a lock,
// baselines the record so detection resumes.
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

	t.Run("no restricting sibling: the record is baselined so rug-pull detection resumes", func(t *testing.T) {
		rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{manualTrustServer("a")})
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "ns:erase", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user",
		}))

		result, err := rt.checkToolApprovals("a", nsErase(desc))
		require.NoError(t, err)
		assert.Empty(t, result.BlockedTools, "an approved, enabled record stays callable")
		rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
		assert.NotEmpty(t, rec.ApprovedHash, "baselined on first discovery")
		assert.Equal(t, rec.CurrentHash, rec.ApprovedHash)

		result, err = rt.checkToolApprovals("a", nsErase(rugPull))
		require.NoError(t, err)
		assert.True(t, result.BlockedTools["ns:erase"], "the rug pull is detected against the baseline")
		assert.Equal(t, 1, result.ChangedCount)
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

		result, err := rt.checkToolApprovals("a", nsErase(desc))
		require.NoError(t, err)
		assert.False(t, result.BlockedTools["ns:erase"])
		rec, err := rt.storageManager.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
		assert.NotEmpty(t, rec.ApprovedHash)
	})

	t.Run("a plain colon-free never-baselined record is baselined too", func(t *testing.T) {
		rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{manualTrustServer("a")})
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "plain", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user",
		}))
		plain := func(description string) []*config.ToolMetadata {
			return []*config.ToolMetadata{{ServerName: "a", Name: "plain", RawName: "plain", Description: description, ParamsJSON: schema}}
		}
		result, err := rt.checkToolApprovals("a", plain(desc))
		require.NoError(t, err)
		assert.Empty(t, result.BlockedTools)
		result, err = rt.checkToolApprovals("a", plain(rugPull))
		require.NoError(t, err)
		assert.True(t, result.BlockedTools["plain"], "detection resumes for colon-free names as well")
	})
}
