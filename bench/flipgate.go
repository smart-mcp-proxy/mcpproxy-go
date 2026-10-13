package bench

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolsig"
)

// This file implements the Spec 085 flip gates (FR-017/FR-018): the metrics
// that authorize the Phase-2 default flip of tool_response_mode to compact.
//
//   - Ranked-ID identity across modes, per golden query (gate: 100% — SC-002).
//   - Full vs compact retrieve_tools response-token distributions and the
//     median reduction (SC-001 is measured from these).
//   - Lossy-signature rate over a frozen corpus via the SHARED
//     internal/toolsig grammar (gate: <20% — SC-005), plus the heaviest
//     signatures for triage.
//   - describe_tool usage per completed task (informational; the E2E suite
//     populates it — a live run leaves it nil).
//
// The live driver (RunFlipGates) is deliberately parameterized over a
// RetrieveToolsFunc so the gate math is testable without a running proxy; the
// production caller (MCPRetrieveCaller, mcpcaller.go) replays real MCP
// retrieve_tools calls with detail=full / detail=compact through the same
// pipeline (FR-017).

// lossyGateThreshold is the FR-018 lossy-signature-rate gate: the flip is
// authorized only when strictly less than 20% of corpus signatures are lossy.
const lossyGateThreshold = 0.20

// RetrieveToolsFunc replays one query through the proxy's MCP retrieve_tools
// tool with the given per-call detail override ("full" | "compact") and
// returns the ordered entry ids (server:tool, best first) plus the raw
// serialized response text — exactly what gets tokenized.
type RetrieveToolsFunc func(query string, limit int, detail string) (rankedIDs []string, responseText string, err error)

// TokenDistribution summarizes per-query response-token counts for one mode.
type TokenDistribution struct {
	Samples int `json:"samples"`
	P50     int `json:"p50"`
	P95     int `json:"p95"`
	Max     int `json:"max"`
}

// QueryModeMismatch records one golden query whose ranked ids differ between
// modes — any entry here fails the SC-002 gate.
type QueryModeMismatch struct {
	QueryID    string   `json:"query_id"`
	Query      string   `json:"query"`
	FullIDs    []string `json:"full_ids"`
	CompactIDs []string `json:"compact_ids"`
}

// RankedIdentityGate is the per-query ranked-ID identity result (gate: 100%).
type RankedIdentityGate struct {
	Queries    int                 `json:"queries"`
	Identical  int                 `json:"identical"`
	Pass       bool                `json:"pass"`
	Mismatches []QueryModeMismatch `json:"mismatches,omitempty"`
}

// TokenReductionReport compares full vs compact response-token distributions.
// MedianReduction = 1 - compact.P50/full.P50 (SC-001 gates on >= 0.50).
type TokenReductionReport struct {
	Encoding        string            `json:"encoding"`
	Full            TokenDistribution `json:"full"`
	Compact         TokenDistribution `json:"compact"`
	MedianReduction float64           `json:"median_reduction"`
}

// HeaviestSignature is one corpus signature ranked by token cost, for triage
// of what compact mode still spends tokens on.
type HeaviestSignature struct {
	ToolID string `json:"tool_id"`
	Tokens int    `json:"tokens"`
	Sig    string `json:"sig"`
	Lossy  bool   `json:"lossy"`
}

// LossySignatureGate is the corpus lossy-rate result (gate: <20%, SC-005).
type LossySignatureGate struct {
	CorpusVersion string              `json:"corpus_version"`
	Tools         int                 `json:"tools"`
	LossyTools    int                 `json:"lossy_tools"`
	Rate          float64             `json:"rate"`
	Pass          bool                `json:"pass"`
	Heaviest      []HeaviestSignature `json:"heaviest,omitempty"`
}

// DescribeToolUsage is the informational FR-018 metric: describe_tool calls
// per completed task, collected by the E2E suite (not by a live bench run).
type DescribeToolUsage struct {
	Calls          int     `json:"calls"`
	CompletedTasks int     `json:"completed_tasks"`
	PerTask        float64 `json:"per_task"`
}

// FlipGateReport bundles every FR-018 flip-gate metric.
type FlipGateReport struct {
	Encoding       string                `json:"encoding"`
	RankedIdentity *RankedIdentityGate   `json:"ranked_identity"`
	Tokens         *TokenReductionReport `json:"tokens"`
	Lossy          *LossySignatureGate   `json:"lossy,omitempty"`
	DescribeTool   *DescribeToolUsage    `json:"describe_tool_usage,omitempty"`
	Notes          []string              `json:"notes,omitempty"`
}

