package bench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// TestRetrievalMetricsConformsToScoreReportSchema proves the live retrieval
// payload validates against the Spec 065 score-report contract — i.e. the
// `retrieval` object carries the required nested `metrics` and `gate`
// sub-objects, not flat fields (CodexReviewer finding on PR #748 / MCP-3167).
func TestRetrievalMetricsConformsToScoreReportSchema(t *testing.T) {
	golden := &GoldenSet{
		CorpusVersion: "corpus_v1",
		Queries: []GoldenQuery{
			{ID: "q1", Query: "x", Labels: []Label{{ToolID: "A", Relevance: 2}}},
		},
	}
	search := func(_ string, _ int) ([]string, error) { return []string{"A"}, nil }
	m, err := ScoreRetrieval(golden, search, []int{1, 3, 5, 10})
	if err != nil {
		t.Fatalf("ScoreRetrieval: %v", err)
	}

	// A score report may hold the retrieval block alone (security is optional).
	raw, err := json.Marshal(map[string]any{"retrieval": m})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse instance: %v", err)
	}

	schemaFile := filepath.Join("..", "specs", "065-evaluation-foundation", "contracts", "score-report.schema.json")
	schemaRaw, err := os.ReadFile(schemaFile)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	schemaDoc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(schemaRaw)))
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("score-report.schema.json", schemaDoc); err != nil {
		t.Fatalf("add schema: %v", err)
	}
	sch, err := c.Compile("score-report.schema.json")
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("live retrieval payload fails score-report.schema.json: %v", err)
	}
}
