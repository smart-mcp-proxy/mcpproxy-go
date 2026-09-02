package server

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/pkoukk/tiktoken-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cache"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/index"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

// One dispatch writes TWO records, and they used to read two different inputs
// for the same question.
//
// The internal_tool_call record derives its stamp from forwardContentResult's
// own `wasTruncated`. The paired tool_call record derived its stamp from
// tokenMetrics.WasTruncated — a field that is assigned only when a tokenizer is
// present AND its count succeeds. Encodings are loaded lazily through
// tiktoken's BPE loader, so a count can fail at run time on a core that started
// fine: an air-gapped deployment whose configured model needs an encoding that
// was never cached is the ordinary way to get there.
//
// When it fails, the old derivation reported contracts.CutNone — and because
// the emitter derives `response_truncated` FROM the stamp, the record then also
// claimed no truncation at all, while forwardContentResult had genuinely cut
// the agent's copy. A record holding the cut text and claiming to be whole is
// the same silent understatement in the other direction: a cost recomputation
// treats the shortened text as everything the call produced.
//
// The failure is injected at the real seam — tiktoken's package-level BPE
// loader — rather than by stubbing our own tokenizer, because the tokenizer is
// reached through a concrete *runtime.Runtime with no injection point, and
// because the loader is exactly what fails in the production scenario.

// failingBpeLoader makes every lazy encoding load fail, which is what an
// air-gapped core with an uncached encoding sees.
type failingBpeLoader struct{}

func (failingBpeLoader) LoadTiktokenBpe(string) (map[string]int, error) {
	return nil, fmt.Errorf("no network and no cached encoding")
}

// breakTokenCounting installs the failing loader for the rest of the test, then
// PROVES the runtime's tokenizer now fails.
//
// It is called AFTER the runtime is built: runtime.New validates the default
// encoding at construction and falls back to a DISABLED tokenizer if that
// fails, and a disabled tokenizer returns (0, nil) — no error, so the old
// derivation would have worked and this test would have proved nothing.
//
// The premise check is not decoration. tiktoken memoizes encodings in a
// package-level map shared by every test in this binary, so if anything else
// ever warms o200k_base first, the loader below stops being reached and both
// tests would pass while asserting nothing. Failing loudly here is the point:
// a guard that quietly stops guarding is worse than no guard.
func breakTokenCounting(t *testing.T, rt *runtime.Runtime) {
	t.Helper()
	tiktoken.SetBpeLoader(failingBpeLoader{})
	t.Cleanup(func() { tiktoken.SetBpeLoader(tiktoken.NewDefaultBpeLoader()) })

	_, err := rt.Tokenizer().CountTokensForModel("premise", "gpt-4o")
	require.Error(t, err,
		"token counting still succeeds, so this test no longer exercises a count FAILURE. "+
			"Most likely something else in this package warmed o200k_base into tiktoken's "+
			"package-level encoding map before this ran — pick an encoding nothing else loads.")
}

const oversizedUpstreamChars = 40_000

// newConnectedUpstreamProxy wires a proxy to a REAL runtime with its activity
// service running and to a REAL upstream MCP server over HTTP, so a call_tool_*
// dispatch runs end to end and the records an operator would export are the
// ones asserted. responseLimit is the tool_response_limit the truncator applies.
func newConnectedUpstreamProxy(t *testing.T, responseLimit int) (*MCPProxyServer, *runtime.Runtime) {
	t.Helper()

	logger := zap.NewNop()

	upstreamSrv := mcpserver.NewMCPServer("bulky", "1.0.0-test", mcpserver.WithToolCapabilities(true))
	upstreamSrv.AddTool(mcp.Tool{
		Name:        "dump_everything",
		Description: "Returns far more text than the proxy forwards",
		InputSchema: mcp.ToolInputSchema{Type: "object", Properties: map[string]interface{}{}},
	}, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(strings.Repeat("payload ", oversizedUpstreamChars/8)), nil
	})

	ts := httptest.NewServer(mcpserver.NewStreamableHTTPServer(upstreamSrv))
	t.Cleanup(ts.Close)

	serverCfg := &config.ServerConfig{
		Name: "bulky", URL: ts.URL, Protocol: "streamable-http", Enabled: true,
	}

	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.ToolResponseLimit = responseLimit
	cfg.Servers = []*config.ServerConfig{serverCfg}
	// The model matters. tiktoken memoizes every encoding it has ever built in
	// a PACKAGE-level map, and runtime.New warms the configured default
	// encoding (cl100k_base) at construction — so a later count for a
	// cl100k_base model is served from that map and cannot be made to fail.
	// gpt-4o maps to o200k_base, which nothing warms, so its load is genuinely
	// lazy and the broken loader below reaches it. That laziness IS the
	// production hazard: an air-gapped core configured for gpt-4o starts
	// cleanly and only fails when it first counts.
	cfg.Tokenizer = &config.TokenizerConfig{
		Enabled: true, Encoding: "cl100k_base", DefaultModel: "gpt-4o",
	}

	rt, err := runtime.New(cfg, "", logger)
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	sm := rt.StorageManager()
	require.NotNil(t, sm)
	require.NoError(t, sm.SaveUpstreamServer(serverCfg))

	idx, err := index.NewManager(t.TempDir(), logger)
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	um := upstream.NewManager(logger, cfg, nil, secret.NewResolver(), nil)
	require.NoError(t, um.AddServer("bulky", serverCfg))

	cm, err := cache.NewManager(sm.GetDB(), logger)
	require.NoError(t, err)
	t.Cleanup(func() { cm.Close() })

	tr := truncate.NewTruncator(responseLimit)
	proxy := NewMCPProxyServer(sm, idx, um, cm, func() *truncate.Truncator { return tr },
		logger, &Server{runtime: rt}, false, cfg, rt.SignatureCache())

	go rt.ActivityService().Start(rt.AppContext(), rt)

	client, ok := um.GetClient("bulky")
	require.True(t, ok, "the upstream client must be registered")
	require.Eventually(t, client.IsConnected, 15*time.Second, 100*time.Millisecond,
		"the upstream fixture must connect, or the dispatch never reaches the truncator")

	return proxy, rt
}

