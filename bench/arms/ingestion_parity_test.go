package arms

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
)

// TestIngestionCanonicalizationParity is the FR-004 contract-parity gate at
// the ingestion boundary: a corpus_v2 file whose schema is deliberately
// NON-canonical (unsorted keys, insignificant whitespace) must load into
// canonical schema bytes, so the baseline arm's rendered token count and the
// plain Tokenizer.CountToolWithSchema count agree exactly. Without
// canonicalization at load, baseline.go canonicalizes while
// CountToolWithSchema appends raw bytes — divergent totals.
func TestIngestionCanonicalizationParity(t *testing.T) {
	// Deliberately non-canonical: keys out of order, extra whitespace.
	nonCanonicalSchema := `{
		"type": "object",
		"required": ["path"],
		"properties": {
			"recursive": {"type": "boolean"},
			"path":      {"type": "string", "description": "Where to read"}
		}
	}`
	corpusJSON := map[string]any{
		"version": "parity-test",
		"tools": []map[string]any{{
			"tool_id":     "fs:read",
			"server":      "fs",
			"tool":        "read",
			"description": "Read a file",
			"schema":      json.RawMessage(nonCanonicalSchema),
		}},
	}
	raw, err := json.Marshal(corpusJSON)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	path := filepath.Join(t.TempDir(), "corpus_v2.tools.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	corpus, err := bench.LoadCorpusV2(path)
	if err != nil {
		t.Fatalf("LoadCorpusV2: %v", err)
	}
	tl := corpus.Tools[0]

	// The loaded schema must already be canonical (sorted keys, compact).
	wantCanon, err := bench.CanonicalJSON(json.RawMessage(nonCanonicalSchema))
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	if string(tl.Schema) != wantCanon {
		t.Errorf("loaded schema not canonical:\ngot:  %s\nwant: %s", tl.Schema, wantCanon)
	}

	tk, err := bench.NewTokenizer(bench.DefaultEncoding)
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	rendered, err := NewBaseline().EncodeTool(tl)
	if err != nil {
		t.Fatalf("EncodeTool: %v", err)
	}
	if got, want := tk.Count(rendered), tk.CountToolWithSchema(tl); got != want {
		t.Errorf("token parity broken after ingestion: baseline arm=%d, CountToolWithSchema=%d", got, want)
	}
}
