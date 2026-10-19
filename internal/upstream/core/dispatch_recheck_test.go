package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// countingCallClient connects to an upstream that answers tools/call at once
// and counts how many arrived.
func countingCallClient(t *testing.T) (*Client, *atomic.Int32, *observer.ObservedLogs) {
	t.Helper()
	disableOAuthForTest(t)
	var calls atomic.Int32
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
				"serverInfo":      map[string]any{"name": "cnt", "version": "1"},
			})
		case "tools/call":
			calls.Add(1)
			writeJSONRPC(w, req.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}})
		default:
			if len(req.ID) == 0 {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			writeJSONRPC(w, req.ID, map[string]any{})
		}
	}))
	t.Cleanup(srv.Close)

	cfg := &config.ServerConfig{Name: "cnt", Protocol: "streamable-http", URL: srv.URL, Enabled: true}
	obsCore, logs := observer.New(zapcore.DebugLevel)
	c, err := NewClient("cnt", cfg, zap.New(obsCore), nil, &config.Config{}, nil, secret.NewResolver())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, c.Connect(ctx))
	t.Cleanup(func() { _ = c.Disconnect() })
	return c, &calls, logs
}

// Spec 117 FR-018: on SSE the call waits for sseRequestMu behind any in-flight
// call. A verdict that flips during that wait (a capture failure marking the
// server review-dirty) refuses the call once it holds the lock, with nothing
// sent upstream.
func TestCallTool_DispatchRecheckAfterSSELockWait(t *testing.T) {
	c, calls, logs := countingCallClient(t)
	// Exercise the SSE serialization path over the test's HTTP transport.
	c.mu.Lock()
	c.transportType = "sse"
	c.mu.Unlock()

	errDirty := errors.New("dirty")
	var dirty atomic.Bool
	ctx := WithDispatchRecheck(context.Background(), func() error {
		if dirty.Load() {
			return errDirty
		}
		return nil
	})

	// A long in-flight SSE call holds the request lock.
	c.sseRequestMu.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := c.CallTool(ctx, "erase", nil)
		done <- err
	}()
	require.Eventually(t, func() bool {
		return logs.FilterMessage("SSE transport detected - serializing CallTool request").Len() == 1
	}, 5*time.Second, 5*time.Millisecond, "the call must be waiting on the SSE request lock")
	time.Sleep(50 * time.Millisecond)

	dirty.Store(true)
	c.sseRequestMu.Unlock()

	select {
	case err := <-done:
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrDispatchRecheckRefused), "got %v", err)
		assert.True(t, errors.Is(err, errDirty), "got %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting call never returned")
	}
	assert.Equal(t, int32(0), calls.Load(), "a refused call is never sent upstream")

	// Control: with the verdict clean the same path dispatches.
	dirty.Store(false)
	_, err := c.CallTool(ctx, "erase", nil)
	require.NoError(t, err)
	assert.Equal(t, int32(1), calls.Load())
}
