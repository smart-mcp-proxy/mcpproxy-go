package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 058 US3, plan risk R1 (security). mcp-go keys in-flight request cancels
// as "<sessionID>:<requestID>" (server/server.go inflightKey) and registers
// every request unconditionally (request_handler.go). A 2026-07-28 request is
// served by an ephemeral session whose id is "", so two modern clients that
// both use JSON-RPC id 1 — the common case — share the key ":1", and a
// `notifications/cancelled` from one cancels the other's in-flight call.
//
// mcpproxy's guard (modernCancellationGuard, mcp.go) sits in the HTTP chain:
// a modern POST carrying a cancellation notification is acknowledged (202) and
// dropped. Under the stateless protocol there is no session to correlate it
// with anyway; a client that wants to abandon a call closes its connection,
// which cancels the handler's context through the transport.
//
// Test names avoid the substring "MCP" (release-qa-gate.yml skips on it).

// cancelProbe is a bare MCP server with one tool that blocks until released or
// cancelled, so a test can observe exactly which of the two happened.
type cancelProbe struct {
	srv     *mcpserver.MCPServer
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newCancelProbe() *cancelProbe {
	p := &cancelProbe{
		srv:     mcpserver.NewMCPServer("cancel-probe", "1.0.0"),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	p.srv.AddTool(
		mcp.NewTool("slow", mcp.WithDescription("blocks until released or cancelled")),
		func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			p.once.Do(func() { close(p.started) })
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-p.release:
				return mcp.NewToolResultText("done"), nil
			}
		},
	)
	return p
}

// serve mounts the probe over an UNPINNED Streamable HTTP transport, optionally
// behind a middleware, and returns the endpoint URL.
func (p *cancelProbe) serve(t *testing.T, wrap func(http.Handler) http.Handler) string {
	t.Helper()
	var h http.Handler = mcpserver.NewStreamableHTTPServer(p.srv)
	if wrap != nil {
		h = wrap(h)
	}
	httpServer := httptest.NewServer(h)
	t.Cleanup(httpServer.Close)
	return httpServer.URL
}

// asyncResult is the outcome of a tools/call issued on its own goroutine.
type asyncResult struct {
	status int
	body   []byte
	err    error
}

// callSlowAsync issues tools/call slow with the given JSON-RPC id and returns
// the channel the outcome lands on. It builds the request through the client's
// own helpers but asserts nothing on the goroutine.
func callSlowAsync(c *rawEraClient, id int) <-chan asyncResult {
	out := make(chan asyncResult, 1)
	req, err := c.buildRequest(string(mcp.MethodToolsCall), map[string]any{"name": "slow", "arguments": map[string]any{}}, id)
	if err != nil {
		out <- asyncResult{err: err}
		return out
	}
	go func() {
		status, body, err := c.do(req)
		out <- asyncResult{status: status, body: body, err: err}
	}()
	return out
}

// cancelledNotification is the raw notifications/cancelled a client posts for
// requestId id.
func cancelledNotificationParams(id int) map[string]any {
	return map[string]any{"requestId": id, "reason": "test"}
}

func awaitStarted(t *testing.T, p *cancelProbe) {
	t.Helper()
	select {
	case <-p.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the slow tool never started")
	}
}

func awaitOutcome(t *testing.T, ch <-chan asyncResult) asyncResult {
	t.Helper()
	select {
	case r := <-ch:
		require.NoError(t, r.err)
		return r
	case <-time.After(15 * time.Second):
		t.Fatal("the in-flight call neither completed nor was cancelled within 15s")
		return asyncResult{}
	}
}