func callBulkyDumpEverything(t *testing.T, proxy *MCPProxyServer) *mcp.CallToolResult {
	t.Helper()

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"name":   "bulky:dump_everything",
		"args":   map[string]interface{}{},
		"intent": map[string]interface{}{"operation_type": "read"},
	}
	result, err := proxy.handleCallToolVariant(context.Background(), req, contracts.ToolVariantRead)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError, "the fixture must produce a real upstream answer: %v", result.Content)
	return result
}

func TestCallToolVariant_CutStampSurvivesTokenCountFailure(t *testing.T) {
	proxy, rt := newConnectedUpstreamProxy(t, 5_000)
	breakTokenCounting(t, rt)

	result := callBulkyDumpEverything(t, proxy)

	// Premise check: the fixture has to actually overflow the limit, or every
	// assertion below is vacuous.
	require.Less(t, len(resultText(t, result)), oversizedUpstreamChars,
		"fixture must exceed tool_response_limit: the agent's text has to be shorter than the upstream payload")

	rec := awaitToolCallActivity(t, rt, "bulky", "dump_everything")

	assert.Equal(t, contracts.CutShortenedAgentAndRecord, rec.ResponseTruncationCut,
		"forwardContentResult cut this response, so the record must say so even though "+
			"token counting failed. Deriving the stamp from tokenMetrics.WasTruncated made "+
			"an unavailable tokenizer look like an untruncated call")
	assert.True(t, rec.ResponseTruncated,
		"the emitter derives the flag from the stamp, so a CutNone stamp also erases the "+
			"truncation itself: the record then holds the CUT text while claiming to be whole")

	resolved := contracts.ResolveResponseTruncation(
		rec.ResponseTruncationCut, rec.ResponseTruncated, rec.ResponseStorageTruncated)
	assert.Equal(t, contracts.StoredEqualsDelivered, resolved.Relation,
		"a forward cut leaves the record holding exactly the agent's copy")
}

// The legacy call_tool handler carried a second copy of the same derivation and
// is fixed the same way. It is no longer registered on the tool surface, but it
// is still live code with its own forwardContentResult call and its own
// emitActivityToolCallCompleted — and a stale copy of a rule is how the rule
// comes back.
func TestCallToolLegacy_CutStampSurvivesTokenCountFailure(t *testing.T) {
	proxy, rt := newConnectedUpstreamProxy(t, 5_000)
	breakTokenCounting(t, rt)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"name": "bulky:dump_everything",
		"args": map[string]interface{}{},
	}
	result, err := proxy.handleCallTool(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError, "the fixture must produce a real upstream answer: %v", result.Content)

	require.Less(t, len(resultText(t, result)), oversizedUpstreamChars,
		"fixture must exceed tool_response_limit")

	rec := awaitToolCallActivity(t, rt, "bulky", "dump_everything")
	assert.Equal(t, contracts.CutShortenedAgentAndRecord, rec.ResponseTruncationCut)
	assert.True(t, rec.ResponseTruncated)
}

// The other half: the stamp still has to mean something when nothing was cut.
// A blanket stamp would be as wrong as a blanket direction.
func TestCallToolVariant_NoCutIsNotStampedWhenTokenCountingFails(t *testing.T) {
	proxy, rt := newConnectedUpstreamProxy(t, 0) // 0 = unlimited
	breakTokenCounting(t, rt)

	result := callBulkyDumpEverything(t, proxy)
	require.Equal(t, oversizedUpstreamChars, len(resultText(t, result)),
		"with no limit the whole upstream payload must be forwarded")

	rec := awaitToolCallActivity(t, rt, "bulky", "dump_everything")
	assert.False(t, rec.ResponseTruncated,
		"nothing was cut, so nothing may be flagged")
	assert.Equal(t, contracts.CutNone, rec.ResponseTruncationCut)
}
