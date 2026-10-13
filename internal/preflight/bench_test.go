package preflight

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 098 SC-002 micro-benchmark: the evaluator alone, over a corpus with
// enough servers and tools that the scope/corpus walks are not free.
//
// The normative measurement is the handler-level benchmark in
// internal/httpapi (it adds response encoding and the activity-record build);
// this one exists so a regression can be attributed — evaluator or plumbing.
//
// The guard at the end is deliberately enormous (three orders of magnitude of
// headroom over the observed cost). It catches a change that makes an
// evaluation do I/O-scale work, and cannot flake on a loaded CI runner the way
// a tight wall-clock assertion would.
const benchPerOpCeiling = 50 * time.Millisecond

// benchWorld builds a fixture of servers × tools with the readers the evaluator
// needs. Everything is in memory: the benchmark measures the evaluator, not a
// database.
func benchWorld(servers, toolsPerServer int) EvalContext {
	index := &fakeIndex{
		tools:       make(map[string][]IndexedTool, servers),
		serverOrder: make([]string, 0, servers),
	}
	approvals := &fakeApprovals{records: make(map[string]*ApprovalState, servers*toolsPerServer)}
	state := &fakeState{states: make(map[string]ServerRuntime, servers)}
	policy := &fakePolicy{
		servers:    make(map[string]ServerPolicy, servers),
		denied:     make(map[string]bool),
		quarantine: true,
	}

	for s := 0; s < servers; s++ {
		serverName := fmt.Sprintf("srv%02d", s)
		index.serverOrder = append(index.serverOrder, serverName)
		state.states[serverName] = ServerRuntime{State: RuntimeStateReady}
		policy.servers[serverName] = ServerPolicy{Found: true, Enabled: true}

		indexed := make([]IndexedTool, 0, toolsPerServer)
		for i := 0; i < toolsPerServer; i++ {
			toolName := fmt.Sprintf("tool%02d", i)
			toolID := serverName + ":" + toolName
			indexed = append(indexed, IndexedTool{
				Name: toolID,
				Annotations: &config.ToolAnnotations{
					ReadOnlyHint:    boolPtr(true),
					DestructiveHint: boolPtr(false),
					OpenWorldHint:   boolPtr(false),
				},
			})
			approvals.records[toolID] = &ApprovalState{
				Status:            ApprovalStatusApproved,
				CurrentHash:       "abc123def456",
				HashSchemaVersion: 2,
			}
		}
		index.tools[serverName] = indexed
	}

	return EvalContext{
		Index:     index,
		Approvals: approvals,
		State:     state,
		Policy:    policy,
		Tier:      TierOperator,
	}
}

func benchRefs(n int) []ToolRef {
	refs := make([]ToolRef, 0, n)
	for i := 0; i < n; i++ {
		refs = append(refs, ToolRef{ID: fmt.Sprintf("srv%02d:tool%02d", i%10, i%5)})
	}
	return refs
}

func assertBenchCeiling(b *testing.B) {
	b.Helper()
	if b.N == 0 {
		return
	}
	perOp := b.Elapsed() / time.Duration(b.N)
	if perOp > benchPerOpCeiling {
		b.Errorf("evaluation took %v per op, over the %v ceiling — the evaluator is doing far more than local reads", perOp, benchPerOpCeiling)
	}
}

// BenchmarkEvaluateReadySet is the SC-002 shape: 10 required tools, all ready.
func BenchmarkEvaluateReadySet(b *testing.B) {
	ctx := context.Background()
	ec := benchWorld(10, 5)
	refs := benchRefs(10)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, err := Evaluate(ctx, ec, refs)
		if err != nil {
			b.Fatalf("evaluate: %v", err)
		}
		if len(results) != len(refs) {
			b.Fatalf("expected %d results, got %d", len(refs), len(results))
		}
	}
	b.StopTimer()
	assertBenchCeiling(b)
}

// BenchmarkEvaluateMixedSet exercises the expensive branches: not_found builds
// the caller-visible corpus and runs did_you_mean over it, and the pinned entry
// runs hash comparison.
func BenchmarkEvaluateMixedSet(b *testing.B) {
	ctx := context.Background()
	ec := benchWorld(10, 5)

	refs := append(benchRefs(7),
		ToolRef{ID: "srv00:tool99"},                                // not_found + did_you_mean
		ToolRef{ID: "ghost:tool00"},                                // server_not_configured
		ToolRef{ID: "srv01:tool01", PinHash: "sha256/v2:deadbeef"}, // hash_mismatch
	)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Evaluate(ctx, ec, refs); err != nil {
			b.Fatalf("evaluate: %v", err)
		}
	}
	b.StopTimer()
	assertBenchCeiling(b)
}

// BenchmarkEvaluateMaxBatch is the request-size ceiling (100 ids), so the cost
// of the largest legal request is on record.
func BenchmarkEvaluateMaxBatch(b *testing.B) {
	ctx := context.Background()
	ec := benchWorld(10, 5)
	refs := benchRefs(100)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Evaluate(ctx, ec, refs); err != nil {
			b.Fatalf("evaluate: %v", err)
		}
	}
	b.StopTimer()
	assertBenchCeiling(b)
}
