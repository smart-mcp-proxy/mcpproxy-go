package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestEnvOptOut_SendsNothing is the "no telemetry is sent" proof behind the
// UI claim "Nothing is sent" (Spec 109 FR-044a, user-test F-03). It uses a
// valid semver build, so only the environment gate can stop the send, and
// covers Start, the shutdown flush (Stop) and the opt-out beacon.
func TestEnvOptOut_SendsNothing(t *testing.T) {
	newSvc := func(t *testing.T, endpoint string) *Service {
		t.Helper()
		cfg := &config.Config{
			Telemetry: &config.TelemetryConfig{
				Enabled:     boolPtr(true),
				AnonymousID: "test-id",
				Endpoint:    endpoint,
			},
			RoutingMode: "retrieve_tools",
		}
		svc := New(cfg, "", "v1.2.3", "personal", zap.NewNop())
		svc.initialDelay = 5 * time.Millisecond
		svc.heartbeatInterval = time.Hour
		svc.SetRuntimeStats(&mockRuntimeStats{})
		return svc
	}

	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{"MCPPROXY_TELEMETRY=false", map[string]string{"MCPPROXY_TELEMETRY": "false"}},
		{"MCPPROXY_TELEMETRY=FALSE", map[string]string{"MCPPROXY_TELEMETRY": "FALSE"}},
		{"DO_NOT_TRACK=1", map[string]string{"DO_NOT_TRACK": "1"}},
		{"CI=true", map[string]string{"CI": "true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setCaseEnv(t, tc.env) // before New: the reason is captured there

			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			svc := newSvc(t, srv.URL)
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			svc.Start(ctx) // returns at once on the env gate
			svc.Registry().RecordSurface(SurfaceMCP)
			svc.Stop() // exercises flushFinalHeartbeat
			if svc.EmitOptOutBeacon(context.Background()) {
				t.Error("EmitOptOutBeacon attempted a send under an env opt-out")
			}
			if got := hits.Load(); got != 0 {
				t.Fatalf("telemetry endpoint received %d request(s) under %s, want 0", got, tc.name)
			}
		})
	}

	// Control: with the env clear the same wiring MUST reach the endpoint, so
	// the zero-hit assertions above cannot pass vacuously.
	t.Run("control_no_env_sends", func(t *testing.T) {
		clearTelemetryEnv(t)
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		svc := newSvc(t, srv.URL)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go svc.Start(ctx)
		deadline := time.Now().Add(2 * time.Second)
		for hits.Load() < 1 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		cancel()
		svc.Stop()
		if hits.Load() < 1 {
			t.Fatal("control: telemetry endpoint received nothing with no env opt-out; test wiring is wrong")
		}
	})
}
