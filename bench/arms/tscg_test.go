package arms

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
)

// corpusV2PathFromArms is the committed Spec 083 schema-bearing frozen corpus,
// relative to this package directory.
const corpusV2PathFromArms = "../../specs/083-discovery-profiler/datasets/corpus_v2.tools.json"

// tscgGoldenPath holds the committed expected encodings of the first 3
// corpus_v2 tools (contract: golden-output tests). JSONL, one
// {"tool_id","encoded"} record per line.
const tscgGoldenPath = "testdata/tscg_golden.txt"

// requireTSCG resolves the tscg arm, skipping the test locally when the node
// runtime is unavailable — but NEVER in CI, where the arm is mandatory
// (FR-006 / SC-002).
func requireTSCG(t *testing.T) *TSCGArm {
	t.Helper()
	arm := NewTSCG()
	if err := arm.Available(); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("tscg arm must never be skipped in CI (FR-006): %v", err)
		}
		t.Skipf("tscg runtime unavailable locally: %v", err)
	}
	return arm
}

// corpusV2Head loads the first n tools of the committed corpus_v2 snapshot.
func corpusV2Head(t *testing.T, n int) []bench.Tool {
	t.Helper()
	corpus, err := bench.LoadCorpusV2(filepath.Clean(corpusV2PathFromArms))
	if err != nil {
		t.Fatalf("LoadCorpusV2: %v", err)
	}
	if len(corpus.Tools) < n {
		t.Fatalf("corpus_v2 has %d tools, need %d", len(corpus.Tools), n)
	}
	return corpus.Tools[:n]
}

type tscgGoldenRecord struct {
	ToolID  string `json:"tool_id"`
	Encoded string `json:"encoded"`
}

func loadTSCGGolden(t *testing.T) []tscgGoldenRecord {
	t.Helper()
	f, err := os.Open(tscgGoldenPath)
	if err != nil {
		t.Fatalf("open golden fixture: %v", err)
	}
	defer f.Close()
	var recs []tscgGoldenRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r tscgGoldenRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("parse golden line %q: %v", line, err)
		}
		recs = append(recs, r)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("golden fixture has %d records, want 3 (first 3 corpus_v2 tools)", len(recs))
	}
	return recs
}

func TestTSCG_ArmContractFlags(t *testing.T) {
	arm := NewTSCG()
	if got := arm.Name(); got != "tscg" {
		t.Errorf("Name() = %q, want tscg", got)
	}
	if !arm.IndexAltering() {
		t.Error("tscg re-encodes both descriptions and parameter text; IndexAltering must be true")
	}
	if !arm.LowerBound() {
		t.Error("tscg's balanced profile elides description phrases; LowerBound must be true")
	}
}

// TestTSCG_GoldenOutput is the golden-output determinism gate (contract:
// golden-output tests): the first 3 corpus_v2 tools must encode to the exact
// committed bytes, twice (byte-determinism, FR-010), and the listing must
// decompose into the per-tool encodings.
func TestTSCG_GoldenOutput(t *testing.T) {
	arm := requireTSCG(t)
	tools := corpusV2Head(t, 3)
	golden := loadTSCGGolden(t)

	for i, tl := range tools {
		if golden[i].ToolID != tl.ToolID {
			t.Fatalf("golden record %d is %s, corpus tool is %s — fixture out of sync with corpus_v2", i, golden[i].ToolID, tl.ToolID)
		}
		a, err := arm.EncodeTool(tl)
		if err != nil {
			t.Fatalf("EncodeTool(%s): %v", tl.ToolID, err)
		}
		b, err := arm.EncodeTool(tl)
		if err != nil {
			t.Fatalf("EncodeTool(%s) 2nd run: %v", tl.ToolID, err)
		}
		if a != b {
			t.Errorf("EncodeTool(%s) not deterministic:\n%q\n%q", tl.ToolID, a, b)
		}
		if a != golden[i].Encoded {
			t.Errorf("EncodeTool(%s) diverges from committed golden output (encoding drift is a reviewed event — update %s in the same PR if intended):\ngot:  %q\nwant: %q",
				tl.ToolID, tscgGoldenPath, a, golden[i].Encoded)
		}
	}

	listing, err := arm.EncodeListing(tools)
	if err != nil {
		t.Fatalf("EncodeListing: %v", err)
	}
	parts := make([]string, len(golden))
	for i, g := range golden {
		parts[i] = g.Encoded
	}
	if want := strings.Join(parts, listingSeparator); listing != want {
		t.Errorf("EncodeListing is not the joined per-tool encodings:\ngot:  %q\nwant: %q", listing, want)
	}
}

