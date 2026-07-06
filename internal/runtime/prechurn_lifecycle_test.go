package runtime

import (
	"path/filepath"
	"testing"

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
