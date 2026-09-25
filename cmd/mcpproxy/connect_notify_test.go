package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// newOnboardingMarkServer returns an httptest server that answers the daemon
// probe (GET /api/v1/status) and records every POST /api/v1/onboarding/mark
// body it receives, keyed by connected_client_id, guarded by a mutex since
// notifyClientConnected races the test's own assertions.
func newOnboardingMarkServer(t *testing.T, key string) (srv *httptest.Server, marks func() []string) {
	t.Helper()
	var mu sync.Mutex
	var received []string

	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != key {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/status":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"data":{"running":true}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/onboarding/mark":
			body, _ := io.ReadAll(r.Body)
			var req struct {
				ConnectedClientID string `json:"connected_client_id"`
			}
			_ = json.Unmarshal(body, &req)
			mu.Lock()
			received = append(received, req.ConnectedClientID)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	marks = func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(received))
		copy(out, received)
		return out
	}
	return srv, marks
}

func TestNotifyClientConnected_RelaysToReachableDaemon(t *testing.T) {
	clearDaemonEnv(t)

	srv, marks := newOnboardingMarkServer(t, "secret")
	defer srv.Close()

	listen := strings.TrimPrefix(srv.URL, "http://")
	cfg := &config.Config{
		DataDir: t.TempDir(), // no socket file here, forces TCP fallback
		Listen:  listen,
		APIKey:  "secret",
	}

	notifyClientConnected(cfg, "claude-code")

	got := marks()
	if len(got) != 1 || got[0] != "claude-code" {
		t.Fatalf("expected one relay for claude-code, got %v", got)
	}
}

func TestNotifyClientConnected_NoDaemonIsSilentNoOp(t *testing.T) {
	clearDaemonEnv(t)

	cfg := &config.Config{
		DataDir: t.TempDir(),   // no socket, no reachable TCP endpoint
		Listen:  "127.0.0.1:1", // reserved, always connection-refused
		APIKey:  "secret",
	}

	// Must not panic, hang, or otherwise surface an error — this is a
	// best-effort void function by design (review round 6).
	notifyClientConnected(cfg, "cursor")
}

func TestNotifyClientConnected_WrongAPIKeyIsSilentNoOp(t *testing.T) {
	clearDaemonEnv(t)

	srv, marks := newOnboardingMarkServer(t, "secret")
	defer srv.Close()

	listen := strings.TrimPrefix(srv.URL, "http://")
	cfg := &config.Config{
		DataDir: t.TempDir(),
		Listen:  listen,
		APIKey:  "wrong-key",
	}

	notifyClientConnected(cfg, "vscode")

	if got := marks(); len(got) != 0 {
		t.Fatalf("expected no relay with a mismatched API key, got %v", got)
	}
}
