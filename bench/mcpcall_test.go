package bench

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

func TestMCPEndpointURL(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		apiKey  string
		want    string
	}{
		{
			name:    "plain base",
			baseURL: "http://127.0.0.1:8092",
			apiKey:  "eval-corpus-snapshot",
			want:    "http://127.0.0.1:8092/mcp?apikey=eval-corpus-snapshot",
		},
		{
			name:    "trailing slash",
			baseURL: "http://127.0.0.1:8092/",
			apiKey:  "k",
			want:    "http://127.0.0.1:8092/mcp?apikey=k",
		},
		{
			name:    "base already ends in /mcp",
			baseURL: "http://127.0.0.1:8092/mcp",
			apiKey:  "k",
			want:    "http://127.0.0.1:8092/mcp?apikey=k",
		},
		{
			name:    "empty api key omits the query param",
			baseURL: "http://127.0.0.1:8092",
			apiKey:  "",
			want:    "http://127.0.0.1:8092/mcp",
		},
		{
			name:    "api key is query-escaped",
			baseURL: "http://127.0.0.1:8092",
			apiKey:  "a b&c=d",
			want:    "http://127.0.0.1:8092/mcp?apikey=a+b%26c%3Dd",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mcpEndpointURL(tc.baseURL, tc.apiKey); got != tc.want {
				t.Errorf("mcpEndpointURL(%q, %q) = %q, want %q", tc.baseURL, tc.apiKey, got, tc.want)
			}
		})
	}
}

// newFixtureMCPServer builds an in-process streamable-http MCP server exposing
// a retrieve_tools tool that returns respText verbatim — the same shape as the
// proxy's handleRetrieveToolsWithMode output — so the full client path
// (initialize session, call tool, extract text content) is exercised
// hermetically. It returns the httptest server plus a counter of how many
// retrieve_tools calls were served and a capture of the last query argument.
func newFixtureMCPServer(t *testing.T, respText string) (*httptest.Server, *atomic.Int64, *atomic.Value) {
	t.Helper()
	var calls atomic.Int64
	var lastQuery atomic.Value

	srv := mcpserver.NewMCPServer("bench-fixture", "1.0.0", mcpserver.WithToolCapabilities(false))
	srv.AddTool(
		mcp.NewTool("retrieve_tools",
			mcp.WithDescription("fixture retrieve_tools"),
			mcp.WithString("query", mcp.Required()),
		),
		func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			calls.Add(1)
			lastQuery.Store(request.GetString("query", ""))
			return mcp.NewToolResultText(respText), nil
		},
	)
	ts := mcpserver.NewTestStreamableHTTPServer(srv)
	t.Cleanup(ts.Close)
	return ts, &calls, &lastQuery
}

func TestInvokeRetrieveTools_Hermetic(t *testing.T) {
	fixture := retrieveToolsResponseFixture(t, 3)
	ts, calls, lastQuery := newFixtureMCPServer(t, fixture)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// The test server serves the MCP endpoint on every path, so appending /mcp
	// (what the real proxy exposes) still routes to it.
	base := strings.TrimSuffix(ts.URL, "/")
	raw, latency, err := InvokeRetrieveTools(ctx, base, "test-key", "create github issue")
	if err != nil {
		t.Fatalf("InvokeRetrieveTools: %v", err)
	}
	if raw != fixture {
		t.Errorf("raw text = %q, want the fixture verbatim (%d vs %d bytes)", raw, len(raw), len(fixture))
	}
	if latency <= 0 {
		t.Errorf("latency = %v, want > 0", latency)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("retrieve_tools served %d times, want 1", got)
	}
	if got, _ := lastQuery.Load().(string); got != "create github issue" {
		t.Errorf("server saw query %q, want %q", got, "create github issue")
	}

	// The returned text must flow straight into the US1 measurement pipeline.
	tk := newTestTokenizer(t)
	m, err := MeasureRetrieveToolsResponse(tk, "q-hermetic", raw, latency.Seconds()*1000)
	if err != nil {
		t.Fatalf("MeasureRetrieveToolsResponse over live-path text: %v", err)
	}
	if m.ResultCount != 3 {
		t.Errorf("ResultCount = %d, want 3", m.ResultCount)
	}
}

func TestInvokeRetrieveTools_APIKeyReachesServer(t *testing.T) {
	fixture := retrieveToolsResponseFixture(t, 1)

	srv := mcpserver.NewMCPServer("bench-fixture", "1.0.0")
	srv.AddTool(
		mcp.NewTool("retrieve_tools", mcp.WithString("query", mcp.Required())),
		func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(fixture), nil
		},
	)
	streamable := mcpserver.NewStreamableHTTPServer(srv)

	var sawKey atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawKey.Store(r.URL.Query().Get("apikey"))
		streamable.ServeHTTP(w, r)
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, _, err := InvokeRetrieveTools(ctx, ts.URL, "sekret", "anything"); err != nil {
		t.Fatalf("InvokeRetrieveTools: %v", err)
	}
	if got, _ := sawKey.Load().(string); got != "sekret" {
		t.Errorf("server saw apikey=%q, want %q", got, "sekret")
	}
}

