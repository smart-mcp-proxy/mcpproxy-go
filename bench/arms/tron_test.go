package arms

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
)

// tronGoldenPath is the committed golden output for the first 3 corpus_v2
// tools (contract: golden-output tests). Regenerate with UPDATE_GOLDEN=1.
const tronGoldenPath = "testdata/tron_golden.txt"

// corpusV2Path locates the schema-bearing frozen corpus relative to this
// package directory (go test runs with cwd = package dir).
const corpusV2Path = "../../specs/083-discovery-profiler/datasets/corpus_v2.tools.json"

// tronTools is a fixture where two tools share a canonically-identical schema
// (in different key orders, to catch canonicalization gaps) and a third has a
// distinct schema — the minimal shape that exercises the dedup mechanism.
func tronTools() []bench.Tool {
	return []bench.Tool{
		{
			ToolID:      "filesystem:read_file",
			Server:      "filesystem",
			Name:        "read_file",
			Description: "Read the complete contents of a file.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
		},
		{
			ToolID:      "filesystem:delete_file",
			Server:      "filesystem",
			Name:        "delete_file",
			Description: "Delete a file.",
			// Same schema content as read_file, different key order: must
			// canonicalize to the same class.
			Schema: json.RawMessage(`{"required":["path"],"properties":{"path":{"type":"string"}},"type":"object"}`),
		},
		{
			ToolID:      "filesystem:stat_file",
			Server:      "filesystem",
			Name:        "stat_file",
			Description: "Return metadata for a file.",
			// Third sharer of the same canonical schema, yet another key order.
			Schema: json.RawMessage(`{"properties":{"path":{"type":"string"}},"type":"object","required":["path"]}`),
		},
		{
			ToolID:      "time:get_current_time",
			Server:      "time",
			Name:        "get_current_time",
			Description: "Get current time in a specific timezone",
			Schema:      json.RawMessage(`{"type":"object","required":["timezone"],"properties":{"timezone":{"type":"string"}}}`),
		},
		{
			ToolID:      "memory:read_graph",
			Server:      "memory",
			Name:        "read_graph",
			Description: "Read the entire knowledge graph",
			// No schema: no class reference.
		},
	}
}

func TestTron_ArmContractFlags(t *testing.T) {
	arm := NewTron()
	if got := arm.Name(); got != "tron_dedup" {
		t.Errorf("Name() = %q, want tron_dedup", got)
	}
	if !arm.IndexAltering() {
		t.Error("tron_dedup replaces per-tool schema text with a class reference; IndexAltering must be true")
	}
	if arm.LowerBound() {
		t.Error("tron_dedup preserves descriptions verbatim; LowerBound must be false")
	}
}

func TestTron_DefaultRegistryRegistered(t *testing.T) {
	arm, err := Resolve("tron_dedup")
	if err != nil {
		t.Fatalf("default registry must resolve tron_dedup: %v", err)
	}
	if arm.Name() != "tron_dedup" {
		t.Errorf("resolved arm name = %q", arm.Name())
	}
}

