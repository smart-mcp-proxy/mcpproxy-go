package arms

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
)

// schemaTools is a small schema-bearing tool set with deliberately unsorted
// object keys and enough map-shaped structure to catch map-iteration-order
// leaks in the renderer.
func schemaTools() []bench.Tool {
	return []bench.Tool{
		{
			ToolID:      "filesystem:search_files",
			Server:      "filesystem",
			Name:        "search_files",
			Description: "Recursively search for files and directories matching a pattern.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string","description":"Glob pattern"},"path":{"type":"string","description":"Starting directory"},"excludePatterns":{"type":"array","items":{"type":"string"}}},"required":["path","pattern"]}`),
		},
		{
			ToolID:      "time:get_current_time",
			Server:      "time",
			Name:        "get_current_time",
			Description: "Get current time in a specific timezone",
			Schema:      json.RawMessage(`{"type":"object","required":["timezone"],"properties":{"timezone":{"type":"string","description":"IANA timezone name"}}}`),
		},
		{
			ToolID:      "memory:read_graph",
			Server:      "memory",
			Name:        "read_graph",
			Description: "Read the entire knowledge graph",
			// No schema: renderer must degrade to name+description only.
		},
	}
}

func TestBaseline_DeterministicAcrossRuns(t *testing.T) {
	arm := NewBaseline()
	tools := schemaTools()

	for _, tl := range tools {
		a, errA := arm.EncodeTool(tl)
		b, errB := arm.EncodeTool(tl)
		if errA != nil || errB != nil {
			t.Fatalf("EncodeTool(%s): errors %v / %v", tl.ToolID, errA, errB)
		}
		if a != b {
			t.Errorf("EncodeTool(%s) not deterministic:\n%q\n%q", tl.ToolID, a, b)
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

func TestBaseline_CanonicalKeyOrder(t *testing.T) {
	arm := NewBaseline()

	// Same schema content, two different key orders, must render identically
	// with sorted object keys.
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
		t.Errorf("key order leaked into encoding:\nunsorted: %q\nsorted:   %q", a, b)
	}

	// The rendered schema must have keys in sorted order: "properties" before
	// "required" before "type", and "alpha" before "zeta".
	for _, pair := range [][2]string{
		{`"properties"`, `"required"`},
		{`"required"`, `"type":"object"`},
		{`"alpha"`, `"zeta"`},
	} {
		i, j := strings.Index(a, pair[0]), strings.Index(a, pair[1])
		if i < 0 || j < 0 || i > j {
			t.Errorf("canonical order violated: %q (at %d) should precede %q (at %d) in %q", pair[0], i, pair[1], j, a)
		}
	}
}

// TestBaseline_CountToolWithSchemaParity is the D7b / FR-004 parity gate: for a
// tool whose schema is already in canonical form (as every corpus_v2 tool is),
// the baseline rendering must be the EXACT text the existing
// Tokenizer.CountToolWithSchema counts — same shape, same token count.
func TestBaseline_CountToolWithSchemaParity(t *testing.T) {
	arm := NewBaseline()
	tk, err := bench.NewTokenizer(bench.DefaultEncoding)
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}

	for _, tl := range schemaTools() {
		// Canonicalize the fixture schema first (corpus_v2 ships canonical JSON).
		canon := tl
		if len(tl.Schema) > 0 {
			cs, cerr := CanonicalJSON(tl.Schema)
			if cerr != nil {
				t.Fatalf("CanonicalJSON(%s): %v", tl.ToolID, cerr)
			}
			canon.Schema = json.RawMessage(cs)
		}

		rendered, rerr := arm.EncodeTool(canon)
		if rerr != nil {
			t.Fatalf("EncodeTool(%s): %v", tl.ToolID, rerr)
		}

		wantText := canon.Name + "\n" + canon.Description
		if len(canon.Schema) > 0 {
			wantText += "\n" + string(canon.Schema)
		}
		if rendered != wantText {
			t.Errorf("%s: rendering diverges from CountToolWithSchema text shape:\ngot:  %q\nwant: %q", tl.ToolID, rendered, wantText)
		}
		if got, want := tk.Count(rendered), tk.CountToolWithSchema(canon); got != want {
			t.Errorf("%s: token parity broken: Count(rendered)=%d, CountToolWithSchema=%d", tl.ToolID, got, want)
		}
	}
}

func TestBaseline_InvalidSchemaIsExplicitError(t *testing.T) {
	arm := NewBaseline()
	bad := bench.Tool{ToolID: "s:bad", Server: "s", Name: "bad", Description: "d",
		Schema: json.RawMessage(`{"type":"object",`)}
	if _, err := arm.EncodeTool(bad); err == nil {
		t.Fatal("EncodeTool must fail explicitly on unencodable input, got nil error")
	}
}

func TestBaseline_ArmContractFlags(t *testing.T) {
	arm := NewBaseline()
	if got := arm.Name(); got != "baseline_json" {
		t.Errorf("Name() = %q, want baseline_json", got)
	}
	if arm.IndexAltering() {
		t.Error("baseline arm must not be index-altering (it IS the production rendering)")
	}
	if arm.LowerBound() {
		t.Error("baseline arm preserves descriptions; LowerBound must be false")
	}
}

// TestBaseline_EncodeIndexMetadataUnchanged verifies the rendering-only arm
// returns the tool's production index fields unchanged (contract rule 4).
func TestBaseline_EncodeIndexMetadataUnchanged(t *testing.T) {
	arm := NewBaseline()
	tl := schemaTools()[0]
	meta, err := arm.EncodeIndexMetadata(tl)
	if err != nil {
		t.Fatalf("EncodeIndexMetadata: %v", err)
	}
	if meta.Name != tl.Name {
		t.Errorf("Name = %q, want %q", meta.Name, tl.Name)
	}
	if meta.ServerName != tl.Server {
		t.Errorf("ServerName = %q, want %q", meta.ServerName, tl.Server)
	}
	if meta.Description != tl.Description {
		t.Errorf("Description = %q, want %q", meta.Description, tl.Description)
	}
	if meta.ParamsJSON != string(tl.Schema) {
		t.Errorf("ParamsJSON = %q, want raw schema %q", meta.ParamsJSON, string(tl.Schema))
	}
}

// TestBaseline_ListingIsToolConcatenation pins the listing shape: the naive
// full-menu count reuses per-tool renderings joined with a fixed separator, so
// listing totals decompose into per-tool costs.
func TestBaseline_ListingIsToolConcatenation(t *testing.T) {
	arm := NewBaseline()
	tools := schemaTools()

	listing, err := arm.EncodeListing(tools)
	if err != nil {
		t.Fatalf("EncodeListing: %v", err)
	}
	parts := make([]string, 0, len(tools))
	for _, tl := range tools {
		enc, terr := arm.EncodeTool(tl)
		if terr != nil {
			t.Fatalf("EncodeTool(%s): %v", tl.ToolID, terr)
		}
		parts = append(parts, enc)
	}
	if want := strings.Join(parts, "\n\n"); listing != want {
		t.Errorf("listing is not the joined per-tool renderings:\ngot:  %q\nwant: %q", listing, want)
	}
}
