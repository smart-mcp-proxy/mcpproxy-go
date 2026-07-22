package runtime

import (
	"path/filepath"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/require"
)

// TestTruncatorSwapRace (#861 defect 1): Runtime.Truncator() must be safe to
// call concurrently while a config apply swaps the truncator under the lock.
// Before the fix, Truncator() read r.truncator without synchronization while
// ApplyConfig wrote it, a data race the -race detector flags. Run with -race.
func TestTruncatorSwapRace(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	initialCfg := config.DefaultConfig()
	initialCfg.Listen = "127.0.0.1:8080"
	initialCfg.DataDir = tmpDir
	initialCfg.ToolResponseLimit = 1000
	require.NoError(t, config.SaveConfig(initialCfg, cfgPath))

	rt, err := New(initialCfg, cfgPath, zap.NewNop())
	require.NoError(t, err)
	defer func() { _ = rt.Close() }()

	const readers = 8
	const iterations = 40

	var wg sync.WaitGroup

	// Readers: hammer the accessor (and use the returned value) concurrently.
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations*4; j++ {
				tr := rt.Truncator()
				if tr != nil {
					_ = tr.ShouldTruncate("some content that may exceed a limit")
				}
			}
		}()
	}

	// Writer: repeatedly apply configs that flip the tool-response limit, which
	// swaps the truncator each time.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < iterations; j++ {
			newCfg := config.DefaultConfig()
			newCfg.Listen = "127.0.0.1:8080"
			newCfg.DataDir = tmpDir
			if j%2 == 0 {
				newCfg.ToolResponseLimit = 2000
			} else {
				newCfg.ToolResponseLimit = 1000
			}
			_, applyErr := rt.ApplyConfig(newCfg, cfgPath)
			require.NoError(t, applyErr)
		}
	}()

	wg.Wait()
}
