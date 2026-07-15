package corpusio

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
)

// syntheticCache is a small committed fixture matching the exact JSON shape
// scripts/fetch-toolret.sh writes into the cache dir. It is SYNTHETIC data —
// no real ToolRet bytes are committed (FR-013: the dataset's license is
// unstated upstream). Both arrays are deliberately UNSORTED on disk so the
// loader's stable-ID sort is actually exercised.
const syntheticCache = "testdata/toolret_synthetic"

func loadSynthetic(t *testing.T) *ToolRet {
	t.Helper()
	tr, err := LoadToolRet(syntheticCache)
	if err != nil {
		t.Fatalf("LoadToolRet(%q): %v", syntheticCache, err)
	}
	return tr
}

func TestLoadToolRet_SyntheticFixture(t *testing.T) {
	tr := loadSynthetic(t)

	// Revision stamping (FR-012): corpus + golden set carry the pinned
	// upstream revision so a report row is reproducible.
	if tr.ToolsRevision != "synthetic-tools-rev-1" {
		t.Errorf("ToolsRevision = %q, want %q", tr.ToolsRevision, "synthetic-tools-rev-1")
	}
	if tr.QueriesRevision != "synthetic-queries-rev-1" {
		t.Errorf("QueriesRevision = %q, want %q", tr.QueriesRevision, "synthetic-queries-rev-1")
	}
	wantVersion := "toolret-tools@synthetic-tools-rev-1"
	if tr.Corpus.Version != wantVersion {
		t.Errorf("Corpus.Version = %q, want %q", tr.Corpus.Version, wantVersion)
	}
	if tr.Golden.CorpusVersion != wantVersion {
		t.Errorf("Golden.CorpusVersion = %q, want %q", tr.Golden.CorpusVersion, wantVersion)
	}

	// All 6 fixture tools load; the config becomes the Server field.
	if got := len(tr.Corpus.Tools); got != 6 {
		t.Fatalf("corpus has %d tools, want 6", got)
	}
	for _, tl := range tr.Corpus.Tools {
		if tl.ToolID == "" || tl.Name == "" || tl.Description == "" {
			t.Errorf("tool %+v missing identity fields", tl)
		}
		if tl.Server != "code" && tl.Server != "web" {
			t.Errorf("tool %s: Server = %q, want fixture config code|web", tl.ToolID, tl.Server)
		}
	}

	// Fixture: 5 queries; synth_query_04 has only relevance-0 labels, so it is
	// unscoreable and dropped (bench semantics: 0/absent = irrelevant).
	if got := len(tr.Golden.Queries); got != 4 {
		t.Fatalf("golden has %d queries, want 4", got)
	}
	if tr.DroppedUnscoreable != 1 {
		t.Errorf("DroppedUnscoreable = %d, want 1", tr.DroppedUnscoreable)
	}
	// The fetch script's per-record drop count is echoed from the envelope.
	if tr.DroppedUpstream != 1 {
		t.Errorf("DroppedUpstream = %d, want 1 (envelope dropped_empty_queries)", tr.DroppedUpstream)
	}

	// Relevance-grade mapping: ToolRet integer relevance is kept as the graded
	// gain (identity), relevance < 1 labels are dropped.
	byID := map[string]bench.GoldenQuery{}
	for _, q := range tr.Golden.Queries {
		byID[q.ID] = q
	}
	q1, ok := byID["synth_query_01"]
	if !ok {
		t.Fatal("golden missing synth_query_01")
	}
	if len(q1.Labels) != 2 {
		t.Fatalf("synth_query_01 has %d labels, want 2 (the relevance-0 label must be dropped)", len(q1.Labels))
	}
	gotRel := map[string]int{}
	for _, l := range q1.Labels {
		gotRel[l.ToolID] = l.Relevance
	}
	if gotRel["synth_tool_02"] != 2 {
		t.Errorf("synth_query_01 label synth_tool_02 relevance = %d, want 2 (graded relevance preserved)", gotRel["synth_tool_02"])
	}
	if gotRel["synth_tool_01"] != 1 {
		t.Errorf("synth_query_01 label synth_tool_01 relevance = %d, want 1", gotRel["synth_tool_01"])
	}
}

