package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConfigFileRaceCondition attempts to reproduce the race condition
// where concurrent reads during writes can result in corrupted JSON.
//
// This test simulates the real-world scenario:
// 1. Web UI saves config (triggers write)
// 2. User clicks "restart" immediately
// 3. Core starts and tries to read config while write is in progress
// 4. Core reads partial/corrupted JSON
//
// Expected: With current implementation (os.WriteFile), this test should
// occasionally fail with JSON parse errors.
func TestConfigFileRaceCondition(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping race condition test in short mode")
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "mcp_config.json")

	const (
		iterations      = 100
		concurrentReads = 5
	)

	var (
		successfulReads  int32
		corruptedReads   int32
		parseErrors      int32
		readAttempts     int32
	)

	t.Logf("Running %d iterations with %d concurrent readers", iterations, concurrentReads)

	for i := 0; i < iterations; i++ {
		// Create a config with varying content
		cfg := createTestConfig(i)

		var wg sync.WaitGroup

		// Writer: Simulate config save
		wg.Add(1)
		go func(iteration int) {
			defer wg.Done()

			// Use current implementation (non-atomic)
			data, err := json.MarshalIndent(cfg, "", "  ")
			if err != nil {
				t.Errorf("Marshal error: %v", err)
				return
			}

			// This is what the current SaveConfig does
			// It truncates the file immediately, then writes
			if err := os.WriteFile(configPath, data, 0600); err != nil {
				t.Errorf("WriteFile error: %v", err)
			}
		}(i)

		// Readers: Simulate core startup trying to read config
		for r := 0; r < concurrentReads; r++ {
			wg.Add(1)
			go func(reader int) {
				defer wg.Done()

				// Tiny delay to spread out the reads
				time.Sleep(time.Duration(reader) * time.Microsecond)

				atomic.AddInt32(&readAttempts, 1)

				// Try to read and parse config
				data, err := os.ReadFile(configPath)
				if err != nil {
					// File might not exist yet
					return
				}

				// Check if we got partial data
				if len(data) < 50 {
					atomic.AddInt32(&corruptedReads, 1)
					t.Logf("Iteration %d, Reader %d: Partial read (%d bytes)", i, reader, len(data))
					return
				}

				// Try to parse
				var testCfg Config
				if err := json.Unmarshal(data, &testCfg); err != nil {
					atomic.AddInt32(&parseErrors, 1)
					t.Logf("Iteration %d, Reader %d: Parse error: %v", i, reader, err)
					t.Logf("Corrupted data (first 100 chars): %s", truncateString(string(data), 100))
					return
				}

				atomic.AddInt32(&successfulReads, 1)
			}(r)
		}

		wg.Wait()

		// Small delay between iterations
		time.Sleep(time.Millisecond)
	}

	totalReads := atomic.LoadInt32(&readAttempts)
	successful := atomic.LoadInt32(&successfulReads)
	corrupted := atomic.LoadInt32(&corruptedReads)
	parseErrs := atomic.LoadInt32(&parseErrors)
	failures := corrupted + parseErrs

	t.Logf("\n" +
		"==============================================\n" +
		"Race Condition Test Results\n" +
		"==============================================\n" +
		"Total read attempts:    %d\n"+
		"Successful reads:       %d (%.1f%%)\n"+
		"Corrupted/partial reads: %d\n"+
		"JSON parse errors:      %d\n"+
		"Total failures:         %d (%.1f%%)\n"+
		"==============================================\n",
		totalReads,
		successful, float64(successful)*100/float64(totalReads),
		corrupted,
		parseErrs,
		failures, float64(failures)*100/float64(totalReads),
	)

	if failures > 0 {
		t.Logf("⚠️  Race condition detected! %d failures out of %d reads", failures, totalReads)
		t.Logf("This demonstrates the issue from GitHub issue #86")
		t.Logf("The core can read corrupted JSON during config writes")
	}

	// NOTE: We don't fail the test here because race conditions are probabilistic
	// The test serves to DEMONSTRATE the issue, not necessarily to fail CI
	// (unless we want it to fail to force the fix)
}

