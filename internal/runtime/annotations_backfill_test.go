package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 109 PR-a review round 2 (finding 4): the tool change-detection hash
// (internal/hash.ComputeToolHashWithOutputSchema) deliberately excludes
// annotations — see calculateToolApprovalHash's comment in tool_quarantine.go:
// annotations are unstable across reconnections and folding them into a
// change-detection hash previously caused false "tool_description_changed"
// spam on every reconnect. That means a tool whose Hash does not change never
// goes through the "modified" reindex path, so annotations_json — added to
// the Bleve document only at (re)index time — can never be backfilled for a
// tool that was already indexed before this feature existed, short of an
// operator deleting index.bleve or removing/re-adding the server.
//
// The fix here is independent of Hash/change-detection and therefore doesn't
// reintroduce the reconnect-churn regression: on every discovery pass,
// unchanged tools (same Hash) whose freshly observed Annotations differ from
// what is currently stored in the index get their document silently
// refreshed (index-only; no approval/quarantine state, no "tool changed" log
// spam, no hash rewrite).
func annotBackfillTool(serverName, name, hash, schema, desc string, annotations *config.ToolAnnotations) *config.ToolMetadata {
	return &config.ToolMetadata{
		ServerName:  serverName,
		Name:        name,
		Description: desc,
		ParamsJSON:  schema,
		Hash:        hash,
		Annotations: annotations,
	}
}

func TestApplyDifferentialToolUpdate_BackfillsAnnotationsForUnchangedTool(t *testing.T) {
	rt := newSigWarmRuntime(t)
	ctx := context.Background()

	schema := `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`

	// First pass: stands in for an index written before annotations existed
	// (or by an upstream that omitted them) — same Hash formula either way,
	// since Hash never includes annotations.
	require.NoError(t, rt.applyDifferentialToolUpdate(ctx, "annot-server", []*config.ToolMetadata{
		annotBackfillTool("annot-server", "tool_a", "annot-hash-a", schema, "Read a file.", nil),
	}))

	before, err := rt.indexManager.GetToolsByServer("annot-server")
	require.NoError(t, err)
	require.Len(t, before, 1)
	assert.Nil(t, before[0].Annotations, "precondition: no annotations stored yet")

	// Second pass: the SAME hash (nothing functionally changed) but the
	// upstream now reports annotations — e.g. after an mcpproxy upgrade that
	// starts capturing them. Hash is deliberately unaffected (see comment
	// above), so this must not go through the "modified" hash-diff branch to
	// still pick up the annotations.
	readOnly := true
	require.NoError(t, rt.applyDifferentialToolUpdate(ctx, "annot-server", []*config.ToolMetadata{
		annotBackfillTool("annot-server", "tool_a", "annot-hash-a", schema, "Read a file.", &config.ToolAnnotations{
			Title:        "Read a file",
			ReadOnlyHint: &readOnly,
		}),
	}))

	after, err := rt.indexManager.GetToolsByServer("annot-server")
	require.NoError(t, err)
	require.Len(t, after, 1)
	require.NotNil(t, after[0].Annotations, "annotations must be backfilled into the index for an otherwise-unchanged tool")
	assert.Equal(t, "Read a file", after[0].Annotations.Title)
	require.NotNil(t, after[0].Annotations.ReadOnlyHint)
	assert.True(t, *after[0].Annotations.ReadOnlyHint)
	// The Hash itself must stay untouched by this — annotations backfill is
	// deliberately NOT change detection.
	assert.Equal(t, "annot-hash-a", after[0].Hash)
}

// A no-op rediscovery (same hash, same annotations — including "still none")
// must not touch the index at all; this is the reconnect-flap guard the
// warning in calculateToolApprovalHash exists for. We can't observe "did not
// write" directly through this package's API, so this instead pins the
// steady-state outcome: repeated identical passes stay stable.
func TestApplyDifferentialToolUpdate_AnnotationsBackfillIsIdempotent(t *testing.T) {
	rt := newSigWarmRuntime(t)
	ctx := context.Background()
	schema := `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`

	tool := annotBackfillTool("annot-server-2", "tool_a", "annot-hash-b", schema, "Read a file.", nil)
	require.NoError(t, rt.applyDifferentialToolUpdate(ctx, "annot-server-2", []*config.ToolMetadata{tool}))
	require.NoError(t, rt.applyDifferentialToolUpdate(ctx, "annot-server-2", []*config.ToolMetadata{tool}))

	got, err := rt.indexManager.GetToolsByServer("annot-server-2")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Nil(t, got[0].Annotations)
	assert.Equal(t, "annot-hash-b", got[0].Hash)
}
