package index

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 105 FR-009 gap FR009-G2 (T005), index layer: two tools on one server
// whose RAW names differ only by a namespace prefix (`erase` vs `ns:erase`)
// MUST occupy two distinct index documents.
//
// Discovery stores the raw upstream name in ToolMetadata.Name with no server
// prefix (internal/upstream/core/client.go). IndexTool / BatchIndex derive the
// docID as `server` + `SplitN(Name, ":", 2)[1]`, which reads the namespace
// prefix of `ns:erase` as if it were a server prefix and strips it — so
// `erase` and `ns:erase` on server `a` both become docID `a:erase` and the
// second write silently overwrites the first. The collapse is observable
// through the document count and GetToolsByServer, which is how these tests
// pin it without depending on any not-yet-existing RawName API.

func newRawNameIndex(t *testing.T) *BleveIndex {
	t.Helper()
	idx, err := NewBleveIndex(t.TempDir(), zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

func rawNamePair(server string) []*config.ToolMetadata {
	return []*config.ToolMetadata{
		{ServerName: server, Name: "erase", Description: "Erase the scratch buffer.", Hash: "h-erase"},
		{ServerName: server, Name: "ns:erase", Description: "Erase everything under ns.", Hash: "h-ns-erase"},
	}
}

func TestBleveIndex_BatchIndex_PairedRawNamesAreDistinctDocs(t *testing.T) {
	idx := newRawNameIndex(t)

	require.NoError(t, idx.BatchIndex(rawNamePair("a")))

	count, err := idx.GetDocumentCount()
	require.NoError(t, err)
	assert.Equal(t, uint64(2), count,
		"FR-009: `erase` and `ns:erase` on server a are two tools and must be two documents")

	got, err := idx.GetToolsByServer("a")
	require.NoError(t, err)
	names := make([]string, 0, len(got))
	for _, tm := range got {
		names = append(names, tm.Name)
	}
	assert.ElementsMatch(t, []string{"a:erase", "a:ns:erase"}, names,
		"FR-009: both raw names must survive indexing under their canonical server:raw form")
}

// Same invariant through the single-document path: indexing `erase` and then
// `ns:erase` one at a time must ADD a second document, never overwrite the
// first. Asserting both `Name`s rather than just the count makes a
// last-writer-wins collapse show WHICH tool was lost.
func TestBleveIndex_IndexTool_NamespacedRawNameDoesNotOverwriteBareName(t *testing.T) {
	idx := newRawNameIndex(t)
	pair := rawNamePair("a")

	require.NoError(t, idx.IndexTool(pair[0]))
	require.NoError(t, idx.IndexTool(pair[1]))

	got, err := idx.GetToolsByServer("a")
	require.NoError(t, err)
	names := make([]string, 0, len(got))
	for _, tm := range got {
		names = append(names, tm.Name)
	}
	assert.ElementsMatch(t, []string{"a:erase", "a:ns:erase"}, names,
		"FR-009: indexing ns:erase after erase must not replace erase's document")

	// DeleteTool addressed by the exact raw name removes only that raw name.
	require.NoError(t, idx.DeleteTool("a", "ns:erase"))
	got, err = idx.GetToolsByServer("a")
	require.NoError(t, err)
	names = names[:0]
	for _, tm := range got {
		names = append(names, tm.Name)
	}
	assert.Equal(t, []string{"a:erase"}, names,
		"FR-009: deleting ns:erase by its raw name must leave erase intact")
}
