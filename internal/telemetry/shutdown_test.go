package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 080 (FR-010, review round 6): the heartbeat loop writes BBolt via
// buildHeartbeat (funnelStore.RecordActivity), so Runtime.Close must be able
// to JOIN the loop — not merely context-cancel it — before the clean-shutdown
// marker resolves and the DB closes. These tests prove the Service-level
// barrier: Stop waits for an in-flight tick, Stop-before-Start makes a late
// Start a no-op, and Stop is idempotent.

// newShutdownTestDB creates a temporary BBolt DB for the funnel-store seam.
func newShutdownTestDB(t *testing.T) *bbolt.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shutdown_test.db")
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("bbolt.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// blockingFunnelStore parks the FIRST RecordActivity call until release is
// closed — a deterministic stand-in for a slow BBolt write inside
// buildHeartbeat, injected through the existing SetFunnelStore seam.
type blockingFunnelStore struct {
	inFlight  chan struct{} // closed when the first RecordActivity is entered
	release   chan struct{} // the first RecordActivity returns after this closes
	completed atomic.Bool   // set once a RecordActivity call has returned
	calls     atomic.Int32
}

func (b *blockingFunnelStore) IncrementWebUIOpened(*bbolt.DB) error { return nil }

func (b *blockingFunnelStore) RecordActivity(*bbolt.DB, time.Time) error {
	if b.calls.Add(1) == 1 {
		close(b.inFlight)
		<-b.release
	}
	b.completed.Store(true)
	return nil
}

func (b *blockingFunnelStore) Snapshot(*bbolt.DB, time.Time) (FunnelState, error) {
	return FunnelState{}, nil
}

// countingFunnelStore just counts RecordActivity calls.
type countingFunnelStore struct {
	calls atomic.Int32
}

func (c *countingFunnelStore) IncrementWebUIOpened(*bbolt.DB) error { return nil }
func (c *countingFunnelStore) RecordActivity(*bbolt.DB, time.Time) error {
	c.calls.Add(1)
	return nil
}
func (c *countingFunnelStore) Snapshot(*bbolt.DB, time.Time) (FunnelState, error) {
	return FunnelState{}, nil
}

// newShutdownTestService builds an enabled Service pointed at the given
// endpoint with a fast initial delay and a one-tick-per-test interval.
func newShutdownTestService(endpoint string) *Service {
	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{
			AnonymousID: "test-uuid-shutdown",
			Endpoint:    endpoint,
		},
		RoutingMode: "retrieve_tools",
	}
	svc := New(cfg, "", "v1.0.0", "personal", zap.NewNop())
	svc.initialDelay = 5 * time.Millisecond
	svc.heartbeatInterval = time.Hour // exactly one tick within any test
	svc.SetRuntimeStats(&mockRuntimeStats{})
	return svc
}

// TestServiceStopJoinsInFlightHeartbeatTick: Stop must block until an
// in-flight heartbeat tick — parked inside its BBolt write — has fully
// completed, even though the loop context is already cancelled. Before the
// fix, Runtime.Close only context-cancelled the loop, so this write could
// land after the shutdown marker resolved (or against a closed DB).
func TestServiceStopJoinsInFlightHeartbeatTick(t *testing.T) {
	clearTelemetryEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	svc := newShutdownTestService(server.URL)
	store := &blockingFunnelStore{
		inFlight: make(chan struct{}),
		release:  make(chan struct{}),
	}
	svc.SetFunnelStore(store, newShutdownTestDB(t))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Start(ctx)

	// Wait until the first tick is INSIDE its funnel BBolt write.
	select {
	case <-store.inFlight:
	case <-time.After(5 * time.Second):
		t.Fatal("heartbeat tick never reached the funnel write")
	}

	// Cancel first (as Runtime.Close's appCancel does), then Stop.
	cancel()
	stopReturned := make(chan struct{})
	go func() {
		svc.Stop()
		close(stopReturned)
	}()

	// Stop must NOT return while the tick's write is still in flight.
	select {
	case <-stopReturned:
		t.Fatal("Stop returned while a heartbeat BBolt write was still in flight")
	case <-time.After(100 * time.Millisecond):
	}

	// Release the write; Stop must now return, and only AFTER the write
	// completed (Stop-returned ⇒ loop exited ⇒ sendHeartbeat ⇒ buildHeartbeat
	// ⇒ RecordActivity returned — the race detector validates the ordering).
	close(store.release)
	select {
	case <-stopReturned:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after the in-flight tick completed")
	}
	if !store.completed.Load() {
		t.Fatal("Stop returned before the in-flight funnel write completed")
	}
}

// TestServiceStopBeforeStartMakesStartNoOp: production launches Start via
// `go` (lifecycle.go), so a fast shutdown can run Stop BEFORE the Start
// goroutine is scheduled. Stop must (a) return immediately and (b) terminally
// stop the service so the late Start neither loops nor writes BBolt.
func TestServiceStopBeforeStartMakesStartNoOp(t *testing.T) {
	clearTelemetryEnv(t)

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	svc := newShutdownTestService(server.URL)
	svc.initialDelay = time.Millisecond // a buggy late Start would tick almost instantly
	store := &countingFunnelStore{}
	svc.SetFunnelStore(store, newShutdownTestDB(t))

	// Stop before Start: must not block (Start never ran → nothing to join).
	stopReturned := make(chan struct{})
	go func() {
		svc.Stop()
		close(stopReturned)
	}()
	select {
	case <-stopReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop blocked although Start never ran")
	}

	// The late Start must be a no-op that returns immediately — otherwise it
	// would sit in the heartbeat loop and write BBolt after the shutdown-
	// marker path already began.
	startReturned := make(chan struct{})
	go func() {
		svc.Start(context.Background())
		close(startReturned)
	}()
	select {
	case <-startReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("Start after Stop did not no-op; the heartbeat loop is running past shutdown")
	}

	if got := store.calls.Load(); got != 0 {
		t.Fatalf("funnel writes after Stop-before-Start: got %d, want 0", got)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("heartbeats sent after Stop-before-Start: got %d, want 0", got)
	}
}

// TestServiceStopIdempotentAndSecondStartRefused: Stop is safe to call
// repeatedly (Runtime.Close on double Close), and the done bookkeeping is
// single-shot — a second Start must be refused instead of re-running the
// loop or double-closing the channel.
func TestServiceStopIdempotentAndSecondStartRefused(t *testing.T) {
	clearTelemetryEnv(t)

	svc := newShutdownTestService("http://127.0.0.1:0") // never contacted
	svc.initialDelay = time.Hour                        // first run parks in the initial delay

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: Start exits during the initial delay
	svc.Start(ctx)

	svc.Stop() // loop already exited: returns immediately
	svc.Stop() // idempotent
	svc.Stop() // and a third

	// A second Start must return immediately (single-shot); if it re-entered
	// the loop it would park in the 1h initial delay and hang here.
	startReturned := make(chan struct{})
	go func() {
		svc.Start(context.Background())
		close(startReturned)
	}()
	select {
	case <-startReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("second Start was not refused")
	}
	svc.Stop() // still safe afterwards
}
