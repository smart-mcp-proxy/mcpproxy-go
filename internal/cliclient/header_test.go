package cliclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func TestCLIClientSetsXMCPProxyClientHeader(t *testing.T) {
	SetClientVersion("v9.9.9")
	defer SetClientVersion("dev")

	var captured string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get("X-MCPProxy-Client")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := NewClient(server.URL, zap.NewNop().Sugar())
	resp, err := c.DoRaw(context.Background(), http.MethodGet, "/api/v1/status", nil)
	if err != nil {
		t.Fatalf("DoRaw error: %v", err)
	}
	resp.Body.Close()

	want := "cli/v9.9.9"
	if captured != want {
		t.Errorf("X-MCPProxy-Client = %q, want %q", captured, want)
	}
}
