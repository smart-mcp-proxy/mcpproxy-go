package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
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
//   - an ACCEPTED periodic tick leaves the flush nothing to send, and an
//     unconfirmed one is joined and re-sent (delivery is at-least-once).

// heartbeatRecorder is a test endpoint that captures every usage heartbeat.
//
// It must distinguish heartbeats from opt-out beacons: the beacon deliberately
// reuses the SAME /heartbeat ingest path (MCP-2482) and is told apart only by
// its `event` field. Counting it as a heartbeat would make the opt-out test
// assert on whichever of two goroutines happened to win.
type heartbeatRecorder struct {
	mu       sync.Mutex
	payloads []HeartbeatPayload
	beacons  []OptOutBeacon
	status   int // response status; 0 means 200
}

func (h *heartbeatRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	var beacon OptOutBeacon
	isBeacon := json.Unmarshal(body, &beacon) == nil && beacon.Event == OptOutEvent

	var p HeartbeatPayload
	if !isBeacon {
		_ = json.Unmarshal(body, &p)
	}

	h.mu.Lock()
	if isBeacon {
		h.beacons = append(h.beacons, beacon)
	} else {
		h.payloads = append(h.payloads, p)
	}
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

func (h *heartbeatRecorder) beaconCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.beacons)
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

	// NotifyConfigChanged fires the opt-out beacon from its own goroutine, and
	// that beacon POSTs to the SAME /heartbeat ingest path (MCP-2482) — so
	// asserting straight after Stop would race it. Wait for the beacon to land
	// first; only then is "no usage heartbeat arrived" a settled fact rather
	// than a statement about which goroutine was faster.
	deadline := time.Now().Add(5 * time.Second)
	for rec.beaconCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("opt-out beacon never reached the endpoint")
		}
		time.Sleep(time.Millisecond)
	}

	// The beacon carries ONLY the anonymous id + event marker. No usage payload
	// may ever follow an opt-out, shutdown flush included.
	if got := rec.count(); got != 0 {
		t.Fatalf("usage heartbeats sent after opt-out: got %d, want 0", got)
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

// TestFlushOnStopSkippedAfterAnAcceptedTick: once a periodic tick has been
// ACCEPTED (2xx ⇒ Reset), the shutdown flush must find nothing left to report
// and send nothing. This is the no-double-send guarantee the flush actually
// makes — it is a property of the counter contract, not of send timing.
func TestFlushOnStopSkippedAfterAnAcceptedTick(t *testing.T) {
	clearTelemetryEnv(t)

	rec := &heartbeatRecorder{}
	server := httptest.NewServer(rec)
	defer server.Close()

	svc := newFlushTestService(t, server.URL)
	svc.initialDelay = 5 * time.Millisecond // the first heartbeat fires promptly

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Registry().RecordSurface(SurfaceMCP)
	svc.Registry().RecordSurface(SurfaceMCP)
	go svc.Start(ctx)

	// Wait for the tick AND its post-2xx reset — that reset is what makes the
	// rest of this test deterministic.
	deadline := time.Now().Add(5 * time.Second)
	for rec.count() == 0 || svc.Registry().HasPendingCounters() {
		if time.Now().After(deadline) {
			t.Fatal("first heartbeat never arrived (or never reset the registry)")
		}
		time.Sleep(time.Millisecond)
	}

	cancel()
	svc.Stop()

	if got := rec.count(); got != 1 {
		t.Fatalf("heartbeats received: got %d, want 1 (the flush must skip after an accepted tick)", got)
	}
	if got := rec.mcpTotal(); got != 2 {
		t.Fatalf("surface_requests.mcp transmitted: got %d, want 2", got)
	}
}

// TestFlushOnStopJoinsAndResendsAnAbortedTick pins the two things that DO hold
// when Stop lands on a send that is still in flight:
//
//   - Stop joins it (the loop is never left running behind shutdown), and
//   - delivery is AT-LEAST-ONCE. The aborted send never observed a 2xx, so the
//     counters stay pending and the flush re-sends them. If the endpoint had in
//     fact accepted that aborted request, it would see the window twice — that
//     is undecidable client-side and is the deliberate trade against the
//     alternative, which is losing the window entirely (the bug the flush
//     exists to fix). Receivers dedup per (anonymous_id, timestamp).
//
// The blocked first request records NOTHING, standing in for exactly that case:
// a request the client could not confirm.
func TestFlushOnStopJoinsAndResendsAnAbortedTick(t *testing.T) {
	clearTelemetryEnv(t)

	rec := &heartbeatRecorder{}
	inFlight := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()

	var seen atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen.Add(1) == 1 {
			// The tick's send: hold it open until the test releases it, and
			// never record it — its outcome is unobservable to the client.
			close(inFlight)
			<-release
			w.WriteHeader(http.StatusOK)
			return
		}
		rec.ServeHTTP(w, r)
	}))
	defer server.Close()

	svc := newFlushTestService(t, server.URL)
	svc.initialDelay = 5 * time.Millisecond

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

	select {
	case <-stopReturned:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return while a tick was in flight")
	}
	unblock()

	if got := rec.count(); got != 1 {
		t.Fatalf("heartbeats recorded: got %d, want 1 (the flush re-sending the unconfirmed window)", got)
	}
	if got := rec.mcpTotal(); got != 2 {
		t.Fatalf("surface_requests.mcp re-sent by the flush: got %d, want 2", got)
	}
	if svc.Registry().HasPendingCounters() {
		t.Fatal("counters were not reset after the accepted shutdown flush")
	}
}

// TestOptOutBeaconReadsAnonymousIDUnderLock covers the second half of the
// s.endpoint drive-by. NotifyConfigChanged replaces s.config under s.mu, and
// GetAnonymousID dereferences cfg.Telemetry — a pointer that
// ensureAnonymousIDOnce / advanceUpgradeFunnelOnce also install under s.mu. The
// opt-out beacon read both unlocked, two lines above the now-locked endpoint
// read. Fails under -race without liveAnonymousID().
func TestOptOutBeaconReadsAnonymousIDUnderLock(t *testing.T) {
	clearTelemetryEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	svc := newFlushTestService(t, server.URL)

	// Every config stays ENABLED, so no enabled->disabled transition fires and
	// this test drives the reader/writer pair directly rather than through the
	// beacon's own goroutine.
	stop := make(chan struct{})
	var reloads sync.WaitGroup
	reloads.Add(1)
	go func() {
		defer reloads.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			svc.NotifyConfigChanged(&config.Config{
				Telemetry: &config.TelemetryConfig{
					AnonymousID: fmt.Sprintf("test-uuid-%d", i),
					Endpoint:    server.URL,
				},
				RoutingMode: "retrieve_tools",
			})
		}
	}()

	ctx := context.Background()
	for i := 0; i < 100; i++ {
		if err := svc.SendOptOutBeacon(ctx); err != nil {
			t.Fatalf("SendOptOutBeacon: %v", err)
		}
		if !svc.EmitOptOutBeacon(ctx) {
			t.Fatal("EmitOptOutBeacon reported no send attempt despite a live anonymous id")
		}
	}

	close(stop)
	reloads.Wait()
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
