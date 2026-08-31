package server

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cache"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/index"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

// Spec 103 (T005). The activity log stores the FULL pre-truncation
// retrieve_tools response while the agent only ever received the truncated
// text. If the record does not say it was truncated, a truncated record is
// indistinguishable from a complete one — and the benchmark loader, which
// tokenizes the stored response to recompute what a workload cost, would
// tokenize text the agent never paid for and OVERSTATE the cost. Overstating
// mcpproxy's own cost is the one direction of error the benchmark cannot be
// allowed to make silently, so the flag has to reach the record, not just the
// log line.
//
// The proxy is wired to a REAL Runtime with its ActivityService running rather
// than to a stub emitter: what matters is the record an operator would later
// export, and only the real service builds that record.

// newTruncatingRetrieveToolsProxy builds a runtime-backed proxy whose response
// limit is small enough that a two-tool discovery response is guaranteed to
// exceed it. The shared createTestProxyWithRuntime helper pins the truncator to
// an unlimited one, which is the opposite of what this test needs.
func newTruncatingRetrieveToolsProxy(t *testing.T, responseLimit int) (*MCPProxyServer, *runtime.Runtime) {
	t.Helper()

	logger := zap.NewNop()

	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.ToolsLimit = 20
	cfg.ToolResponseLimit = responseLimit
	cfg.Servers = []*config.ServerConfig{{Name: "github", Enabled: true}}

	rt, err := runtime.New(cfg, "", logger)
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	sm := rt.StorageManager()
	require.NotNil(t, sm, "runtime must expose a storage manager")

	idx, err := index.NewManager(t.TempDir(), logger)
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	um := upstream.NewManager(logger, cfg, nil, secret.NewResolver(), nil)

	cm, err := cache.NewManager(sm.GetDB(), logger)
	require.NoError(t, err)
	t.Cleanup(func() { cm.Close() })

	tr := truncate.NewTruncator(responseLimit)

	mainSrv := &Server{runtime: rt}
	proxy := NewMCPProxyServer(sm, idx, um, cm, func() *truncate.Truncator { return tr },
		logger, mainSrv, false, cfg, rt.SignatureCache())

	// The service is what turns an emitted event into a persisted record;
	// production starts it the same way (internal/runtime/lifecycle.go).
	go rt.ActivityService().Start(rt.AppContext(), rt)

	return proxy, rt
}

// awaitRetrieveToolsActivity polls until the retrieve_tools record lands.
// Emission publishes onto the event bus synchronously but the service drains it
// on its own goroutine, so a bare read after the handler returns is a race.
func awaitRetrieveToolsActivity(t *testing.T, sm *storage.Manager) *storage.ActivityRecord {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		records, _, err := sm.ListActivities(storage.ActivityFilter{Limit: 50})
		require.NoError(t, err)
		for _, rec := range records {
			if rec.Type == storage.ActivityTypeInternalToolCall && rec.ToolName == "retrieve_tools" {
				return rec
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("no retrieve_tools activity record was persisted within 5s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestRetrieveTools_TruncatedResponseIsFlaggedOnTheActivityRecord is the
// guard: a retrieve_tools call whose response was cut must leave a record that
// SAYS it was cut.
func TestRetrieveTools_TruncatedResponseIsFlaggedOnTheActivityRecord(t *testing.T) {
	// Small enough that two indexed tools with real schemas cannot fit, large
	// enough that the truncator's own banner still has room.
	proxy, rt := newTruncatingRetrieveToolsProxy(t, 400)

	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "github", Enabled: true}))
	for _, tool := range []string{"create_issue", "create_pull_request"} {
		require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
			Name:        tool,
			ServerName:  "github",
			Description: "Create a GitHub " + tool + " with a title, a body, labels, assignees and milestones",
			ParamsJSON:  `{"type":"object","properties":{"title":{"type":"string","description":"the title"},"body":{"type":"string","description":"the body"},"labels":{"type":"array","items":{"type":"string"}}}}`,
			Hash:        "hash-" + tool,
		}))
	}

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"query": "create", "limit": float64(20), "detail": config.ToolResponseModeFull,
	}
	result, err := proxy.handleRetrieveTools(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	rec := awaitRetrieveToolsActivity(t, rt.StorageManager())

	// Premise check. If the response were NOT actually cut, the assertion
	// below would be asserting nothing — and this test's whole value is that
	// it fails when the flag is missing, not when the fixture stops truncating.
	require.Less(t, len(resultText(t, result)), len(rec.Response),
		"fixture must actually truncate: the agent's text has to be shorter than the stored full response")

	assert.True(t, rec.ResponseTruncated,
		"the truncated retrieve_tools response must be flagged on the activity record; "+
			"without the flag a benchmark loader tokenizes the stored FULL response and overstates agent cost")
}

// TestRetrieveTools_UntruncatedResponseIsNotFlagged is the other half of the
// contract: the flag must mean something. A blanket true would let the loader
// exclude every retrieve_tools record and lose the whole discovery surface from
// the accounting.
func TestRetrieveTools_UntruncatedResponseIsNotFlagged(t *testing.T) {
	proxy, rt := newTruncatingRetrieveToolsProxy(t, 0) // 0 = unlimited

	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "github", Enabled: true}))
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name:        "create_issue",
		ServerName:  "github",
		Description: "Create a GitHub issue with a title and body",
		ParamsJSON:  `{"type":"object","properties":{"title":{"type":"string"}}}`,
		Hash:        "hash-create-issue",
	}))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"query": "create issue", "limit": float64(20), "detail": config.ToolResponseModeFull,
	}
	_, err := proxy.handleRetrieveTools(context.Background(), req)
	require.NoError(t, err)

	rec := awaitRetrieveToolsActivity(t, rt.StorageManager())
	assert.False(t, rec.ResponseTruncated,
		"a complete response must not be flagged truncated, or the loader excludes everything")
}
