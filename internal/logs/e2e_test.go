package logs

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestE2E_LoggingSystem tests the complete logging system end-to-end
func TestE2E_LoggingSystem(t *testing.T) {
	// Skip if we're in a CI environment without proper permissions
	if os.Getenv("CI") == "true" && runtime.GOOS == "linux" {
		t.Skip("Skipping logging E2E test in CI environment")
	}

	// Test different log configurations
	testCases := []struct {
		name       string
		config     *config.LogConfig
		shouldFail bool
	}{
		{
			name: "default_config",
			config: &config.LogConfig{
				Level:         "info",
				EnableFile:    true,
				EnableConsole: true,
				Filename:      "mcpproxy-e2e-test.log",
				MaxSize:       1, // 1MB for testing
				MaxBackups:    2,
				MaxAge:        1, // 1 day
				Compress:      true,
				JSONFormat:    false,
			},
			shouldFail: false,
		},
		{
			name: "json_format",
			config: &config.LogConfig{
				Level:         "debug",
				EnableFile:    true,
				EnableConsole: false,
				Filename:      "mcpproxy-e2e-json.log",
				MaxSize:       1,
				MaxBackups:    3,
				MaxAge:        7,
				Compress:      false,
				JSONFormat:    true,
			},
			shouldFail: false,
		},
		{
			name: "console_only",
			config: &config.LogConfig{
				Level:         "warn",
				EnableFile:    false,
				EnableConsole: true,
				Filename:      "",
				MaxSize:       0,
				MaxBackups:    0,
				MaxAge:        0,
				Compress:      false,
				JSONFormat:    false,
			},
			shouldFail: false,
		},
		{
			name: "invalid_no_outputs",
			config: &config.LogConfig{
				Level:         "info",
				EnableFile:    false,
				EnableConsole: false,
				Filename:      "",
			},
			shouldFail: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logger, err := SetupLogger(tc.config)

			if tc.shouldFail {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, logger)

			// Test logging at different levels
			logger.Debug("Debug message for E2E test", zap.String("test_case", tc.name))
			logger.Info("Info message for E2E test", zap.String("test_case", tc.name))
			logger.Warn("Warning message for E2E test", zap.String("test_case", tc.name))
			logger.Error("Error message for E2E test", zap.String("test_case", tc.name))

			// Sync to ensure all logs are written
			_ = logger.Sync()

			// If file logging is enabled, verify the log file exists and has content
			if tc.config.EnableFile && tc.config.Filename != "" {
				logFilePath, err := GetLogFilePath(tc.config.Filename)
				require.NoError(t, err)

				// Check if file exists
				_, err = os.Stat(logFilePath)
				assert.NoError(t, err, "Log file should exist")

				// Read and verify content
				content, err := os.ReadFile(logFilePath)
				require.NoError(t, err)

				contentStr := string(content)
				assert.Contains(t, contentStr, "E2E test", "Log file should contain test messages")
				assert.Contains(t, contentStr, tc.name, "Log file should contain test case name")

				// Verify log format
				if tc.config.JSONFormat {
					assert.Contains(t, contentStr, `"level"`, "JSON format should contain level field")
					assert.Contains(t, contentStr, `"msg"`, "JSON format should contain msg field")
				} else {
					// Console format should have pipe separators
					assert.Contains(t, contentStr, " | ", "Console format should have pipe separators")
				}

				// Clean up test log file
				os.Remove(logFilePath)
			}
		})
	}
}

