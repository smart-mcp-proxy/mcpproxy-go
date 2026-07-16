package cliclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newKeyGatedServer returns an httptest server that responds 401 unless the
// request carries X-API-Key: <key>. On success it returns the given body.
func newKeyGatedServer(t *testing.T, key, body string, sawClientHeader *bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sawClientHeader != nil && r.Header.Get("X-MCPProxy-Client") != "" {
			*sawClientHeader = true
		}
		if r.Header.Get("X-API-Key") != key {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"success":false,"error":"unauthorized"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

// TestPingSendsAPIKeyOverTCP verifies that a client constructed with an API
// key authenticates Ping against a TCP endpoint. /api/v1/status sits behind
// apiKeyAuthMiddleware, so without this the CLI's TCP fallback can never
// detect a running daemon (v0.51.0-rc.1 QA finding CLI-SOCKET).
func TestPingSendsAPIKeyOverTCP(t *testing.T) {
	ts := newKeyGatedServer(t, "secret", `{"success":true,"data":{}}`, nil)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	withKey := NewClientWithAPIKey(ts.URL, "secret", nil)
	if err := withKey.Ping(ctx); err != nil {
		t.Fatalf("Ping with API key should succeed, got: %v", err)
	}

	withoutKey := NewClient(ts.URL, nil)
	if err := withoutKey.Ping(ctx); err == nil {
		t.Fatal("Ping without API key should fail against a key-gated endpoint")
	}
}

// TestTransportInjectsAPIKeyOnAllRequests verifies the API key is injected at
// the transport level, covering methods that do not call prepareRequest
// (e.g. GetServers). Also guards the Spec 042 X-MCPProxy-Client header.
func TestTransportInjectsAPIKeyOnAllRequests(t *testing.T) {
	sawClientHeader := false
	ts := newKeyGatedServer(t, "secret", `{"success":true,"data":{"servers":[]}}`, &sawClientHeader)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := NewClientWithAPIKey(ts.URL, "secret", nil)
	servers, err := client.GetServers(ctx)
	if err != nil {
		t.Fatalf("GetServers with API key should succeed, got: %v", err)
	}
	if len(servers) != 0 {
		t.Fatalf("expected empty server list, got %d entries", len(servers))
	}
	if !sawClientHeader {
		t.Fatal("X-MCPProxy-Client header must still be sent (Spec 042 regression)")
	}
}
