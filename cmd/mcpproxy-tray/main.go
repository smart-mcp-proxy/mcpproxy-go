//go:build darwin

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
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
	defaultCoreURL   = "http://127.0.0.1:8080"
	errNoBundledCore = errors.New("no bundled core binary found")
	trayAPIKey       = "" // API key generated for core communication
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

// generateAPIKey creates a cryptographically secure random API key
func generateAPIKey() string {
	bytes := make([]byte, 32) // 32 bytes = 256 bits
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to less secure method if crypto/rand fails
		return fmt.Sprintf("tray_%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
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

	// Generate API key for secure communication between tray and core
	if trayAPIKey == "" {
		trayAPIKey = generateAPIKey()
		logger.Info("Generated API key for tray-core communication")
	}

	// Prepare API client and tray adapters immediately so the icon appears before the core is ready
	apiClient := api.NewClient(coreURL, logger.Sugar())
	// Set the API key BEFORE any API calls
	apiClient.SetAPIKey(trayAPIKey)
	logger.Info("API key configured for tray-core communication")

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
	go manageCoreProcess(ctx, logger, trayApp, apiClient, coreURL)

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

	if listen := normalizeListen(strings.TrimSpace(os.Getenv("MCPPROXY_TRAY_LISTEN"))); listen != "" {
		return "http://127.0.0.1" + listen
	}

	if port := strings.TrimSpace(os.Getenv("MCPPROXY_TRAY_PORT")); port != "" {
		return fmt.Sprintf("http://127.0.0.1:%s", port)
	}

	return defaultCoreURL
}

func shouldSkipCoreLaunch() bool {
	value := strings.TrimSpace(os.Getenv("MCPPROXY_TRAY_SKIP_CORE"))
	return value == "1" || strings.EqualFold(value, "true")
}

func manageCoreProcess(ctx context.Context, logger *zap.Logger, trayApp *tray.App, apiClient *api.Client, coreURL string) {
	if shouldSkipCoreLaunch() {
		logger.Info("Skipping core auto-launch due to MCPPROXY_TRAY_SKIP_CORE")
		trayApp.SetConnectionState(tray.ConnectionStateConnecting)
		return
	}

	if isServerRunning(coreURL) {
		logger.Info("Core mcpproxy server already running", zap.String("core_url", coreURL))
		// Check authentication for external core server
		if checkAPIAuthentication(coreURL, trayAPIKey) {
			logger.Info("External core server authentication successful")
			apiClient.SetAPIKey(trayAPIKey)
			trayApp.SetConnectionState(tray.ConnectionStateConnecting)
		} else {
			logger.Warn("External core server requires different API key")
			trayApp.SetConnectionState(tray.ConnectionStateAuthError)
		}
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

	args := buildCoreArgs(coreURL)
	cmd, waitCh, err := startCoreServer(logger, coreBinary, args)
	if err != nil {
		logger.Error("Failed to start core server", zap.String("binary", coreBinary), zap.Error(err))
		trayApp.SetConnectionState(tray.ConnectionStateDisconnected)
		return
	}

	go func() {
		select {
		case err := <-waitCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("mcpproxy server process exited", zap.Error(err))
			}
			trayApp.SetConnectionState(tray.ConnectionStateDisconnected)
		case <-ctx.Done():
			killTimer := terminateProcess(logger, cmd)
			if err := <-waitCh; err != nil && !errors.Is(err, context.Canceled) {
				logger.Info("mcpproxy server process terminated", zap.Error(err))
			}
			if killTimer != nil {
				killTimer.Stop()
			}
		}
	}()

	if !waitForServerAndCheckAuth(ctx, coreURL, trayAPIKey, 30*time.Second, trayApp, logger) {
		logger.Error("Core server failed to start within timeout or authentication failed", zap.String("core_url", coreURL))
		terminateProcess(logger, cmd)
		// Connection state already set by waitForServerAndCheckAuth
		return
	}

	logger.Info("Core server started successfully with authentication", zap.String("core_url", coreURL))

	// Set the API key in the client for secure communication
	if trayAPIKey != "" {
		apiClient.SetAPIKey(trayAPIKey)
		logger.Info("API key configured for tray-core communication")
	}

	trayApp.SetConnectionState(tray.ConnectionStateConnecting)
}

// isServerRunning checks if the core mcpproxy server is running using the unauthenticated health endpoint
func isServerRunning(baseURL string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(strings.TrimSuffix(baseURL, "/") + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 400
}

// checkAPIAuthentication verifies if API authentication is working
func checkAPIAuthentication(baseURL, apiKey string) bool {
	if apiKey == "" {
		return true // No authentication required
	}

	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest("GET", strings.TrimSuffix(baseURL, "/")+"/api/v1/servers", http.NoBody)
	if err != nil {
		return false
	}
	req.Header.Set("X-API-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode != 401
}

// startCoreServer starts the core mcpproxy server process
func startCoreServer(logger *zap.Logger, binaryPath string, args []string) (*exec.Cmd, <-chan error, error) {
	// Generate API key for secure communication between tray and core
	if trayAPIKey == "" {
		trayAPIKey = generateAPIKey()
		logger.Info("Generated API key for tray-core communication")
	}

	cmd := exec.Command(binaryPath, args...)
	cmd.Stdout = nil // Don't capture output to avoid blocking
	cmd.Stderr = nil

	// Build clean environment - filter out any existing MCPP_API_KEY to avoid conflicts
	env := []string{}
	for _, envVar := range os.Environ() {
		if !strings.HasPrefix(envVar, "MCPP_API_KEY=") {
			env = append(env, envVar)
		}
	}
	// Add our environment variables
	env = append(env,
		"MCPP_ENABLE_TRAY=false",
		fmt.Sprintf("MCPP_API_KEY=%s", trayAPIKey))
	cmd.Env = env

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true, // Create new process group
	}

	if logger != nil {
		logger.Info("Launching mcpproxy core",
			zap.String("binary", binaryPath),
			zap.Strings("args", args))
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("failed to start mcpproxy server: %w", err)
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
		close(waitCh)
	}()

	return cmd, waitCh, nil
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


// waitForServerAndCheckAuth waits for server and checks authentication status
func waitForServerAndCheckAuth(ctx context.Context, baseURL, apiKey string, timeout time.Duration, trayApp *tray.App, logger *zap.Logger) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		if isServerRunning(baseURL) {
			// Server is running, now check authentication
			if checkAPIAuthentication(baseURL, apiKey) {
				logger.Info("Server is running and authentication is working")
				return true
			} else {
				logger.Warn("Server is running but API authentication failed")
				trayApp.SetConnectionState(tray.ConnectionStateAuthError)
				return false
			}
		}

		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			// Final check on timeout
			if isServerRunning(baseURL) {
				if checkAPIAuthentication(baseURL, apiKey) {
					return true
				} else {
					trayApp.SetConnectionState(tray.ConnectionStateAuthError)
					return false
				}
			}
			return false
		case <-ticker.C:
		}
	}
}

