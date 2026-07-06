package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

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
