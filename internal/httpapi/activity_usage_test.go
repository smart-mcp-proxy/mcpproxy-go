package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// =============================================================================
// Spec 069 A3 (MCP-750): GET /api/v1/activity/usage endpoint tests.
// =============================================================================

// mockUsageController serves a prebuilt usage snapshot + token metrics, and
// counts full-scan calls so the perf assertion (SC-005) can prove the endpoint
// never scans the log per request.
type mockUsageController struct {
	baseController
	apiKey    string
	snap      *internalRuntime.UsageAggregate
	tokens    *contracts.ServerTokenMetrics
	scanCalls int
}

func (m *mockUsageController) GetCurrentConfig() any {
	return &config.Config{APIKey: m.apiKey, Observability: config.DefaultObservabilityConfig()}
}

func (m *mockUsageController) UsageSnapshot() *internalRuntime.UsageAggregate { return m.snap }

func (m *mockUsageController) GetTokenSavings() (*contracts.ServerTokenMetrics, error) {
	return m.tokens, nil
}

func (m *mockUsageController) AggregateToolUsage(_ time.Time) (map[string]storage.ToolUsageStat, error) {
	m.scanCalls++ // any call here is a per-request full scan — must stay 0 (SC-005)
	return map[string]storage.ToolUsageStat{}, nil
}

func toolCall(server, tool, status string, reqBytes, respBytes int, durationMs int64, ts time.Time) *storage.ActivityRecord {
	return &storage.ActivityRecord{
		Type:          storage.ActivityTypeToolCall,
		ServerName:    server,
		ToolName:      tool,
		Status:        status,
		RequestBytes:  reqBytes,
		ResponseBytes: respBytes,
		DurationMs:    durationMs,
		Timestamp:     ts,
	}
}

// buildUsageSnapshot constructs a realistic aggregate by replaying records
// through the real Apply path (so latency buckets / sized counts are correct).
func buildUsageSnapshot(records ...*storage.ActivityRecord) *internalRuntime.UsageAggregate {
	agg := &internalRuntime.UsageAggregate{
		Tools:   map[string]*internalRuntime.ToolUsage{},
		Buckets: map[int64]*internalRuntime.TimeBucket{},
	}
	for _, r := range records {
		agg.Apply(r)
	}
	return agg
}

