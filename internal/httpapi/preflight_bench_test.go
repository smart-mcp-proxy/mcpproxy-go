package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 098 SC-002, normative measurement: "a preflight of 10 tools completes in
// under 50 ms (p95) … at the handler level (evaluator + response +
// activity-record build, excluding HTTP transport)".
//
// What is inside the measured loop: request decode, validation + dedup, tier
// detection, a REAL evaluation over in-memory readers, response construction,
// JSON encoding of the envelope, and the activity-record build. What is
// deliberately outside: the network, chi routing and auth middleware (excluded
// by calling the handler directly), and the BBolt write behind RecordPreflight
// (a storage cost, measured where storage is measured).
//
// The assertion is a ceiling with two orders of magnitude of headroom rather
// than the 50 ms budget itself: it fails on an architectural regression (an
// upstream call, a full index scan) and cannot flake on a loaded CI runner.
const preflightBenchPerOpCeiling = 250 * time.Millisecond

// --- in-memory evaluator world ---------------------------------------------

type benchIndex struct {
	tools   map[string][]preflight.IndexedTool
	servers []string
}

func (b *benchIndex) ToolsByServer(serverName string) ([]preflight.IndexedTool, error) {
	return b.tools[serverName], nil
}

func (b *benchIndex) IndexedServerNames() ([]string, error) { return b.servers, nil }

type benchApprovals struct {
	records map[string]*preflight.ApprovalState
}

func (b *benchApprovals) ToolApproval(serverName, toolName string) (*preflight.ApprovalState, error) {
	return b.records[serverName+":"+toolName], nil
}

type benchState struct {
	states map[string]preflight.ServerRuntime
}

func (b *benchState) ServerRuntime(serverName string) (preflight.ServerRuntime, bool) {
	rt, ok := b.states[serverName]
	return rt, ok
}

type benchPolicy struct {
	servers map[string]preflight.ServerPolicy
}

func (b *benchPolicy) ServerPolicy(serverName string) (preflight.ServerPolicy, error) {
	return b.servers[serverName], nil
}

func (b *benchPolicy) ToolConfigDenied(_, _ string) (bool, error) { return false, nil }
func (b *benchPolicy) QuarantineEnabled() bool                    { return true }

// benchPreflightController runs the real evaluator over an in-memory world, so
// the benchmark measures the same code path a served preflight takes minus the
// database.
type benchPreflightController struct {
	baseController
	ec preflight.EvalContext
}

func newBenchPreflightController(servers, toolsPerServer int) *benchPreflightController {
	index := &benchIndex{tools: make(map[string][]preflight.IndexedTool, servers)}
	approvals := &benchApprovals{records: make(map[string]*preflight.ApprovalState, servers*toolsPerServer)}
	state := &benchState{states: make(map[string]preflight.ServerRuntime, servers)}
	policy := &benchPolicy{servers: make(map[string]preflight.ServerPolicy, servers)}

	readOnly, destructive, openWorld := true, false, false
	for s := 0; s < servers; s++ {
		serverName := fmt.Sprintf("srv%02d", s)
		index.servers = append(index.servers, serverName)
		state.states[serverName] = preflight.ServerRuntime{State: preflight.RuntimeStateReady}
		policy.servers[serverName] = preflight.ServerPolicy{Found: true, Enabled: true}

		tools := make([]preflight.IndexedTool, 0, toolsPerServer)
		for i := 0; i < toolsPerServer; i++ {
			toolID := fmt.Sprintf("%s:tool%02d", serverName, i)
			tools = append(tools, preflight.IndexedTool{
				Name: toolID,
				Annotations: &config.ToolAnnotations{
					ReadOnlyHint:    &readOnly,
					DestructiveHint: &destructive,
					OpenWorldHint:   &openWorld,
				},
			})
			approvals.records[toolID] = &preflight.ApprovalState{
				Status:            preflight.ApprovalStatusApproved,
				CurrentHash:       "abc123def456",
				HashSchemaVersion: 2,
			}
		}
		index.tools[serverName] = tools
	}

	return &benchPreflightController{
		ec: preflight.EvalContext{
			Index:     index,
			Approvals: approvals,
			State:     state,
			Policy:    policy,
			Tier:      preflight.TierOperator,
		},
	}
}

func (c *benchPreflightController) GetCurrentConfig() interface{} {
	return &config.Config{APIKey: preflightTestAPIKey}
}

func (c *benchPreflightController) RunPreflight(ctx context.Context, params preflight.Params) (preflight.Outcome, error) {
	ec := c.ec
	ec.Tier = params.Tier
	ec.Filters = params.Filters
	results, err := preflight.Evaluate(ctx, ec, params.Tools)
	if err != nil {
		return preflight.Outcome{}, err
	}
	return preflight.Outcome{Verdict: preflight.VerdictForResults(results), Results: results}, nil
}

// RecordPreflight accepts the built record and discards it: the record BUILD is
// what SC-002 counts, the BBolt write is storage's cost.
func (c *benchPreflightController) RecordPreflight(rec internalRuntime.PreflightActivity) error {
	if len(rec.Tools) == 0 {
		return fmt.Errorf("empty preflight activity record")
	}
	return nil
}

func benchPreflightBody(b *testing.B, ids ...string) []byte {
	b.Helper()
	request := contracts.PreflightRequest{Tools: make([]contracts.PreflightToolRef, 0, len(ids))}
	for _, id := range ids {
		request.Tools = append(request.Tools, contracts.PreflightToolRef{ID: id})
	}
	body, err := json.Marshal(request)
	if err != nil {
		b.Fatalf("marshal request: %v", err)
	}
	return body
}

func benchPreflightIDs(n int) []string {
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ids = append(ids, fmt.Sprintf("srv%02d:tool%02d", i%10, i%5))
	}
	return ids
}

func runPreflightBenchmark(b *testing.B, ids []string) {
	b.Helper()
	srv := NewServer(newBenchPreflightController(10, 5), zap.NewNop().Sugar(), nil)
	body := benchPreflightBody(b, ids...)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/preflight", bytes.NewReader(body))
		w := httptest.NewRecorder()
		// The handler is called directly: SC-002 excludes HTTP transport, and
		// routing/auth are measured by their own middlewares' tests.
		srv.handlePreflight(w, req)
		if w.Code != http.StatusOK {
			b.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
	}
	b.StopTimer()

	if b.N > 0 {
		if perOp := b.Elapsed() / time.Duration(b.N); perOp > preflightBenchPerOpCeiling {
			b.Errorf("preflight took %v per op, over the %v ceiling — something in the path is doing I/O-scale work", perOp, preflightBenchPerOpCeiling)
		}
	}
}

// BenchmarkPreflightHandler10Tools is the SC-002 shape.
func BenchmarkPreflightHandler10Tools(b *testing.B) {
	runPreflightBenchmark(b, benchPreflightIDs(10))
}

// BenchmarkPreflightHandler100Tools is the largest legal request.
func BenchmarkPreflightHandler100Tools(b *testing.B) {
	runPreflightBenchmark(b, benchPreflightIDs(100))
}

// BenchmarkPreflightHandlerMixed includes the diagnosis-heavy branches an
// unhappy cron run actually hits (unknown id + did_you_mean, unknown server).
func BenchmarkPreflightHandlerMixed(b *testing.B) {
	ids := append(benchPreflightIDs(8), "srv00:missing", "ghost:tool00")
	runPreflightBenchmark(b, ids)
}
