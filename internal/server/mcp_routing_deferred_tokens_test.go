package server

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/pkoukk/tiktoken-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolsig"
)

// Spec 102 T035a — the SC-001/SC-002 token gates for US1's own claim.
//
// Measured in-process against the REAL renderer rather than through a bench
// arm, so what is counted is the production wire payload (mcp.Tool marshalling,
// the "[server] " prefix, mcp-go's annotation defaults, the raw-schema
// placeholder) and not a re-implementation of it. The reusable bench/arms/ arm
// stays T075/T076. The encoder is cl100k_base — the same one the spec-083
// profiler pins, and the same one TestDescribeTool_DefinitionTokenBudget uses.
//
// ── SC-001 IS NOT REACHABLE ON THIS CORPUS, AND THAT IS A SPEC-NUMBERS DEFECT ──
//
// SC-001 declares "≥70% smaller in tokens than full mode" on the frozen 45-tool
// reference corpus. Measured here (cl100k_base, real renderer):
//
//	full     = 6116 tokens  (inputSchema 2604, annotations 1125,
//	                         description 1784, name 286, outputSchema 0)
//	deferred = 4301 tokens
//	reduction = 29.7%
//
// The shortfall is arithmetic, not an implementation defect. Deferral can only
// remove the upstream inputSchema — 2604 of 6116 tokens, 42.6% of the payload.
// Everything else is either required by FR-004 (the untouched description,
// unchanged annotations, the name) or is the appended signature FR-004 mandates.
// So even a hypothetical listing that deleted BOTH the schema and the signature
// would reach only 38.9%: the ceiling on this corpus is below SC-001's floor.
//
// SC-001's threshold was calibrated on issue #971's fleet shape (~300 tokens per
// tool, ~30K per 100 tools), where the schema is assumed to be the dominant
// term. corpus_v2 is schema-light and description-heavy (~136 tokens/tool), and
// mcp-go's unconditional annotations block alone is a fixed ~25 tokens per entry
// that no serialization change can touch.
//
// SC-001's second clause does not hold either. Measured the same way over the
// 527-tool livemcptool snapshot (specs/083-discovery-profiler/datasets/
// livemcptool_snapshot/tools.json, ~190 tokens/tool — the closest thing this
// repo has to the "~100-tool fleet"): full = 99918, deferred = 65138,
// reduction = 34.8%, against SC-001's projected ≥85%. So the criterion is not
// merely mis-fitted to the reference corpus; the ≥70%/≥85% numbers assume the
// upstream schema is a far larger share of a tools/list payload than it is in
// either dataset available here.
//
// Truncating descriptions to close the gap is not available: FR-004 requires the
// existing description untouched.
//
// The gate asserted below is therefore a REGRESSION FLOOR on the implementation,
// not a restatement of SC-001. Resolving SC-001 itself — restating its threshold
// per corpus shape, or re-targeting the criterion at a schema-heavy corpus — is
// escalated to T076, where the criterion is formally re-measured and recorded.
const (
	// deferredCorpusPath is the frozen 45-tool reference corpus WITH schemas.
	// corpus_v1 (spec 065) carries no schemas and cannot measure deferral.
	deferredCorpusPath = "../../specs/083-discovery-profiler/datasets/corpus_v2.tools.json"

	// deferredCorpusReductionFloor is the regression floor for the measured
	// payload reduction (measured: 29.7%). Set just under it so an
	// implementation regression — a schema leaking back into a deferred entry, a
	// signature ballooning — fails, while corpus-neutral churn does not.
	deferredCorpusReductionFloor = 0.25

	// deferredCorpusSC001Target is SC-001's declared threshold, kept here as the
	// documented record of what the spec asks for versus what this corpus can
	// deliver. Deliberately NOT asserted — see the defect note above.
	deferredCorpusSC001Target = 0.70

	// deferredCorpusNonLossyFloor is SC-002 as written: ≥80% of corpus tools are
	// callable one-shot from the deferred listing, i.e. their signature is not
	// lossy. This one IS the spec's own number and IS asserted.
	deferredCorpusNonLossyFloor = 0.80
)