// TestTron_DedupSharesClass is the mechanism test: canonically-identical
// schemas (regardless of key order) get exactly ONE class definition in the
// listing preamble, and both tools reference it by name.
func TestTron_DedupSharesClass(t *testing.T) {
	arm := NewTron()
	tools := tronTools()

	listing, err := arm.EncodeListing(tools)
	if err != nil {
		t.Fatalf("EncodeListing: %v", err)
	}

	// 4 schema-bearing tools, 2 distinct canonical schemas -> exactly 2
	// class definitions.
	if got := strings.Count(listing, "class C"); got != 2 {
		t.Errorf("listing must define exactly 2 classes, found %d:\n%s", got, listing)
	}

	// The shared schema body must appear exactly once (in its class
	// definition), not inline in the tool entries.
	sharedBody := `{"properties":{"path":{"type":"string"}},"required":["path"],"type":"object"}`
	if got := strings.Count(listing, sharedBody); got != 1 {
		t.Errorf("shared schema body must appear exactly once (amortized), found %d times:\n%s", got, listing)
	}

	// Both sharing tools reference the SAME class name.
	classOfLine := func(toolName string) string {
		for _, entry := range strings.Split(listing, listingSeparator) {
			if strings.HasPrefix(entry, toolName+"|") {
				fields := strings.Split(entry, "|")
				return fields[len(fields)-1]
			}
		}
		t.Fatalf("tool entry %q not found in listing:\n%s", toolName, listing)
		return ""
	}
	readClass := classOfLine("read_file")
	deleteClass := classOfLine("delete_file")
	statClass := classOfLine("stat_file")
	timeClass := classOfLine("get_current_time")
	if readClass != deleteClass || readClass != statClass {
		t.Errorf("canonically-identical schemas must share one class: read_file=%q delete_file=%q stat_file=%q", readClass, deleteClass, statClass)
	}
	if readClass == timeClass {
		t.Errorf("distinct schemas must NOT share a class: read_file=%q get_current_time=%q", readClass, timeClass)
	}
	if !strings.HasPrefix(readClass, "C") {
		t.Errorf("class reference %q must be a class name (C-prefixed)", readClass)
	}
	// The class must actually be defined in the preamble.
	if !strings.Contains(listing, "class "+readClass+" = ") {
		t.Errorf("referenced class %q has no definition in the preamble:\n%s", readClass, listing)
	}

	// The schemaless tool carries no class reference.
	for _, entry := range strings.Split(listing, listingSeparator) {
		if strings.HasPrefix(entry, "read_graph|") {
			if got, want := entry, "read_graph|Read the entire knowledge graph"; got != want {
				t.Errorf("schemaless tool entry = %q, want %q", got, want)
			}
		}
	}
}

// TestTron_ListingShorterThanInlineWhenShared is the honesty check on the
// amortization claim: with shared schemas, the deduped listing must be
// strictly shorter than the non-deduped per-tool encodings joined the same
// way. (With all-distinct schemas the class overhead may make it longer —
// that is the honest cost and is NOT asserted away.)
func TestTron_ListingShorterThanInlineWhenShared(t *testing.T) {
	arm := NewTron()
	tools := tronTools()

	listing, err := arm.EncodeListing(tools)
	if err != nil {
		t.Fatalf("EncodeListing: %v", err)
	}
	inline := make([]string, 0, len(tools))
	for _, tl := range tools {
		enc, terr := arm.EncodeTool(tl)
		if terr != nil {
			t.Fatalf("EncodeTool(%s): %v", tl.ToolID, terr)
		}
		inline = append(inline, enc)
	}
	if joined := strings.Join(inline, listingSeparator); len(listing) >= len(joined) {
		t.Errorf("deduped listing (%d bytes) must be shorter than inline concatenation (%d bytes) when schemas are shared", len(listing), len(joined))
	}
}

func TestTron_DeterministicAcrossRuns(t *testing.T) {
	arm := NewTron()
	tools := tronTools()

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
		t.Error("EncodeListing not deterministic")
	}
}

// TestTron_EncodeToolIsInlineSingleForm pins contract rule 6: amortization
// lives ONLY in EncodeListing; EncodeTool is the non-deduped single form
// name|description|canonical-schema with no class machinery.
func TestTron_EncodeToolIsInlineSingleForm(t *testing.T) {
	arm := NewTron()

	tl := tronTools()[0]
	got, err := arm.EncodeTool(tl)
	if err != nil {
		t.Fatalf("EncodeTool: %v", err)
	}
	want := `read_file|Read the complete contents of a file.|{"properties":{"path":{"type":"string"}},"required":["path"],"type":"object"}`
	if got != want {
		t.Errorf("EncodeTool =\n%q\nwant\n%q", got, want)
	}
	if strings.Contains(got, "class ") {
		t.Errorf("EncodeTool must not carry class definitions: %q", got)
	}

	schemaless := tronTools()[4]
	got, err = arm.EncodeTool(schemaless)
	if err != nil {
		t.Fatalf("EncodeTool(schemaless): %v", err)
	}
	if want := "read_graph|Read the entire knowledge graph"; got != want {
		t.Errorf("EncodeTool(schemaless) = %q, want %q", got, want)
	}
}

func TestTron_InvalidSchemaIsExplicitError(t *testing.T) {
	arm := NewTron()
	bad := bench.Tool{ToolID: "s:bad", Server: "s", Name: "bad", Description: "d",
		Schema: json.RawMessage(`{"type":"object",`)}
	if _, err := arm.EncodeTool(bad); err == nil {
		t.Error("EncodeTool must fail explicitly on unencodable input")
	}
	if _, err := arm.EncodeListing([]bench.Tool{bad}); err == nil {
		t.Error("EncodeListing must fail explicitly on unencodable input")
	}
	if _, err := arm.EncodeIndexMetadata(bad); err == nil {
		t.Error("EncodeIndexMetadata must fail explicitly on unencodable input")
	}
}

