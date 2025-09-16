//go:build darwin

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"mcpproxy-go/internal/logs"
	"mcpproxy-go/internal/tray"
)

var version = "development" // Set by build flags

func main() {
	// Setup logging
	logger, err := setupLogging()
	if err != nil {
		log.Fatalf("Failed to setup logging: %v", err)
	}
	defer logger.Sync()

	logger.Info("Starting mcpproxy-tray", zap.String("version", version))

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Start core mcpproxy if not running
	coreURL := "http://localhost:8080"
	if !isServerRunning(coreURL) {
		logger.Info("Core mcpproxy server not running, starting it...")
		if err := startCoreServer(); err != nil {
			logger.Fatal("Failed to start core server", zap.Error(err))
		}

		// Wait for server to be ready
		if !waitForServer(coreURL, 30*time.Second) {
			logger.Fatal("Core server failed to start within timeout")
		}
		logger.Info("Core server started successfully")
	} else {
		logger.Info("Core mcpproxy server already running")
	}

	// Create HTTP client adapter for server interface
	httpClient := NewHTTPServerClient(coreURL, logger.Sugar())

	// Create shutdown function
	shutdownFunc := func() {
		logger.Info("Tray shutdown requested")
		cancel()
	}

	// Create tray application
	trayApp := tray.New(httpClient, logger.Sugar(), version, shutdownFunc)

	// Handle shutdown signal
	go func() {
		<-sigCh
		logger.Info("Received shutdown signal")
		cancel()
	}()

	// This is a blocking call that runs the tray event loop
	logger.Info("Starting tray event loop")
	if err := trayApp.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("Tray application error", zap.Error(err))
	}

	logger.Info("mcpproxy-tray shutdown complete")
}

// setupLogging configures the logger with appropriate settings for the tray
func setupLogging() (*zap.Logger, error) {
	// Get log directory
	logDir, err := logs.GetLogDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get log directory: %w", err)
	}

	// Ensure log directory exists
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	// Create tray-specific log file
	logFile := filepath.Join(logDir, "tray.log")

	config := zap.NewProductionConfig()
	config.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	config.Development = false
	config.Sampling = &zap.SamplingConfig{
		Initial:    100,
		Thereafter: 100,
	}
	config.Encoding = "json"
	config.EncoderConfig = zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "message",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	// Log to both file and stdout
	config.OutputPaths = []string{
		"stdout",
		logFile,
	}
	config.ErrorOutputPaths = []string{
		"stderr",
		logFile,
	}

	logger, err := config.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build logger: %w", err)
	}

	return logger, nil
}

// isServerRunning checks if the core mcpproxy server is running
func isServerRunning(baseURL string) bool {
	client := NewHTTPServerClient(baseURL, nil)
	status := client.GetStatus()

	// Check if we got a valid response
	if statusMap, ok := status.(map[string]interface{}); ok {
		if running, ok := statusMap["running"].(bool); ok {
			return running
		}
	}

	return false
}

// startCoreServer starts the core mcpproxy server process
func startCoreServer() error {
	// Find the mcpproxy binary
	mcpproxyPath, err := findMcpproxyBinary()
	if err != nil {
		return fmt.Errorf("failed to find mcpproxy binary: %w", err)
	}

	// Start the server in background
	cmd := exec.Command(mcpproxyPath, "serve", "--tray=false")
	cmd.Stdout = nil // Don't capture output to avoid blocking
	cmd.Stderr = nil

	// Start the process in a new process group
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true, // Create new process group
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start mcpproxy server: %w", err)
	}

	// Don't wait for the process - let it run independently
	go func() {
		cmd.Wait() // Clean up zombie process
	}()

	return nil
}

// findMcpproxyBinary finds the mcpproxy binary in various locations
func findMcpproxyBinary() (string, error) {
	// Try different possible locations
	candidates := []string{
		"mcpproxy",                    // In PATH
		"./mcpproxy",                  // Current directory
		"../mcpproxy/mcpproxy",        // Development setup
		"/usr/local/bin/mcpproxy",     // Homebrew location
		"/opt/homebrew/bin/mcpproxy",  // Apple Silicon Homebrew
	}

	for _, path := range candidates {
		if _, err := exec.LookPath(path); err == nil {
			return path, nil
		}
		if _, err := os.Stat(path); err == nil {
			absPath, err := filepath.Abs(path)
			if err == nil {
				return absPath, nil
			}
		}
	}

	return "", fmt.Errorf("mcpproxy binary not found in any of the expected locations")
}

// waitForServer waits for the server to become available
func waitForServer(baseURL string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if isServerRunning(baseURL) {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}

	return false
}