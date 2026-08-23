package telemetry

import (
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestBuildHeartbeatConfigSwapRace pins the fix for the cross-model review
// finding on schema v9: buildHeartbeat used to read s.config directly, but
// NotifyConfigChanged REPLACES that pointer under s.mu on every live config
// reload (REST apply, disk watcher). Reading the field outside the mutex is a
// data race, and the schema-v9 trust_mode_distribution builder walks
// cfg.Servers, so it sat squarely on the racing read.
//
// buildHeartbeat now takes ONE snapshot via liveConfig() and reads only that,
// which also guarantees a single payload can never splice fields from two
// different configs. Run under -race: without the snapshot this fails with
// "DATA RACE ... telemetry.(*Service).buildHeartbeat".
func TestBuildHeartbeatConfigSwapRace(t *testing.T) {
	newCfg := func(trust string) *config.Config {
		return &config.Config{
			DataDir: t.TempDir(),
			Servers: []*config.ServerConfig{
				{Name: "a", Protocol: "stdio", TrustMode: trust},
				{Name: "b", Protocol: "http", TrustMode: trust},
			},
			Telemetry: &config.TelemetryConfig{AnonymousID: "anon-race-test"},
		}
	}

	s := &Service{
		logger:  zap.NewNop(),
		version: "0.0.0-test",
		config:  newCfg(string(config.TrustModeScan)),
	}
	s.resolvedEnabled = true

	const iterations = 200
	var wg sync.WaitGroup
	wg.Add(2)

	// Writer: hammer the live-config swap path the daemon uses on reload.
	go func() {
		defer wg.Done()
		modes := []string{
			string(config.TrustModeScan),
			string(config.TrustModeAuto),
			string(config.TrustModeManual),
		}
		for i := 0; i < iterations; i++ {
			s.NotifyConfigChanged(newCfg(modes[i%len(modes)]))
		}
	}()

	// Reader: build heartbeats concurrently.
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			payload := s.buildHeartbeat()
			// Whichever config the snapshot caught, the histogram contract holds:
			// all three fixed keys present, and the two servers land in exactly
			// one tier between them (never split across a mid-build swap).
			dist := payload.TrustModeDistribution
			if len(dist) != len(trustModeKeys) {
				t.Errorf("trust_mode_distribution has %d keys, want %d", len(dist), len(trustModeKeys))
				return
			}
			total := 0
			for k, v := range dist {
				if !IsTrustModeKey(k) {
					t.Errorf("trust_mode_distribution carries non-enum key %q", k)
					return
				}
				total += v
			}
			if total != 2 {
				t.Errorf("trust_mode_distribution totals %d servers, want 2 (a torn read across a config swap)", total)
				return
			}
		}
	}()

	wg.Wait()
}