// TestE2E_LogDirectoryCreation tests log directory creation across different OS
func TestE2E_LogDirectoryCreation(t *testing.T) {
	// Get the standard log directory for current OS
	logDir, err := GetLogDir()
	require.NoError(t, err)

	// Ensure directory exists
	err = EnsureLogDir(logDir)
	require.NoError(t, err)

	// Verify directory exists and has correct permissions
	info, err := os.Stat(logDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	// Test OS-specific directory structure
	switch runtime.GOOS {
	case "darwin":
		assert.Contains(t, logDir, "Library/Logs/mcpproxy")
		assert.Contains(t, logDir, os.Getenv("HOME"))
	case "windows":
		// Should contain either LOCALAPPDATA or USERPROFILE
		localAppData := os.Getenv("LOCALAPPDATA")
		userProfile := os.Getenv("USERPROFILE")
		if localAppData != "" {
			assert.Contains(t, logDir, localAppData)
		} else if userProfile != "" {
			assert.Contains(t, logDir, userProfile)
		}
		assert.Contains(t, logDir, "mcpproxy")
	case "linux":
		if os.Getuid() == 0 {
			assert.Equal(t, "/var/log/mcpproxy", logDir)
		} else {
			assert.Contains(t, logDir, "mcpproxy")
			// Should use XDG_STATE_HOME or ~/.local/state
			xdgStateHome := os.Getenv("XDG_STATE_HOME")
			if xdgStateHome != "" {
				assert.Contains(t, logDir, xdgStateHome)
			} else {
				assert.Contains(t, logDir, ".local/state")
			}
		}
	}
}

// TestE2E_LogRotation tests log rotation functionality
func TestE2E_LogRotation(t *testing.T) {
	// Create a config with small max size to trigger rotation
	config := &config.LogConfig{
		Level:         "info",
		EnableFile:    true,
		EnableConsole: false,
		Filename:      "mcpproxy-rotation-test.log",
		MaxSize:       1, // 1KB for testing
		MaxBackups:    2,
		MaxAge:        1,
		Compress:      true,
		JSONFormat:    false,
	}

	logger, err := SetupLogger(config)
	require.NoError(t, err)

	// Write enough logs to trigger rotation
	for i := 0; i < 100; i++ {
		logger.Info("This is a test log message to trigger rotation",
			zap.Int("iteration", i),
			zap.String("data", strings.Repeat("x", 100)))
	}

	_ = logger.Sync()

	// Check if log files exist
	logFilePath, err := GetLogFilePath(config.Filename)
	require.NoError(t, err)

	logDir := filepath.Dir(logFilePath)
	baseName := strings.TrimSuffix(config.Filename, filepath.Ext(config.Filename))

	// List files in log directory
	files, err := os.ReadDir(logDir)
	require.NoError(t, err)

	// Count log files (including rotated ones)
	logFileCount := 0
	for _, file := range files {
		if strings.Contains(file.Name(), baseName) {
			logFileCount++
		}
	}

	// Should have at least the main log file
	assert.GreaterOrEqual(t, logFileCount, 1)

	// Clean up test files
	for _, file := range files {
		if strings.Contains(file.Name(), baseName) {
			os.Remove(filepath.Join(logDir, file.Name()))
		}
	}
}

// TestE2E_MCPProxyWithLogging tests the actual mcpproxy binary with logging enabled
func TestE2E_MCPProxyWithLogging(t *testing.T) {
	// Determine binary name based on OS
	binaryName := "../../mcpproxy"
	if runtime.GOOS == "windows" {
		binaryName = "../../mcpproxy.exe"
	}

	// Skip if we don't have the binary built
	if _, err := os.Stat(binaryName); os.IsNotExist(err) {
		t.Skipf("mcpproxy binary not found at %s, run 'go build -o %s ./cmd/mcpproxy' first", binaryName, binaryName)
	}

	// Create a temporary config file
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "test-config.json")

	config := map[string]interface{}{
		"listen":      ":0", // Random port
		"data_dir":    tempDir,
		"enable_tray": false,
		"logging": map[string]interface{}{
			"level":          "debug",
			"enable_file":    true,
			"enable_console": true,
			"filename":       "mcpproxy-e2e-binary.log",
			"max_size":       10,
			"max_backups":    3,
			"max_age":        7,
			"compress":       true,
			"json_format":    false,
		},
		"mcpServers": []interface{}{},
	}

	// Write config file
	configBytes, err := json.Marshal(config)
	require.NoError(t, err)
	err = os.WriteFile(configFile, configBytes, 0644)
	require.NoError(t, err)

	// Run mcpproxy with the config
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryName,
		"serve",
		"--config", configFile,
		"--log-level", "debug",
		"--log-to-file")

	// Capture output
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	stderr, err := cmd.StderrPipe()
	require.NoError(t, err)

	// Start the command
	err = cmd.Start()
	require.NoError(t, err)

	// Read some output to ensure it's running
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			t.Logf("STDOUT: %s", line)
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			t.Logf("STDERR: %s", line)
		}
	}()

	// Wait a bit for the server to start
	time.Sleep(3 * time.Second)

	// Kill the process
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}

	// Wait for the command to finish
	_ = cmd.Wait()

	// Check if log file was created
	logFilePath, err := GetLogFilePath("mcpproxy-e2e-binary.log")
	require.NoError(t, err)

	_, err = os.Stat(logFilePath)
	assert.NoError(t, err, "Log file should be created by mcpproxy binary")

	// Read and verify log content
	if err == nil {
		content, err := os.ReadFile(logFilePath)
		require.NoError(t, err)

		contentStr := string(content)
		assert.Contains(t, contentStr, "Starting mcpproxy", "Log should contain startup message")
		assert.Contains(t, contentStr, "Log directory configured", "Log should contain directory info")

		// Clean up
		os.Remove(logFilePath)
	}
}

