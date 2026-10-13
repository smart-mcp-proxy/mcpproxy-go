package arms

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
)

// -update regenerates the committed golden fixture from the current encoder
// output. Changing the arm's encoding requires rerunning with -update and
// committing the new fixture in the same PR (contract: golden-output tests).
var updateToonGolden = flag.Bool("update", false, "rewrite golden fixtures from current encoder output")

const toonGoldenPath = "testdata/toon_golden.txt"

// armsCorpusV2 is the repo-relative path to the frozen schema-bearing corpus,
// seen from the bench/arms package directory.
const armsCorpusV2 = "../../specs/083-discovery-profiler/datasets/corpus_v2.tools.json"

func loadCorpusV2Head(t *testing.T, n int) []bench.Tool {
	t.Helper()
	c, err := bench.LoadCorpusV2(armsCorpusV2)
	if err != nil {
		t.Fatalf("LoadCorpusV2(%s): %v", armsCorpusV2, err)
	}
	if len(c.Tools) < n {
		t.Fatalf("corpus_v2 has %d tools, need at least %d", len(c.Tools), n)
	}
	return c.Tools[:n]
}

func TestToonListing_ContractFlags(t *testing.T) {
	arm := NewToonListing()
	if got := arm.Name(); got != "toon_listing" {
		t.Errorf("Name() = %q, want toon_listing", got)
	}
	if ToonListingName != "toon_listing" {
		t.Errorf("ToonListingName = %q, want toon_listing", ToonListingName)
	}
	// The TOON text representation of the schema replaces ParamsJSON in the
	// index mapping, so the arm alters what the retrieval index ingests.
	if !arm.IndexAltering() {
		t.Error("IndexAltering() = false, want true (schema text representation changes)")
	}
	// Descriptions are preserved verbatim — savings are not a lower bound.
	if arm.LowerBound() {
		t.Error("LowerBound() = true, want false (descriptions preserved)")
	}
}

func TestToonListing_Registered(t *testing.T) {
	arm, err := Resolve(ToonListingName)
	if err != nil {
		t.Fatalf("Resolve(%s): %v", ToonListingName, err)
	}
	if arm.Name() != ToonListingName {
		t.Errorf("resolved arm name = %q, want %q", arm.Name(), ToonListingName)
	}
}

