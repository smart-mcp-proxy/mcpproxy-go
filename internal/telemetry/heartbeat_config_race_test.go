package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

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

// TestPersistConfigSkipsSwappedSnapshot pins the SECOND cross-model review
// finding, which was a regression introduced by the fix for the first one.
//
// The heartbeat path works from a config SNAPSHOT, and persistConfig writes the
// WHOLE config file. So if NotifyConfigChanged installs a newer config while a
// heartbeat is in flight, persisting the snapshot would silently roll the
// user's change back on disk: snapshot A -> user applies B (already written to
// disk by the config-write path) -> the in-flight anonymous-ID rotation saves A
// -> B is gone.
//
// persistConfig now writes only while the passed config is still s.config.
// Skipping is safe because the mutation is idempotent: the next heartbeat
// re-evaluates it against the live config.
func TestPersistConfigSkipsSwappedSnapshot(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcp_config.json")

	// configA is what the in-flight heartbeat snapshotted; configB is what the
	// user applied while that heartbeat was still running.
	configA := &config.Config{
		Listen:    "127.0.0.1:8080",
		DataDir:   dir,
		Telemetry: &config.TelemetryConfig{AnonymousID: "id-from-config-a"},
	}
	configB := &config.Config{
		Listen:    "127.0.0.1:9999", // the user's change — must survive
		DataDir:   dir,
		Telemetry: &config.TelemetryConfig{AnonymousID: "id-from-config-b"},
	}

	s := &Service{
		logger:  zap.NewNop(),
		version: "1.0.0",
		cfgPath: cfgPath,
		config:  configA,
	}
	s.resolvedEnabled = true

	// The config-write path persists B, then notifies the telemetry service.
	if err := config.SaveConfig(configB, cfgPath); err != nil {
		t.Fatalf("SaveConfig(configB): %v", err)
	}
	s.NotifyConfigChanged(configB)

	// The in-flight heartbeat now tries to persist its stale snapshot.
	s.persistConfig(configA, "stale snapshot from an in-flight heartbeat")

	readBack := func() *config.Config {
		t.Helper()
		raw, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		var got config.Config
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		return &got
	}

	got := readBack()
	if got.Listen != configB.Listen {
		t.Errorf("stale snapshot clobbered the live config on disk: listen = %q, want %q",
			got.Listen, configB.Listen)
	}
	if got.Telemetry == nil || got.Telemetry.AnonymousID != "id-from-config-b" {
		t.Errorf("stale snapshot clobbered telemetry on disk: %+v, want id-from-config-b", got.Telemetry)
	}

	// Sanity: the guard is a liveness check, not a blanket refusal to write —
	// persisting the CURRENT config still lands on disk.
	configB.Telemetry.LastReportedVersion = "1.0.0"
	s.persistConfig(configB, "live config")
	if got := readBack(); got.Telemetry == nil || got.Telemetry.LastReportedVersion != "1.0.0" {
		t.Errorf("persistConfig refused to write the LIVE config: %+v", got.Telemetry)
	}
}

// TestRotationOnStaleSnapshotDoesNotClobber is the end-to-end shape of the same
// hazard: the rotation runs on the snapshot and calls persistConfig itself, so
// the liveness guard has to hold through maybeRotateAnonymousID too.
func TestRotationOnStaleSnapshotDoesNotClobber(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcp_config.json")

	stale := &config.Config{
		Listen:  "127.0.0.1:8080",
		DataDir: dir,
		Telemetry: &config.TelemetryConfig{
			AnonymousID: "00000000-0000-0000-0000-00000000000a",
			// Older than 365 days → this snapshot WILL rotate and try to persist.
			AnonymousIDCreatedAt: time.Now().UTC().Add(-400 * 24 * time.Hour).Format(time.RFC3339),
		},
	}
	live := &config.Config{
		Listen:    "127.0.0.1:7777",
		DataDir:   dir,
		Telemetry: &config.TelemetryConfig{AnonymousID: "00000000-0000-0000-0000-00000000000b"},
	}

	s := &Service{logger: zap.NewNop(), version: "1.0.0", cfgPath: cfgPath, config: stale}
	s.resolvedEnabled = true

	if err := config.SaveConfig(live, cfgPath); err != nil {
		t.Fatalf("SaveConfig(live): %v", err)
	}
	s.NotifyConfigChanged(live)

	s.maybeRotateAnonymousID(stale, time.Now().UTC())

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var got config.Config
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Listen != live.Listen {
		t.Errorf("rotation on a stale snapshot clobbered the live config: listen = %q, want %q",
			got.Listen, live.Listen)
	}
}
