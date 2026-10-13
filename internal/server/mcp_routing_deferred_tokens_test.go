package server

import (
	"encoding/json"
	"fmt"
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
// ── SC-001 WAS RESTATED PER CORPUS SHAPE (maintainer decision, 2026-08-29) ──
//
// As originally written SC-001 asked for ≥70% reduction on the frozen 45-tool
// corpus and ≥85% on a ~100-tool fleet. T035a measured:
//
//	corpus_v2.tools.json          45 tools   6116 → 4301   29.7%   (asked ≥70%)
//	livemcptool_snapshot          527 tools  99918 → 65138 34.8%   (asked ≥85%)
//
// The ceiling on corpus_v2 is 38.9% — what deleting BOTH the schema and the
// signature would yield — so no implementation of this design could reach 70%
// on this corpus shape. The original number assumed schemas dominate the
// payload (spec-083 profiling put them at ~77%); at these shapes names,
// descriptions and annotations dominate, and FR-004 forbids touching those.
//
// SC-001 now asks ≥25% on corpus_v2 and ≥30% on the 527-tool snapshot — roughly
// 15% relative below each measured value, as regression headroom rather than a
// second projection. Both bounds are asserted here, so this file IS the SC-001
// gate rather than a stand-in for one.
//
// The 70% tripwire is retained: clearing it would mean the corpus premise
// behind the restatement has changed, and the criterion must be re-derived
// rather than left in place.

const (
	// deferredCorpusPath is the frozen 45-tool reference corpus WITH schemas.
	// corpus_v1 (spec 065) carries no schemas and cannot measure deferral.
	deferredCorpusPath = "../../specs/083-discovery-profiler/datasets/corpus_v2.tools.json"

	// deferredLargeCorpusPath is the 527-tool LiveMCPBench snapshot — the
	// second half of the revised SC-001, and the only in-repo corpus large
	// enough to stand in for a real fleet.
	deferredLargeCorpusPath = "../../specs/083-discovery-profiler/datasets/livemcptool_snapshot/tools.json"

	// deferredCorpusReductionFloor is revised SC-001's reference-corpus bound
	// (measured 29.7%, ceiling 38.9%).
	deferredCorpusReductionFloor = 0.25

	// deferredLargeCorpusReductionFloor is revised SC-001's fleet-scale bound
	// (measured 34.8%).
	deferredLargeCorpusReductionFloor = 0.30

	// deferredCorpusSC001Tripwire is the ORIGINAL threshold, retained as an
	// upper guard. Clearing it means the corpus premise behind the restatement
	// no longer holds and SC-001 must be re-derived — not silently enjoyed.
	deferredCorpusSC001Tripwire = 0.70

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
	rendered := p.renderDirectTools(directCatalogFor(p, tools))
	require.Len(t, rendered, len(tools), "every corpus tool must be listed in both modes")
	for _, st := range rendered {
		raw, err := json.Marshal(st.Tool)
		require.NoErrorf(t, err, "marshal %q", st.Tool.Name)
		total += len(enc.Encode(string(raw), nil, nil))
	}
	return total
}

// loadDeferredLargeCorpus reads the 527-tool LiveMCPBench snapshot. Its rows
// use `inputSchema` where corpus_v2 uses `schema`, and carry no tool_id, so it
// needs its own decoder rather than a shared one.
func loadDeferredLargeCorpus(t *testing.T) []*config.ToolMetadata {
	t.Helper()

	raw, err := os.ReadFile(deferredLargeCorpusPath)
	require.NoError(t, err, "the LiveMCPBench snapshot must be present")

	var corpus struct {
		ToolCount int `json:"tool_count"`
		Tools     []struct {
			Server      string          `json:"server"`
			Tool        string          `json:"tool"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(raw, &corpus))
	require.Len(t, corpus.Tools, 527, "SC-001's fleet-scale bound is stated against the 527-tool snapshot")

	out := make([]*config.ToolMetadata, 0, len(corpus.Tools))
	for i, tool := range corpus.Tools {
		params := string(tool.InputSchema)
		if params == "" || params == "null" {
			params = `{"type":"object"}`
		}
		out = append(out, &config.ToolMetadata{
			ServerName:  tool.Server,
			Name:        tool.Tool,
			Description: tool.Description,
			ParamsJSON:  params,
			// Distinct per row: the signature cache is keyed by hash, so a
			// shared one would collapse 527 tools onto a single signature and
			// silently understate the deferred payload.
			Hash: fmt.Sprintf("large-corpus-%d", i),
		})
	}
	return out
}

func TestDeferredDirect_TokenReduction_Corpus45(t *testing.T) {
	tools := loadDeferredCorpus(t)

	full := renderCorpusTokens(t, config.DirectToolResponseModeFull, tools)
	deferred := renderCorpusTokens(t, config.DirectToolResponseModeDeferred, tools)

	require.Positive(t, full)
	reduction := 1 - float64(deferred)/float64(full)

	t.Logf("SC-001 (corpus_v2, 45 tools, cl100k_base): full=%d deferred=%d reduction=%.1f%% (revised bound %.0f%%)",
		full, deferred, reduction*100, deferredCorpusReductionFloor*100)

	assert.GreaterOrEqualf(t, reduction, deferredCorpusReductionFloor,
		"revised SC-001: %.1f%% reduction is below the %.0f%% reference-corpus bound",
		reduction*100, deferredCorpusReductionFloor*100)

	assert.Lessf(t, reduction, deferredCorpusSC001Tripwire,
		"reduction now clears the original %.0f%% on this corpus — the shape premise behind the SC-001 restatement no longer holds; re-derive the criterion instead of leaving it",
		deferredCorpusSC001Tripwire*100)
}

// The fleet-scale half of revised SC-001. corpus_v2 is 45 tools; a real
// deployment is hundreds, and the reduction is a function of corpus shape, so
// asserting only the small corpus would leave half the criterion unenforced.
func TestDeferredDirect_TokenReduction_LargeCorpus(t *testing.T) {
	tools := loadDeferredLargeCorpus(t)

	full := renderCorpusTokens(t, config.DirectToolResponseModeFull, tools)
	deferred := renderCorpusTokens(t, config.DirectToolResponseModeDeferred, tools)

	require.Positive(t, full)
	reduction := 1 - float64(deferred)/float64(full)

	t.Logf("SC-001 (livemcptool snapshot, %d tools, cl100k_base): full=%d deferred=%d reduction=%.1f%% (revised bound %.0f%%)",
		len(tools), full, deferred, reduction*100, deferredLargeCorpusReductionFloor*100)

	assert.GreaterOrEqualf(t, reduction, deferredLargeCorpusReductionFloor,
		"revised SC-001: %.1f%% reduction is below the %.0f%% fleet-scale bound",
		reduction*100, deferredLargeCorpusReductionFloor*100)

	assert.Lessf(t, reduction, deferredCorpusSC001Tripwire,
		"reduction now clears the original %.0f%% at fleet scale — re-derive SC-001 rather than leaving the restatement in place",
		deferredCorpusSC001Tripwire*100)
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
