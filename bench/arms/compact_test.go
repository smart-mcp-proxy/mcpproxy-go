package arms

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
)

// compactCorpusV2Path is the schema-bearing frozen corpus the compact golden-
// output test pins its fixture to (contracts/arm-interface.md).
const compactCorpusV2Path = "../../specs/083-discovery-profiler/datasets/corpus_v2.tools.json"

func TestCompact_ArmContractFlags(t *testing.T) {
	arm := NewCompact()
	if got := arm.Name(); got != "compact_sig" {
		t.Errorf("Name() = %q, want compact_sig", got)
	}
	if !arm.IndexAltering() {
		t.Error("compact_sig replaces the ParamsJSON the index ingests; IndexAltering must be true")
	}
	if arm.LowerBound() {
		t.Error("compact_sig preserves descriptions verbatim; LowerBound must be false")
	}
}

func TestCompact_Registered(t *testing.T) {
	a, err := Resolve("compact_sig")
	if err != nil {
		t.Fatalf("Resolve(compact_sig): %v", err)
	}
	if a.Name() != "compact_sig" {
		t.Errorf("resolved arm Name() = %q", a.Name())
	}
}

func TestCompact_EncodeTool(t *testing.T) {
	arm := NewCompact()

	cases := []struct {
		name string
		tool bench.Tool
		want string
	}{
		{
			name: "required bare, optional suffixed ?, optional sorted after required",
			tool: bench.Tool{
				ToolID: "s:fetch", Server: "s", Name: "fetch", Description: "Fetch a URL.",
				Schema: json.RawMessage(`{"type":"object","properties":{"raw":{"type":"boolean"},"url":{"type":"string"},"max_length":{"type":"integer"}},"required":["url"]}`),
			},
			want: "fetch(url:string, max_length?:int, raw?:bool)|Fetch a URL.",
		},
		{
			name: "multiple required keep required-array order",
			tool: bench.Tool{
				ToolID: "s:search", Server: "s", Name: "search", Description: "Search files.",
				Schema: json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"}},"required":["path","pattern"]}`),
			},
			want: "search(path:string, pattern:string)|Search files.",
		},
		{
			name: "full type mapping: string/int/number/bool/obj/arr",
			tool: bench.Tool{
				ToolID: "s:typed", Server: "s", Name: "typed", Description: "d",
				Schema: json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"integer"},"c":{"type":"number"},"d":{"type":"boolean"},"e":{"type":"object","properties":{"nested":{"type":"string"}}},"f":{"type":"array","items":{"type":"string"}}},"required":["a","b","c","d","e","f"]}`),
			},
			want: "typed(a:string, b:int, c:number, d:bool, e:obj, f:arr)|d",
		},
		{
			name: "anyOf string|null (Pydantic Optional) resolves to string",
			tool: bench.Tool{
				ToolID: "s:opt", Server: "s", Name: "opt", Description: "d",
				Schema: json.RawMessage(`{"type":"object","properties":{"contains":{"anyOf":[{"type":"string"},{"type":"null"}],"default":null}},"required":[]}`),
			},
			want: "opt(contains?:string)|d",
		},
		{
			name: "no schema renders empty parens",
			tool: bench.Tool{
				ToolID: "s:noschema", Server: "s", Name: "noschema", Description: "No inputs.",
			},
			want: "noschema()|No inputs.",
		},
		{
			name: "empty properties renders empty parens",
			tool: bench.Tool{
				ToolID: "s:noprops", Server: "s", Name: "noprops", Description: "d",
				Schema: json.RawMessage(`{"type":"object","properties":{}}`),
			},
			want: "noprops()|d",
		},
		{
			name: "description preserved verbatim including newlines",
			tool: bench.Tool{
				ToolID: "s:multi", Server: "s", Name: "multi", Description: "Line one.\n\nLine two.",
				Schema: json.RawMessage(`{"type":"object","properties":{"p":{"type":"string"}},"required":["p"]}`),
			},
			want: "multi(p:string)|Line one.\n\nLine two.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := arm.EncodeTool(tc.tool)
			if err != nil {
				t.Fatalf("EncodeTool: %v", err)
			}
			if got != tc.want {
				t.Errorf("EncodeTool:\ngot:  %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// TestCompact_DeterministicAcrossKeyOrder feeds the same schema content in two
// different JSON key orders: identical output proves no map-iteration-order
// leak (FR-010) — optional params are emitted in sorted name order regardless
// of their order in the schema text.
func TestCompact_DeterministicAcrossKeyOrder(t *testing.T) {
	arm := NewCompact()

	a := bench.Tool{
		ToolID: "s:t", Server: "s", Name: "t", Description: "d",
		Schema: json.RawMessage(`{"type":"object","properties":{"zeta":{"type":"string"},"alpha":{"type":"number"},"mid":{"type":"boolean"}},"required":["mid"]}`),
	}
	b := a
	b.Schema = json.RawMessage(`{"required":["mid"],"properties":{"alpha":{"type":"number"},"mid":{"type":"boolean"},"zeta":{"type":"string"}},"type":"object"}`)

	ea, err := arm.EncodeTool(a)
	if err != nil {
		t.Fatalf("EncodeTool(a): %v", err)
	}
	eb, err := arm.EncodeTool(b)
	if err != nil {
		t.Fatalf("EncodeTool(b): %v", err)
	}
	if ea != eb {
		t.Errorf("key order leaked into encoding:\na: %q\nb: %q", ea, eb)
	}
	if want := "t(mid:bool, alpha?:number, zeta?:string)|d"; ea != want {
		t.Errorf("encoding = %q, want %q", ea, want)
	}

	// Two runs on the same input are byte-equal.
	ea2, err := arm.EncodeTool(a)
	if err != nil {
		t.Fatalf("EncodeTool(a) 2nd run: %v", err)
	}
	if ea != ea2 {
		t.Error("EncodeTool not deterministic across runs")
	}
}

func TestCompact_InvalidSchemaIsExplicitError(t *testing.T) {
	arm := NewCompact()
	bad := bench.Tool{ToolID: "s:bad", Server: "s", Name: "bad", Description: "d",
		Schema: json.RawMessage(`{"type":"object",`)}
	if _, err := arm.EncodeTool(bad); err == nil {
		t.Fatal("EncodeTool must fail explicitly on unencodable input, got nil error")
	}
	if _, err := arm.EncodeIndexMetadata(bad); err == nil {
		t.Fatal("EncodeIndexMetadata must fail explicitly on unencodable input, got nil error")
	}
}

// TestCompact_EncodeIndexMetadata pins the arm's index-ingestion mapping
// (contract rule 4): Name/ServerName/Description unchanged, ParamsJSON
// replaced by the compact params text — the exact text the armindex builder
// feeds BatchIndexTools for this arm.
func TestCompact_EncodeIndexMetadata(t *testing.T) {
	arm := NewCompact()
	tl := bench.Tool{
		ToolID: "s:fetch", Server: "s", Name: "fetch", Description: "Fetch a URL.",
		Schema: json.RawMessage(`{"type":"object","properties":{"raw":{"type":"boolean"},"url":{"type":"string"}},"required":["url"]}`),
	}

	meta, err := arm.EncodeIndexMetadata(tl)
	if err != nil {
		t.Fatalf("EncodeIndexMetadata: %v", err)
	}
	if meta.Name != tl.Name {
		t.Errorf("Name = %q, want unchanged %q", meta.Name, tl.Name)
	}
	if meta.ServerName != tl.Server {
		t.Errorf("ServerName = %q, want unchanged %q", meta.ServerName, tl.Server)
	}
	if meta.Description != tl.Description {
		t.Errorf("Description = %q, want unchanged %q", meta.Description, tl.Description)
	}
	if want := "url:string, raw?:bool"; meta.ParamsJSON != want {
		t.Errorf("ParamsJSON = %q, want compact params text %q", meta.ParamsJSON, want)
	}
	// The arm declares IndexAltering — its metadata must actually differ from
	// the baseline mapping on a schema-bearing tool (two-sided contract).
	base, err := NewBaseline().EncodeIndexMetadata(tl)
	if err != nil {
		t.Fatalf("baseline EncodeIndexMetadata: %v", err)
	}
	if meta.ParamsJSON == base.ParamsJSON {
		t.Error("compact_sig ParamsJSON equals baseline; arm would over-declare IndexAltering")
	}
}

// TestCompact_GoldenCorpusV2 is the committed golden-output determinism test
// (contracts/arm-interface.md): first 3 tools of corpus_v2, encoded bytes
// pinned in testdata/compact_golden.txt. Changing the arm's output requires
// updating the fixture in the same PR — encoding drift is a reviewed event.
func TestCompact_GoldenCorpusV2(t *testing.T) {
	corpus, err := bench.LoadCorpus(compactCorpusV2Path)
	if err != nil {
		t.Fatalf("LoadCorpus(corpus_v2): %v", err)
	}
	if len(corpus.Tools) < 3 {
		t.Fatalf("corpus_v2 has %d tools, need at least 3", len(corpus.Tools))
	}
	first3 := corpus.Tools[:3]

	arm := NewCompact()
	listing, err := arm.EncodeListing(first3)
	if err != nil {
		t.Fatalf("EncodeListing(first 3): %v", err)
	}

	wantBytes, err := os.ReadFile(filepath.Join("testdata", "compact_golden.txt"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	// Pre-commit's end-of-file-fixer guarantees a trailing newline on committed
	// fixtures; the encoder's output has no such requirement, so compare trimmed.
	if want := strings.TrimRight(string(wantBytes), "\n"); strings.TrimRight(listing, "\n") != want {
		t.Errorf("compact_sig output drifted from committed golden fixture:\ngot:  %q\nwant: %q", listing, want)
	}

	// Listing decomposes exactly into per-tool encodings + separators, so
	// listing totals decompose into per-tool costs.
	parts := make([]string, 0, len(first3))
	for _, tl := range first3 {
		enc, terr := arm.EncodeTool(tl)
		if terr != nil {
			t.Fatalf("EncodeTool(%s): %v", tl.ToolID, terr)
		}
		parts = append(parts, enc)
	}
	if joined := strings.Join(parts, "\n\n"); listing != joined {
		t.Errorf("listing is not the joined per-tool encodings:\nlisting: %q\njoined:  %q", listing, joined)
	}

	// Second encoding run is byte-identical (FR-010).
	listing2, err := arm.EncodeListing(first3)
	if err != nil {
		t.Fatalf("EncodeListing 2nd run: %v", err)
	}
	if listing != listing2 {
		t.Error("EncodeListing not deterministic across runs")
	}
}
