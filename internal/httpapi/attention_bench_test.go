package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Review finding F7: SC-011 as written ("GET /api/v1/attention p95 ≤ 20ms")
// had no test timing the actual HTTP route. internal/runtime's
// TestComputeSC011LargeFleetP95 only times Compute() building the snapshot;
// this is its httpapi companion, timing what handleGetAttention itself adds
// on top of an already-computed snapshot — caller-scope filtering
// (filterAttentionItems) and JSON envelope encoding — so a regression in
// either (e.g. an accidental per-item allocation, a scope check that
// re-walks the server list) fails this test even while Compute stays fast.
//
// Same convention as preflight_bench_test.go: the handler is called
// directly, excluding the network, chi routing and auth middleware (a
// request with no AuthContext reaches handleGetAttention as an
// administrator would, per filterAttentionItems).
const attentionHandlerBenchP95Ceiling = 20 * time.Millisecond

const attentionHandlerBenchRuns = 200

// buildAttentionHandlerBenchItems mirrors internal/runtime's SC-011 fixture
// shape (100 servers / 1,000 tools / 50 clients) through the real Compute
// pipeline, so the item count and shape handleGetAttention serializes here
// matches what a live 100-server fleet's snapshot actually looks like.
func buildAttentionHandlerBenchItems() []contracts.AttentionItem {
	now := time.Now()
	const servers = 100
	const toolsPerServer = 10

	in := internalRuntime.AttentionInput{Now: now}
	for i := 0; i < servers; i++ {
		s := internalRuntime.AttentionServer{
			Name:       fmt.Sprintf("server-%03d", i),
			Enabled:    true,
			StateSince: now.Add(-90 * time.Second),
			Health:     contracts.HealthStatus{Status: health.StatusReady},
		}
		switch i % 5 {
		case 0:
			s.Health.Status = health.StatusSignInRequired
		case 1:
			s.Quarantined = true
		case 2:
			s.Pending = toolsPerServer / 2
			s.Changed = toolsPerServer / 2
		case 3:
			s.Health.Status = health.StatusError
		case 4:
			s.Health.Status = health.StatusNeedsSecret
		}
		in.Servers = append(in.Servers, s)
	}
	for i := 0; i < 50; i++ {
		connectedAt := now.Add(-10 * time.Minute)
		in.Clients = append(in.Clients, internalRuntime.AttentionClient{
			ID: fmt.Sprintf("client-%02d", i), ConnectedAt: &connectedAt,
		})
	}
	return internalRuntime.Compute(in)
}

// TestGetAttentionHandlerSC011P95 is the httpapi-level SC-011 regression
// guard: handleGetAttention's own work (scope filtering + JSON encoding)
// over the 100-server/1,000-tool/50-client fixture must stay at or under the
// 20ms GET /attention budget, independent of Compute's own (separately
// guarded) cost.
func TestGetAttentionHandlerSC011P95(t *testing.T) {
	items := buildAttentionHandlerBenchItems()
	if len(items) == 0 {
		t.Fatal("fixture produced no attention items; the p95 assertion would pass vacuously")
	}

	ctrl := &scopeController{attentionItemsOverride: items}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	durations := make([]time.Duration, 0, attentionHandlerBenchRuns)
	for i := 0; i < attentionHandlerBenchRuns; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/attention", http.NoBody)
		w := httptest.NewRecorder()

		start := time.Now()
		srv.handleGetAttention(w, req)
		durations = append(durations, time.Since(start))

		if w.Code != http.StatusOK {
			t.Fatalf("handleGetAttention: unexpected status %d, body: %s", w.Code, w.Body.String())
		}
	}

	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[int(float64(len(durations))*0.95)]
	if p95 > attentionHandlerBenchP95Ceiling {
		t.Errorf("handleGetAttention p95 = %v over the SC-011 %v budget at %d items",
			p95, attentionHandlerBenchP95Ceiling, len(items))
	}
}

// BenchmarkGetAttentionHandler is the `go test -bench` companion, for
// profiling and `-benchmem`.
func BenchmarkGetAttentionHandler(b *testing.B) {
	items := buildAttentionHandlerBenchItems()
	ctrl := &scopeController{attentionItemsOverride: items}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/attention", http.NoBody)
		w := httptest.NewRecorder()
		srv.handleGetAttention(w, req)
	}
}