// TestToonListing_GoldenOutput pins the exact bytes of the listing encoding of
// the first 3 corpus_v2 tools (contract: golden-output tests). Encoding drift
// is a reviewed event — update via `go test ./bench/arms -run Golden -update`.
func TestToonListing_GoldenOutput(t *testing.T) {
	arm := NewToonListing()
	tools := loadCorpusV2Head(t, 3)

	got, err := arm.EncodeListing(tools)
	if err != nil {
		t.Fatalf("EncodeListing: %v", err)
	}

	if *updateToonGolden {
		if err := os.MkdirAll(filepath.Dir(toonGoldenPath), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(toonGoldenPath, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}

	want, err := os.ReadFile(toonGoldenPath)
	if err != nil {
		t.Fatalf("read golden %s (run with -update to generate): %v", toonGoldenPath, err)
	}
	// end-of-file-fixer appends a trailing newline to committed fixtures; compare trimmed.
	if strings.TrimRight(got, "\n") != strings.TrimRight(string(want), "\n") {
		t.Errorf("EncodeListing output drifted from committed golden %s.\ngot:\n%s\nwant:\n%s", toonGoldenPath, got, want)
	}
}

func TestToonListing_Deterministic(t *testing.T) {
	arm := NewToonListing()
	tools := loadCorpusV2Head(t, 3)

	for _, tl := range tools {
		a, errA := arm.EncodeTool(tl)
		b, errB := arm.EncodeTool(tl)
		if errA != nil || errB != nil {
			t.Fatalf("EncodeTool(%s): errors %v / %v", tl.ToolID, errA, errB)
		}
		if a != b {
			t.Errorf("EncodeTool(%s) not deterministic", tl.ToolID)
		}
	}

	la, err := arm.EncodeListing(tools)
	if err != nil {
		t.Fatalf("EncodeListing: %v", err)
	}
	lb, err := arm.EncodeListing(tools)
	if err != nil {
		t.Fatalf("EncodeListing (2nd run): %v", err)
	}
	if la != lb {
		t.Errorf("EncodeListing not deterministic")
	}
}

// TestToonListing_KeyOrderCanonical: identical schema content in different JSON
// key orders must produce byte-identical TOON (FR-010 — no key-order leak).
func TestToonListing_KeyOrderCanonical(t *testing.T) {
	arm := NewToonListing()

	unsorted := bench.Tool{
		ToolID: "s:t", Server: "s", Name: "t", Description: "d",
		Schema: json.RawMessage(`{"type":"object","properties":{"zeta":{"type":"string"},"alpha":{"type":"number"}},"required":["alpha"]}`),
	}
	sorted := unsorted
	sorted.Schema = json.RawMessage(`{"properties":{"alpha":{"type":"number"},"zeta":{"type":"string"}},"required":["alpha"],"type":"object"}`)

	a, err := arm.EncodeTool(unsorted)
	if err != nil {
		t.Fatalf("EncodeTool(unsorted): %v", err)
	}
	b, err := arm.EncodeTool(sorted)
	if err != nil {
		t.Fatalf("EncodeTool(sorted): %v", err)
	}
	if a != b {
		t.Errorf("key order leaked into TOON encoding:\nunsorted: %q\nsorted:   %q", a, b)
	}
	// Sorted keys inside the schema scope: alpha before zeta.
	if i, j := strings.Index(a, "alpha"), strings.Index(a, "zeta"); i < 0 || j < 0 || i > j {
		t.Errorf("schema keys not in sorted order in %q", a)
	}
}

func TestToonListing_DescriptionPreserved(t *testing.T) {
	arm := NewToonListing()
	tools := loadCorpusV2Head(t, 3)

	for _, tl := range tools {
		enc, err := arm.EncodeTool(tl)
		if err != nil {
			t.Fatalf("EncodeTool(%s): %v", tl.ToolID, err)
		}
		// TOON quotes multi-line strings with \n escapes; either the verbatim
		// description or its escaped form must be present in full.
		escaped := strings.ReplaceAll(tl.Description, "\n", `\n`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		if !strings.Contains(enc, tl.Description) && !strings.Contains(enc, escaped) {
			t.Errorf("EncodeTool(%s): description not preserved in output", tl.ToolID)
		}
	}
}

func TestToonListing_NoSchemaTool(t *testing.T) {
	arm := NewToonListing()
	tl := bench.Tool{ToolID: "memory:read_graph", Server: "memory", Name: "read_graph",
		Description: "Read the entire knowledge graph"}

	enc, err := arm.EncodeTool(tl)
	if err != nil {
		t.Fatalf("EncodeTool(no schema): %v", err)
	}
	if strings.Contains(enc, "inputSchema") {
		t.Errorf("schema-less tool must omit the inputSchema field, got %q", enc)
	}
	if !strings.Contains(enc, "read_graph") || !strings.Contains(enc, tl.Description) {
		t.Errorf("name/description missing from %q", enc)
	}

	meta, err := arm.EncodeIndexMetadata(tl)
	if err != nil {
		t.Fatalf("EncodeIndexMetadata(no schema): %v", err)
	}
	if meta.ParamsJSON != "" {
		t.Errorf("schema-less tool ParamsJSON = %q, want empty", meta.ParamsJSON)
	}
}

// TestToonListing_InvalidSchemaIsExplicitError: unencodable input fails
// explicitly (contract rule 2), in every encoding path.
func TestToonListing_InvalidSchemaIsExplicitError(t *testing.T) {
	arm := NewToonListing()
	bad := bench.Tool{ToolID: "s:bad", Server: "s", Name: "bad", Description: "d",
		Schema: json.RawMessage(`{"type":"object",`)}

	if _, err := arm.EncodeTool(bad); err == nil {
		t.Error("EncodeTool must fail explicitly on invalid schema JSON, got nil error")
	}
	if _, err := arm.EncodeListing([]bench.Tool{bad}); err == nil {
		t.Error("EncodeListing must fail explicitly on invalid schema JSON, got nil error")
	}
	if _, err := arm.EncodeIndexMetadata(bad); err == nil {
		t.Error("EncodeIndexMetadata must fail explicitly on invalid schema JSON, got nil error")
	}

	// Trailing garbage after the schema value is also a hard error, not a
	// silently half-parsed schema.
	trailing := bad
	trailing.Schema = json.RawMessage(`{"type":"object"} {"extra":1}`)
	if _, err := arm.EncodeTool(trailing); err == nil {
		t.Error("EncodeTool must reject trailing data after the schema value")
	}
}

// TestToonListing_EncodeIndexMetadata: the arm's index mapping keeps
// Name/ServerName/Description verbatim and replaces ParamsJSON with the
// TOON-encoded schema text (FR-008 explicit mapping).
func TestToonListing_EncodeIndexMetadata(t *testing.T) {
	arm := NewToonListing()
	tools := loadCorpusV2Head(t, 3)

	for _, tl := range tools {
		meta, err := arm.EncodeIndexMetadata(tl)
		if err != nil {
			t.Fatalf("EncodeIndexMetadata(%s): %v", tl.ToolID, err)
		}
		if meta.Name != tl.Name || meta.ServerName != tl.Server || meta.Description != tl.Description {
			t.Errorf("%s: Name/ServerName/Description must be unchanged, got %+v", tl.ToolID, meta)
		}
		if meta.ParamsJSON == "" {
			t.Fatalf("%s: ParamsJSON empty for a schema-bearing tool", tl.ToolID)
		}
		// The replacement is TOON text, not JSON.
		if json.Valid([]byte(meta.ParamsJSON)) {
			t.Errorf("%s: ParamsJSON is still valid JSON — expected TOON schema text, got %q", tl.ToolID, meta.ParamsJSON)
		}
		if !strings.Contains(meta.ParamsJSON, "type: object") {
			t.Errorf("%s: TOON schema text missing 'type: object': %q", tl.ToolID, meta.ParamsJSON)
		}
		// It must differ from the baseline mapping (the IndexAltering()==true
		// declaration is honest for corpus_v2).
		base, err := NewBaseline().EncodeIndexMetadata(tl)
		if err != nil {
			t.Fatalf("baseline EncodeIndexMetadata(%s): %v", tl.ToolID, err)
		}
		if meta.ParamsJSON == base.ParamsJSON {
			t.Errorf("%s: ParamsJSON identical to baseline — IndexAltering would be over-declared", tl.ToolID)
		}
	}
}

// TestToonListing_ListingAmortizesHeader: the listing is one TOON array
// document (shared array header), not per-tool documents concatenated —
// contract rule 6.
func TestToonListing_ListingAmortizesHeader(t *testing.T) {
	arm := NewToonListing()
	tools := loadCorpusV2Head(t, 3)

	listing, err := arm.EncodeListing(tools)
	if err != nil {
		t.Fatalf("EncodeListing: %v", err)
	}
	if !strings.HasPrefix(listing, "[3]") {
		t.Errorf("listing must start with a shared TOON array header [3], got prefix %q", listing[:min(len(listing), 20)])
	}
	single, err := arm.EncodeTool(tools[0])
	if err != nil {
		t.Fatalf("EncodeTool: %v", err)
	}
	if strings.HasPrefix(single, "[") {
		t.Errorf("per-tool encoding must not carry the listing array header, got prefix %q", single[:min(len(single), 20)])
	}
}
