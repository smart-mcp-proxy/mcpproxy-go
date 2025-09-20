//go:build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"mcpproxy-go/cmd/mcpproxy-tray/internal/api"
	"mcpproxy-go/internal/tray"
)

var (
	version          = "development" // Set by build flags
	defaultCoreURL   = "http://localhost:8080"
	errNoBundledCore = errors.New("no bundled core binary found")
)

// getLogDir returns the standard log directory for the current OS.
// Falls back to a temporary directory when a platform path cannot be resolved.
func getLogDir() string {
	fallback := filepath.Join(os.TempDir(), "mcpproxy", "logs")

	switch runtime.GOOS {
	case "darwin":
		if homeDir, err := os.UserHomeDir(); err == nil {
			return filepath.Join(homeDir, "Library", "Logs", "mcpproxy")
		}
	case "windows":
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			return filepath.Join(localAppData, "mcpproxy", "logs")
		}
		if userProfile := os.Getenv("USERPROFILE"); userProfile != "" {
			return filepath.Join(userProfile, "AppData", "Local", "mcpproxy", "logs")
		}
	default: // linux and others
		if homeDir, err := os.UserHomeDir(); err == nil {
			return filepath.Join(homeDir, ".mcpproxy", "logs")
		}
	}

	return fallback
}

func main() {
	// Setup logging
	logger, err := setupLogging()
	if err != nil {
		log.Fatalf("Failed to setup logging: %v", err)
	}
	defer func() {
		if syncErr := logger.Sync(); syncErr != nil {
			logger.Error("Failed to sync logger", zap.Error(syncErr))
		}
	}()

	logger.Info("Starting mcpproxy-tray", zap.String("version", version))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Resolve core configuration up front
	coreURL := resolveCoreURL()
	logger.Info("Resolved core URL", zap.String("core_url", coreURL))

	// Prepare API client and tray adapters immediately so the icon appears before the core is ready
	apiClient := api.NewClient(coreURL, logger.Sugar())
	if err := apiClient.StartSSE(ctx); err != nil {
		logger.Error("Failed to start SSE connection", zap.Error(err))
	}

	shutdownFunc := func() {
		logger.Info("Tray shutdown requested")
		apiClient.StopSSE()
		cancel()
	}

	trayApp := tray.NewWithAPIClient(api.NewServerAdapter(apiClient), apiClient, logger.Sugar(), version, shutdownFunc)
	trayApp.ObserveConnectionState(ctx, apiClient.ConnectionStateChannel())

	// Handle signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Info("Received shutdown signal")
		cancel()
	}()

	// Launch core management loop in the background so the tray can appear immediately
	go manageCoreProcess(ctx, logger, trayApp, coreURL)

	logger.Info("Starting tray event loop")
	if err := trayApp.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("Tray application error", zap.Error(err))
	}

	logger.Info("mcpproxy-tray shutdown complete")
}

// setupLogging configures the logger with appropriate settings for the tray
func setupLogging() (*zap.Logger, error) {
	// Get log directory
	logDir := getLogDir()

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

func resolveCoreURL() string {
	if override := strings.TrimSpace(os.Getenv("MCPPROXY_CORE_URL")); override != "" {
		return override
	}
	return defaultCoreURL
}

func shouldSkipCoreLaunch() bool {
	value := strings.TrimSpace(os.Getenv("MCPPROXY_TRAY_SKIP_CORE"))
	return value == "1" || strings.EqualFold(value, "true")
}

func manageCoreProcess(ctx context.Context, logger *zap.Logger, trayApp *tray.App, coreURL string) {
	if shouldSkipCoreLaunch() {
		logger.Info("Skipping core auto-launch due to MCPPROXY_TRAY_SKIP_CORE")
		trayApp.SetConnectionState(tray.ConnectionStateConnecting)
		return
	}

	if isServerRunning(coreURL) {
		logger.Info("Core mcpproxy server already running", zap.String("core_url", coreURL))
		trayApp.SetConnectionState(tray.ConnectionStateConnecting)
		return
	}

	trayApp.SetConnectionState(tray.ConnectionStateStartingCore)
	logger.Info("Core mcpproxy server not running, starting it", zap.String("core_url", coreURL))

	coreBinary, err := resolveCoreBinary(logger)
	if err != nil {
		logger.Error("Failed to resolve core binary", zap.Error(err))
		trayApp.SetConnectionState(tray.ConnectionStateDisconnected)
		return
	}

	if err := startCoreServer(logger, coreBinary); err != nil {
		logger.Error("Failed to start core server", zap.String("binary", coreBinary), zap.Error(err))
		trayApp.SetConnectionState(tray.ConnectionStateDisconnected)
		return
	}

	if !waitForServer(ctx, coreURL, 30*time.Second) {
		logger.Error("Core server failed to start within timeout", zap.String("core_url", coreURL))
		trayApp.SetConnectionState(tray.ConnectionStateDisconnected)
		return
	}

	logger.Info("Core server started successfully", zap.String("core_url", coreURL))
	trayApp.SetConnectionState(tray.ConnectionStateConnecting)
}

// isServerRunning checks if the core mcpproxy server is running
func isServerRunning(baseURL string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(strings.TrimSuffix(baseURL, "/") + "/api/v1/servers")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 400
}

// startCoreServer starts the core mcpproxy server process
func startCoreServer(logger *zap.Logger, binaryPath string) error {
	cmd := exec.Command(binaryPath, "serve")
	cmd.Stdout = nil // Don't capture output to avoid blocking
	cmd.Stderr = nil
	cmd.Env = append(os.Environ(), "MCPP_ENABLE_TRAY=false")

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true, // Create new process group
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start mcpproxy server: %w", err)
	}

	go func() {
		if waitErr := cmd.Wait(); waitErr != nil {
			if logger != nil {
				logger.Error("mcpproxy server process exited with error", zap.Error(waitErr))
			}
		}
	}()

	return nil
}