func TestLoadToolRet_SortStability(t *testing.T) {
	tr := loadSynthetic(t)

	// The on-disk fixture is unsorted; the loader must return ID-sorted slices.
	if !sort.SliceIsSorted(tr.Corpus.Tools, func(i, j int) bool {
		return tr.Corpus.Tools[i].ToolID < tr.Corpus.Tools[j].ToolID
	}) {
		t.Errorf("corpus tools not sorted by tool ID: %v", toolIDs(tr.Corpus.Tools))
	}
	if !sort.SliceIsSorted(tr.Golden.Queries, func(i, j int) bool {
		return tr.Golden.Queries[i].ID < tr.Golden.Queries[j].ID
	}) {
		t.Errorf("golden queries not sorted by query ID: %v", queryIDs(tr.Golden.Queries))
	}
	for _, q := range tr.Golden.Queries {
		if !sort.SliceIsSorted(q.Labels, func(i, j int) bool {
			return q.Labels[i].ToolID < q.Labels[j].ToolID
		}) {
			t.Errorf("query %s labels not sorted by tool ID", q.ID)
		}
	}

	// Two loads of the same cache are deep-equal (determinism, FR-010/014).
	again := loadSynthetic(t)
	if !reflect.DeepEqual(tr, again) {
		t.Error("two loads of the same cache dir differ")
	}
}