func TestMCPCaller_SessionReuse(t *testing.T) {
	fixture := retrieveToolsResponseFixture(t, 2)
	ts, calls, _ := newFixtureMCPServer(t, fixture)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	caller, err := NewMCPCaller(ctx, ts.URL, "")
	if err != nil {
		t.Fatalf("NewMCPCaller: %v", err)
	}
	defer caller.Close()

	for i, query := range []string{"first query", "second query", "third query"} {
		raw, latency, err := caller.RetrieveTools(ctx, query, 0)
		if err != nil {
			t.Fatalf("RetrieveTools call %d: %v", i+1, err)
		}
		if raw != fixture {
			t.Errorf("call %d: raw != fixture", i+1)
		}
		// Windows' monotonic clock granularity can round a sub-tick in-process
		// round-trip down to exactly 0; elsewhere the call is always measurably
		// positive. Accept 0 only on Windows, and never accept a negative.
		if latency < 0 || (latency == 0 && runtime.GOOS != "windows") {
			t.Errorf("call %d: latency = %v, want > 0", i+1, latency)
		}
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("retrieve_tools served %d times, want 3 (one session, three calls)", got)
	}
}

func TestMCPCaller_RetrieveToolsLimit(t *testing.T) {
	fixture := retrieveToolsResponseFixture(t, 1)
	var sawLimit atomic.Value

	srv := mcpserver.NewMCPServer("bench-fixture", "1.0.0")
	srv.AddTool(
		mcp.NewTool("retrieve_tools",
			mcp.WithString("query", mcp.Required()),
			mcp.WithNumber("limit"),
		),
		func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, _ := request.Params.Arguments.(map[string]interface{})
			_, present := args["limit"]
			sawLimit.Store(present)
			return mcp.NewToolResultText(fixture), nil
		},
	)
	ts := mcpserver.NewTestStreamableHTTPServer(srv)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	caller, err := NewMCPCaller(ctx, ts.URL, "")
	if err != nil {
		t.Fatalf("NewMCPCaller: %v", err)
	}
	defer caller.Close()

	// limit <= 0 → omitted, so the proxy's configured tools_limit default rules.
	if _, _, err := caller.RetrieveTools(ctx, "q", 0); err != nil {
		t.Fatalf("RetrieveTools(limit=0): %v", err)
	}
	if got, _ := sawLimit.Load().(bool); got {
		t.Error("limit=0 must omit the limit argument")
	}
	// limit > 0 → sent.
	if _, _, err := caller.RetrieveTools(ctx, "q", 7); err != nil {
		t.Fatalf("RetrieveTools(limit=7): %v", err)
	}
	if got, _ := sawLimit.Load().(bool); !got {
		t.Error("limit=7 must send the limit argument")
	}
}

func TestMCPCaller_ToolError(t *testing.T) {
	srv := mcpserver.NewMCPServer("bench-fixture", "1.0.0")
	srv.AddTool(
		mcp.NewTool("retrieve_tools", mcp.WithString("query", mcp.Required())),
		func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultError("index unavailable: boom"), nil
		},
	)
	ts := mcpserver.NewTestStreamableHTTPServer(srv)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _, err := InvokeRetrieveTools(ctx, ts.URL, "", "q")
	if err == nil {
		t.Fatal("expected error for IsError tool result, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error %q should carry the server's error text", err)
	}
}

func TestInvokeRetrieveTools_Unreachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Reserved TEST-NET-1 address: connection must fail fast, not hang.
	if _, _, err := InvokeRetrieveTools(ctx, "http://127.0.0.1:1", "", "q"); err == nil {
		t.Fatal("expected error for unreachable proxy, got nil")
	}
}

// TestInvokeRetrieveTools_Live is the T015 integration test against a real
// running mcpproxy. Guarded by BENCH_LIVE_PROXY (base URL, e.g.
// http://127.0.0.1:8094); BENCH_LIVE_PROXY_KEY carries the API key (optional —
// /mcp is unauthenticated unless require_mcp_auth). Set BENCH_LIVE_DUMP to a
// file path to save the raw response for fixture review.
func TestInvokeRetrieveTools_Live(t *testing.T) {
	base := os.Getenv("BENCH_LIVE_PROXY")
	if base == "" {
		t.Skip("BENCH_LIVE_PROXY not set — skipping live proxy integration test")
	}
	apiKey := os.Getenv("BENCH_LIVE_PROXY_KEY")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	raw, latency, err := InvokeRetrieveTools(ctx, base, apiKey, "read a file from disk")
	if err != nil {
		t.Fatalf("InvokeRetrieveTools against %s: %v", base, err)
	}
	if raw == "" {
		t.Fatal("empty response text")
	}
	if latency <= 0 {
		t.Errorf("latency = %v, want > 0", latency)
	}
	t.Logf("live retrieve_tools: %d bytes, latency %v", len(raw), latency)

	if dump := os.Getenv("BENCH_LIVE_DUMP"); dump != "" {
		if werr := os.WriteFile(dump, []byte(raw), 0o644); werr != nil {
			t.Logf("dump write failed: %v", werr)
		}
	}

	// The real response must flow through the US1 measurement pipeline: exact
	// span partition (sum==total) over the wire bytes.
	tk := newTestTokenizer(t)
	m, err := MeasureRetrieveToolsResponse(tk, "live-q", raw, latency.Seconds()*1000)
	if err != nil {
		t.Fatalf("MeasureRetrieveToolsResponse over live response: %v", err)
	}
	if m.ResultCount <= 0 {
		t.Errorf("ResultCount = %d, want > 0 (snapshot proxy has indexed tools)", m.ResultCount)
	}
	sum := 0
	for _, v := range m.Components {
		sum += v
	}
	if sum != m.TotalTokens {
		t.Errorf("sum(components) = %d, want %d", sum, m.TotalTokens)
	}
	t.Logf("live measurement: total=%d components=%v results=%d", m.TotalTokens, m.Components, m.ResultCount)
}
