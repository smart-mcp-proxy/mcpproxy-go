package registries

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Issue #953 follow-up: a registry with zero servers must produce a non-nil
// slice so search_servers serializes "servers": [] — never null — for strict
// MCP clients that iterate the array. With an empty query filterServers
// returns the fetched slice untouched, so the nil has to be stopped at the
// SearchServers boundary.
func TestSearchServers_EmptyRegistryReturnsNonNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"servers": []}`))
	}))
	defer server.Close()

	originalList := registryList
	registryList = []RegistryEntry{
		{
			ID:         "test-empty",
			Name:       "Test Empty Registry",
			ServersURL: server.URL,
			Protocol:   "modelcontextprotocol/registry",
		},
	}
	defer func() { registryList = originalList }()

	servers, err := SearchServers(context.Background(), "test-empty", "", "", 10, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if servers == nil {
		t.Fatal("servers must be a non-nil slice so it serializes as [], not null")
	}
	if len(servers) != 0 {
		t.Fatalf("expected 0 servers, got %d", len(servers))
	}
}
