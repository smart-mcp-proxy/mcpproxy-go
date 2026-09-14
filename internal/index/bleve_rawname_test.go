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

// productionShapedCorpus is metadata exactly as discovery hands it to the
// index (internal/upstream/core/client.go ListTools): Name is the RAW upstream
// name with no server prefix. Every golden fixture in internal/server indexes
// canonical-shaped names ("github:get_repo"), for which the pre- and post-FR-009
// documents coincide, so this corpus is the only thing that pins the scored
// fields for the shape production actually writes.
func productionShapedCorpus(stampRawName bool) []*config.ToolMetadata {
	corpus := []*config.ToolMetadata{
		{ServerName: "a", Name: "erase", Description: "Erase the scratch buffer.", ParamsJSON: `{"type":"object","properties":{}}`, Hash: "h-erase"},
		{ServerName: "a", Name: "wipe_disk", Description: "Wipe the whole disk. Dangerous erase of every partition.", ParamsJSON: `{"type":"object","properties":{"disk":{"type":"string"}}}`, Hash: "h-wipe"},
		{ServerName: "a", Name: "list", Description: "List files and directories.", ParamsJSON: `{"type":"object","properties":{"path":{"type":"string"}}}`, Hash: "h-list"},
		{ServerName: "b", Name: "erase_cache", Description: "Erase cached entries for a key.", ParamsJSON: `{"type":"object","properties":{"key":{"type":"string"}}}`, Hash: "h-erase-cache"},
		{ServerName: "b", Name: "get_repo", Description: "Get a repository by name.", ParamsJSON: `{"type":"object","properties":{"name":{"type":"string"}}}`, Hash: "h-get-repo"},
	}
	if stampRawName {
		for _, tm := range corpus {
			tm.RawName = tm.Name
		}
	}
	return corpus
}

// TestBleveIndex_RawShapedExactNameScoresUnchanged pins the SC-005 parity
// requirement at the index layer: for production-shaped documents (raw
// names, no colon) the administrator retrieve_tools score of every hit is
// byte-for-byte what the pre-FR-009 index produced. The expected values were
// captured by running this corpus against origin/main's bleve.go (the
// commit before the docID change); they move whenever a SCORED field —
// tool_name, full_tool_name, searchable_text or the _all catch-all — stops
// holding the bytes it held before. Storing the canonical id in
// full_tool_name, for instance, halved the exact-name score of "erase"
// (2.742679 → 1.264508) because the boost-4.0 TermQuery stopped matching.
func TestBleveIndex_RawShapedExactNameScoresUnchanged(t *testing.T) {
	type hit struct {
		name  string
		score float64
	}
	expected := map[string][]hit{
		"erase":     {{"a:erase", 2.742679}, {"b:erase_cache", 0.265228}, {"a:wipe_disk", 0.025785}},
		"wipe_disk": {{"a:wipe_disk", 3.827535}},
		"list":      {{"a:list", 3.491087}},
		"get_repo":  {{"b:get_repo", 3.864724}},
		// The canonical id is not a query the pre-FR-009 index answered for a
		// production-shaped document (full_tool_name held the raw name), and
		// it must not start answering it now.
		"a:erase": nil,
	}

	for _, stamp := range []bool{false, true} {
		label := "Name only (pre-105 discovery shape)"
		if stamp {
			label = "RawName stamped (post-105 discovery shape)"
		}
		t.Run(label, func(t *testing.T) {
			idx := newRawNameIndex(t)
			require.NoError(t, idx.BatchIndex(productionShapedCorpus(stamp)))

			for query, want := range expected {
				results, err := idx.SearchTools(query, 10)
				require.NoError(t, err, query)
				got := make([]hit, 0, len(results))
				for _, r := range results {
					got = append(got, hit{name: r.Tool.Name, score: r.Score})
				}
				require.Len(t, got, len(want), "query %q: hit membership must match the pre-FR-009 index (got %v)", query, got)
				for i := range want {
					assert.Equal(t, want[i].name, got[i].name, "query %q: ordering must match the pre-FR-009 index", query)
					assert.InDelta(t, want[i].score, got[i].score, 1e-6,
						"query %q hit %q: score must equal the pre-FR-009 value (a scored field's bytes moved)", query, want[i].name)
				}
			}
		})
	}
}

// TestBleveIndex_SelfPrefixedRawName_RoundTrip: a raw name that itself begins
// with the server's own prefix ("a:erase" on server "a") is the one shape the
// RawName stamp exists for. Its docID is "a:a:erase" and it must read back as
// RawName "a:erase" — distinct from the sibling raw "erase" (docID "a:erase",
// RawName "erase") — under both read seams. Without the stamp (or with the
// identity derived from the stored full_tool_name instead of the docID) the
// two collapse and every rediscovery mis-diffs the self-prefixed tool as
// removed + added, deleting its exact approval record.
func TestBleveIndex_SelfPrefixedRawName_RoundTrip(t *testing.T) {
	idx := newRawNameIndex(t)
	require.NoError(t, idx.IndexTool(&config.ToolMetadata{ServerName: "a", Name: "a:erase", RawName: "a:erase", Description: "Self-prefixed erase.", Hash: "h-self"}))
	require.NoError(t, idx.IndexTool(&config.ToolMetadata{ServerName: "a", Name: "erase", RawName: "erase", Description: "Plain erase.", Hash: "h-plain"}))

	count, err := idx.GetDocumentCount()
	require.NoError(t, err)
	require.Equal(t, uint64(2), count, "a raw a:erase and a raw erase on server a are two documents")

	byRaw := map[string]*config.ToolMetadata{}
	got, err := idx.GetToolsByServer("a")
	require.NoError(t, err)
	for _, tm := range got {
		byRaw[tm.RawName] = tm
	}
	require.Len(t, byRaw, 2)
	require.Contains(t, byRaw, "a:erase")
	require.Contains(t, byRaw, "erase")
	assert.Equal(t, "a:a:erase", byRaw["a:erase"].Name, "the canonical id of a self-prefixed raw name carries the prefix twice")
	assert.Equal(t, "h-self", byRaw["a:erase"].Hash)
	assert.Equal(t, "a:erase", byRaw["erase"].Name)
	assert.Equal(t, "h-plain", byRaw["erase"].Hash)

	// The search seam reads identity the same way.
	results, err := idx.SearchTools("erase", 10)
	require.NoError(t, err)
	seen := map[string]string{}
	for _, r := range results {
		seen[r.Tool.RawName] = r.Tool.Name
	}
	assert.Equal(t, map[string]string{"a:erase": "a:a:erase", "erase": "a:erase"}, seen)

	// DeleteTool by exact raw name removes only that identity.
	require.NoError(t, idx.DeleteTool("a", "a:erase"))
	got, err = idx.GetToolsByServer("a")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "erase", got[0].RawName)
}