// TestLoadToolRet_ValidationErrors mutates the synthetic fixture into temp
// cache dirs and asserts each per-record validation failure (missing ID,
// empty text, duplicates, dangling label references, missing revision).
func TestLoadToolRet_ValidationErrors(t *testing.T) {
	validTools := `{"dataset":"synthetic/toolret-tools-shape","revision":"r1","tools":[
		{"id":"tool_a","config":"code","documentation":"does a thing"},
		{"id":"tool_b","config":"code","documentation":"does another thing"}]}`
	validQueries := `{"dataset":"synthetic/toolret-queries-shape","revision":"r2","dropped_empty_queries":0,"queries":[
		{"id":"q1","config":"code","category":"web","query":"find the thing","instruction":"",
		 "labels":[{"id":"tool_a","relevance":1}]}]}`

	cases := []struct {
		name    string
		tools   string // "" = omit the file entirely
		queries string
		wantErr string
	}{
		{
			name:    "missing tools.json",
			tools:   "",
			queries: validQueries,
			wantErr: "tools.json",
		},
		{
			name:    "missing queries.json",
			tools:   validTools,
			queries: "",
			wantErr: "queries.json",
		},
		{
			name:    "tool missing id",
			tools:   `{"dataset":"d","revision":"r1","tools":[{"id":"","config":"code","documentation":"text"}]}`,
			queries: validQueries,
			wantErr: "missing id",
		},
		{
			name:    "tool empty documentation",
			tools:   `{"dataset":"d","revision":"r1","tools":[{"id":"tool_a","config":"code","documentation":"  "}]}`,
			queries: validQueries,
			wantErr: "empty documentation",
		},
		{
			name: "duplicate tool id",
			tools: `{"dataset":"d","revision":"r1","tools":[
				{"id":"tool_a","config":"code","documentation":"one"},
				{"id":"tool_a","config":"web","documentation":"two"}]}`,
			queries: validQueries,
			wantErr: "duplicate tool id",
		},
		{
			name:  "query missing id",
			tools: validTools,
			queries: `{"dataset":"d","revision":"r2","queries":[
				{"id":"","config":"code","category":"web","query":"text","labels":[{"id":"tool_a","relevance":1}]}]}`,
			wantErr: "missing id",
		},
		{
			name:  "query empty text",
			tools: validTools,
			queries: `{"dataset":"d","revision":"r2","queries":[
				{"id":"q1","config":"code","category":"web","query":"   ","labels":[{"id":"tool_a","relevance":1}]}]}`,
			wantErr: "empty query text",
		},
		{
			name:  "duplicate query id",
			tools: validTools,
			queries: `{"dataset":"d","revision":"r2","queries":[
				{"id":"q1","config":"code","category":"web","query":"one","labels":[{"id":"tool_a","relevance":1}]},
				{"id":"q1","config":"code","category":"web","query":"two","labels":[{"id":"tool_b","relevance":1}]}]}`,
			wantErr: "duplicate query id",
		},
		{
			name:  "label missing tool id",
			tools: validTools,
			queries: `{"dataset":"d","revision":"r2","queries":[
				{"id":"q1","config":"code","category":"web","query":"text","labels":[{"id":"","relevance":1}]}]}`,
			wantErr: "missing id",
		},
		{
			name:  "label references unknown tool",
			tools: validTools,
			queries: `{"dataset":"d","revision":"r2","queries":[
				{"id":"q1","config":"code","category":"web","query":"text","labels":[{"id":"tool_zzz","relevance":1}]}]}`,
			wantErr: "unknown tool",
		},
		{
			name:    "tools missing revision",
			tools:   `{"dataset":"d","revision":"","tools":[{"id":"tool_a","config":"code","documentation":"text"}]}`,
			queries: validQueries,
			wantErr: "revision",
		},
		{
			name:    "queries missing revision",
			tools:   validTools,
			queries: `{"dataset":"d","queries":[{"id":"q1","config":"code","category":"web","query":"text","labels":[{"id":"tool_a","relevance":1}]}]}`,
			wantErr: "revision",
		},
		{
			name:    "tools invalid json",
			tools:   `{"dataset":`,
			queries: validQueries,
			wantErr: "parse",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.tools != "" {
				mustWrite(t, filepath.Join(dir, "tools.json"), tc.tools)
			}
			if tc.queries != "" {
				mustWrite(t, filepath.Join(dir, "queries.json"), tc.queries)
			}
			_, err := LoadToolRet(dir)
			if err == nil {
				t.Fatalf("LoadToolRet must fail (%s)", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestSubsetQueries_SameSeedSameSubset(t *testing.T) {
	golden := makeGolden(50)

	a, err := SubsetQueries(golden, 42, 10)
	if err != nil {
		t.Fatalf("SubsetQueries: %v", err)
	}
	b, err := SubsetQueries(golden, 42, 10)
	if err != nil {
		t.Fatalf("SubsetQueries: %v", err)
	}
	if len(a.Queries) != 10 {
		t.Fatalf("subset has %d queries, want 10", len(a.Queries))
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("same seed+size produced different subsets:\n%v\n%v", queryIDs(a.Queries), queryIDs(b.Queries))
	}

	// Determinism must not depend on the caller's query order (FR-014: same
	// revision + seed + size => same subset). Feed a reversed copy.
	rev := makeGolden(50)
	for i, j := 0, len(rev.Queries)-1; i < j; i, j = i+1, j-1 {
		rev.Queries[i], rev.Queries[j] = rev.Queries[j], rev.Queries[i]
	}
	c, err := SubsetQueries(rev, 42, 10)
	if err != nil {
		t.Fatalf("SubsetQueries: %v", err)
	}
	if !reflect.DeepEqual(a, c) {
		t.Errorf("subset depends on input order:\n%v\n%v", queryIDs(a.Queries), queryIDs(c.Queries))
	}

	// Different seed => different subset (seeds chosen so this holds; the
	// assertion is deterministic once written).
	d, err := SubsetQueries(golden, 43, 10)
	if err != nil {
		t.Fatalf("SubsetQueries: %v", err)
	}
	if reflect.DeepEqual(queryIDs(a.Queries), queryIDs(d.Queries)) {
		t.Error("seeds 42 and 43 selected the identical subset — seed is ignored")
	}

	// Output is ID-sorted for downstream determinism.
	if !sort.SliceIsSorted(a.Queries, func(i, j int) bool { return a.Queries[i].ID < a.Queries[j].ID }) {
		t.Errorf("subset not sorted by query ID: %v", queryIDs(a.Queries))
	}
}

func TestSubsetQueries_Bounds(t *testing.T) {
	golden := makeGolden(5)

	// size >= len => the full (sorted) set.
	all, err := SubsetQueries(golden, 7, 5)
	if err != nil {
		t.Fatalf("SubsetQueries: %v", err)
	}
	if len(all.Queries) != 5 {
		t.Errorf("size==len subset has %d queries, want 5", len(all.Queries))
	}
	more, err := SubsetQueries(golden, 7, 99)
	if err != nil {
		t.Fatalf("SubsetQueries: %v", err)
	}
	if len(more.Queries) != 5 {
		t.Errorf("size>len subset has %d queries, want 5", len(more.Queries))
	}
	if all.CorpusVersion != golden.CorpusVersion {
		t.Errorf("subset lost corpus version: %q", all.CorpusVersion)
	}

	if _, err := SubsetQueries(golden, 7, 0); err == nil {
		t.Error("size 0 must be rejected")
	}
	if _, err := SubsetQueries(golden, 7, -3); err == nil {
		t.Error("negative size must be rejected")
	}
	if _, err := SubsetQueries(nil, 7, 1); err == nil {
		t.Error("nil golden set must be rejected")
	}
}

// --- helpers ---

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func makeGolden(n int) *bench.GoldenSet {
	g := &bench.GoldenSet{CorpusVersion: "toolret-tools@test"}
	for i := 0; i < n; i++ {
		g.Queries = append(g.Queries, bench.GoldenQuery{
			ID:     queryID(i),
			Query:  "synthetic query text",
			Labels: []bench.Label{{ToolID: "tool_a", Relevance: 1}},
		})
	}
	return g
}

func queryID(i int) string {
	// zero-padded so lexicographic order == numeric order
	const digits = "0123456789"
	return "q_" + string(digits[i/10%10]) + string(digits[i%10])
}

func toolIDs(ts []bench.Tool) []string {
	out := make([]string, len(ts))
	for i, tl := range ts {
		out[i] = tl.ToolID
	}
	return out
}

func queryIDs(qs []bench.GoldenQuery) []string {
	out := make([]string, len(qs))
	for i, q := range qs {
		out[i] = q.ID
	}
	return out
}