// TestTSCG_EncodeIndexMetadata pins the index-ingestion mapping (FR-008): the
// compiled text is split into Description (TSCG-rewritten prose) and
// ParamsJSON (compiled parameter lines + CLOSURE signature) such that the
// full compiled encoding is exactly reconstructible — nothing the arm renders
// escapes the index, and nothing extra enters it.
func TestTSCG_EncodeIndexMetadata(t *testing.T) {
	arm := requireTSCG(t)
	tools := corpusV2Head(t, 3)

	altered := false
	for _, tl := range tools {
		enc, err := arm.EncodeTool(tl)
		if err != nil {
			t.Fatalf("EncodeTool(%s): %v", tl.ToolID, err)
		}
		meta, err := arm.EncodeIndexMetadata(tl)
		if err != nil {
			t.Fatalf("EncodeIndexMetadata(%s): %v", tl.ToolID, err)
		}
		if meta.Name != tl.Name {
			t.Errorf("%s: Name = %q, want %q (tool names are not re-encoded)", tl.ToolID, meta.Name, tl.Name)
		}
		if meta.ServerName != tl.Server {
			t.Errorf("%s: ServerName = %q, want %q", tl.ToolID, meta.ServerName, tl.Server)
		}
		if meta.Description == "" {
			t.Errorf("%s: Description is empty", tl.ToolID)
		}
		if !strings.Contains(meta.ParamsJSON, "[CLOSURE:") {
			t.Errorf("%s: ParamsJSON %q must carry the compiled CLOSURE signature", tl.ToolID, meta.ParamsJSON)
		}
		// Reconstruction invariant: the metadata fields partition the compiled
		// encoding exactly.
		if got := meta.Name + ": " + meta.Description + "\n" + meta.ParamsJSON; got != enc {
			t.Errorf("%s: metadata does not reconstruct the compiled encoding:\nreconstructed: %q\nencoded:       %q", tl.ToolID, got, enc)
		}
		if meta.Description != tl.Description || meta.ParamsJSON != string(tl.Schema) {
			altered = true
		}
	}
	if !altered {
		t.Error("tscg metadata identical to baseline for all sampled tools — IndexAltering declaration would be an over-declaration")
	}
}

// TestTSCG_NoParamsToolMapsToClosureOnly covers the compiled shape of a
// parameterless tool: no indented parameter block, ParamsJSON is the bare
// CLOSURE line.
func TestTSCG_NoParamsToolMapsToClosureOnly(t *testing.T) {
	arm := requireTSCG(t)
	tl := bench.Tool{
		ToolID: "memory:read_graph", Server: "memory", Name: "read_graph",
		Description: "Read the entire knowledge graph",
		Schema:      json.RawMessage(`{"type":"object","properties":{}}`),
	}
	meta, err := arm.EncodeIndexMetadata(tl)
	if err != nil {
		t.Fatalf("EncodeIndexMetadata: %v", err)
	}
	if !strings.HasPrefix(meta.ParamsJSON, "[CLOSURE:read_graph(") {
		t.Errorf("ParamsJSON = %q, want bare CLOSURE line for a parameterless tool", meta.ParamsJSON)
	}
}

func TestTSCG_InvalidSchemaIsExplicitError(t *testing.T) {
	arm := requireTSCG(t)
	bad := bench.Tool{ToolID: "s:bad", Server: "s", Name: "bad", Description: "d",
		Schema: json.RawMessage(`{"type":"object",`)}
	if _, err := arm.EncodeTool(bad); err == nil {
		t.Fatal("EncodeTool must fail explicitly on unencodable input, got nil error")
	}
	if _, err := arm.EncodeIndexMetadata(bad); err == nil {
		t.Fatal("EncodeIndexMetadata must fail explicitly on unencodable input, got nil error")
	}
}

// TestTSCG_UnavailableWhenShimMissing verifies contract rule 5: a missing
// bench/tscg installation surfaces ErrArmUnavailable at registry-resolution
// time, before any tool is processed.
func TestTSCG_UnavailableWhenShimMissing(t *testing.T) {
	arm := NewTSCGAt(filepath.Join(t.TempDir(), "no-such-dir"))
	r := NewRegistry()
	if err := r.Register(arm); err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, err := r.Resolve("tscg")
	if err == nil {
		t.Fatal("Resolve must fail when the shim directory is missing")
	}
	if !errors.Is(err, ErrArmUnavailable) {
		t.Fatalf("Resolve error %v must wrap ErrArmUnavailable", err)
	}
}

// TestTSCG_UnavailableWhenNodeMissing verifies the node-binary half of the
// availability check (the lookup happens at construction, off PATH).
func TestTSCG_UnavailableWhenNodeMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	arm := NewTSCG()
	if err := arm.Available(); !errors.Is(err, ErrArmUnavailable) {
		t.Fatalf("Available() = %v, want ErrArmUnavailable when node is not on PATH", err)
	}
}

// TestTSCG_RegisteredInDefaultRegistry: the tscg arm is part of the mandatory
// set and must join the package default registry at init.
func TestTSCG_RegisteredInDefaultRegistry(t *testing.T) {
	found := false
	for _, n := range Names() {
		if n == "tscg" {
			found = true
		}
	}
	if !found {
		t.Fatalf("tscg not in default registry: %v", Names())
	}
}
