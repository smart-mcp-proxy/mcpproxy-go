package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Graceful-shutdown flush: telemetry counters live in memory and are reset only
// after an ACCEPTED (2xx) send, so before this everything recorded after the
// 5-minute first heartbeat was dropped unless the process survived to the 24h
// tick. Stop now performs ONE bounded final send after joining the loop.
//
// The invariants these tests pin down:
//   - flush sends exactly once and resets the counters (2xx),
//   - a dead endpoint cannot hang shutdown, and its counters are RETAINED,
//   - opted-out / disabled installs send nothing at all,
//   - a periodic tick racing Stop never causes the same counts to be sent twice.

// heartbeatRecorder is a test endpoint that captures every heartbeat payload.
type heartbeatRecorder struct {
	mu       sync.Mutex
	payloads []HeartbeatPayload
	status   int // response status; 0 means 200
}

func (h *heartbeatRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var p HeartbeatPayload
	_ = json.Unmarshal(body, &p)

	h.mu.Lock()
	h.payloads = append(h.payloads, p)
	status := h.status
	h.mu.Unlock()

	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
}

func (h *heartbeatRecorder) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.payloads)
}

// mcpTotal sums surface_requests.mcp across every received payload. Because
// counters reset only on an accepted send, this total must equal the number of
// MCP events recorded — anything higher proves a double-send.
func (h *heartbeatRecorder) mcpTotal() int64 { return h.surfaceTotal("mcp") }

func (h *heartbeatRecorder) surfaceTotal(key string) int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	var total int64
	for i := range h.payloads {
		total += h.payloads[i].SurfaceRequests[key]
	}
	return total
}

// newFlushTestService builds an enabled service whose heartbeat loop parks in
// the initial delay (no periodic send) unless the caller shortens it, with a
// snappy shutdown-flush timeout.
func newFlushTestService(t *testing.T, endpoint string) *Service {
	t.Helper()
	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{
			AnonymousID: "test-uuid-flush",
			Endpoint:    endpoint,
		},
		RoutingMode: "retrieve_tools",
	}
	svc := New(cfg, "", "v1.0.0", "personal", zap.NewNop())
	svc.initialDelay = time.Hour // no periodic send unless overridden
	svc.heartbeatInterval = time.Hour
	svc.flushTimeout = 2 * time.Second
	svc.SetRuntimeStats(&mockRuntimeStats{})
	return svc
}

