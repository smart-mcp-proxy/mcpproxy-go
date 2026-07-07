package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"go.etcd.io/bbolt"
	berrors "go.etcd.io/bbolt/errors"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// newPreChurnTestConfig builds a minimal config rooted at dataDir. A fresh
// struct per instance mirrors real startups (config is re-read each run).
func newPreChurnTestConfig(dataDir string) *config.Config {
	return &config.Config{
		DataDir:           dataDir,
		Listen:            "127.0.0.1:0",
		ToolResponseLimit: 0,
		Servers:           []*config.ServerConfig{},
	}
}

// previousShutdownVia boots a Runtime on dataDir, reads the heartbeat's
// previous_shutdown through the real telemetry wiring (SetTelemetry +
// BuildPayload), and returns the runtime for the caller to end as it sees fit.
func previousShutdownVia(t *testing.T, dataDir string) (*Runtime, string) {
	t.Helper()
	cfg := newPreChurnTestConfig(dataDir)
	rt, err := New(cfg, filepath.Join(dataDir, "config.json"), zap.NewNop())
	if err != nil {
		t.Fatalf("runtime.New: %v", err)
	}
	rt.SetTelemetry("v0.0.0-test", "personal")
	payload := rt.TelemetryService().BuildPayload()
	return rt, payload.PreviousShutdown
}

// TestRuntimePreviousShutdownLifecycle drives the full FR-010/FR-011/FR-013
// sequence through the real runtime wiring: first run → unknown/absent,
// graceful Close → clean, storage death without the shutdown path → crash,
// and recovery back to clean.
func TestRuntimePreviousShutdownLifecycle(t *testing.T) {
	t.Setenv("MCPPROXY_LAUNCHED_BY", "")
	dataDir := t.TempDir()

	// Instance 1: first-ever run — never reported as a crash (FR-013).
	rt1, prev1 := previousShutdownVia(t, dataDir)
	if prev1 != "" {
		t.Fatalf("first run: expected previous_shutdown absent, got %q", prev1)
	}
	if err := rt1.Close(); err != nil {
		t.Fatalf("close 1: %v", err)
	}

	// Instance 2: prior instance closed gracefully.
	rt2, prev2 := previousShutdownVia(t, dataDir)
	if prev2 != "clean" {
		t.Fatalf("after graceful close: expected clean, got %q", prev2)
	}

	// Simulate a crash: the storage layer dies without the graceful path
	// running first. The later rt2.Close() then resolves against a closed DB
	// — which must fail harmlessly WITHOUT rewriting the armed marker.
	if err := rt2.StorageManager().Close(); err != nil {
		t.Fatalf("simulated crash close: %v", err)
	}
	_ = rt2.Close() // releases index/lock; marker stays armed (DB already closed)

	// Instance 3: sees the unresolved marker as a crash.
	rt3, prev3 := previousShutdownVia(t, dataDir)
	if prev3 != "crash" {
		t.Fatalf("after simulated crash: expected crash, got %q", prev3)
	}

	// FR-011: the value is stable across heartbeats of the same instance.
	if again := rt3.TelemetryService().BuildPayload().PreviousShutdown; again != "crash" {
		t.Fatalf("previous_shutdown drifted within instance: %q", again)
	}
	if err := rt3.Close(); err != nil {
		t.Fatalf("close 3: %v", err)
	}

	// Instance 4: back to clean.
	rt4, prev4 := previousShutdownVia(t, dataDir)
	if prev4 != "clean" {
		t.Fatalf("after recovery close: expected clean, got %q", prev4)
	}
	if err := rt4.Close(); err != nil {
		t.Fatalf("close 4: %v", err)
	}
}

// TestRuntimeCloseCleanupBranchStillResolvesAndClosesStorage guards the Close
// restructure behind FR-010: the Docker container-cleanup verification used to
// contain `return nil` branches that exited Close BEFORE resolving the
// shutdown marker and closing cache/index/storage/configSvc. Those exits now
// live inside verifyContainerCleanup and merely return to Close, so even when
// the cleanup-verification branch bails out early, Close still (a) resolves
// the marker — the next instance reads previous_shutdown="clean" — and
// (b) actually closes the storage DB.
func TestRuntimeCloseCleanupBranchStillResolvesAndClosesStorage(t *testing.T) {
	t.Setenv("MCPPROXY_LAUNCHED_BY", "")
	dataDir := t.TempDir()

	rt, _ := previousShutdownVia(t, dataDir)
	db := rt.StorageManager().GetDB()
	if db == nil {
		t.Fatal("precondition: storage DB must be open")
	}

	// Drive the former early-return branch directly: an already-canceled
	// context hits the "Cleanup verification timeout" exit immediately
	// (ticker can't have fired yet). It must return to the caller without
	// touching the marker or the DB.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rt.verifyContainerCleanup(ctx)

	if err := rt.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// (b) Storage really closed — the old early returns leaked it open.
	if err := db.View(func(*bbolt.Tx) error { return nil }); !errors.Is(err, berrors.ErrDatabaseNotOpen) {
		t.Fatalf("expected storage closed after Close, View err = %v", err)
	}

	// (a) Marker resolved: the next instance reports a clean shutdown.
	rt2, prev := previousShutdownVia(t, dataDir)
	if prev != "clean" {
		t.Fatalf("after Close through the cleanup branch: expected clean, got %q", prev)
	}
	if err := rt2.Close(); err != nil {
		t.Fatalf("close 2: %v", err)
	}
}