func doUsageRequest(t *testing.T, srv *Server, query string) (*httptest.ResponseRecorder, *contracts.UsageAggregateResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/activity/usage"+query, nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		return w, nil
	}
	var resp struct {
		Success bool                             `json:"success"`
		Data    contracts.UsageAggregateResponse `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	return w, &resp.Data
}

func TestActivityUsage_RankingAndMath(t *testing.T) {
	now := time.Now().UTC()
	snap := buildUsageSnapshot(
		// github:search_issues — the token sink: 3 calls, 1 error, big responses.
		toolCall("github", "search_issues", "success", 100, 5000, 120, now),
		toolCall("github", "search_issues", "success", 100, 4000, 130, now),
		toolCall("github", "search_issues", "error", 100, 3000, 140, now),
		// github:get_repo — small responses.
		toolCall("github", "get_repo", "success", 50, 200, 20, now),
		toolCall("github", "get_repo", "success", 50, 100, 25, now),
	)
	ctrl := &mockUsageController{
		apiKey: "test-key",
		snap:   snap,
		tokens: &contracts.ServerTokenMetrics{SavedTokens: 184320, SavedTokensPercentage: 92.4},
	}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	t.Run("default sort is resp_bytes desc", func(t *testing.T) {
		w, data := doUsageRequest(t, srv, "")
		require.Equal(t, http.StatusOK, w.Code)
		require.Len(t, data.Tools, 2)
		assert.Equal(t, "search_issues", data.Tools[0].Tool, "token sink ranks first by resp_bytes")
		assert.Equal(t, "get_repo", data.Tools[1].Tool)
		assert.Equal(t, "bytes", data.TokenSource)
	})

	t.Run("error_rate math", func(t *testing.T) {
		_, data := doUsageRequest(t, srv, "?sort=calls")
		var si *contracts.UsageToolStat
		for i := range data.Tools {
			if data.Tools[i].Tool == "search_issues" {
				si = &data.Tools[i]
			}
		}
		require.NotNil(t, si)
		assert.Equal(t, int64(3), si.Calls)
		assert.Equal(t, int64(1), si.Errors)
		assert.InDelta(t, 1.0/3.0, si.ErrorRate, 0.0001)
		assert.Equal(t, int64(12000), si.TotalRespBytes)
		require.NotNil(t, si.AvgRespBytes)
		assert.Equal(t, int64(4000), *si.AvgRespBytes) // 12000 / 3 sized calls
	})

	t.Run("tokens_saved echoed from metrics", func(t *testing.T) {
		_, data := doUsageRequest(t, srv, "")
		assert.Equal(t, 184320, data.TokensSaved)
		assert.InDelta(t, 92.4, data.TokensSavedPercentage, 0.001)
	})

	t.Run("p95 sort orders by latency", func(t *testing.T) {
		_, data := doUsageRequest(t, srv, "?sort=p95")
		require.Len(t, data.Tools, 2)
		assert.Equal(t, "search_issues", data.Tools[0].Tool, "slower tool ranks first by p95")
	})
}

func TestActivityUsage_AvgExcludesZeroByteCalls(t *testing.T) {
	now := time.Now().UTC()
	snap := buildUsageSnapshot(
		toolCall("svc", "tool", "success", 0, 1000, 10, now), // sized
		toolCall("svc", "tool", "success", 0, 0, 10, now),    // legacy 0-byte — excluded from avg
	)
	ctrl := &mockUsageController{apiKey: "test-key", snap: snap}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	_, data := doUsageRequest(t, srv, "")
	require.Len(t, data.Tools, 1)
	tu := data.Tools[0]
	assert.Equal(t, int64(2), tu.Calls)
	assert.Equal(t, int64(1), tu.SizedCalls, "only the non-zero-byte call counts as sized")
	require.NotNil(t, tu.AvgRespBytes)
	assert.Equal(t, int64(1000), *tu.AvgRespBytes, "avg over sized calls only, not 500")
}

func TestActivityUsage_WindowFilter(t *testing.T) {
	now := time.Now().UTC()
	snap := buildUsageSnapshot(
		toolCall("recent", "tool", "success", 10, 100, 5, now),
		toolCall("old", "tool", "success", 10, 100, 5, now.Add(-48*time.Hour)),
	)
	ctrl := &mockUsageController{apiKey: "test-key", snap: snap}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	t.Run("24h excludes tools idle beyond the window", func(t *testing.T) {
		_, data := doUsageRequest(t, srv, "?window=24h")
		require.Len(t, data.Tools, 1)
		assert.Equal(t, "recent", data.Tools[0].Server)
		assert.Equal(t, "24h", data.Window)
		// Timeline trimmed to the window: the 48h-old bucket is excluded.
		for _, b := range data.Timeline {
			assert.False(t, b.Start.Before(now.Add(-24*time.Hour)), "no buckets older than 24h")
		}
	})

	t.Run("all includes every tool", func(t *testing.T) {
		_, data := doUsageRequest(t, srv, "?window=all")
		assert.Len(t, data.Tools, 2)
	})
}

func TestActivityUsage_TopNFold(t *testing.T) {
	now := time.Now().UTC()
	snap := buildUsageSnapshot(
		toolCall("a", "t1", "success", 10, 3000, 5, now),
		toolCall("a", "t2", "success", 10, 2000, 5, now),
		toolCall("a", "t3", "success", 10, 1000, 5, now),
	)
	ctrl := &mockUsageController{apiKey: "test-key", snap: snap}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	_, data := doUsageRequest(t, srv, "?top=1")
	require.Len(t, data.Tools, 1)
	assert.Equal(t, "t1", data.Tools[0].Tool)
	require.NotNil(t, data.Other, "tail folded into other")
	assert.Equal(t, 2, data.Other.ToolsFolded)
	assert.Equal(t, int64(2), data.Other.Calls)
	assert.Equal(t, int64(3000), data.Other.TotalRespBytes) // 2000 + 1000
}

func TestActivityUsage_Filters(t *testing.T) {
	now := time.Now().UTC()
	snap := buildUsageSnapshot(
		toolCall("github", "a", "success", 10, 100, 5, now),
		toolCall("gitlab", "b", "success", 10, 100, 5, now),
		toolCall("github", "c", "error", 10, 100, 5, now),
	)
	ctrl := &mockUsageController{apiKey: "test-key", snap: snap}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	t.Run("server filter", func(t *testing.T) {
		_, data := doUsageRequest(t, srv, "?server=github")
		require.Len(t, data.Tools, 2)
		for _, tu := range data.Tools {
			assert.Equal(t, "github", tu.Server)
		}
	})
	t.Run("tool filter", func(t *testing.T) {
		_, data := doUsageRequest(t, srv, "?tool=a")
		require.Len(t, data.Tools, 1)
		assert.Equal(t, "a", data.Tools[0].Tool)
	})
	t.Run("status=error filters to tools with errors", func(t *testing.T) {
		_, data := doUsageRequest(t, srv, "?status=error")
		require.Len(t, data.Tools, 1)
		assert.Equal(t, "c", data.Tools[0].Tool)
	})
}

func TestActivityUsage_EmptyState(t *testing.T) {
	// nil snapshot (service not ready) and empty snapshot both yield a clean 200.
	for name, snap := range map[string]*internalRuntime.UsageAggregate{
		"nil":   nil,
		"empty": buildUsageSnapshot(),
	} {
		t.Run(name, func(t *testing.T) {
			ctrl := &mockUsageController{
				apiKey: "test-key",
				snap:   snap,
				tokens: &contracts.ServerTokenMetrics{SavedTokens: 42, SavedTokensPercentage: 10},
			}
			srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
			w, data := doUsageRequest(t, srv, "")
			require.Equal(t, http.StatusOK, w.Code)
			assert.Empty(t, data.Tools)
			assert.Empty(t, data.Timeline)
			assert.Nil(t, data.Other)
			assert.Equal(t, 42, data.TokensSaved, "tokens_saved still echoed on empty log")
		})
	}
}

func TestActivityUsage_BadEnumReturns400(t *testing.T) {
	ctrl := &mockUsageController{apiKey: "test-key", snap: buildUsageSnapshot()}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	for _, q := range []string{"?window=year", "?sort=bogus", "?status=maybe", "?top=abc", "?top=-1"} {
		t.Run(q, func(t *testing.T) {
			w, _ := doUsageRequest(t, srv, q)
			assert.Equal(t, http.StatusBadRequest, w.Code, "bad query %q must be 400", q)
		})
	}
}

// TestActivityUsage_NoFullScanPerRequest is the SC-005 / T015 perf assertion:
// the endpoint serves from the in-memory snapshot and must never trigger the
// full-log scan path on a normal request.
func TestActivityUsage_NoFullScanPerRequest(t *testing.T) {
	now := time.Now().UTC()
	ctrl := &mockUsageController{
		apiKey: "test-key",
		snap:   buildUsageSnapshot(toolCall("svc", "tool", "success", 10, 100, 5, now)),
	}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	for i := 0; i < 5; i++ {
		w, _ := doUsageRequest(t, srv, "?window=all")
		require.Equal(t, http.StatusOK, w.Code)
	}
	assert.Equal(t, 0, ctrl.scanCalls, "usage endpoint must not scan the activity log per request")
}