// startAndArm launches the heartbeat loop and waits until it has passed every
// emission gate (the point at which the shutdown flush is armed).
func startAndArm(t *testing.T, svc *Service, ctx context.Context) {
	t.Helper()
	go svc.Start(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for !svc.flushEligible.Load() {
		if time.Now().After(deadline) {
			t.Fatal("telemetry Start never armed the shutdown flush")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestFlushOnStopSendsFinalHeartbeatAndResetsCounters: activity recorded before
// any heartbeat went out must be transmitted exactly once on graceful shutdown,
// and the 2xx must reset the counters through the normal send+reset path.
func TestFlushOnStopSendsFinalHeartbeatAndResetsCounters(t *testing.T) {
	clearTelemetryEnv(t)

	rec := &heartbeatRecorder{}
	server := httptest.NewServer(rec)
	defer server.Close()

	svc := newFlushTestService(t, server.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startAndArm(t, svc, ctx)

	// Usage the process would otherwise take to the grave.
	svc.Registry().RecordSurface(SurfaceMCP)
	svc.Registry().RecordUpstreamTool()

	cancel()
	svc.Stop()

	if got := rec.count(); got != 1 {
		t.Fatalf("heartbeats received: got %d, want 1 (the shutdown flush)", got)
	}
	if got := rec.mcpTotal(); got != 1 {
		t.Fatalf("surface_requests.mcp transmitted: got %d, want 1", got)
	}
	if svc.Registry().HasPendingCounters() {
		t.Fatal("counters were not reset after the accepted shutdown flush")
	}

	// Stop is idempotent and the flush is single-shot.
	svc.Stop()
	svc.Stop()
	if got := rec.count(); got != 1 {
		t.Fatalf("repeated Stop re-sent the flush: got %d heartbeats, want 1", got)
	}
}

// TestFlushOnStopTimeoutDoesNotBlockShutdown: a blackholed endpoint must not
// turn shutdown into a hang, and because no 2xx arrived the counters must be
// RETAINED for the next run rather than silently dropped.
func TestFlushOnStopTimeoutDoesNotBlockShutdown(t *testing.T) {
	clearTelemetryEnv(t)

	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()

	var received atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	svc := newFlushTestService(t, server.URL)
	svc.flushTimeout = 100 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startAndArm(t, svc, ctx)
	svc.Registry().RecordSurface(SurfaceMCP)

	cancel()
	stopReturned := make(chan struct{})
	start := time.Now()
	go func() {
		svc.Stop()
		close(stopReturned)
	}()
	select {
	case <-stopReturned:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop hung on an unresponsive telemetry endpoint")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("shutdown flush was not hard-bounded: took %v", elapsed)
	}

	if received.Load() == 0 {
		t.Fatal("shutdown flush never attempted a send")
	}
	if !svc.Registry().HasPendingCounters() {
		t.Fatal("counters were dropped by a flush that was never accepted")
	}

	unblock()
}

// TestFlushOnStopSkippedWhenOptedOut: once the opt-out latch is set, shutdown
// must not become a loophole that ships one last usage payload.
func TestFlushOnStopSkippedWhenOptedOut(t *testing.T) {
	clearTelemetryEnv(t)

	rec := &heartbeatRecorder{}
	server := httptest.NewServer(rec)
	defer server.Close()

	svc := newFlushTestService(t, server.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startAndArm(t, svc, ctx)
	svc.Registry().RecordSurface(SurfaceMCP)

	// The user turns telemetry off mid-run: NotifyConfigChanged latches
	// optedOut and swaps in the disabled config.
	disabled := false
	svc.NotifyConfigChanged(&config.Config{
		Telemetry: &config.TelemetryConfig{
			AnonymousID: "test-uuid-flush",
			Endpoint:    server.URL,
			Enabled:     &disabled,
		},
		RoutingMode: "retrieve_tools",
	})

	cancel()
	svc.Stop()

	// Only the opt-out beacon may ever reach the endpoint, and it goes to
	// /opt-out — never /heartbeat.
	if got := rec.count(); got != 0 {
		t.Fatalf("heartbeats sent after opt-out: got %d, want 0", got)
	}
}

// TestFlushOnStopSkippedWhenTelemetryDisabled: a service whose loop never
// started (telemetry disabled by config) has no armed flush, so Stop sends
// nothing even though counters were recorded.
func TestFlushOnStopSkippedWhenTelemetryDisabled(t *testing.T) {
	clearTelemetryEnv(t)

	rec := &heartbeatRecorder{}
	server := httptest.NewServer(rec)
	defer server.Close()

	disabled := false
	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{
			AnonymousID: "test-uuid-flush",
			Endpoint:    server.URL,
			Enabled:     &disabled,
		},
		RoutingMode: "retrieve_tools",
	}
	svc := New(cfg, "", "v1.0.0", "personal", zap.NewNop())
	svc.flushTimeout = time.Second
	svc.SetRuntimeStats(&mockRuntimeStats{})
	svc.Registry().RecordSurface(SurfaceMCP)

	svc.Start(context.Background()) // returns immediately: disabled by config
	svc.Stop()

	if got := rec.count(); got != 0 {
		t.Fatalf("heartbeats sent by a disabled install: got %d, want 0", got)
	}
	if svc.flushEligible.Load() {
		t.Fatal("shutdown flush was armed although telemetry is disabled")
	}
}

// TestFlushOnStopNoDoubleSendWithPeriodicTick: the periodic tick and Stop race.
// The tick's send is held open while Stop is invoked, so Stop joins a send that
// is still in flight. Whatever the interleaving, the counts recorded must be
// transmitted exactly once in total — the accepted tick resets the registry, so
// the flush must find nothing left to report.
func TestFlushOnStopNoDoubleSendWithPeriodicTick(t *testing.T) {
	clearTelemetryEnv(t)

	rec := &heartbeatRecorder{}
	inFlight := make(chan struct{})
	release := make(chan struct{})
	var seen atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen.Add(1) == 1 {
			close(inFlight)
			<-release
		}
		rec.ServeHTTP(w, r)
	}))
	defer server.Close()

	svc := newFlushTestService(t, server.URL)
	svc.initialDelay = 5 * time.Millisecond // the first heartbeat fires promptly

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Registry().RecordSurface(SurfaceMCP)
	svc.Registry().RecordSurface(SurfaceMCP)
	go svc.Start(ctx)

	select {
	case <-inFlight:
	case <-time.After(5 * time.Second):
		t.Fatal("first heartbeat never reached the endpoint")
	}

	// Stop while that send is still in flight (Runtime.Close cancels first).
	stopReturned := make(chan struct{})
	go func() {
		cancel()
		svc.Stop()
		close(stopReturned)
	}()

	time.Sleep(20 * time.Millisecond)
	close(release)

	select {
	case <-stopReturned:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return")
	}

	if got := rec.mcpTotal(); got != 2 {
		t.Fatalf("surface_requests.mcp transmitted across all heartbeats: got %d, want 2 (no double-send)", got)
	}
	if svc.Registry().HasPendingCounters() {
		t.Fatal("counters survived an accepted send")
	}
}

// TestFlushOnStopCapturesActivityAfterFirstHeartbeat is the regression the
// whole change exists for: work done AFTER the 5-minute first heartbeat used to
// be lost unless the process lived to the 24h tick.
func TestFlushOnStopCapturesActivityAfterFirstHeartbeat(t *testing.T) {
	clearTelemetryEnv(t)

	rec := &heartbeatRecorder{}
	server := httptest.NewServer(rec)
	defer server.Close()

	svc := newFlushTestService(t, server.URL)
	svc.initialDelay = 5 * time.Millisecond

	// A marker recorded BEFORE the first heartbeat: once it has been reported
	// and the registry reset, the initial send is provably complete, so the
	// activity recorded below cannot be swallowed by that send's Reset.
	svc.Registry().RecordSurface(SurfaceCLI)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Start(ctx)

	// Wait for the first (initial-delay) heartbeat AND its post-2xx reset.
	deadline := time.Now().Add(5 * time.Second)
	for rec.count() == 0 || svc.Registry().HasPendingCounters() {
		if time.Now().After(deadline) {
			t.Fatal("first heartbeat never arrived (or never reset the registry)")
		}
		time.Sleep(time.Millisecond)
	}

	// Post-heartbeat usage — the data that used to be dropped.
	svc.Registry().RecordSurface(SurfaceMCP)
	svc.Registry().RecordSurface(SurfaceMCP)
	svc.Registry().RecordSurface(SurfaceMCP)

	cancel()
	svc.Stop()

	if got := rec.count(); got != 2 {
		t.Fatalf("heartbeats received: got %d, want 2 (initial + shutdown flush)", got)
	}
	if got := rec.mcpTotal(); got != 3 {
		t.Fatalf("surface_requests.mcp transmitted: got %d, want 3", got)
	}
	if got := rec.surfaceTotal("cli"); got != 1 {
		t.Fatalf("surface_requests.cli transmitted: got %d, want 1 (no double-count)", got)
	}
	if svc.Registry().HasPendingCounters() {
		t.Fatal("counters were not reset after the accepted shutdown flush")
	}
}