type deferredCorpusTool struct {
	ToolID      string          `json:"tool_id"`
	Server      string          `json:"server"`
	Tool        string          `json:"tool"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

func loadDeferredCorpus(t *testing.T) []*config.ToolMetadata {
	t.Helper()

	raw, err := os.ReadFile(deferredCorpusPath)
	require.NoError(t, err, "the frozen reference corpus must be present")

	var corpus struct {
		Version string               `json:"version"`
		Tools   []deferredCorpusTool `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(raw, &corpus))
	require.Len(t, corpus.Tools, 45, "SC-001/SC-002 are stated against the 45-tool corpus")

	out := make([]*config.ToolMetadata, 0, len(corpus.Tools))
	for _, tool := range corpus.Tools {
		out = append(out, &config.ToolMetadata{
			ServerName:  tool.Server,
			Name:        tool.Tool,
			Description: tool.Description,
			ParamsJSON:  string(tool.Schema),
			// The corpus carries no Spec-032 hash; tool_id is unique per tool and
			// is only ever used here as the signature-cache key.
			Hash: tool.ToolID,
		})
	}
	return out
}

// renderCorpusTokens renders the whole corpus in one mode and returns the token
// count of the marshalled entries, as a client would receive them.
func renderCorpusTokens(t *testing.T, mode string, tools []*config.ToolMetadata) int {
	t.Helper()

	p := newDeferredRenderProxy(mode)
	// Warmed the way the indexing path warms it, so the deferred path measures
	// a cache HIT (FR-005). Measuring the miss path would understate the
	// signature cost and overstate the saving.
	warmFixtureSignatures(p, tools)

	enc, err := tiktoken.GetEncoding("cl100k_base")
	require.NoError(t, err, "cl100k_base must be loadable (the profiler pins the same encoder)")

	total := 0
	rendered := p.renderDirectTools(buildDirectCatalog(tools, nil))
	require.Len(t, rendered, len(tools), "every corpus tool must be listed in both modes")
	for _, st := range rendered {
		raw, err := json.Marshal(st.Tool)
		require.NoErrorf(t, err, "marshal %q", st.Tool.Name)
		total += len(enc.Encode(string(raw), nil, nil))
	}
	return total
}

func TestDeferredDirect_TokenReduction_Corpus45(t *testing.T) {
	tools := loadDeferredCorpus(t)

	full := renderCorpusTokens(t, config.DirectToolResponseModeFull, tools)
	deferred := renderCorpusTokens(t, config.DirectToolResponseModeDeferred, tools)

	require.Positive(t, full)
	reduction := 1 - float64(deferred)/float64(full)

	t.Logf("SC-001 measurement (corpus_v2, 45 tools, cl100k_base): full=%d deferred=%d reduction=%.1f%% (spec target %.0f%%)",
		full, deferred, reduction*100, deferredCorpusSC001Target*100)

	assert.GreaterOrEqualf(t, reduction, deferredCorpusReductionFloor,
		"deferred rendering regressed: %.1f%% reduction is below the %.0f%% floor",
		reduction*100, deferredCorpusReductionFloor*100)

	// A guard on the guard: if a future change ever DOES clear SC-001 on this
	// corpus, the defect note above has gone stale and must be revisited rather
	// than silently left in place.
	assert.Lessf(t, reduction, deferredCorpusSC001Target,
		"reduction now clears SC-001's %.0f%% on this corpus — update the spec-defect note and promote this to a real gate",
		deferredCorpusSC001Target*100)
}

func TestDeferredDirect_NonLossyShare_Corpus45(t *testing.T) {
	tools := loadDeferredCorpus(t)

	nonLossy := 0
	for _, tool := range tools {
		sig, _ := toolsig.Render(tool.ParamsJSON, tool.Description)
		if !sig.Lossy {
			nonLossy++
		}
	}

	share := float64(nonLossy) / float64(len(tools))
	t.Logf("SC-002 measurement: %d/%d tools one-shot callable (%.1f%%)", nonLossy, len(tools), share*100)

	assert.GreaterOrEqualf(t, share, deferredCorpusNonLossyFloor,
		"SC-002: only %.1f%% of corpus tools are callable one-shot from the deferred listing", share*100)
}
