package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"go.uber.org/zap"
)

// TestHeartbeatIDStaysStableUntilAccepted pins the receiver's idempotency
// contract: rebuilding an unaccepted counter window may change timestamp, but
// not heartbeat_id. The id rotates only after a 2xx resets those counters.
func TestHeartbeatIDStaysStableUntilAccepted(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")

	var mu sync.Mutex
	var payloads []HeartbeatPayload
	status := http.StatusInternalServerError
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload HeartbeatPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		mu.Lock()
		payloads = append(payloads, payload)
		responseStatus := status
		mu.Unlock()
		w.WriteHeader(responseStatus)
	}))
	defer server.Close()

	svc := New(&config.Config{
		Telemetry: &config.TelemetryConfig{
			AnonymousID: "550e8400-e29b-41d4-a716-446655440000",
			Endpoint:    server.URL,
		},
		RoutingMode: "retrieve_tools",
	}, "", "v1.0.0", "personal", zap.NewNop())
	svc.SetRuntimeStats(&mockRuntimeStats{})
	svc.Registry().RecordSurface(SurfaceMCP)

	svc.sendHeartbeat(context.Background()) // 500: retain counters + id
	// This event happens after the first window was detached. It must not leak
	// into the retry or be erased when that retry is accepted.
	svc.Registry().RecordSurface(SurfaceCLI)
	// A live config replacement may rotate the anonymous install ID. The
	// counter-window ID must remain stable so the receiver's globally unique
	// heartbeat_id still deduplicates an accepted-but-response-lost retry.
	svc.NotifyConfigChanged(&config.Config{
		Telemetry: &config.TelemetryConfig{
			AnonymousID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee",
			Endpoint:    server.URL,
		},
		RoutingMode: "retrieve_tools",
	})
	mu.Lock()
	status = http.StatusNoContent
	mu.Unlock()
	svc.sendHeartbeat(context.Background()) // 204: reset counters + rotate id
	svc.Registry().RecordSurface(SurfaceMCP)
	svc.sendHeartbeat(context.Background()) // next window

	mu.Lock()
	got := append([]HeartbeatPayload(nil), payloads...)
	mu.Unlock()
	if len(got) != 3 {
		t.Fatalf("received %d heartbeats, want 3", len(got))
	}
	if _, err := uuid.Parse(got[0].HeartbeatID); err != nil {
		t.Fatalf("first heartbeat_id is not a UUID: %q: %v", got[0].HeartbeatID, err)
	}
	if got[1].HeartbeatID != got[0].HeartbeatID {
		t.Fatalf("retry heartbeat_id changed: %q != %q", got[1].HeartbeatID, got[0].HeartbeatID)
	}
	if got[1].AnonymousID == got[0].AnonymousID {
		t.Fatalf("test did not exercise anonymous-id rotation: retry id = %q", got[1].AnonymousID)
	}
	if got[2].HeartbeatID == got[1].HeartbeatID {
		t.Fatalf("accepted counter window did not rotate heartbeat_id %q", got[2].HeartbeatID)
	}
	for i, payload := range got {
		if payload.SurfaceRequests[SurfaceMCP.String()] != 1 {
			t.Fatalf("payload %d surface_requests.mcp = %d, want 1", i, payload.SurfaceRequests[SurfaceMCP.String()])
		}
	}
	if got[0].SurfaceRequests[SurfaceCLI.String()] != 0 || got[1].SurfaceRequests[SurfaceCLI.String()] != 0 {
		t.Fatalf("event recorded after drain leaked into retry window: first=%d retry=%d",
			got[0].SurfaceRequests[SurfaceCLI.String()], got[1].SurfaceRequests[SurfaceCLI.String()])
	}
	if got[2].SurfaceRequests[SurfaceCLI.String()] != 1 {
		t.Fatalf("event recorded during retry was lost: next cli=%d, want 1", got[2].SurfaceRequests[SurfaceCLI.String()])
	}
}
