package telemetry

import (
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestAdvanceUpgradeFunnelConfigRace pins the second half of the schema-v9
// config-race fix. buildHeartbeat was made snapshot-safe, but
// advanceUpgradeFunnel still mutated cfg.Telemetry.LastReportedVersion OUTSIDE
// s.mu while buildHeartbeat read that very field unlocked. Both run
// concurrently in production: the heartbeat loop advances the cursor
// immediately after a successful send, and BuildPayload is exported and served
// from the REST handler behind `mcpproxy telemetry show-payload`.
//
// Run under -race: before the fix this reports
// "DATA RACE ... advanceUpgradeFunnel ... buildHeartbeat" on telemetry.go.
func TestAdvanceUpgradeFunnelConfigRace(t *testing.T) {
	newCfg := func() *config.Config {
		return &config.Config{
			DataDir:   t.TempDir(),
			Servers:   []*config.ServerConfig{{Name: "a", Protocol: "stdio"}},
			Telemetry: &config.TelemetryConfig{AnonymousID: "anon-funnel-race"},
		}
	}

	s := &Service{
		logger:  zap.NewNop(),
		version: "1.2.3",
		config:  newCfg(),
	}
	s.resolvedEnabled = true

	const iterations = 300
	var wg sync.WaitGroup
	wg.Add(3)

	// The daemon reloading live config, so the cursor keeps needing an advance.
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			s.NotifyConfigChanged(newCfg())
		}
	}()

	// The heartbeat loop advancing the funnel cursor after a 2xx send.
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			s.advanceUpgradeFunnel()
		}
	}()

	// The REST handler rendering the payload concurrently.
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = s.BuildPayload()
		}
	}()

	wg.Wait()

	// The cursor must actually land on the current version, not merely avoid a
	// race: a final advance against the settled live config has to stick.
	s.advanceUpgradeFunnel()
	cfg := s.liveConfig()
	if cfg.Telemetry == nil || cfg.Telemetry.LastReportedVersion != s.version {
		t.Errorf("last_reported_version = %+v, want %q", cfg.Telemetry, s.version)
	}
}