// RunFlipGates replays every golden query through retrieve_tools twice — once
// per detail mode, same pipeline (FR-017) — and computes the ranked-ID
// identity gate plus the full/compact token distributions. The lossy gate is
// computed separately (ComputeLossyGate) because it needs a schema-bearing
// corpus, which a golden set does not carry.
func RunFlipGates(retrieve RetrieveToolsFunc, golden *GoldenSet, tk *Tokenizer, limit int) (*FlipGateReport, error) {
	if golden == nil || len(golden.Queries) == 0 {
		return nil, fmt.Errorf("golden set is empty")
	}
	if retrieve == nil {
		return nil, fmt.Errorf("retrieve function is nil")
	}

	identity := &RankedIdentityGate{Queries: len(golden.Queries)}
	fullTokens := make([]int, 0, len(golden.Queries))
	compactTokens := make([]int, 0, len(golden.Queries))

	for _, q := range golden.Queries {
		fullIDs, fullText, err := retrieve(q.Query, limit, "full")
		if err != nil {
			return nil, fmt.Errorf("retrieve %q (full): %w", q.ID, err)
		}
		compactIDs, compactText, err := retrieve(q.Query, limit, "compact")
		if err != nil {
			return nil, fmt.Errorf("retrieve %q (compact): %w", q.ID, err)
		}

		if equalIDs(fullIDs, compactIDs) {
			identity.Identical++
		} else {
			identity.Mismatches = append(identity.Mismatches, QueryModeMismatch{
				QueryID: q.ID, Query: q.Query, FullIDs: fullIDs, CompactIDs: compactIDs,
			})
		}
		fullTokens = append(fullTokens, tk.Count(fullText))
		compactTokens = append(compactTokens, tk.Count(compactText))
	}
	identity.Pass = identity.Identical == identity.Queries

	tokens := &TokenReductionReport{
		Encoding: tk.encoding,
		Full:     distribution(fullTokens),
		Compact:  distribution(compactTokens),
	}
	if tokens.Full.P50 > 0 {
		tokens.MedianReduction = 1.0 - float64(tokens.Compact.P50)/float64(tokens.Full.P50)
	}

	return &FlipGateReport{
		Encoding:       tk.encoding,
		RankedIdentity: identity,
		Tokens:         tokens,
		Notes: []string{
			"Ranked-ID identity gate (SC-002): 100% required — any per-query mismatch blocks the Phase-2 default flip.",
			"Median reduction compares p50 response tokens, compact vs full, over the golden set replayed through the same live pipeline (FR-017).",
			"describe_tool usage is collected by the E2E suite (informational, FR-018); a live bench run leaves it unset.",
		},
	}, nil
}

// RunLiveFlipGates is the live compact arm (FR-017): it connects an MCP
// client to a running proxy, replays the golden set through retrieve_tools in
// both detail modes, and computes the full flip-gate report. The lossy gate
// runs over the live upstream tool corpus (with schemas from
// GET /api/v1/tools); the frozen corpus_v2 re-baseline follows the 083 rebase
// (tasks T040/T043).
func RunLiveFlipGates(ctx context.Context, mcpURL string, golden *GoldenSet, upstream []Tool, corpusVersion string, tk *Tokenizer, limit int) (*FlipGateReport, error) {
	caller, err := NewMCPRetrieveCaller(ctx, mcpURL)
	if err != nil {
		return nil, err
	}
	defer caller.Close()

	rep, err := RunFlipGates(caller.RetrieveFunc(ctx), golden, tk, limit)
	if err != nil {
		return nil, err
	}
	rep.Lossy = ComputeLossyGate(tk, corpusVersion, upstream, 10)
	rep.Notes = append(rep.Notes,
		"Lossy gate runs over the live upstream corpus; the frozen corpus_v2 (spec 083) re-baseline lands after the 085 rebase on the merged 083 branch (tasks T040/T043).")
	return rep, nil
}

// ComputeLossyGate renders every corpus tool through the SHARED
// internal/toolsig grammar (FR-019) and reports the lossy-signature rate
// (gate: <20%) plus the topN heaviest signatures by token cost. A tool with
// no schema is a parameter-less tool (renders "()", never lossy) — matching
// the live normalizeSchema semantics — not an unparseable one.
func ComputeLossyGate(tk *Tokenizer, corpusVersion string, tools []Tool, topN int) *LossySignatureGate {
	gate := &LossySignatureGate{CorpusVersion: corpusVersion, Tools: len(tools)}

	heaviest := make([]HeaviestSignature, 0, len(tools))
	for _, tl := range tools {
		params := string(tl.Schema)
		if params == "" {
			params = `{"type":"object","properties":{}}`
		}
		sig, _ := toolsig.Render(params, tl.Description) // fail-soft: (~) on unparseable
		if sig.Lossy {
			gate.LossyTools++
		}
		heaviest = append(heaviest, HeaviestSignature{
			ToolID: tl.ToolID,
			Tokens: tk.Count(tl.ToolID + sig.Sig + " " + sig.Desc),
			Sig:    sig.Sig,
			Lossy:  sig.Lossy,
		})
	}
	if gate.Tools > 0 {
		gate.Rate = float64(gate.LossyTools) / float64(gate.Tools)
	}
	gate.Pass = gate.Rate < lossyGateThreshold

	// Deterministic order: tokens descending, tool id ascending on ties.
	sort.Slice(heaviest, func(i, j int) bool {
		if heaviest[i].Tokens != heaviest[j].Tokens {
			return heaviest[i].Tokens > heaviest[j].Tokens
		}
		return heaviest[i].ToolID < heaviest[j].ToolID
	})
	if topN > 0 && len(heaviest) > topN {
		heaviest = heaviest[:topN]
	}
	gate.Heaviest = heaviest
	return gate
}

// distribution computes nearest-rank percentiles over token counts.
func distribution(samples []int) TokenDistribution {
	d := TokenDistribution{Samples: len(samples)}
	if len(samples) == 0 {
		return d
	}
	sorted := make([]int, len(samples))
	copy(sorted, samples)
	sort.Ints(sorted)
	d.P50 = intPercentile(sorted, 50)
	d.P95 = intPercentile(sorted, 95)
	d.Max = sorted[len(sorted)-1]
	return d
}

// intPercentile is the nearest-rank percentile p (0-100) of a sorted slice.
func intPercentile(sorted []int, p float64) int {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p / 100.0 * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

// equalIDs reports whether two ordered id lists are identical.
func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
