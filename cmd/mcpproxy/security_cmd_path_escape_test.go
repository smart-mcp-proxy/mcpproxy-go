package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
)

// `mcpproxy security status|report|scan io.github.owner/repo` must address the
// server as one %2F-encoded path segment; an unescaped slash splits it and the
// request lands on another route.
func TestSecurityCmd_EscapesSlashServerName(t *testing.T) {
	const name = "io.github.owner/repo"

	var (
		mu    sync.Mutex
		paths []string
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.EscapedPath())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
	}))
	defer ts.Close()

	client := cliclient.NewClient(ts.URL, zap.NewNop().Sugar())
	ctx := context.Background()

	_ = fetchFailedScannerInfo(client, ctx, name)
	_ = printScanSummary(client, ctx, name)
	_ = printScanDryRunPlan(client, name, "")

	mu.Lock()
	defer mu.Unlock()
	var perServer []string
	for _, p := range paths {
		if strings.HasPrefix(p, "/api/v1/servers/") {
			perServer = append(perServer, p)
		}
	}
	if len(perServer) < 3 {
		t.Fatalf("expected per-server requests, got %v", paths)
	}
	for _, p := range perServer {
		if !strings.HasPrefix(p, "/api/v1/servers/io.github.owner%2Frepo/") {
			t.Errorf("per-server request path %q does not carry the escaped server name", p)
		}
	}
}

func TestServerAPIPath(t *testing.T) {
	if got, want := serverAPIPath("io.github.owner/repo", "/scan"), "/api/v1/servers/io.github.owner%2Frepo/scan"; got != want {
		t.Errorf("serverAPIPath = %q, want %q", got, want)
	}
	if got, want := serverAPIPath("github", "/integrity"), "/api/v1/servers/github/integrity"; got != want {
		t.Errorf("serverAPIPath = %q, want %q", got, want)
	}
}