// callSucceeded reports whether the raw tools/call response is the released
// tool's success result.
func callSucceeded(body []byte) bool {
	var envelope struct {
		Result *struct {
			IsError bool              `json:"isError"`
			Content []json.RawMessage `json:"content"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Result == nil || envelope.Error != nil {
		return false
	}
	return !envelope.Result.IsError && bytes.Contains(body, []byte(`"done"`))
}

// Documents the library defect the guard exists for, so the guard can be
// retired the day this test SKIPS: against an unguarded mcp-go v1.0.0
// transport, client B's notifications/cancelled for id 1 cancels client A's
// in-flight call with id 1. Both are modern, so both run under session id "".
func TestCancelIsolation_LibraryCollisionStillPresent(t *testing.T) {
	probe := newCancelProbe()
	url := probe.serve(t, nil)
	era := modernEra()

	a := newRawEraClient(t, url, era)
	b := newRawEraClient(t, url, era)

	inflight := callSlowAsync(a, 1)
	awaitStarted(t, probe)

	resp, _ := b.send(string(mcp.MethodNotificationCancelled), cancelledNotificationParams(1), false)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	// A bounded window for a cross-client cancellation to land. If A is still
	// running afterwards, the library no longer collides: release A, require
	// the released result, and skip — the signal to retire the guard.
	select {
	case r := <-inflight:
		require.NoError(t, r.err)
		assert.False(t, callSucceeded(r.body), "A ended before release, so it must not carry the released result: %s", r.body)
		assert.Contains(t, string(r.body), "context canceled",
			"A must have ended by cancellation, not by some other failure: %s", r.body)
		close(probe.release)
		return
	case <-time.After(500 * time.Millisecond):
	}
	close(probe.release)
	outcome := awaitOutcome(t, inflight)
	require.True(t, callSucceeded(outcome.body), "A was not cancelled yet did not complete when released: %s", outcome.body)
	t.Skip("mcp-go no longer lets one modern client cancel another's request: " +
		"the upstream fix has landed — retire modernCancellationGuard and this test together")
}

// The guard: with mcpproxy's middleware in the chain, B's cancellation is
// acknowledged and dropped, and A's call runs to completion when released.
func TestCancelIsolation_ModernClientsCannotCancelEachOther(t *testing.T) {
	probe := newCancelProbe()
	url := probe.serve(t, modernCancellationGuard)
	era := modernEra()

	a := newRawEraClient(t, url, era)
	b := newRawEraClient(t, url, era)

	inflight := callSlowAsync(a, 1)
	awaitStarted(t, probe)

	resp, body := b.send(string(mcp.MethodNotificationCancelled), cancelledNotificationParams(1), false)
	assert.Equal(t, http.StatusAccepted, resp.StatusCode, "a dropped notification is still acknowledged: %s", body)

	// Give a wrongly-forwarded cancellation time to land before releasing.
	select {
	case r := <-inflight:
		t.Fatalf("A's in-flight call ended before it was released — B's cancellation reached it: %s", r.body)
	case <-time.After(300 * time.Millisecond):
	}
	close(probe.release)

	outcome := awaitOutcome(t, inflight)
	require.Equal(t, http.StatusOK, outcome.status)
	assert.True(t, callSucceeded(outcome.body), "A's call must complete with the released result, got: %s", outcome.body)
}

// The guard is scoped to the modern era: a legacy session's own
// notifications/cancelled still cancels its own in-flight request, exactly as
// before (the session id keys it, so there is no collision to guard against).
func TestCancelIsolation_LegacySelfCancelStillWorks(t *testing.T) {
	probe := newCancelProbe()
	url := probe.serve(t, modernCancellationGuard)

	a := newRawEraClient(t, url, legacyEra())
	// buildRequest hands out ids explicitly, so the call takes 1 here and the
	// notification refers to it.
	inflight := callSlowAsync(a, 1)
	awaitStarted(t, probe)

	resp, _ := a.send(string(mcp.MethodNotificationCancelled), cancelledNotificationParams(1), false)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	outcome := awaitOutcome(t, inflight)
	assert.False(t, callSucceeded(outcome.body), "a legacy session must still be able to cancel its own request; got a completed result: %s", outcome.body)
	close(probe.release)
}

// A modern tools/call that is NOT a cancellation passes through the guard
// untouched: the body is re-readable downstream and the result is served.
func TestCancelIsolation_GuardPassesOrdinaryRequests(t *testing.T) {
	probe := newCancelProbe()
	close(probe.release) // the tool answers immediately
	url := probe.serve(t, modernCancellationGuard)

	a := newRawEraClient(t, url, modernEra())
	result := a.callTool("slow", map[string]any{})
	assert.False(t, result.IsError)
	require.NotEmpty(t, result.Content)
	text, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, "done", text.Text)

	// Malformed bodies are left to mcp-go's own parse error, not swallowed.
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte("{not json")))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(mcp.HeaderProtocolVersion, mcp.LATEST_PROTOCOL_VERSION)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	assert.NotEqual(t, http.StatusAccepted, resp.StatusCode, "a malformed body is not a cancellation to acknowledge: %s", raw)
}
