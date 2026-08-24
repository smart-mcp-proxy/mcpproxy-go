package telemetry

import (
	"sync"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestPreflightSinkTelemetryPointerRace pins the round-2 cross-model review
// finding. preflightSink snapshotted s.config under s.mu but then called
// EffectiveTelemetryEnabled(cfg) after unlocking, and that path dereferences
// cfg.Telemetry (config.IsTelemetryEnabled). Meanwhile ensureAnonymousIDOnce
// and advanceUpgradeFunnelOnce INSTALL that same pointer under s.mu on a config
// that arrived without a telemetry block — the ordinary fresh-install shape.
//
// A locked write paired with an unlocked read is still a data race, and the two
// sides are the request path (Record*, via preflightSink) and the heartbeat
// loop, which run concurrently by construction.
//
// Run under -race: before the fix this reports
// "DATA RACE ... IsTelemetryEnabled ... preflightSink" against
// "advanceUpgradeFunnelOnce".
func TestPreflightSinkTelemetryPointerRace(t *testing.T) {
	pinTelemetryEnvEnabled(t)

	// Telemetry deliberately nil: this is what a config without a telemetry
	// block looks like, and it is the case that makes the writers install the
	// pointer rather than just mutate fields behind it.
	cfg := &config.Config{DataDir: t.TempDir()}
	svc, _ := newPreflightService(t, cfg)
	svc.resolvedEnabled = true

	const iterations = 500
	var wg sync.WaitGroup
	wg.Add(2)

	// Heartbeat loop installing cfg.Telemetry under s.mu, repeatedly.
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			svc.mu.Lock()
			svc.config.Telemetry = nil
			svc.mu.Unlock()
			svc.advanceUpgradeFunnel()
		}
	}()

	// Request path reading it through preflightSink.
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			svc.RecordDiscoveryOmission()
		}
	}()

	wg.Wait()
}