// TestRuntimeCloseWaitsForActivityWritersBeforeMarkerResolve guards the Close
// ordering added in review round 4 (Spec 080 FR-010): the ActivityService owns
// BBolt writers (activity records, usage-snapshot flushes, retention pruning,
// async detection), and before this fix Close only context-cancelled it —
// its final flush-on-shutdown (persistUsage) raced the shutdown-marker resolve
// and the DB close, so the flush could be lost or land AFTER the marker
// claimed "no writes remain". Now Close awaits ActivityService.Stop() before
// StopAsync/ResolveCleanShutdown/db.Close, so the flush must both survive and
// precede the clean marker.
func TestRuntimeCloseWaitsForActivityWritersBeforeMarkerResolve(t *testing.T) {
	t.Setenv("MCPPROXY_LAUNCHED_BY", "")
	dataDir := t.TempDir()

	rt, _ := previousShutdownVia(t, dataDir)

	// Start the activity service exactly as StartBackgroundInitialization does.
	rt.ActivityService().SetEventEmitter(rt)
	go rt.ActivityService().Start(rt.AppContext(), rt)

	// Emit a completed tool call and wait until the event loop has persisted it
	// and folded it into the in-memory usage aggregate (Apply runs only after a
	// successful SaveActivity). Re-emit each attempt: an event published before
	// Start's SubscribeEvents registers is silently dropped, so a single early
	// emit could race the service's startup.
	deadline := time.Now().Add(5 * time.Second)
	for {
		rt.EmitActivityToolCallCompleted(
			"prechurn-srv", "prechurn-tool", "sess-1", "req-1", "mcp",
			"success", "", 7, nil, "ok", false, "", nil, "", "", 0, 0)
		if snap := rt.ActivityService().UsageSnapshot(); snap != nil && len(snap.Tools) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the activity event to reach the usage aggregate")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The default flush cadence (30s) cannot fire within this test, so the ONLY
	// path that persists the aggregate is the flush-on-shutdown — which Close
	// must wait for before resolving the marker and closing the DB.
	if err := rt.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen: the flush landed AND the marker resolved to clean afterwards.
	rt2, prev := previousShutdownVia(t, dataDir)
	defer func() { _ = rt2.Close() }()
	if prev != "clean" {
		t.Fatalf("after graceful close: expected clean, got %q", prev)
	}
	data, err := rt2.StorageManager().LoadUsageSnapshot()
	if err != nil {
		t.Fatalf("load usage snapshot: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("usage snapshot missing: shutdown flush did not land before the DB closed")
	}
	agg, err := decodeUsageAggregate(data)
	if err != nil {
		t.Fatalf("decode usage snapshot: %v", err)
	}
	if _, ok := agg.Tools[toolKey("prechurn-srv", "prechurn-tool")]; !ok {
		t.Fatalf("persisted usage snapshot lacks the recorded tool call; tools = %d", len(agg.Tools))
	}
}

// TestActivityServiceStopSafeWhenNeverStartedAndIdempotent: Close() now calls
// ActivityService.Stop() unconditionally, including on runtimes whose
// background initialization never ran (every other test in this file) and on
// double Close. Stop must return immediately when Start never ran, and be
// idempotent after a normal shutdown.
func TestActivityServiceStopSafeWhenNeverStartedAndIdempotent(t *testing.T) {
	t.Setenv("MCPPROXY_LAUNCHED_BY", "")
	dataDir := t.TempDir()

	rt, _ := previousShutdownVia(t, dataDir)

	// Never started: must not block.
	doneCh := make(chan struct{})
	go func() {
		rt.ActivityService().Stop()
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("ActivityService.Stop blocked although Start never ran")
	}

	// Started, then Close (which stops it), then Stop again: idempotent.
	// (Runtime.Close itself is not re-runnable — cache.Manager.Close panics on
	// a second call, a pre-existing constraint — so only Stop is re-invoked.)
	go rt.ActivityService().Start(rt.AppContext(), rt)
	if err := rt.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	rt.ActivityService().Stop() // second stop must not hang or panic
	rt.ActivityService().Stop() // and a third
}

// TestRuntimeCloseWaitsForTelemetryLoopBeforeMarkerResolve guards the Close
// ordering added in review round 6 (Spec 080 FR-010): the telemetry heartbeat
// loop is a BBolt writer too — v7's buildHeartbeat records funnel activity
// (funnelStore.RecordActivity) — and before this fix Close only
// context-cancelled the goroutine launched by lifecycle.go
// (`go telemetryService.Start(appCtx)`) without joining it, so an in-flight
// tick could write after the marker resolved to clean or against a closed DB.
// Close now calls telemetryService.Stop() before StopAsync/ResolveCleanShutdown/
// db.Close. This test drives the exact production launch shape: Close may beat
// the Start goroutine entirely (the Stop-before-Start race) or catch the loop
// mid-initial-delay; either way the service must be TERMINALLY stopped before
// the marker resolves — proven by a post-Close Start returning immediately
// instead of entering the heartbeat loop (which would park in its 5-minute
// initial delay and could later write BBolt). The in-flight-tick join itself
// is proven deterministically at the service level in
// internal/telemetry/shutdown_test.go (TestServiceStopJoinsInFlightHeartbeatTick).
func TestRuntimeCloseWaitsForTelemetryLoopBeforeMarkerResolve(t *testing.T) {
	t.Setenv("MCPPROXY_LAUNCHED_BY", "")
	// Clear env vars that would disable telemetry (CI sets CI=true): the loop
	// must actually start so Close has something to join.
	t.Setenv("CI", "")
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")
	dataDir := t.TempDir()

	rt, _ := previousShutdownVia(t, dataDir)
	svc := rt.TelemetryService()

	// Launch exactly as StartBackgroundInitialization does.
	go svc.Start(rt.AppContext())

	if err := rt.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Terminal stop: Start after Close must be a no-op that returns
	// immediately. If Close had only cancelled (not joined/terminally
	// stopped), this Start would run the loop and hang in its initial delay.
	startReturned := make(chan struct{})
	go func() {
		svc.Start(context.Background())
		close(startReturned)
	}()
	select {
	case <-startReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("telemetry Start after Close did not no-op; the loop could write BBolt after the shutdown marker resolved")
	}

	// Stop stays idempotent after Close (double-Close path).
	svc.Stop()
	svc.Stop()

	// The marker resolved to clean with the telemetry loop joined first.
	rt2, prev := previousShutdownVia(t, dataDir)
	defer func() { _ = rt2.Close() }()
	if prev != "clean" {
		t.Fatalf("after graceful close with telemetry running: expected clean, got %q", prev)
	}
}

// TestRuntimeCloseAfterExternalStopAsyncStillResolvesClean guards the split
// Close sequence (Spec 080 FR-010, review round 3): Close now runs
// storage.StopAsync (stop + drain queued async DB ops) BEFORE resolving the
// shutdown marker, then closes the DB — so the marker is truly the last DB
// write. Driving StopAsync externally first exercises the double-stop path
// (StopAsync inside Close, then again inside storageManager.Close): it must
// not panic, and the marker must still resolve to clean.
func TestRuntimeCloseAfterExternalStopAsyncStillResolvesClean(t *testing.T) {
	t.Setenv("MCPPROXY_LAUNCHED_BY", "")
	dataDir := t.TempDir()

	rt, _ := previousShutdownVia(t, dataDir)
	db := rt.StorageManager().GetDB()
	if db == nil {
		t.Fatal("precondition: storage DB must be open")
	}

	// Worst-case double stop: the async manager is already stopped and
	// drained before Close runs the same sequence again.
	rt.StorageManager().StopAsync()
	if err := rt.Close(); err != nil {
		t.Fatalf("close after external StopAsync: %v", err)
	}

	// Storage fully closed…
	if err := db.View(func(*bbolt.Tx) error { return nil }); !errors.Is(err, berrors.ErrDatabaseNotOpen) {
		t.Fatalf("expected storage closed after Close, View err = %v", err)
	}

	// …and the marker resolved: the next instance reports a clean shutdown.
	rt2, prev := previousShutdownVia(t, dataDir)
	if prev != "clean" {
		t.Fatalf("after Close with external StopAsync: expected clean, got %q", prev)
	}
	if err := rt2.Close(); err != nil {
		t.Fatalf("close 2: %v", err)
	}
}
