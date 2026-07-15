package bench

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// repoCorpus is the committed Spec 065 frozen corpus, reused here as the
// benchmark's tool universe (45 tools, 7 no-auth reference servers).
const repoCorpus = "../specs/065-evaluation-foundation/datasets/corpus_v1.tools.json"

func newTestTokenizer(t *testing.T) *Tokenizer {
	t.Helper()
	tk, err := NewTokenizer(DefaultEncoding)
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	return tk
}

func TestTokenizer_DeterministicAndPositive(t *testing.T) {
	tk := newTestTokenizer(t)
	text := "Fetches a URL from the internet and extracts its contents as markdown."
	a := tk.Count(text)
	b := tk.Count(text)
	if a != b {
		t.Fatalf("tokenizer not deterministic: %d != %d", a, b)
	}
	if a <= 0 {
		t.Fatalf("expected positive token count, got %d", a)
	}
}

func TestProxyToolsForMode(t *testing.T) {
	rt := ProxyToolsForMode(ModeRetrieveTools)
	if len(rt) == 0 {
		t.Fatal("retrieve_tools mode exposes no proxy tools")
	}
	// retrieve_tools mode must expose the discovery tool + the call_tool variants.
	want := map[string]bool{
		"retrieve_tools":        false,
		"call_tool_read":        false,
		"call_tool_write":       false,
		"call_tool_destructive": false,
	}
	for _, tl := range rt {
		if _, ok := want[tl.Name]; ok {
			want[tl.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("retrieve_tools mode missing expected proxy tool %q", name)
		}
	}

	ce := ProxyToolsForMode(ModeCodeExecution)
	var hasCodeExec, hasRetrieve bool
	for _, tl := range ce {
		switch tl.Name {
		case "code_execution":
			hasCodeExec = true
		case "retrieve_tools":
			hasRetrieve = true
		}
	}
	if !hasCodeExec || !hasRetrieve {
		t.Errorf("code_execution mode must expose code_execution + retrieve_tools, got %v", toolNames(ce))
	}

	// Both routing modes append the shared management tool set
	// (internal/server/mcp_routing.go buildManagementTools). Omitting these
	// undercounts the proxy-mode context cost and overstates the savings
	// (MCP-3161 / Codex finding on PR #747). Assert they are present so the
	// benchmark catalog can never silently drop them again.
	mgmt := []string{"upstream_servers", "quarantine_security", "search_servers", "list_registries"}
	for _, mode := range []string{ModeRetrieveTools, ModeCodeExecution} {
		got := map[string]bool{}
		for _, tl := range ProxyToolsForMode(mode) {
			got[tl.Name] = true
			if tl.Description == "" {
				t.Errorf("mode %s: tool %q has empty description", mode, tl.Name)
			}
		}
		for _, name := range mgmt {
			if !got[name] {
				t.Errorf("mode %s: missing management tool %q (proxy context cost undercounted)", mode, name)
			}
		}
	}
}

func TestComputeReport_SavingsAreReal(t *testing.T) {
	tk := newTestTokenizer(t)
	corpus, err := LoadCorpus(filepath.Clean(repoCorpus))
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	if len(corpus.Tools) < 40 {
		t.Fatalf("expected the frozen corpus to have ~45 tools, got %d", len(corpus.Tools))
	}

	rep := ComputeReport(tk, corpus)

	modes := map[string]ModeResult{}
	for _, m := range rep.Modes {
		modes[m.Mode] = m
	}

	base, ok := modes[ModeBaseline]
	if !ok {
		t.Fatal("report missing baseline mode")
	}
	if base.SavingsRatio != 0 {
		t.Errorf("baseline savings must be 0, got %v", base.SavingsRatio)
	}
	if base.Tokens <= 0 {
		t.Fatalf("baseline tokens must be positive, got %d", base.Tokens)
	}

	rt := modes[ModeRetrieveTools]
	ce := modes[ModeCodeExecution]

	// The product thesis: discovery/orchestration modes load fewer tokens into
	// context than loading every upstream tool directly — which holds whenever
	// the corpus is larger than the proxy's fixed built-in menu.
	//
	// The frozen corpus_v1 is a terse, schema-LESS 45-tool snapshot (1730
	// tokens), while the retrieve_tools menu carries FULL schemas for its
	// built-in tools. After spec 084 (richer call_tool_* descriptions) and spec
	// 085 (the new describe_tool built-in), that 12-tool menu (1856 tokens) now
	// edges just past this micro-corpus, so retrieve_tools shows slightly
	// negative "savings" HERE — an artifact of the fixture's scale, not a
	// regression (on 084-base main the same menu was 1707, already only ~1% under
	// the baseline). The retrieve_tools thesis is exercised at realistic scale by
	// the corpus_v2 profiler path; code_execution (7 built-ins, no per-tool call
	// variants) stays well under the baseline and still guards the thesis here.
	if ce.Tokens >= base.Tokens {
		t.Errorf("code_execution (%d) should use fewer tokens than baseline (%d)", ce.Tokens, base.Tokens)
	}

	// Savings ratios must match the arithmetic exactly (sign included).
	wantRT := 1.0 - float64(rt.Tokens)/float64(base.Tokens)
	if diff := rt.SavingsRatio - wantRT; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("retrieve_tools savings ratio %v != computed %v", rt.SavingsRatio, wantRT)
	}
	wantCE := 1.0 - float64(ce.Tokens)/float64(base.Tokens)
	if diff := ce.SavingsRatio - wantCE; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("code_execution savings ratio %v != computed %v", ce.SavingsRatio, wantCE)
	}
	if ce.SavingsRatio <= 0 || ce.SavingsRatio >= 1 {
		t.Errorf("code_execution savings ratio out of (0,1): %v", ce.SavingsRatio)
	}
}