// TestAtomicConfigWrite tests that atomic writes prevent race conditions
func TestAtomicConfigWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping atomic write test in short mode")
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "mcp_config.json")

	// Create initial config
	initialCfg := DefaultConfig()
	initialCfg.Servers = []*ServerConfig{
		{Name: "initial", URL: "http://localhost:8000", Enabled: true},
	}
	require.NoError(t, SaveConfig(initialCfg, configPath))

	const (
		iterations      = 200
		concurrentReads = 10
	)

	var (
		successfulReads int32
		parseErrors     int32
		readAttempts    int32
	)

	t.Logf("Running %d iterations with %d concurrent readers", iterations, concurrentReads)

	for i := 0; i < iterations; i++ {
		// Create a config with varying content
		cfg := createTestConfig(i)

		var wg sync.WaitGroup

		// Writer: Save config (will use SaveConfig which should be atomic)
		wg.Add(1)
		go func(iteration int) {
			defer wg.Done()

			// When we implement atomicWriteFile, this should be safe
			if err := SaveConfig(cfg, configPath); err != nil {
				t.Errorf("SaveConfig error: %v", err)
			}
		}(i)

		// Readers: Simulate concurrent core startups
		for r := 0; r < concurrentReads; r++ {
			wg.Add(1)
			go func(reader int) {
				defer wg.Done()

				// Spread out reads
				time.Sleep(time.Duration(reader) * 100 * time.Microsecond)

				atomic.AddInt32(&readAttempts, 1)

				// Read and parse
				data, err := os.ReadFile(configPath)
				if err != nil {
					t.Errorf("Read error: %v", err)
					return
				}

				var testCfg Config
				if err := json.Unmarshal(data, &testCfg); err != nil {
					atomic.AddInt32(&parseErrors, 1)
					t.Errorf("Iteration %d, Reader %d: Parse error: %v", i, reader, err)
					t.Errorf("Data: %s", truncateString(string(data), 200))
					return
				}

				atomic.AddInt32(&successfulReads, 1)
			}(r)
		}

		wg.Wait()
	}

	totalReads := atomic.LoadInt32(&readAttempts)
	successful := atomic.LoadInt32(&successfulReads)
	parseErrs := atomic.LoadInt32(&parseErrors)

	t.Logf("\n" +
		"==============================================\n" +
		"Atomic Write Test Results\n" +
		"==============================================\n" +
		"Total read attempts:  %d\n"+
		"Successful reads:     %d (%.1f%%)\n"+
		"JSON parse errors:    %d\n"+
		"==============================================\n",
		totalReads,
		successful, float64(successful)*100/float64(totalReads),
		parseErrs,
	)

	// With atomic writes, we should have ZERO parse errors
	assert.Equal(t, int32(0), parseErrs, "Atomic writes should prevent all race conditions")
	assert.Equal(t, totalReads, successful, "All reads should succeed with atomic writes")
}

// createTestConfig creates a test config with varying content
func createTestConfig(iteration int) *Config {
	cfg := DefaultConfig()
	cfg.Listen = fmt.Sprintf("127.0.0.1:%d", 8080+iteration)
	cfg.APIKey = fmt.Sprintf("test-key-iteration-%d-with-some-extra-data-to-make-it-longer", iteration)

	// Add multiple servers to make the config larger
	cfg.Servers = []*ServerConfig{
		{
			Name:    fmt.Sprintf("server-%d-1", iteration),
			URL:     "http://localhost:8001",
			Enabled: true,
		},
		{
			Name:    fmt.Sprintf("server-%d-2", iteration),
			URL:     "http://localhost:8002",
			Enabled: true,
		},
		{
			Name:    fmt.Sprintf("server-%d-3", iteration),
			URL:     "http://localhost:8003",
			Enabled: false,
		},
		{
			Name:        fmt.Sprintf("server-%d-4", iteration),
			Command:     "npx",
			Args:        []string{"-y", "@modelcontextprotocol/server-everything"},
			Enabled:     true,
			Quarantined: true,
		},
		{
			Name:    fmt.Sprintf("server-%d-5", iteration),
			URL:     "http://localhost:8005",
			Enabled: true,
			Env: map[string]string{
				"API_KEY": fmt.Sprintf("key-%d", iteration),
				"DEBUG":   "true",
			},
		},
	}

	return cfg
}

// truncateString truncates a string to maxLen characters
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// BenchmarkConfigWriteRead benchmarks config write/read performance
func BenchmarkConfigWriteRead(b *testing.B) {
	tmpDir := b.TempDir()
	cfg := createTestConfig(0)

	b.Run("Current_NonAtomic", func(b *testing.B) {
		configPath := filepath.Join(tmpDir, "config_nonatomic.json")

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Current implementation
			data, _ := json.MarshalIndent(cfg, "", "  ")
			_ = os.WriteFile(configPath, data, 0600)

			// Read back
			data, _ = os.ReadFile(configPath)
			var testCfg Config
			_ = json.Unmarshal(data, &testCfg)
		}
	})

	// This will benchmark the atomic version once implemented
	b.Run("Proposed_Atomic", func(b *testing.B) {
		configPath := filepath.Join(tmpDir, "config_atomic.json")

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = SaveConfig(cfg, configPath)

			// Read back
			data, _ := os.ReadFile(configPath)
			var testCfg Config
			_ = json.Unmarshal(data, &testCfg)
		}
	})
}