// TestTron_EncodeIndexMetadata pins the index-ingestion mapping (FR-008):
// under TRON the schema body lives in a shared class definition, so the
// per-tool params text the index ingests is the content-addressed class
// reference — not the schema JSON. Name/Server/Description are unchanged.
func TestTron_EncodeIndexMetadata(t *testing.T) {
	arm := NewTron()
	tools := tronTools()

	metaRead, err := arm.EncodeIndexMetadata(tools[0])
	if err != nil {
		t.Fatalf("EncodeIndexMetadata: %v", err)
	}
	if metaRead.Name != tools[0].Name || metaRead.ServerName != tools[0].Server || metaRead.Description != tools[0].Description {
		t.Errorf("Name/ServerName/Description must be unchanged, got %+v", metaRead)
	}
	if metaRead.ParamsJSON == string(tools[0].Schema) {
		t.Error("ParamsJSON must differ from the raw schema (IndexAltering=true would be over-declared otherwise)")
	}
	var ref struct {
		Class string `json:"$class"`
	}
	if err := json.Unmarshal([]byte(metaRead.ParamsJSON), &ref); err != nil || ref.Class == "" {
		t.Fatalf("ParamsJSON must be a class-reference JSON object, got %q (err=%v)", metaRead.ParamsJSON, err)
	}

	// Canonically-identical schemas map to the same class reference; the
	// class name must match the one used in the listing.
	metaDelete, err := arm.EncodeIndexMetadata(tools[1])
	if err != nil {
		t.Fatalf("EncodeIndexMetadata(delete_file): %v", err)
	}
	if metaRead.ParamsJSON != metaDelete.ParamsJSON {
		t.Errorf("identical canonical schemas must share the class reference: %q vs %q", metaRead.ParamsJSON, metaDelete.ParamsJSON)
	}
	listing, err := arm.EncodeListing(tools)
	if err != nil {
		t.Fatalf("EncodeListing: %v", err)
	}
	if !strings.Contains(listing, "class "+ref.Class+" = ") {
		t.Errorf("index class reference %q must match the listing's class name:\n%s", ref.Class, listing)
	}

	// Schemaless tool: ParamsJSON empty, same as baseline.
	metaNone, err := arm.EncodeIndexMetadata(tools[4])
	if err != nil {
		t.Fatalf("EncodeIndexMetadata(schemaless): %v", err)
	}
	if metaNone.ParamsJSON != "" {
		t.Errorf("schemaless tool ParamsJSON = %q, want empty", metaNone.ParamsJSON)
	}
}

// TestTron_GoldenCorpusV2First3 is the committed golden-output test required
// by the arm contract: first 3 corpus_v2 tools -> exact expected bytes.
// Changing the arm's output requires regenerating the fixture in the same PR
// (UPDATE_GOLDEN=1 go test ./bench/arms/ -run TestTron_Golden).
func TestTron_GoldenCorpusV2First3(t *testing.T) {
	corpus, err := bench.LoadCorpusV2(corpusV2Path)
	if err != nil {
		t.Fatalf("LoadCorpusV2: %v", err)
	}
	if len(corpus.Tools) < 3 {
		t.Fatalf("corpus_v2 has %d tools, need >= 3", len(corpus.Tools))
	}

	arm := NewTron()
	got, err := arm.EncodeListing(corpus.Tools[:3])
	if err != nil {
		t.Fatalf("EncodeListing: %v", err)
	}

	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(tronGoldenPath), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(tronGoldenPath, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("golden updated: %s (%d bytes)", tronGoldenPath, len(got))
		return
	}

	want, err := os.ReadFile(tronGoldenPath)
	if err != nil {
		t.Fatalf("read golden (regenerate with UPDATE_GOLDEN=1): %v", err)
	}
	// end-of-file-fixer appends a trailing newline to committed fixtures; compare trimmed.
	if strings.TrimRight(got, "\n") != strings.TrimRight(string(want), "\n") {
		t.Errorf("encoding drift vs committed golden %s — if intentional, regenerate the fixture in the same PR.\ngot:\n%s\nwant:\n%s", tronGoldenPath, got, want)
	}
}