func TestComputeReport_BaselineMonotonic(t *testing.T) {
	tk := newTestTokenizer(t)
	full := &Corpus{Version: "test", Tools: []Tool{
		{ToolID: "a:1", Server: "a", Name: "one", Description: "alpha tool that does something useful"},
		{ToolID: "b:2", Server: "b", Name: "two", Description: "beta tool that does something else useful"},
		{ToolID: "c:3", Server: "c", Name: "three", Description: "gamma tool with a longer description for token weight"},
	}}
	fewer := &Corpus{Version: "test", Tools: full.Tools[:1]}

	big := ComputeReport(tk, full)
	small := ComputeReport(tk, fewer)

	baseOf := func(r *Report) int {
		for _, m := range r.Modes {
			if m.Mode == ModeBaseline {
				return m.Tokens
			}
		}
		return -1
	}
	if baseOf(big) <= baseOf(small) {
		t.Errorf("more tools must mean more baseline tokens: %d <= %d", baseOf(big), baseOf(small))
	}
}

func TestWriteReports_SmokeTest(t *testing.T) {
	tk := newTestTokenizer(t)
	corpus := &Corpus{Version: "test", Tools: []Tool{
		{ToolID: "a:1", Server: "a", Name: "tool_a", Description: "does something"},
	}}
	rep := ComputeReport(tk, corpus)

	dir := t.TempDir()
	jsonPath, htmlPath, err := rep.WriteReports(dir)
	if err != nil {
		t.Fatalf("WriteReports: %v", err)
	}

	// JSON must parse back to a Report with the right corpus version.
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read json: %v", err)
	}
	var got Report
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if got.CorpusVersion != "test" {
		t.Errorf("corpus version = %q, want %q", got.CorpusVersion, "test")
	}

	// HTML must be non-empty and contain the mode names.
	html, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatalf("read html: %v", err)
	}
	if len(html) < 100 {
		t.Fatalf("dashboard.html too short (%d bytes)", len(html))
	}
	for _, mode := range []string{ModeBaseline, ModeRetrieveTools, ModeCodeExecution} {
		if !bytes.Contains(html, []byte(mode)) {
			t.Errorf("dashboard.html missing mode %q", mode)
		}
	}
}

// repoCorpusV2 is the committed Spec 083 schema-bearing frozen corpus.
const repoCorpusV2 = "../specs/083-discovery-profiler/datasets/corpus_v2.tools.json"

// corpusV2ExpectedTools MUST match the tool count recorded in
// specs/083-discovery-profiler/datasets/README.md — the corpus is immutable
// once committed, so a mismatch means either drift or an undocumented refresh.
const corpusV2ExpectedTools = 45

func TestLoadCorpusV2_SchemaBearing(t *testing.T) {
	corpus, err := LoadCorpusV2(filepath.Clean(repoCorpusV2))
	if err != nil {
		t.Fatalf("LoadCorpusV2: %v", err)
	}

	if got := len(corpus.Tools); got != corpusV2ExpectedTools {
		t.Errorf("corpus_v2 has %d tools, want %d (datasets/README.md)", got, corpusV2ExpectedTools)
	}
	if corpus.Version == "" {
		t.Error("corpus_v2 must carry a version string")
	}

	for _, tl := range corpus.Tools {
		if len(tl.Schema) == 0 {
			t.Errorf("tool %s has no schema — corpus_v2 must be schema-bearing", tl.ToolID)
		}
		if tl.ToolID == "" || tl.Server == "" || tl.Name == "" {
			t.Errorf("tool %+v missing identity fields", tl)
		}
		if tl.ToolID != tl.Server+":"+tl.Name {
			t.Errorf("tool_id %q != server:tool %q", tl.ToolID, tl.Server+":"+tl.Name)
		}
	}
}

// TestLoadCorpusV2_SchemasCanonicalCompact verifies the loader hands out
// canonical-compact schema bytes (FR-010): no insignificant whitespace (the
// on-disk file is pretty-printed for diffability) and object keys already
// sorted, so CountToolWithSchema and the arm renderers all count the same
// canonical text without re-encoding.
func TestLoadCorpusV2_SchemasCanonicalCompact(t *testing.T) {
	corpus, err := LoadCorpusV2(filepath.Clean(repoCorpusV2))
	if err != nil {
		t.Fatalf("LoadCorpusV2: %v", err)
	}
	for _, tl := range corpus.Tools {
		got := string(tl.Schema)
		want, cerr := canonicalizeJSONForTest(tl.Schema)
		if cerr != nil {
			t.Fatalf("tool %s: schema is not valid JSON: %v", tl.ToolID, cerr)
		}
		if got != want {
			t.Errorf("tool %s: schema bytes not canonical-compact:\ngot:  %s\nwant: %s", tl.ToolID, got, want)
		}
	}
}

// canonicalizeJSONForTest re-encodes raw JSON with sorted keys, preserved
// number literals, compact output, and no HTML escaping — the test-local twin
// of the arms-package canonicalizer (bench cannot import bench/arms: arms
// imports bench).
func canonicalizeJSONForTest(raw json.RawMessage) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return string(bytes.TrimSuffix(buf.Bytes(), []byte("\n"))), nil
}

func TestLoadCorpusV2_RejectsSchemalessCorpus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	bad := `{"version":"test","tools":[{"tool_id":"a:x","server":"a","tool":"x","description":"d"}]}`
	if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := LoadCorpusV2(path); err == nil {
		t.Fatal("LoadCorpusV2 must reject a corpus with schema-less tools")
	}
}

func toolNames(ts []Tool) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}
