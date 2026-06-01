package upstream

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestDiscoverTools_ConfigRace reproduces MCP-770: a data race between
// Manager.DiscoverTools (background tool indexing) reading client.Config and
// managed.Client.SetConfig (reconcile add path in AddServerConfig) writing it.
//
// AddServerConfig releases m.mu before calling SetConfig (to avoid deadlock with
// GetServerState), so the write is guarded only by the managed client's mc.mu.
// DiscoverTools must therefore read the config through the mutex-guarded
// GetConfig() accessor rather than touching client.Config directly. Run under
// `go test -race` — without the fix the race detector flags concurrent
// read/write on the mc.Config field.
func TestDiscoverTools_ConfigRace(t *testing.T) {
	serverConfig := &config.ServerConfig{
		Name:     "race-server",
		URL:      "http://127.0.0.1:0",
		Protocol: "http",
		Enabled:  true,
		Created:  time.Now(),
	}

	manager, _ := createTestManagerWithClient(t, serverConfig)

	const iterations = 200
	var wg sync.WaitGroup
	wg.Add(2)

	// Writer: reconcile add path -> SetConfig swaps the mc.Config pointer.
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			// Fresh, equal config each iteration so the unchanged-config branch
			// in AddServerConfig calls SetConfig with a new pointer.
			cfg := *serverConfig
			cfg.Created = time.Now()
			_ = manager.AddServerConfig(serverConfig.Name, &cfg)
		}
	}()

	// Reader: background tool indexing + API-facing status readers snapshot
	// client.Config. All must go through the mutex-guarded accessor.
	go func() {
		defer wg.Done()
		ctx := context.Background()
		for i := 0; i < iterations; i++ {
			_, _ = manager.DiscoverTools(ctx)
			_ = manager.GetStats()
			_ = manager.GetTotalToolCount()
			_ = manager.ListServers()
		}
	}()

	wg.Wait()
}
