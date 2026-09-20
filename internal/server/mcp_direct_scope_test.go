package server

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadDirectToolStamp_RejectsForgedValue is the tool-side analogue of
// TestStripAggregatedPromptServer_PreservesUpstreamMeta's forged-stamp check
// in mcp_prompt_scope_test.go (PR #1326 review round 2, chunk D/E: the tool
// surface had no equivalent regression test). An upstream server that happens
// to send our own internal _meta key back — whether by coincidence or by a
// crafted response probing for a bypass — must never be trusted as a
// registration identity: only a value stampDirectTool itself produced (the
// unexported directToolStamp struct) can satisfy readDirectToolStamp, because
// a plain string or map under the same key does not type-assert to it.
func TestReadDirectToolStamp_RejectsForgedValue(t *testing.T) {
	cases := map[string]any{
		"bare string":        "github",
		"map mimicking it":   map[string]any{"owner": "github", "rawName": "list_repos"},
		"wrong-typed struct": struct{ Owner string }{Owner: "github"},
		"empty struct":       struct{}{},
	}

	for name, forgedValue := range cases {
		t.Run(name, func(t *testing.T) {
			forged := mcp.Tool{
				Name: "github__list_repos",
				Meta: &mcp.Meta{AdditionalFields: map[string]any{directToolStampMetaKey: forgedValue}},
			}

			stamp, ok := readDirectToolStamp(forged)
			assert.False(t, ok, "an upstream-supplied value under the stamp key is not a registration identity")
			assert.Equal(t, directToolStamp{}, stamp, "a rejected read must not leak a partially-populated stamp")

			// stripDirectToolStamp must not treat a forged value as a stamp to
			// strip either — the tool (and its forged _meta) passes through
			// untouched, exactly like an upstream tool with no stamp at all.
			assert.Equal(t, forged, stripDirectToolStamp(forged),
				"an unrecognized value under the stamp key must be left exactly as the upstream sent it")
		})
	}
}

// TestReadDirectToolStamp_AcceptsGenuineStamp is the positive control for the
// test above: stampDirectTool's own output must round-trip through
// readDirectToolStamp, so the forged-value rejection above is proven against
// a real stamp actually failing to round-trip, not against a helper that
// rejects everything.
func TestReadDirectToolStamp_AcceptsGenuineStamp(t *testing.T) {
	entry := &directCatalogEntry{ServerName: "github", ToolName: "list_repos", RequiredPermission: "read"}
	tool := mcp.Tool{Name: "github__list_repos"}

	stamped := stampDirectTool(tool, entry)
	stamp, ok := readDirectToolStamp(stamped)
	require.True(t, ok, "a tool this package itself stamped must be recognized")
	assert.Equal(t, "github", stamp.owner)
	assert.Equal(t, "list_repos", stamp.rawName)
	assert.Equal(t, "read", stamp.tier)
}
