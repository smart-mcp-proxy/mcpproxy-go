package cliclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestClient_CodeExec_ClientSideTimeoutIsExplicit pins that a deadline that
// expires before the daemon answers is reported as a client-side timeout
// rather than as an opaque transport failure, and that the sentinel stays
// inspectable with errors.Is.
func TestClient_CodeExec_ClientSideTimeoutIsExplicit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(time.Second):
		case <-r.Context().Done():
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := client.CodeExec(ctx, "code", map[string]interface{}{}, 60000, 0, nil)
	if err == nil {
		t.Fatal("expected an error when the client deadline expires first")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error %v does not wrap context.DeadlineExceeded", err)
	}
	if !strings.Contains(err.Error(), "client-side timeout") {
		t.Fatalf("error %q does not name the client-side timeout", err.Error())
	}
}

// TestClient_CodeExec_NotCappedBySharedClientTimeout pins that code execution
// is bounded by the caller's context alone: the shared HTTP client's blanket
// timeout would otherwise cut off executions whose --timeout budget is larger.
func TestClient_CodeExec_NotCappedBySharedClientTimeout(t *testing.T) {
	client := NewClient("http://127.0.0.1:1", nil)

	execClient := client.execHTTPClient()
	if execClient.Timeout != 0 {
		t.Fatalf("exec HTTP client Timeout = %s, want 0 so the request context governs", execClient.Timeout)
	}
	if execClient.Transport != client.httpClient.Transport {
		t.Fatal("exec HTTP client must reuse the shared transport (headers, socket dialer)")
	}
	if client.httpClient.Timeout == 0 {
		t.Fatal("the shared client should keep its blanket timeout for other commands")
	}
}

// TestClient_CodeExec_OutlivesSharedClientTimeout drives the real call site: a
// daemon that answers well after the shared client's blanket timeout must still
// produce a result, because CodeExec is expected to dispatch on the uncapped
// exec client. Reverting the request to c.httpClient.Do turns this red.
func TestClient_CodeExec_OutlivesSharedClientTimeout(t *testing.T) {
	const serverDelay = 200 * time.Millisecond

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/code/exec" {
			t.Errorf("unexpected request path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		select {
		case <-time.After(serverDelay):
		case <-r.Context().Done():
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":     true,
			"result": "done",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	// Shorter than the daemon's response time: only a request that escapes the
	// shared client's blanket timeout can succeed here.
	client.httpClient.Timeout = 50 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := client.CodeExec(ctx, "code", map[string]interface{}{}, 600000, 0, nil)
	if err != nil {
		t.Fatalf("CodeExec must be bounded by the context, not the shared client timeout: %v", err)
	}
	if !result.OK {
		t.Fatalf("CodeExec result = %+v, want OK", result)
	}
}
