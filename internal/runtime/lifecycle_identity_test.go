package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 105 FR-009 gap FR009-G2 (T005): identity MUST be preserved end to end
// through the index update — "a namespaced tool never collapses to its suffix
// at any stage".
//
// Discovery stores a tool's RAW upstream name in ToolMetadata.Name (no server
// prefix; see internal/upstream/core/client.go ListTools). A server that
// exposes both `erase` and `ns:erase` therefore hands applyDifferentialToolUpdate
// two distinct tools whose names differ only by a namespace prefix. On HEAD the
// update collapses them in three places (gap-map §1 FR009-G2):
//
//  1. newToolsMap keys by the text after the FIRST colon, so `ns:erase` and
//     `erase` share the key `erase` (last writer wins) — one tool is never
//     even considered for indexing;
//  2. the bleve docID is `server` + SplitN(Name,":",2)[1], so both raw names
//     land on `a:erase` (last writer wins) — GetToolsByServer reports ONE
//     tool where the server serves TWO;
//  3. on the next discovery the OLD key (`ns:erase`, read back from the
//     canonical index name) no longer matches the NEW key (`erase`), so the
//     update reports `ns:erase` as REMOVED and calls
//     DeleteToolApproval(server, "ns:erase") — which deletes the operator's
//     exact-name Disabled toggle record. After that a full-tier token
//     dispatches `a:ns:erase` because the deny record is gone.
//
// Both tests observe the collapse only through existing production APIs
// (GetToolsByServer, GetToolApproval); they carry no dependency on the
// RawName plumbing PR A introduces.

func newIdentityRuntime(t *testing.T) *Runtime {
	t.Helper()
	cfg := &config.Config{
		DataDir: t.TempDir(),
		Listen:  "127.0.0.1:0",
		Servers: []*config.ServerConfig{
			{Name: "a", Enabled: true},
		},
		// Quarantine off: every discovered tool auto-approves, so the only
		// thing under test is identity preservation, not the approval gate.
		QuarantineEnabled: boolP(false),
	}
	rt, err := New(cfg, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

// pairedNameTools returns the raw-name pair a single server exposes: the bare
// `erase` and the namespaced `ns:erase`, exactly as discovery would hand them
// to the differential update (raw names, no server prefix).
func pairedNameTools() []*config.ToolMetadata {
	return []*config.ToolMetadata{
		{
			ServerName:  "a",
			Name:        "erase",
			Description: "Erase the scratch buffer.",
			ParamsJSON:  `{"type":"object","properties":{}}`,
			Hash:        "hash-erase",
		},
		{
			ServerName:  "a",
			Name:        "ns:erase",
			Description: "Erase everything under the ns namespace.",
			ParamsJSON:  `{"type":"object","properties":{"prefix":{"type":"string"}},"required":["prefix"]}`,
			Hash:        "hash-ns-erase",
		},
	}
}

// FR009-G2 (index): after one differential update with [erase, ns:erase] the
// index MUST hold two documents for server `a` — one per raw name — and their
// canonical names MUST be `a:erase` and `a:ns:erase`. HEAD returns one.
func TestApplyDifferentialToolUpdate_PairedRawNamesIndexDistinctly(t *testing.T) {
	rt := newIdentityRuntime(t)
	ctx := context.Background()

	require.NoError(t, rt.applyDifferentialToolUpdate(ctx, "a", pairedNameTools()))

	indexed, err := rt.indexManager.GetToolsByServer("a")
	require.NoError(t, err)

	names := make([]string, 0, len(indexed))
	for _, tm := range indexed {
		names = append(names, tm.Name)
	}
	assert.Len(t, indexed, 2,
		"FR-009: `erase` and `ns:erase` are two tools on server a and must occupy two index entries (got %v)", names)
	assert.ElementsMatch(t, []string{"a:erase", "a:ns:erase"}, names,
		"FR-009: canonical index names must carry the raw name uncollapsed")
}

// FR009-G2 (storage): an exact-name approval record for `ns:erase` — here the
// operator's Disabled toggle — MUST survive a rediscovery of the SAME tool set.
// On HEAD the rerun mis-diffs `ns:erase` as removed (old key from the index vs
// collapsed new key) and deletes the record, so GetToolApproval returns
// ErrToolApprovalNotFound and the deny decision silently evaporates.
func TestApplyDifferentialToolUpdate_ExactNameDisabledRecordSurvivesRerun(t *testing.T) {
	rt := newIdentityRuntime(t)
	ctx := context.Background()

	// First discovery: both raw names are new.
	require.NoError(t, rt.applyDifferentialToolUpdate(ctx, "a", pairedNameTools()))

	// Operator disables the namespaced tool by its exact raw name (the key the
	// toggle path in tool_quarantine.go writes).
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "a",
		ToolName:   "ns:erase",
		Status:     storage.ToolApprovalStatusApproved,
		ApprovedAt: time.Now().UTC(),
		ApprovedBy: "operator",
		Disabled:   true,
	}))
	seeded, err := rt.storageManager.GetToolApproval("a", "ns:erase")
	require.NoError(t, err, "precondition: exact-name record must be readable before the rerun")
	require.True(t, seeded.Disabled)

	// Second discovery of the IDENTICAL tool set: nothing was removed
	// upstream, so no approval record may be deleted.
	require.NoError(t, rt.applyDifferentialToolUpdate(ctx, "a", pairedNameTools()))

	record, err := rt.storageManager.GetToolApproval("a", "ns:erase")
	require.NoError(t, err,
		"FR-009: rediscovering an unchanged tool set must not delete the exact-name record for ns:erase")
	assert.True(t, record.Disabled,
		"FR-009: the operator's Disabled toggle on ns:erase must survive rediscovery")
	assert.Equal(t, "ns:erase", record.ToolName)
}
