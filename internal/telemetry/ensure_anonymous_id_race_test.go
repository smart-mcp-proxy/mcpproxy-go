package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestEnsureAnonymousIDConfigRace pins the cross-model review finding that
// ensureAnonymousID read s.config — and called config.SaveConfig on it —
// entirely outside s.mu. Start() is launched with `go` from
// runtime/lifecycle.go, so it runs concurrently with the daemon's config
// reload path, which swaps s.config under the mutex.
//
// Run under -race: before the fix this reports
// "DATA RACE ... ensureAnonymousID ... NotifyConfigChanged".
func TestEnsureAnonymousIDConfigRace(t *testing.T) {
	newCfg := func() *config.Config {
		return &config.Config{
			DataDir:   t.TempDir(),
			Servers:   []*config.ServerConfig{{Name: "a", Protocol: "stdio"}},
			Telemetry: &config.TelemetryConfig{},
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
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			s.NotifyConfigChanged(newCfg())
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			s.ensureAnonymousID()
		}
	}()

	wg.Wait()

	// Whatever config won the last swap must end up with an id.
	s.ensureAnonymousID()
	cfg := s.liveConfig()
	if cfg.Telemetry == nil || cfg.Telemetry.AnonymousID == "" {
		t.Fatalf("live config has no anonymous id: %+v", cfg.Telemetry)
	}
	if cfg.Telemetry.AnonymousIDCreatedAt == "" {
		t.Error("anonymous id has no created_at, so rotation can never fire")
	}
}

// TestEnsureAnonymousIDDoesNotSaveStaleConfig pins the second half of that
// finding: the direct config.SaveConfig call wrote the WHOLE config file from
// a pointer the daemon may already have replaced, silently rolling the user's
// just-applied change back on disk. persistConfigLocked's liveness check has
// to gate that write.
func TestEnsureAnonymousIDDoesNotSaveStaleConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcp_config.json")

	stale := &config.Config{
		DataDir:   dir,
		Listen:    "127.0.0.1:1111",
		Telemetry: &config.TelemetryConfig{},
	}
	fresh := &config.Config{
		DataDir:   dir,
		Listen:    "127.0.0.1:2222",
		Telemetry: &config.TelemetryConfig{AnonymousID: "already-set", AnonymousIDCreatedAt: "2026-01-01T00:00:00Z"},
	}
	if err := config.SaveConfig(fresh, cfgPath); err != nil {
		t.Fatalf("seed SaveConfig: %v", err)
	}

	s := &Service{
		logger:  zap.NewNop(),
		version: "1.2.3",
		config:  fresh,
		cfgPath: cfgPath,
	}
	s.resolvedEnabled = true

	// Hand ensureAnonymousIDOnce a snapshot that is no longer live.
	s.mu.Lock()
	s.config = fresh
	s.mu.Unlock()

	// Simulate the stale-snapshot write directly: persistConfigLocked must
	// refuse it because `stale` is not s.config.
	s.mu.Lock()
	wrote := s.persistConfigLocked(stale, "stale snapshot")
	s.mu.Unlock()
	if wrote {
		t.Fatal("persistConfigLocked wrote a config that is not the live one")
	}

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var onDisk config.Config
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if onDisk.Listen != "127.0.0.1:2222" {
		t.Errorf("stale config clobbered the live one on disk: listen = %q, want 127.0.0.1:2222", onDisk.Listen)
	}
}
