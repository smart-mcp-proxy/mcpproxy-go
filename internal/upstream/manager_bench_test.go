package upstream

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/storage"
)

// BenchmarkAddServer measures the time to add and configure a new server
func BenchmarkAddServer(b *testing.B) {
	tmpDir := b.TempDir()
	cfg := &config.Config{DataDir: tmpDir}
	logger := zap.NewNop()
	storageManager, _ := storage.NewBoltDB(tmpDir, zap.NewNop().Sugar())
	defer storageManager.Close()

	manager := NewManager(logger, cfg, storageManager, nil)
	defer manager.DisconnectAll()

	serverConfig := &config.ServerConfig{
		Name:     "test-server",
		Enabled:  true,
		URL:      "http://localhost:9999",
		Protocol: "http",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = manager.AddServer("test-server", serverConfig)
		manager.RemoveServer("test-server")
	}
}

// BenchmarkConnectAll measures the overhead of connection attempts across all servers
func BenchmarkConnectAll(b *testing.B) {
	tmpDir := b.TempDir()
	cfg := &config.Config{DataDir: tmpDir}
	logger := zap.NewNop()
	storageManager, _ := storage.NewBoltDB(tmpDir, zap.NewNop().Sugar())
	defer storageManager.Close()

	manager := NewManager(logger, cfg, storageManager, nil)
	defer manager.DisconnectAll()

	// Add several disconnected servers
	for i := 0; i < 5; i++ {
		id := "test-server-" + string(rune('0'+i))
		serverConfig := &config.ServerConfig{
			Name:     id,
			Enabled:  true,
			URL:      "http://localhost:9999",
			Protocol: "http",
		}
		_ = manager.AddServerConfig(id, serverConfig)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = manager.ConnectAll(ctx)
	}
}

// BenchmarkDiscoverTools measures tool discovery latency with multiple servers
func BenchmarkDiscoverTools(b *testing.B) {
	tmpDir := b.TempDir()
	cfg := &config.Config{DataDir: tmpDir}
	logger := zap.NewNop()
	storageManager, _ := storage.NewBoltDB(tmpDir, zap.NewNop().Sugar())
	defer storageManager.Close()

	manager := NewManager(logger, cfg, storageManager, nil)
	defer manager.DisconnectAll()

	// Add servers (they won't connect, but structure is present)
	for i := 0; i < 3; i++ {
		id := "test-server-" + string(rune('0'+i))
		serverConfig := &config.ServerConfig{
			Name:     id,
			Enabled:  true,
			URL:      "http://localhost:9999",
			Protocol: "http",
		}
		_ = manager.AddServerConfig(id, serverConfig)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = manager.DiscoverTools(ctx)
	}
}

// BenchmarkGetStats measures the overhead of stats collection
func BenchmarkGetStats(b *testing.B) {
	tmpDir := b.TempDir()
	cfg := &config.Config{DataDir: tmpDir}
	logger := zap.NewNop()
	storageManager, _ := storage.NewBoltDB(tmpDir, zap.NewNop().Sugar())
	defer storageManager.Close()

	manager := NewManager(logger, cfg, storageManager, nil)
	defer manager.DisconnectAll()

	// Add several servers
	for i := 0; i < 10; i++ {
		id := "test-server-" + string(rune('0'+i))
		serverConfig := &config.ServerConfig{
			Name:     id,
			Enabled:  true,
			URL:      "http://localhost:9999",
			Protocol: "http",
		}
		_ = manager.AddServerConfig(id, serverConfig)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = manager.GetStats()
	}
}

// BenchmarkCallToolWithLock measures the overhead of tool calls under read lock
func BenchmarkCallToolWithLock(b *testing.B) {
	tmpDir := b.TempDir()
	cfg := &config.Config{DataDir: tmpDir}
	logger := zap.NewNop()
	storageManager, _ := storage.NewBoltDB(tmpDir, zap.NewNop().Sugar())
	defer storageManager.Close()

	manager := NewManager(logger, cfg, storageManager, nil)
	defer manager.DisconnectAll()

	serverConfig := &config.ServerConfig{
		Name:     "test-server",
		Enabled:  true,
		URL:      "http://localhost:9999",
		Protocol: "http",
	}
	_ = manager.AddServerConfig("test-server", serverConfig)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// This will fail since server is not connected, but measures lock overhead
		_, _ = manager.CallTool(ctx, "test-server:dummy_tool", map[string]interface{}{})
	}
}

// BenchmarkRemoveServer measures the time to remove and disconnect a server
func BenchmarkRemoveServer(b *testing.B) {
	tmpDir := b.TempDir()
	cfg := &config.Config{DataDir: tmpDir}
	logger := zap.NewNop()
	storageManager, _ := storage.NewBoltDB(tmpDir, zap.NewNop().Sugar())
	defer storageManager.Close()

	manager := NewManager(logger, cfg, storageManager, nil)
	defer manager.DisconnectAll()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		serverConfig := &config.ServerConfig{
			Name:     "test-server",
			Enabled:  true,
			URL:      "http://localhost:9999",
			Protocol: "http",
		}
		_ = manager.AddServerConfig("test-server", serverConfig)
		b.StartTimer()

		manager.RemoveServer("test-server")
	}
}