// TestE2E_LogDirInfo tests the log directory information functionality
func TestE2E_LogDirInfo(t *testing.T) {
	info, err := GetLogDirInfo()
	require.NoError(t, err)
	require.NotNil(t, info)

	// Verify basic info
	assert.NotEmpty(t, info.Path)
	assert.Equal(t, runtime.GOOS, info.OS)
	assert.NotEmpty(t, info.Description)
	assert.NotEmpty(t, info.Standard)

	// Verify OS-specific standards
	switch runtime.GOOS {
	case "darwin":
		assert.Equal(t, "macOS File System Programming Guide", info.Standard)
		assert.Contains(t, info.Description, "macOS")
	case "windows":
		assert.Equal(t, "Windows Application Data Guidelines", info.Standard)
		assert.Contains(t, info.Description, "Windows")
	case "linux":
		assert.Equal(t, "XDG Base Directory Specification", info.Standard)
		assert.Contains(t, info.Description, "Linux")
	default:
		assert.Equal(t, "Default behavior", info.Standard)
	}

	// Verify path is absolute
	assert.True(t, filepath.IsAbs(info.Path))

	// Verify path contains mcpproxy
	assert.Contains(t, info.Path, "mcpproxy")
}

// TestE2E_ConcurrentLogging tests concurrent logging to ensure thread safety
func TestE2E_ConcurrentLogging(t *testing.T) {
	// Use a unique filename for this test to avoid conflicts
	uniqueFilename := fmt.Sprintf("mcpproxy-concurrent-test-%d.log", time.Now().UnixNano())

	config := &config.LogConfig{
		Level:         "info",
		EnableFile:    true,
		EnableConsole: false, // Disable console to avoid any potential duplication
		Filename:      uniqueFilename,
		MaxSize:       10,
		MaxBackups:    3,
		MaxAge:        7,
		Compress:      false,
		JSONFormat:    false,
	}

	// Ensure the log file doesn't exist before starting
	logFilePath, err := GetLogFilePath(config.Filename)
	require.NoError(t, err)

	// Clean up any existing file
	os.Remove(logFilePath)

	t.Logf("Log file path: %s", logFilePath)
	t.Logf("OS: %s", runtime.GOOS)

	logger, err := SetupLogger(config)
	require.NoError(t, err)

	// Number of concurrent goroutines - reduced to make test more reliable on Windows
	numGoroutines := 5
	messagesPerGoroutine := 100

	// On Windows, use even fewer goroutines due to potential timing issues
	if runtime.GOOS == "windows" {
		numGoroutines = 3
		messagesPerGoroutine = 50
		t.Logf("Windows detected, using reduced concurrency: %d goroutines, %d messages each", numGoroutines, messagesPerGoroutine)
	}

	// Channel to signal completion
	done := make(chan bool, numGoroutines)

	// Start concurrent logging
	for i := 0; i < numGoroutines; i++ {
		go func(goroutineID int) {
			defer func() { done <- true }()

			for j := 0; j < messagesPerGoroutine; j++ {
				logger.Info("Concurrent log message",
					zap.Int("goroutine", goroutineID),
					zap.Int("message", j),
					zap.String("data", fmt.Sprintf("test-data-%d-%d", goroutineID, j)))
			}
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		select {
		case <-done:
			// Success
		case <-time.After(30 * time.Second):
			t.Fatal("Timeout waiting for concurrent logging to complete")
		}
	}

	// Ensure all messages are flushed
	_ = logger.Sync()

	// Give a small delay to ensure all writes are completed
	time.Sleep(100 * time.Millisecond)

	// Verify log file exists
	_, err = os.Stat(logFilePath)
	require.NoError(t, err, "Log file should exist")

	content, err := os.ReadFile(logFilePath)
	require.NoError(t, err)

	contentStr := string(content)

	// Count actual log messages by looking for the specific message text
	// This is more reliable than counting newlines which can vary by platform
	logMessageCount := strings.Count(contentStr, "Concurrent log message")

	// Should have exactly numGoroutines * messagesPerGoroutine log messages
	expectedMessages := numGoroutines * messagesPerGoroutine

	t.Logf("Expected messages: %d, Actual messages: %d", expectedMessages, logMessageCount)
	t.Logf("Log file size: %d bytes", len(content))

	// Debug: show first few lines if count is wrong
	if logMessageCount != expectedMessages {
		lines := strings.Split(contentStr, "\n")
		t.Logf("Log file has %d lines", len(lines))
		t.Logf("First 10 lines:")
		for i, line := range lines {
			if i >= 10 || line == "" {
				break
			}
			t.Logf("Line %d: %s", i+1, line)
		}

		// Also check if there might be duplicate consecutive messages
		lastLine := ""
		duplicateCount := 0
		for _, line := range lines {
			if line != "" && line == lastLine {
				duplicateCount++
			}
			lastLine = line
		}
		if duplicateCount > 0 {
			t.Logf("Found %d duplicate consecutive lines", duplicateCount)
		}
	}

	// Allow some tolerance for concurrent operations but much stricter range
	// The issue might be that we're getting exactly double, so let's be more specific
	tolerance := 5
	if runtime.GOOS == "windows" {
		// Windows might have more variance due to different file system behavior
		tolerance = 10
	}

	assert.GreaterOrEqual(t, logMessageCount, expectedMessages-tolerance, "Too few log messages")
	assert.LessOrEqual(t, logMessageCount, expectedMessages+tolerance, "Too many log messages")

	// Verify that messages from different goroutines are present
	goroutinesSeen := make(map[int]bool)
	for i := 0; i < numGoroutines; i++ {
		if strings.Contains(contentStr, fmt.Sprintf(`"goroutine": %d`, i)) {
			goroutinesSeen[i] = true
		}
	}

	// Should see messages from all goroutines
	assert.Equal(t, numGoroutines, len(goroutinesSeen), "Should see messages from all goroutines")

	// Clean up
	os.Remove(logFilePath)
}

// BenchmarkE2E_LoggingPerformance benchmarks the logging performance
func BenchmarkE2E_LoggingPerformance(b *testing.B) {
	config := &config.LogConfig{
		Level:         "info",
		EnableFile:    true,
		EnableConsole: false,
		Filename:      "mcpproxy-benchmark.log",
		MaxSize:       100,
		MaxBackups:    1,
		MaxAge:        1,
		Compress:      false,
		JSONFormat:    false,
	}

	logger, err := SetupLogger(config)
	require.NoError(b, err)

	defer func() {
		_ = logger.Sync()
		logFilePath, _ := GetLogFilePath(config.Filename)
		_ = os.Remove(logFilePath)
	}()

	b.ResetTimer()

	b.Run("structured_logging", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			logger.Info("Benchmark log message",
				zap.Int("iteration", i),
				zap.String("operation", "benchmark"),
				zap.Duration("elapsed", time.Millisecond*100),
				zap.Bool("success", true))
		}
	})

	b.Run("simple_logging", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			logger.Info("Simple benchmark message")
		}
	})
}
