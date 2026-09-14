package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 105 FR-009 (T013): index documents written by the pre-FR-009 docID
// derivation ("<server>:" + text after the FIRST colon) are healed by the
// differential index update itself — there is no global rebuild trigger.
//
// A pre-upgrade index holds ONE document for a server that serves both
// "erase" and "ns:erase": docID "a:erase", carrying whichever of the two was
// written last (here ns:erase's description and hash, the collapsing case).
// applyDifferentialToolUpdate keys both sides by raw name, so on the first
// discovery after upgrade the collapsed document reads back as raw "erase"
// (from its docID), the new set is [erase, ns:erase], and the diff is:
// "erase" modified (re-hashed under a:erase with erase's real content),
// "ns:erase" added under a:ns:erase, nothing removed — so no approval record
// is deleted. A global index wipe (which would empty retrieve_tools until
// every server reconnected, and which coupled to the output-schema schema
// counter fired on every start) buys nothing on top of that.
//
// The same differential heals the older-binary round-trip: a stable-channel
// core run against the same data dir re-plants the collapsed document (and,
// via its own collapsing diff, deletes the exact a:ns:erase document); the
// next discovery on the new binary re-adds a:ns:erase and re-hashes a:erase
// exactly as on the first upgrade.

// plantCollapsedLegacyDoc writes, by hand and through the production write
// path, the document the pre-FR-009 index held for a raw "ns:erase": docID
// "a:erase" (RawName "erase" is what the old derivation keyed it by),
// tool_name "erase", full_tool_name "ns:erase" (ToolMetadata.Name verbatim),
// and ns:erase's description and hash.
func plantCollapsedLegacyDoc(t *testing.T, rt *Runtime) {
	t.Helper()
	nsErase := pairedNameTools()[1]
	require.NoError(t, rt.indexManager.IndexTool(&config.ToolMetadata{
		ServerName:  "a",
		Name:        nsErase.Name, // "ns:erase" — the old full_tool_name bytes
		RawName:     "erase",      // the old docID key: text after the FIRST colon
		Description: nsErase.Description,
		ParamsJSON:  nsErase.ParamsJSON,
		Hash:        nsErase.Hash,
	}))

	indexed, err := rt.indexManager.GetToolsByServer("a")
	require.NoError(t, err)
	require.Len(t, indexed, 1, "precondition: the pre-upgrade index holds ONE collapsed document")
	require.Equal(t, "a:erase", indexed[0].Name, "precondition: the collapsed document sits under the bare docID")
	require.Equal(t, nsErase.Hash, indexed[0].Hash, "precondition: it carries ns:erase's content (last writer won)")
}

func indexedByRawName(t *testing.T, rt *Runtime) map[string]*config.ToolMetadata {
	t.Helper()
	indexed, err := rt.indexManager.GetToolsByServer("a")
	require.NoError(t, err)
	byRaw := make(map[string]*config.ToolMetadata, len(indexed))
	for _, tm := range indexed {
		byRaw[config.RawToolName(tm)] = tm
	}
	return byRaw
}

func assertHealedIndex(t *testing.T, rt *Runtime) {
	t.Helper()
	pair := pairedNameTools()
	byRaw := indexedByRawName(t, rt)
	require.Len(t, byRaw, 2, "both raw names must be indexed after the differential (got %v)", byRaw)
	require.Contains(t, byRaw, "erase")
	require.Contains(t, byRaw, "ns:erase")
	assert.Equal(t, "a:erase", byRaw["erase"].Name)
	assert.Equal(t, pair[0].Hash, byRaw["erase"].Hash, "a:erase must be re-hashed with erase's own content")
	assert.Equal(t, pair[0].Description, byRaw["erase"].Description)
	assert.Equal(t, "a:ns:erase", byRaw["ns:erase"].Name)
	assert.Equal(t, pair[1].Hash, byRaw["ns:erase"].Hash, "a:ns:erase must carry ns:erase's content under its own docID")
	assert.Equal(t, pair[1].Description, byRaw["ns:erase"].Description)
}

func TestApplyDifferentialToolUpdate_HealsPreUpgradeCollapsedDoc(t *testing.T) {
	rt := newIdentityRuntime(t)
	ctx := context.Background()
	plantCollapsedLegacyDoc(t, rt)

	// The approval store a pre-105 binary left behind: the baseline record
	// for "erase" (which, collapsed, also stood for ns:erase). Its survival is
	// the oracle for "no approval record was deleted".
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved,
		ApprovedBy: "legacy", ApprovedHash: "legacy-hash", CurrentHash: "legacy-hash",
		HashSchemaVersion: storage.OutputSchemaHashSchemaVersion,
	}))

	// First discovery after upgrade.
	require.NoError(t, rt.applyDifferentialToolUpdate(ctx, "a", pairedNameTools()))
	assertHealedIndex(t, rt)

	legacy, err := rt.storageManager.GetToolApproval("a", "erase")
	require.NoError(t, err, "the differential must not report erase as removed — its record must survive")
	assert.Equal(t, "legacy", legacy.ApprovedBy, "the pre-upgrade record is the same record, not a re-created one")
	nsRecord, err := rt.storageManager.GetToolApproval("a", "ns:erase")
	require.NoError(t, err, "ns:erase gets its own record on the first discovery")
	assert.Equal(t, "ns:erase", nsRecord.ToolName)

	// Rerun on the identical set: stable, nothing removed.
	require.NoError(t, rt.applyDifferentialToolUpdate(ctx, "a", pairedNameTools()))
	assertHealedIndex(t, rt)
	_, err = rt.storageManager.GetToolApproval("a", "ns:erase")
	require.NoError(t, err, "a rediscovery of an unchanged set must not delete the exact-name record")

	// Older-binary round-trip: a pre-105 core against the same data dir
	// re-plants the collapsed document and drops the exact a:ns:erase one (its
	// collapsing diff reads a:ns:erase as an unknown key and deletes it). The
	// next discovery on this binary heals it again with no version gate.
	require.NoError(t, rt.indexManager.DeleteTool("a", "ns:erase"))
	plantCollapsedLegacyDoc(t, rt)
	require.NoError(t, rt.applyDifferentialToolUpdate(ctx, "a", pairedNameTools()))
	assertHealedIndex(t, rt)
	_, err = rt.storageManager.GetToolApproval("a", "erase")
	require.NoError(t, err)
	_, err = rt.storageManager.GetToolApproval("a", "ns:erase")
	require.NoError(t, err)
}
