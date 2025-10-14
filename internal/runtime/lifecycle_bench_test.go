package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
)

// BenchmarkLoadConfiguredServers measures the time to load and synchronize server configurations
func BenchmarkLoadConfiguredServers(b *testing.B) {
	tmpDir := b.TempDir()
	cfg := &config.Config{
		DataDir: tmpDir,
		Servers: []*config.ServerConfig{
			{Name: "test-server-1", Enabled: true, URL: "http://localhost:8080"},
			{Name: "test-server-2", Enabled: true, URL: "http://localhost:8081"},
			{Name: "test-server-3", Enabled: false, URL: "http://localhost:8082"},
		},
	}

	rt, err := New(cfg, filepath.Join(tmpDir, "config.json"), zap.NewNop())
	if err != nil {
		b.Fatalf("failed to create runtime: %v", err)
	}
	defer rt.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = rt.LoadConfiguredServers(nil)
	}
}

// BenchmarkBackgroundConnections measures the reconnection loop overhead
func BenchmarkBackgroundConnections(b *testing.B) {
	tmpDir := b.TempDir()
	cfg := &config.Config{
		DataDir: tmpDir,
		Servers: []*config.ServerConfig{
			{Name: "test-server", Enabled: true, URL: "http://localhost:9999"}, // Non-existent server
		},
	}

	rt, err := New(cfg, filepath.Join(tmpDir, "config.json"), zap.NewNop())
	if err != nil {
		b.Fatalf("failed to create runtime: %v", err)
	}
	defer rt.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rt.connectAllWithRetry(ctx)
	}
}

// BenchmarkDiscoverAndIndexTools measures tool discovery and indexing latency
func BenchmarkDiscoverAndIndexTools(b *testing.B) {
	tmpDir := b.TempDir()
	cfg := &config.Config{
		DataDir: tmpDir,
		Servers: []*config.ServerConfig{},
	}

	rt, err := New(cfg, filepath.Join(tmpDir, "config.json"), zap.NewNop())
	if err != nil {
		b.Fatalf("failed to create runtime: %v", err)
	}
	defer rt.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = rt.DiscoverAndIndexTools(ctx)
	}
}

// BenchmarkEnableServerToggle measures the latency of enable/disable operations
func BenchmarkEnableServerToggle(b *testing.B) {
	tmpDir := b.TempDir()
	cfg := &config.Config{
		DataDir: tmpDir,
		Servers: []*config.ServerConfig{
			{Name: "test-server", Enabled: true, URL: "http://localhost:8080"},
		},
	}

	rt, err := New(cfg, filepath.Join(tmpDir, "config.json"), zap.NewNop())
	if err != nil {
		b.Fatalf("failed to create runtime: %v", err)
	}
	defer rt.Close()

	// Pre-save server to storage
	_ = rt.StorageManager().SaveUpstreamServer(cfg.Servers[0])

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		enabled := i%2 == 0
		_ = rt.EnableServer("test-server", enabled)
		// Small delay to allow async operations to complete
		time.Sleep(10 * time.Millisecond)
	}
}

// BenchmarkConfigReload measures full configuration reload latency
func BenchmarkConfigReload(b *testing.B) {
	tmpDir := b.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	cfg := &config.Config{
		DataDir: tmpDir,
		Listen:  "127.0.0.1:8080",
		Servers: []*config.ServerConfig{
			{Name: "test-server", Enabled: true, URL: "http://localhost:8080"},
		},
	}

	// Save initial config
	_ = config.SaveConfig(cfg, cfgPath)

	rt, err := New(cfg, cfgPath, zap.NewNop())
	if err != nil {
		b.Fatalf("failed to create runtime: %v", err)
	}
	defer rt.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = rt.ReloadConfiguration()
		// Small delay to allow async background operations
		time.Sleep(50 * time.Millisecond)
	}
}

// BenchmarkUpdateStatus measures status update overhead
func BenchmarkUpdateStatus(b *testing.B) {
	tmpDir := b.TempDir()
	cfg := &config.Config{
		DataDir: tmpDir,
		Servers: []*config.ServerConfig{},
	}

	rt, err := New(cfg, filepath.Join(tmpDir, "config.json"), zap.NewNop())
	if err != nil {
		b.Fatalf("failed to create runtime: %v", err)
	}
	defer rt.Close()

	stats := map[string]interface{}{"total_tools": 5}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rt.UpdateStatus(PhaseReady, "test message", stats, 5)
	}
}
