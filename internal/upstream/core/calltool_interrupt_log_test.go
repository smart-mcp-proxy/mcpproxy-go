package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// blockingCallClient connects to an upstream whose tools/call never answers
// until the test ends, so every call ends by timeout or cancellation.
func blockingCallClient(t *testing.T, callTimeout time.Duration) (*Client, *observer.ObservedLogs) {
	t.Helper()
	disableOAuthForTest(t)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				ProtocolVersion string `json:"protocolVersion"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		switch req.Method {
		case "initialize":
			writeJSONRPC(w, req.ID, map[string]any{
				"protocolVersion": req.Params.ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "blk", "version": "1"},
			})
		case "tools/call":
			select {
			case <-release:
			case <-r.Context().Done():
			}
		default:
			if len(req.ID) == 0 {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			writeJSONRPC(w, req.ID, map[string]any{})
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })

	cfg := &config.ServerConfig{Name: "blk", Protocol: "streamable-http", URL: srv.URL, Enabled: true}
	gc := &config.Config{CallToolTimeout: config.Duration(callTimeout)}
	obsCore, logs := observer.New(zapcore.DebugLevel)
	c, err := NewClient("blk", cfg, zap.New(obsCore), nil, gc, nil, secret.NewResolver())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, c.Connect(ctx))
	t.Cleanup(func() { _ = c.Disconnect() })
	return c, logs
}

func interruptEntries(logs *observer.ObservedLogs) []observer.LoggedEntry {
	return logs.FilterMessage("Upstream tools/call interrupted").All()
}

func TestCallTool_TimeoutLogsStructuredFields(t *testing.T) {
	c, logs := blockingCallClient(t, 300*time.Millisecond)
	ctx := reqcontext.WithRequestID(WithConnectionGeneration(context.Background(), 7), "req-123")

	_, err := c.CallTool(ctx, "slow", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CallTool 'slow' timed out after 300ms", "error text is frozen")

	entries := interruptEntries(logs)
	require.Len(t, entries, 1)
	assert.Equal(t, zapcore.WarnLevel, entries[0].Level)
	f := entries[0].ContextMap()
	assert.Equal(t, "blk", f["server"])
	assert.Equal(t, "slow", f["tool"])
	assert.Equal(t, int64(7), f["connection_generation"])
	assert.Equal(t, "req-123", f["request_id"])
	assert.Equal(t, "call_timeout", f["cancellation_source"])
	assert.Equal(t, "connected", f["resulting_state"])
	assert.Contains(t, f, "pid")
}

func TestCallTool_CallerCancelLogsStructuredFields(t *testing.T) {
	c, logs := blockingCallClient(t, time.Minute)
	ctx, cancel := context.WithCancel(reqcontext.WithRequestID(context.Background(), "req-9"))
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()

	_, err := c.CallTool(ctx, "slow", nil)
	require.Error(t, err)

	entries := interruptEntries(logs)
	require.Len(t, entries, 1)
	f := entries[0].ContextMap()
	assert.Equal(t, "caller_cancel", f["cancellation_source"])
	assert.Equal(t, "req-9", f["request_id"])
}
