package bench

import (
	"fmt"
	"strings"
	"testing"
)

// Spec 085 US5 — T041/T042 (FR-017/FR-018): the flip-gate computation that
// authorizes the Phase-2 default flip. RunFlipGates replays the golden set
// through retrieve_tools in BOTH detail modes with the same pipeline and
// emits:
//   - per-query ranked-ID identity across modes (gate: 100%),
//   - full/compact response-token distributions + median reduction,
//   - the lossy-signature rate over a frozen corpus (gate: <20%) via the
//     shared internal/toolsig grammar,
//   - describe_tool usage (informational; populated by the E2E suite).

// stubRetrieve builds a RetrieveToolsFunc from canned per-(query,detail)
// responses.
func stubRetrieve(t *testing.T, ranked map[string][]string, text map[string]string) RetrieveToolsFunc {
	t.Helper()
	return func(query string, _ int, detail string) ([]string, string, error) {
		key := query + "|" + detail
		ids, ok := ranked[key]
		if !ok {
			return nil, "", fmt.Errorf("stub has no ranking for %q", key)
		}
		return ids, text[key], nil
	}
}

func testGolden() *GoldenSet {
	return &GoldenSet{
		CorpusVersion: "test_v1",
		Queries: []GoldenQuery{
			{ID: "q1", Query: "read a file"},
			{ID: "q2", Query: "create an issue"},
		},
	}
}

func TestRunFlipGates_IdentityPassAndTokenReduction(t *testing.T) {
	tk, err := NewTokenizer(DefaultEncoding)
	if err != nil {
		t.Fatalf("tokenizer: %v", err)
	}

	fullText := strings.Repeat("full schema payload ", 50) // ~4x compact
	compactText := strings.Repeat("sig ", 25)
	ranked := map[string][]string{
		"read a file|full":        {"fs:read_file", "fs:list_directory"},
		"read a file|compact":     {"fs:read_file", "fs:list_directory"},
		"create an issue|full":    {"github:create_issue"},
		"create an issue|compact": {"github:create_issue"},
	}
	text := map[string]string{
		"read a file|full":        fullText,
		"read a file|compact":     compactText,
		"create an issue|full":    fullText,
		"create an issue|compact": compactText,
	}

	rep, err := RunFlipGates(stubRetrieve(t, ranked, text), testGolden(), tk, 10)
	if err != nil {
		t.Fatalf("RunFlipGates: %v", err)
	}

	id := rep.RankedIdentity
	if id == nil || id.Queries != 2 || id.Identical != 2 || !id.Pass {
		t.Fatalf("identity gate must pass 2/2: %+v", id)
	}
	if len(id.Mismatches) != 0 {
		t.Fatalf("no mismatches expected: %+v", id.Mismatches)
	}

	tok := rep.Tokens
	if tok == nil || tok.Full.Samples != 2 || tok.Compact.Samples != 2 {
		t.Fatalf("token distributions must cover every query: %+v", tok)
	}
	if tok.Full.P50 <= tok.Compact.P50 {
		t.Fatalf("full p50 (%d) must exceed compact p50 (%d)", tok.Full.P50, tok.Compact.P50)
	}
	wantReduction := 1.0 - float64(tok.Compact.P50)/float64(tok.Full.P50)
	if diff := rep.Tokens.MedianReduction - wantReduction; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("median reduction %v != %v", rep.Tokens.MedianReduction, wantReduction)
	}
}

func TestRunFlipGates_IdentityMismatchFailsGate(t *testing.T) {
	tk, err := NewTokenizer(DefaultEncoding)
	if err != nil {
		t.Fatalf("tokenizer: %v", err)
	}
	ranked := map[string][]string{
		"read a file|full":        {"fs:read_file", "fs:list_directory"},
		"read a file|compact":     {"fs:list_directory", "fs:read_file"}, // re-ordered!
		"create an issue|full":    {"github:create_issue"},
		"create an issue|compact": {"github:create_issue"},
	}
	text := map[string]string{
		"read a file|full": "f", "read a file|compact": "c",
		"create an issue|full": "f", "create an issue|compact": "c",
	}

	rep, err := RunFlipGates(stubRetrieve(t, ranked, text), testGolden(), tk, 10)
	if err != nil {
		t.Fatalf("RunFlipGates: %v", err)
	}
	id := rep.RankedIdentity
	if id.Pass {
		t.Fatalf("a single re-ordered query must fail the 100%% gate (SC-002): %+v", id)
	}
	if id.Identical != 1 || len(id.Mismatches) != 1 || id.Mismatches[0].QueryID != "q1" {
		t.Fatalf("mismatch must name the offending query: %+v", id.Mismatches)
	}
}

func TestComputeLossyGate(t *testing.T) {
	tk, err := NewTokenizer(DefaultEncoding)
	if err != nil {
		t.Fatalf("tokenizer: %v", err)
	}
	tools := []Tool{
		{ToolID: "fs:read_file", Description: "Read a file.",
			Schema: []byte(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
		{ToolID: "gh:create_issue", Description: "Create an issue.",
			Schema: []byte(`{"type":"object","properties":{"meta":{"type":"object","properties":{"k":{"type":"string"}}}}}`)},
		{ToolID: "time:now", Description: "Current time."}, // no schema = parameter-less, never lossy
		{ToolID: "bad:tool", Description: "Broken schema.",
			Schema: []byte(`not json {`)}, // unparseable -> (~), lossy
	}

	gate := ComputeLossyGate(tk, "test_corpus_v1", tools, 2)
	if gate.Tools != 4 {
		t.Fatalf("tools counted: %d", gate.Tools)
	}
	if gate.LossyTools != 2 {
		t.Fatalf("exactly the nested-object and unparseable tools are lossy, got %d", gate.LossyTools)
	}
	if gate.Rate != 0.5 {
		t.Fatalf("rate = %v, want 0.5", gate.Rate)
	}
	if gate.Pass {
		t.Fatalf("50%% lossy must fail the <20%% gate")
	}
	if len(gate.Heaviest) != 2 {
		t.Fatalf("heaviest capped at topN: %+v", gate.Heaviest)
	}
	if gate.Heaviest[0].Tokens < gate.Heaviest[1].Tokens {
		t.Fatalf("heaviest must be sorted descending by token cost: %+v", gate.Heaviest)
	}
	// Determinism (FR-019): same input, identical output.
	gate2 := ComputeLossyGate(tk, "test_corpus_v1", tools, 2)
	if fmt.Sprintf("%+v", gate) != fmt.Sprintf("%+v", gate2) {
		t.Fatalf("lossy gate must be deterministic")
	}
}

func TestComputeLossyGate_PassBelowThreshold(t *testing.T) {
	tk, err := NewTokenizer(DefaultEncoding)
	if err != nil {
		t.Fatalf("tokenizer: %v", err)
	}
	tools := make([]Tool, 0, 10)
	for i := 0; i < 9; i++ {
		tools = append(tools, Tool{
			ToolID: fmt.Sprintf("s:flat_%d", i), Description: "Flat tool.",
			Schema: []byte(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`),
		})
	}
	tools = append(tools, Tool{ToolID: "s:nested", Description: "Nested tool.",
		Schema: []byte(`{"type":"object","properties":{"o":{"type":"object","properties":{"k":{"type":"string"}}}}}`)})

	gate := ComputeLossyGate(tk, "test_corpus_v1", tools, 3)
	if gate.LossyTools != 1 || gate.Rate != 0.1 {
		t.Fatalf("1/10 lossy expected: %+v", gate)
	}
	if !gate.Pass {
		t.Fatalf("10%% lossy must pass the <20%% gate")
	}
}
