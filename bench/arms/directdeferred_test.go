package arms

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
)

// directDeferredCorpusV2Path is the schema-bearing frozen corpus the golden-
// output test pins its fixture to (contracts/arm-interface.md).
const directDeferredCorpusV2Path = "../../specs/083-discovery-profiler/datasets/corpus_v2.tools.json"

func TestDirectDeferred_ArmContractFlags(t *testing.T) {
	arm := NewDirectDeferred()
	if got := arm.Name(); got != "direct_deferred" {
		t.Errorf("Name() = %q, want direct_deferred", got)
	}
	if arm.IndexAltering() {
		t.Error("deferral changes wire serialization only; the index still ingests the full schema, so IndexAltering must be false")
	}
	if arm.LowerBound() {
		t.Error("direct_deferred appends the signature to the full description; LowerBound must be false")
	}
}

func TestDirectDeferred_Registered(t *testing.T) {
	a, err := Resolve("direct_deferred")
	if err != nil {
		t.Fatalf("Resolve(direct_deferred): %v", err)
	}
	if a.Name() != "direct_deferred" {
		t.Errorf("resolved arm Name() = %q", a.Name())
	}
}

// TestDirectDeferred_EncodeTool pins the wire shape of one deferred entry: the
// "__" display name, the "[server] " prefixed description with the bare tool
// name carrying the signature on its own line, the permissive placeholder
// schema, and no outputSchema.
func TestDirectDeferred_EncodeTool(t *testing.T) {
	arm := NewDirectDeferred()

	cases := []struct {
		name string
		tool bench.Tool
		want string
	}{
		{
			name: "required marked *, optionals sorted after required",
			tool: bench.Tool{
				ToolID: "github:create_issue", Server: "github", Name: "create_issue", Description: "Create a new issue.",
				Schema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"owner":{"type":"string"},"labels":{"type":"array","items":{"type":"string"}}},"required":["owner","title"]}`),
			},
			want: `{"description":"[github] Create a new issue.\ncreate_issue(owner*:str, title*:str, labels:[str])","inputSchema":{"type":"object"},"name":"github__create_issue"}`,
		},
		{
			name: "no schema renders empty parens",
			tool: bench.Tool{
				ToolID: "s:ping", Server: "s", Name: "ping", Description: "No inputs.",
			},
			want: `{"description":"[s] No inputs.\nping()","inputSchema":{"type":"object"},"name":"s__ping"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := arm.EncodeTool(tc.tool)
			if err != nil {
				t.Fatalf("EncodeTool: %v", err)
			}
			if got != tc.want {
				t.Errorf("EncodeTool:\ngot:  %s\nwant: %s", got, tc.want)
			}
		})
	}
}

// TestDirectDeferred_NoUpstreamSchemaInPayload is the feature's whole claim,
// asserted rather than assumed: the entry must carry no property name, no
// required list, and no outputSchema from the upstream definition.
func TestDirectDeferred_NoUpstreamSchemaInPayload(t *testing.T) {
	tool := bench.Tool{
		ToolID: "s:t", Server: "s", Name: "t", Description: "d",
		Schema: json.RawMessage(`{"type":"object","properties":{"unmistakable_property":{"type":"string","description":"very long upstream prose"}},"required":["unmistakable_property"]}`),
	}

	enc, err := NewDirectDeferred().EncodeTool(tool)
	if err != nil {
		t.Fatalf("EncodeTool: %v", err)
	}

	var entry struct {
		InputSchema json.RawMessage `json:"inputSchema"`
	}
	if err := json.Unmarshal([]byte(enc), &entry); err != nil {
		t.Fatalf("entry is not valid JSON: %v", err)
	}
	if got := string(entry.InputSchema); got != `{"type":"object"}` {
		t.Errorf("inputSchema = %s, want the permissive placeholder %s", got, `{"type":"object"}`)
	}
	if strings.Contains(enc, "very long upstream prose") {
		t.Error("entry leaks upstream property prose; the schema must be deferred, not embedded")
	}
	if strings.Contains(enc, "outputSchema") {
		t.Error("entry carries an outputSchema; deferral strips it (FR-006)")
	}
	// The signature is what makes the deferred entry callable, so its absence
	// would be a silent regression to a nameless, typeless listing.
	if !strings.Contains(enc, `t(unmistakable_property*:str)`) {
		t.Errorf("entry is missing the named compact signature: %s", enc)
	}
}

// TestDirectDeferred_UnparseableSchemaIsExplicitError guards the one place this
// arm deliberately departs from the serving path: toolsig.Render's fail-soft
// "(~)" is right for a listing that must never fail and wrong for a
// measurement, which would then charge ~3 tokens for a tool baseline_json
// charges in full (contract rule 2).
func TestDirectDeferred_UnparseableSchemaIsExplicitError(t *testing.T) {
	arm := NewDirectDeferred()
	bad := bench.Tool{ToolID: "s:bad", Server: "s", Name: "bad", Description: "d",
		Schema: json.RawMessage(`{"type":"object",`)}
	if _, err := arm.EncodeTool(bad); err == nil {
		t.Fatal("EncodeTool must fail explicitly on an unparseable schema, got nil error")
	}
	if _, err := arm.EncodeListing([]bench.Tool{bad}); err == nil {
		t.Fatal("EncodeListing must propagate the per-tool failure, got nil error")
	}
}

// TestDirectDeferred_GoldenCorpusV2 is the committed golden-output determinism
// test (contracts/arm-interface.md): first 3 tools of corpus_v2, encoded bytes
// pinned in testdata/directdeferred_golden.txt. Changing the arm's output
// requires updating the fixture in the same PR — encoding drift is a reviewed
// event, because it invalidates cross-run comparisons.
func TestDirectDeferred_GoldenCorpusV2(t *testing.T) {
	corpus, err := bench.LoadCorpus(directDeferredCorpusV2Path)
	if err != nil {
		t.Fatalf("LoadCorpus(corpus_v2): %v", err)
	}
	if len(corpus.Tools) < 3 {
		t.Fatalf("corpus_v2 has %d tools, need at least 3", len(corpus.Tools))
	}
	first3 := corpus.Tools[:3]

	arm := NewDirectDeferred()
	listing, err := arm.EncodeListing(first3)
	if err != nil {
		t.Fatalf("EncodeListing(first 3): %v", err)
	}

	wantBytes, err := os.ReadFile(filepath.Join("testdata", "directdeferred_golden.txt"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	// Pre-commit's end-of-file-fixer guarantees a trailing newline on committed
	// fixtures; the encoder's output has no such requirement, so compare trimmed.
	if want := strings.TrimRight(string(wantBytes), "\n"); strings.TrimRight(listing, "\n") != want {
		t.Errorf("direct_deferred output drifted from committed golden fixture:\ngot:  %s\nwant: %s", listing, want)
	}

	// The listing is the per-tool entries verbatim inside a once-paid envelope,
	// so listing totals decompose exactly into per-tool costs.
	parts := make([]string, 0, len(first3))
	for _, tl := range first3 {
		enc, terr := arm.EncodeTool(tl)
		if terr != nil {
			t.Fatalf("EncodeTool(%s): %v", tl.ToolID, terr)
		}
		parts = append(parts, enc)
	}
	if joined := `{"tools":[` + strings.Join(parts, ",") + `]}`; listing != joined {
		t.Errorf("listing is not the enveloped per-tool encodings:\nlisting: %s\njoined:  %s", listing, joined)
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