// resolveCoreBinary locates or stages the core binary for launching.
func resolveCoreBinary(logger *zap.Logger) (string, error) {
	if override := strings.TrimSpace(os.Getenv("MCPPROXY_CORE_PATH")); override != "" {
		if info, err := os.Stat(override); err == nil && !info.IsDir() {
			return override, nil
		}
		return "", fmt.Errorf("MCPPROXY_CORE_PATH does not point to a valid binary: %s", override)
	}

	if managedPath, err := ensureManagedCoreBinary(logger); err == nil {
		return managedPath, nil
	} else if !errors.Is(err, errNoBundledCore) {
		return "", err
	}

	return findMcpproxyBinary()
}

// ensureManagedCoreBinary copies the bundled core binary into a writable location if necessary.
func ensureManagedCoreBinary(logger *zap.Logger) (string, error) {
	bundled, err := discoverBundledCore()
	if err != nil {
		return "", err
	}

	targetDir, err := getManagedBinDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create managed binary directory: %w", err)
	}

	targetPath := filepath.Join(targetDir, "mcpproxy")
	copyNeeded, err := shouldCopyBinary(bundled, targetPath)
	if err != nil {
		return "", err
	}
	if copyNeeded {
		if err := copyFile(bundled, targetPath); err != nil {
			return "", fmt.Errorf("failed to stage bundled core binary: %w", err)
		}
		if err := os.Chmod(targetPath, 0755); err != nil {
			return "", fmt.Errorf("failed to set permissions on managed core binary: %w", err)
		}
		if logger != nil {
			logger.Info("Staged bundled core binary", zap.String("source", bundled), zap.String("target", targetPath))
		}
	}

	return targetPath, nil
}

func discoverBundledCore() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}

	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve executable path: %w", err)
	}

	macOSDir := filepath.Dir(execPath)
	contentsDir := filepath.Dir(macOSDir)
	if !strings.HasSuffix(contentsDir, "Contents") {
		return "", errNoBundledCore
	}

	resourcesDir := filepath.Join(contentsDir, "Resources")
	candidate := filepath.Join(resourcesDir, "bin", "mcpproxy")
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate, nil
	}

	return "", errNoBundledCore
}

func getManagedBinDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	if runtime.GOOS == "darwin" {
		return filepath.Join(homeDir, "Library", "Application Support", "mcpproxy", "bin"), nil
	}

	return filepath.Join(homeDir, ".mcpproxy", "bin"), nil
}

func shouldCopyBinary(source, target string) (bool, error) {
	srcInfo, err := os.Stat(source)
	if err != nil {
		return false, fmt.Errorf("failed to stat source binary: %w", err)
	}

	dstInfo, err := os.Stat(target)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to stat target binary: %w", err)
	}

	if srcInfo.Size() != dstInfo.Size() {
		return true, nil
	}

	if srcInfo.ModTime().After(dstInfo.ModTime()) {
		return true, nil
	}

	return false, nil
}

func copyFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(target)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	return out.Sync()
}

// findMcpproxyBinary finds the mcpproxy binary in various locations
func findMcpproxyBinary() (string, error) {
	// Try different possible locations
	candidates := []string{
		"mcpproxy",                   // In PATH
		"./mcpproxy",                 // Current directory
		"../mcpproxy/mcpproxy",       // Development setup
		"../../mcpproxy",             // Nested dev setups
		"/usr/local/bin/mcpproxy",    // Homebrew location
		"/opt/homebrew/bin/mcpproxy", // Apple Silicon Homebrew
	}

	for _, path := range candidates {
		if resolved, err := exec.LookPath(path); err == nil {
			return resolved, nil
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			absPath, err := filepath.Abs(path)
			if err == nil {
				return absPath, nil
			}
		}
	}

	return "", fmt.Errorf("mcpproxy binary not found in any of the expected locations")
}

// waitForServer waits for the server to become available or until timeout/context cancellation occurs.
func waitForServer(ctx context.Context, baseURL string, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		if isServerRunning(baseURL) {
			return true
		}

		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return isServerRunning(baseURL)
		case <-ticker.C:
		}
	}
}