func buildCoreArgs(coreURL string) []string {
	args := []string{"serve"}

	if cfg := strings.TrimSpace(os.Getenv("MCPPROXY_TRAY_CONFIG_PATH")); cfg != "" {
		args = append(args, "--config", cfg)
	}

	if listen := listenArgFromURL(coreURL); listen != "" {
		args = append(args, "--listen", listen)
	} else if listenEnv := normalizeListen(strings.TrimSpace(os.Getenv("MCPPROXY_TRAY_LISTEN"))); listenEnv != "" {
		args = append(args, "--listen", listenEnv)
	}

	if extra := strings.TrimSpace(os.Getenv("MCPPROXY_TRAY_EXTRA_ARGS")); extra != "" {
		args = append(args, strings.Fields(extra)...)
	}

	return args
}

func listenArgFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}

	port := u.Port()
	if port == "" {
		return ""
	}

	host := u.Hostname()
	if host == "" || host == "localhost" || host == "127.0.0.1" {
		// Always use localhost binding for security, never bind to all interfaces
		return "127.0.0.1:" + port
	}

	return net.JoinHostPort(host, port)
}

func normalizeListen(listen string) string {
	if listen == "" {
		return ""
	}

	if strings.HasPrefix(listen, "localhost:") {
		return strings.TrimPrefix(listen, "localhost")
	}

	if strings.HasPrefix(listen, "127.0.0.1:") {
		return strings.TrimPrefix(listen, "127.0.0.1")
	}

	if strings.HasPrefix(listen, ":") {
		return listen
	}

	if !strings.Contains(listen, ":") {
		return ":" + listen
	}

	return listen
}

func terminateProcess(logger *zap.Logger, cmd *exec.Cmd) *time.Timer {
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	pid := cmd.Process.Pid
	if logger != nil {
		logger.Info("Sending SIGTERM to core process", zap.Int("pid", pid))
	}

	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		if logger != nil {
			logger.Warn("Failed to send SIGTERM to core process", zap.Error(err))
		}
	}

	return time.AfterFunc(5*time.Second, func() {
		if logger != nil {
			logger.Warn("Force killing core process", zap.Int("pid", pid))
		}
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	})
}
