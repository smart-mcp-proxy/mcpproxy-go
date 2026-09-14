package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 105 FR-009 (T013): documents written by the pre-FR-009 docID
// derivation ("<server>:" + text after the FIRST colon) cannot be repaired in
// place — the collapsed docID does not say which raw name produced it. The
// runtime therefore clears the shared index ONCE on the first start after
// upgrade, gated on the storage schema version, and lets discovery rebuild it
// under the exact "<server>:<raw name>" identity.
//
// The stale document is planted through the index manager's own write path
// with a fixture that reproduces the old shape (a canonical-looking
// "a:erase" standing in for the collapsed "ns:erase"); what the trigger sees is
// just "documents exist at a pre-identity schema version".

func plantStaleIndexDoc(t *testing.T, rt *Runtime) {
	t.Helper()
	require.NoError(t, rt.indexManager.IndexTool(&config.ToolMetadata{
		ServerName: "a", Name: "a:erase", Description: "collapsed ns:erase", Hash: "h",
	}))
	count, err := rt.indexManager.GetDocumentCount()
	require.NoError(t, err)
	require.Equal(t, uint64(1), count, "precondition: one stale document present")
}

func schemaVersion(t *testing.T, rt *Runtime) uint64 {
	t.Helper()
	v, err := rt.storageManager.GetSchemaVersion()
	require.NoError(t, err)
	return v
}

func TestRebuildIndexForToolIdentity_ClearsIndexAndRecordsVersion(t *testing.T) {
	rt := newIdentityRuntime(t)
	plantStaleIndexDoc(t, rt)
	require.NoError(t, rt.storageManager.SetSchemaVersion(storage.ToolIdentitySchemaVersion-1))

	rt.rebuildIndexForToolIdentity()

	count, err := rt.indexManager.GetDocumentCount()
	require.NoError(t, err)
	assert.Equal(t, uint64(0), count, "a pre-identity index must be dropped so stale collapsed docIDs cannot linger")
	assert.Equal(t, uint64(storage.ToolIdentitySchemaVersion), schemaVersion(t, rt),
		"the migration must record itself so the clear is one-shot")

	// Second start: nothing to do — the rebuilt documents must survive.
	require.NoError(t, rt.indexManager.IndexTool(&config.ToolMetadata{ServerName: "a", Name: "ns:erase", Hash: "h"}))
	rt.rebuildIndexForToolIdentity()
	count, err = rt.indexManager.GetDocumentCount()
	require.NoError(t, err)
	assert.Equal(t, uint64(1), count, "an index at the identity schema version must not be cleared again")
}

func TestRebuildIndexForToolIdentity_CurrentVersionIsNoop(t *testing.T) {
	rt := newIdentityRuntime(t)
	require.Equal(t, uint64(storage.CurrentSchemaVersion), schemaVersion(t, rt), "a fresh database starts current")
	require.NoError(t, rt.indexManager.IndexTool(&config.ToolMetadata{ServerName: "a", Name: "ns:erase", Hash: "h"}))

	rt.rebuildIndexForToolIdentity()

	count, err := rt.indexManager.GetDocumentCount()
	require.NoError(t, err)
	assert.Equal(t, uint64(1), count, "a current database is never cleared")
}

// The output-schema hash migration (schema v3) is completed lazily by
// checkToolApprovals and is gated on the SAME version counter. Stamping v4
// early would skip it and false-flag every approved tool with an outputSchema
// as a rug-pull, so while a pre-v3 approved record is still un-backfilled the
// index is cleared but the version is left for that migration to advance.
func TestRebuildIndexForToolIdentity_DefersVersionWhileOutputSchemaMigrationPending(t *testing.T) {
	rt := newIdentityRuntime(t)
	plantStaleIndexDoc(t, rt)
	require.NoError(t, rt.storageManager.SetSchemaVersion(storage.OutputSchemaHashSchemaVersion-1))
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved,
		ApprovedHash: "legacy", CurrentHash: "legacy", // HashSchemaVersion 0: not yet backfilled
	}))

	rt.rebuildIndexForToolIdentity()

	count, err := rt.indexManager.GetDocumentCount()
	require.NoError(t, err)
	assert.Equal(t, uint64(0), count, "the stale index is dropped regardless")
	assert.Equal(t, uint64(storage.OutputSchemaHashSchemaVersion-1), schemaVersion(t, rt),
		"the version must stay below v3 so the output-schema backfill still runs")

	// Once the backfill is done (no pre-v3 approved record remains) the trigger
	// may record the identity version directly.
	require.NoError(t, rt.storageManager.DeleteToolApproval("a", "erase"))
	rt.rebuildIndexForToolIdentity()
	assert.Equal(t, uint64(storage.ToolIdentitySchemaVersion), schemaVersion(t, rt))
}
