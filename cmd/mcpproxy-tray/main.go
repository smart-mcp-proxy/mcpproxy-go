//go:build darwin || windows

package main

import (
	"bytes"
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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"mcpproxy-go/cmd/mcpproxy-tray/internal/api"
	"mcpproxy-go/cmd/mcpproxy-tray/internal/monitor"
	"mcpproxy-go/cmd/mcpproxy-tray/internal/state"
	"mcpproxy-go/internal/tray"
)

const (
	platformDarwin  = "darwin"
	platformWindows = "windows"
)

var (
	version          = "development" // Set by build flags
	defaultCoreURL   = "http://127.0.0.1:8080"
	errNoBundledCore = errors.New("no bundled core binary found")
	trayAPIKey       = ""                  // API key generated for core communication
	shutdownComplete = make(chan struct{}) // Signal when shutdown is complete
	shutdownOnce     sync.Once

	errDockerPaused      = errors.New("docker engine paused")
	errDockerUnavailable = errors.New("docker engine unavailable")
)

// getLogDir returns the standard log directory for the current OS.
// Falls back to a temporary directory when a platform path cannot be resolved.
func getLogDir() string {
	fallback := filepath.Join(os.TempDir(), "mcpproxy", "logs")

	switch runtime.GOOS {
	case platformDarwin:
		if homeDir, err := os.UserHomeDir(); err == nil {
			return filepath.Join(homeDir, "Library", "Logs", "mcpproxy")
		}
	case platformWindows: // This case will never be reached due to build constraints, but kept for clarity
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

	// Check environment variables for configuration
	coreTimeout := getCoreTimeout()
	retryDelay := getRetryDelay()
	stateDebug := getStateDebug()

	if stateDebug {
		logger.Info("State machine debug mode enabled")
	}

	logger.Info("Tray configuration",
		zap.Duration("core_timeout", coreTimeout),
		zap.Duration("retry_delay", retryDelay),
		zap.Bool("state_debug", stateDebug),
		zap.Bool("skip_core", shouldSkipCoreLaunch()))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Resolve core configuration up front
	coreURL := resolveCoreURL()
	logger.Info("Resolved core URL", zap.String("core_url", coreURL))

	// Determine if we're using socket/pipe communication (which doesn't need API key)
	usingSocketCommunication := isSocketEndpoint(coreURL)

	// Setup API key for secure communication between tray and core
	// Skip API key generation for socket/pipe connections as they're trusted by default
	if trayAPIKey == "" && !usingSocketCommunication {
		// Check environment variable first (for consistency with core behavior)
		if envAPIKey := os.Getenv("MCPPROXY_API_KEY"); envAPIKey != "" {
			trayAPIKey = envAPIKey
			logger.Info("Using API key from environment variable for tray-core communication",
				zap.String("api_key_source", "MCPPROXY_API_KEY environment variable"),
				zap.String("api_key_prefix", maskAPIKey(trayAPIKey)))
		} else {
			trayAPIKey = generateAPIKey()
			logger.Info("Generated API key for tray-core communication",
				zap.String("api_key_source", "auto-generated"),
				zap.String("api_key_prefix", maskAPIKey(trayAPIKey)))
		}
	} else if usingSocketCommunication {
		logger.Info("Using socket/pipe communication - API key not required",
			zap.String("connection_type", "socket"))
	}

	// Create state machine
	stateMachine := state.NewMachine(logger.Sugar())

	// Create enhanced API client with better connection management
	apiClient := api.NewClient(coreURL, logger.Sugar())
	apiClient.SetAPIKey(trayAPIKey)

	// Create launcher variable that will be set after tray app is created
	var launcher *CoreProcessLauncher
	var trayApp *tray.App

	// Create tray application early so icon appears
	shutdownFunc := func() {
		firstCaller := false
		shutdownOnce.Do(func() {
			firstCaller = true

			go func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Error("Recovered from panic during shutdown", zap.Any("panic", r))
					}
					close(shutdownComplete)
				}()

				logger.Info("Tray shutdown requested")

				// Notify the state machine so it stops launching or reconnecting cores.
				stateMachine.SendEvent(state.EventShutdown)

				// Cancel the shared context early so tray background workers stop emitting logs.
				logger.Info("Cancelling tray context")
				cancel()

				// Shutdown launcher (stops SSE, health monitor, kills core)
				if launcher != nil {
					launcher.handleShutdown()
				}

				// Shutdown state machine (waits up to its internal timeout)
				stateMachine.Shutdown()

				// Give tray.Run() goroutine a moment to notice cancellation before requesting explicit quit.
				time.Sleep(50 * time.Millisecond)

				// Finally, request the tray UI to quit (safe even if already quitting)
				if trayApp != nil {
					logger.Info("Quitting system tray")
					trayApp.Quit()
				}

				logger.Info("Shutdown sequence finished")
			}()
		})

		if !firstCaller {
			// Wait until the first shutdown completes so callers don't proceed early.
			<-shutdownComplete
		}
	}

	trayApp = tray.NewWithAPIClient(api.NewServerAdapter(apiClient), apiClient, logger.Sugar(), version, shutdownFunc)

	// Start the state machine (without automatic initial event)
	stateMachine.Start()

	// Launch core management with state machine
	launcher = NewCoreProcessLauncher(
		coreURL,
		logger.Sugar(),
		stateMachine,
		apiClient,
		trayApp,
		coreTimeout,
	)

	// Send the appropriate initial event based on:
	// 1. SKIP_CORE environment variable (explicit override)
	// 2. Existing running core detection (auto-detect)
	// 3. Default: launch core as subprocess

	// Start launcher FIRST to ensure it subscribes to transitions before events are sent
	go launcher.Start(ctx)

	// Give the launcher goroutine a moment to subscribe to state transitions
	// This prevents race condition where initial event is sent before subscription is ready
	time.Sleep(10 * time.Millisecond)

	if shouldSkipCoreLaunch() {
		logger.Info("Skipping core launch (MCPPROXY_TRAY_SKIP_CORE=1)")
		stateMachine.SendEvent(state.EventSkipCore)
	} else if isCoreAlreadyRunning(coreURL, logger) {
		logger.Info("Detected existing running core, will use it instead of launching subprocess",
			zap.String("core_url", coreURL))
		stateMachine.SendEvent(state.EventSkipCore)
	} else {
		logger.Info("No running core detected, will launch new core process")
		stateMachine.SendEvent(state.EventStart)
	}

	// Handle signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Info("Received shutdown signal")

		// Use the same shutdown flow as Quit menu item
		shutdownFunc()
	}()

	logger.Info("Starting tray event loop")
	if err := trayApp.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("Tray application error", zap.Error(err))
	}

	// Wait for shutdown to complete (with timeout)
	select {
	case <-shutdownComplete:
		logger.Info("Shutdown completed successfully")
	case <-time.After(5 * time.Second):
		logger.Warn("Shutdown timeout - forcing exit")
	}

	// Final cleanup
	stateMachine.Shutdown()

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
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
		EncodeName:     zapcore.FullNameEncoder,
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
	// Priority 1: Explicit override via environment variable
	if override := strings.TrimSpace(os.Getenv("MCPPROXY_CORE_URL")); override != "" {
		return override
	}

	// Priority 2: Try socket/pipe communication first (preferred for local tray-core communication)
	// This provides better security (no API key needed) and performance
	// Note: We return the socket path even if it doesn't exist yet, because:
	//   - When launching core: Core will create the socket
	//   - When connecting: isCoreAlreadyRunning() will check existence and fall back if needed
	socketPath := api.DetectSocketPath("") // Empty dataDir uses default ~/.mcpproxy
	if socketPath != "" {
		return socketPath
	}

	// Priority 3: Fall back to TCP (HTTP/HTTPS)
	// Determine protocol based on TLS setting
	protocol := "http"
	if strings.TrimSpace(os.Getenv("MCPPROXY_TLS_ENABLED")) == "true" {
		protocol = "https"
	}

	if listen := normalizeListen(strings.TrimSpace(os.Getenv("MCPPROXY_TRAY_LISTEN"))); listen != "" {
		return protocol + "://127.0.0.1" + listen
	}

	if port := strings.TrimSpace(os.Getenv("MCPPROXY_TRAY_PORT")); port != "" {
		return fmt.Sprintf("%s://127.0.0.1:%s", protocol, port)
	}

	// Update default URL based on TLS setting
	if protocol == "https" {
		return "https://127.0.0.1:8080"
	}
	return defaultCoreURL
}

func shouldSkipCoreLaunch() bool {
	value := strings.TrimSpace(os.Getenv("MCPPROXY_TRAY_SKIP_CORE"))
	return value == "1" || strings.EqualFold(value, "true")
}

// isSocketEndpoint returns true if the endpoint uses socket/pipe communication
func isSocketEndpoint(endpoint string) bool {
	return strings.HasPrefix(endpoint, "unix://") || strings.HasPrefix(endpoint, "npipe://")
}

// isCoreAlreadyRunning checks if a core instance is already running and healthy
// Returns true if core is accessible and responding to health checks
func isCoreAlreadyRunning(coreURL string, logger *zap.Logger) bool {
	// For socket endpoints, first verify the socket file exists
	if isSocketEndpoint(coreURL) {
		parsed, err := url.Parse(coreURL)
		if err != nil {
			return false
		}

		if parsed.Scheme == "unix" {
			socketPath := parsed.Path
			if socketPath == "" {
				socketPath = parsed.Opaque
			}
			// Check if socket file exists and is a socket
			info, err := os.Stat(socketPath)
			if err != nil {
				logger.Debug("Socket file does not exist", zap.String("path", socketPath))
				return false
			}
			// Check if it's a socket (not a regular file)
			if info.Mode()&os.ModeSocket == 0 {
				logger.Debug("Path exists but is not a socket", zap.String("path", socketPath))
				return false
			}
		}
	}

	// Try to connect and perform health check
	client := &http.Client{
		Timeout: 2 * time.Second, // Short timeout for quick detection
	}

	// Create custom dialer if using socket
	if isSocketEndpoint(coreURL) {
		dialer, baseURL, err := api.CreateDialer(coreURL)
		if err != nil {
			logger.Debug("Failed to create dialer for core health check", zap.Error(err))
			return false
		}

		transport := &http.Transport{}
		if dialer != nil {
			transport.DialContext = dialer
		}
		client.Transport = transport

		// Use the base URL for the request
		coreURL = baseURL
	}

	// Try the /ready endpoint (lightweight health check)
	healthURL := fmt.Sprintf("%s/ready", strings.TrimSuffix(coreURL, "/"))
	resp, err := client.Get(healthURL)
	if err != nil {
		logger.Debug("Core health check failed",
			zap.String("url", healthURL),
			zap.Error(err))
		return false
	}
	defer resp.Body.Close()

	// Check if response is successful
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		logger.Debug("Core health check successful",
			zap.String("url", healthURL),
			zap.Int("status", resp.StatusCode))
		return true
	}

	logger.Debug("Core health check returned non-success status",
		zap.String("url", healthURL),
		zap.Int("status", resp.StatusCode))
	return false
}

// Legacy functions removed - replaced by state machine architecture

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

	if runtime.GOOS == platformDarwin {
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

// findMcpproxyBinary resolves the core binary deterministically, preferring
// well-known locations before falling back to PATH lookups.
func findMcpproxyBinary() (string, error) {
	var candidates []string
	seen := make(map[string]struct{})
	addCandidate := func(path string) {
		if path == "" {
			return
		}
		clean := filepath.Clean(path)
		if _, ok := seen[clean]; ok {
			return
		}
		seen[clean] = struct{}{}
		candidates = append(candidates, clean)
	}

	// 1. Paths derived from the tray executable (common during development builds).
	if execPath, err := os.Executable(); err == nil {
		if resolvedExec, err := filepath.EvalSymlinks(execPath); err == nil {
			execDir := filepath.Dir(resolvedExec)
			addCandidate(filepath.Join(execDir, "mcpproxy"))
			addCandidate(filepath.Join(filepath.Dir(execDir), "mcpproxy"))
			addCandidate(filepath.Join(filepath.Dir(filepath.Dir(execDir)), "mcpproxy"))
			addCandidate(filepath.Join(filepath.Dir(execDir), "mcpproxy", "mcpproxy"))
		}
	}

	// 2. Working-directory relative binary (local dev workflow).
	addCandidate(filepath.Join(".", "mcpproxy"))
	if runtime.GOOS == platformWindows {
		addCandidate(filepath.Join(".", "mcpproxy-windows-amd64"))
	}

	// 3. Managed installation directories (Application Support on macOS).
	if homeDir, err := os.UserHomeDir(); err == nil {
		addCandidate(filepath.Join(homeDir, ".mcpproxy", "bin", "mcpproxy"))
		if runtime.GOOS == platformDarwin {
			addCandidate(filepath.Join(homeDir, "Library", "Application Support", "mcpproxy", "bin", "mcpproxy"))
		}
	}

	// 4. Common package manager locations.
	addCandidate("/opt/homebrew/bin/mcpproxy")
	addCandidate("/usr/local/bin/mcpproxy")

	for _, candidate := range candidates {
		if resolved, ok := resolveExecutableCandidate(candidate); ok {
			return resolved, nil
		}
	}

	// 5. Final fallback to PATH search.
	if resolved, err := exec.LookPath("mcpproxy"); err == nil {
		return resolved, nil
	}

	return "", fmt.Errorf("mcpproxy binary not found; checked %v and PATH", candidates)
}

func resolveExecutableCandidate(path string) (string, bool) {
	var abs string
	if runtime.GOOS == platformWindows {
		candidate := path
		lower := strings.ToLower(candidate)
		// try adding .exe if not present
		if !strings.HasSuffix(lower, ".exe") {
			if filepath.IsAbs(candidate) {
				candidate = candidate + ".exe"
			} else {
				candidate = candidate + ".exe"
			}
		}

		if filepath.IsAbs(candidate) {
			abs = candidate
		} else {
			var err error
			abs, err = filepath.Abs(candidate)
			if err != nil {
				return "", false
			}
		}

		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			return "", false
		}
		// On Windows, execute bit is not meaningful; presence is enough
		return abs, true
	}

	if filepath.IsAbs(path) {
		abs = path
	} else {
		var err error
		abs, err = filepath.Abs(path)
		if err != nil {
			return "", false
		}
	}

	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return "", false
	}

	if info.Mode()&0o111 == 0 {
		return "", false
	}

	return abs, true
}

// Legacy health check functions removed - replaced by monitor.HealthMonitor

func buildCoreArgs(coreURL string) []string {
	args := []string{"serve"}

	if cfg := strings.TrimSpace(os.Getenv("MCPPROXY_TRAY_CONFIG_PATH")); cfg != "" {
		args = append(args, "--config", cfg)
	}

	// IMPORTANT: Only add --listen for TCP/HTTP connections
	// Socket/pipe connections should NOT have --listen (core enables socket by default)
	if !isSocketEndpoint(coreURL) {
		if listen := listenArgFromURL(coreURL); listen != "" {
			args = append(args, "--listen", listen)
		} else if listenEnv := normalizeListen(strings.TrimSpace(os.Getenv("MCPPROXY_TRAY_LISTEN"))); listenEnv != "" {
			args = append(args, "--listen", listenEnv)
		}
	}

	if extra := strings.TrimSpace(os.Getenv("MCPPROXY_TRAY_EXTRA_ARGS")); extra != "" {
		args = append(args, strings.Fields(extra)...)
	}

	return args
}

func wrapCoreLaunchWithShell(coreBinary string, args []string) (string, []string, error) {
	shellPath, err := selectUserShell()
	if err != nil {
		return "", nil, err
	}

	command := buildShellExecCommand(coreBinary, args)
	return shellPath, []string{"-l", "-c", command}, nil
}

func selectUserShell() (string, error) {
	candidates := []string{}
	if shellEnv := strings.TrimSpace(os.Getenv("SHELL")); shellEnv != "" {
		candidates = append(candidates, shellEnv)
	}
	candidates = append(candidates,
		"/bin/zsh",
		"/bin/bash",
		"/bin/sh",
	)

	seen := make(map[string]struct{})
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}

		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("no usable shell found for core launch")
}

func buildShellExecCommand(binary string, args []string) string {
	quoted := make([]string, 0, len(args)+1)
	quoted = append(quoted, shellQuote(binary))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}

	return "exec " + strings.Join(quoted, " ")
}

func shellQuote(arg string) string {
	if arg == "" {
		return "''"
	}

	var builder strings.Builder
	builder.Grow(len(arg) + 2)
	builder.WriteByte('\'')
	for i := 0; i < len(arg); i++ {
		if arg[i] == '\'' {
			builder.WriteString("'\\''")
		} else {
			builder.WriteByte(arg[i])
		}
	}
	builder.WriteByte('\'')
	return builder.String()
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

// Legacy process termination removed - replaced by monitor.ProcessMonitor

// getCoreTimeout returns the configured core startup timeout
func getCoreTimeout() time.Duration {
	if timeoutStr := strings.TrimSpace(os.Getenv("MCPPROXY_TRAY_CORE_TIMEOUT")); timeoutStr != "" {
		if timeout, err := strconv.Atoi(timeoutStr); err == nil && timeout > 0 {
			return time.Duration(timeout) * time.Second
		}
	}
	return 30 * time.Second // Default timeout
}

// getRetryDelay returns the configured retry delay
func getRetryDelay() time.Duration {
	if delayStr := strings.TrimSpace(os.Getenv("MCPPROXY_TRAY_RETRY_DELAY")); delayStr != "" {
		if delay, err := strconv.Atoi(delayStr); err == nil && delay > 0 {
			return time.Duration(delay) * time.Second
		}
	}
	return 5 * time.Second // Default delay
}

// getStateDebug returns whether state machine debug mode is enabled
func getStateDebug() bool {
	value := strings.TrimSpace(os.Getenv("MCPPROXY_TRAY_STATE_DEBUG"))
	return value == "1" || strings.EqualFold(value, "true")
}

// maskAPIKey masks an API key for logging (shows first and last 4 chars)
func maskAPIKey(apiKey string) string {
	if len(apiKey) <= 8 {
		return "****"
	}
	return apiKey[:4] + "****" + apiKey[len(apiKey)-4:]
}

// CoreProcessLauncher manages the mcpproxy core process with state machine integration
type CoreProcessLauncher struct {
	coreURL      string
	logger       *zap.SugaredLogger
	stateMachine *state.Machine
	apiClient    *api.Client
	trayApp      *tray.App
	coreTimeout  time.Duration

	processMonitor *monitor.ProcessMonitor
	healthMonitor  *monitor.HealthMonitor

	dockerRetryMu          sync.Mutex
	dockerRetryCancel      context.CancelFunc
	dockerReconnectPending bool
}

// NewCoreProcessLauncher creates a new core process launcher
func NewCoreProcessLauncher(
	coreURL string,
	logger *zap.SugaredLogger,
	stateMachine *state.Machine,
	apiClient *api.Client,
	trayApp *tray.App,
	coreTimeout time.Duration,
) *CoreProcessLauncher {
	return &CoreProcessLauncher{
		coreURL:      coreURL,
		logger:       logger,
		stateMachine: stateMachine,
		apiClient:    apiClient,
		trayApp:      trayApp,
		coreTimeout:  coreTimeout,
	}
}

// Start starts the core process launcher and state machine integration
func (cpl *CoreProcessLauncher) Start(ctx context.Context) {
	cpl.logger.Info("Core process launcher starting")

	// Subscribe to state machine transitions
	transitionsCh := cpl.stateMachine.Subscribe()

	// Handle state transitions
	go cpl.handleStateTransitions(ctx, transitionsCh)

	// The initial event (EventStart or EventSkipCore) is now sent from main.go
	// based on the shouldSkipCoreLaunch() check, so we just wait for state transitions
}

// handleStateTransitions processes state machine transitions
func (cpl *CoreProcessLauncher) handleStateTransitions(ctx context.Context, transitionsCh <-chan state.Transition) {
	for {
		select {
		case <-ctx.Done():
			cpl.logger.Debug("State transition handler context cancelled")
			return

		case transition := <-transitionsCh:
			cpl.logger.Infow("State transition",
				"from", transition.From,
				"to", transition.To,
				"event", transition.Event,
				"timestamp", transition.Timestamp.Format(time.RFC3339))

			// Update tray connection state based on machine state
			cpl.updateTrayConnectionState(transition.To)

			// Handle specific state entries
			switch transition.To {
			case state.StateLaunchingCore:
				go cpl.safeHandleLaunchCore(ctx)

			case state.StateWaitingForCore:
				go cpl.safeHandleWaitForCore(ctx)

			case state.StateConnectingAPI:
				go cpl.safeHandleConnectAPI(ctx)

			case state.StateConnected:
				cpl.handleConnected()

			case state.StateReconnecting:
				go cpl.safeHandleReconnecting(ctx)

			case state.StateCoreErrorPortConflict:
				cpl.handlePortConflictError()

			case state.StateCoreErrorDBLocked:
				cpl.handleDBLockedError()

			case state.StateCoreErrorDocker:
				cpl.handleDockerUnavailable(ctx)

			case state.StateCoreRecoveringDocker:
				cpl.handleDockerRecovering(ctx)

			case state.StateCoreErrorConfig:
				cpl.handleConfigError()

			case state.StateCoreErrorGeneral:
				cpl.handleGeneralError()

			case state.StateShuttingDown:
				// handleShutdown() is called directly in shutdownFunc and signal handler
				// to ensure it executes before context cancellation kills the goroutines.
				// No action needed here.
			}
		}
	}
}

// updateTrayConnectionState updates the tray app's connection state based on the state machine state
func (cpl *CoreProcessLauncher) updateTrayConnectionState(machineState state.State) {
	var trayState tray.ConnectionState

	switch machineState {
	case state.StateInitializing:
		trayState = tray.ConnectionStateInitializing
	case state.StateLaunchingCore:
		trayState = tray.ConnectionStateStartingCore
	case state.StateWaitingForCore:
		trayState = tray.ConnectionStateStartingCore
	case state.StateConnectingAPI:
		trayState = tray.ConnectionStateConnecting
	case state.StateConnected:
		trayState = tray.ConnectionStateConnected
	case state.StateReconnecting:
		trayState = tray.ConnectionStateReconnecting
	// ADD: Map specific error states to detailed tray states
	case state.StateCoreErrorPortConflict:
		trayState = tray.ConnectionStateErrorPortConflict
	case state.StateCoreErrorDBLocked:
		trayState = tray.ConnectionStateErrorDBLocked
	case state.StateCoreErrorDocker:
		trayState = tray.ConnectionStateErrorDocker
	case state.StateCoreRecoveringDocker:
		trayState = tray.ConnectionStateRecoveringDocker
	case state.StateCoreErrorConfig:
		trayState = tray.ConnectionStateErrorConfig
	case state.StateCoreErrorGeneral:
		trayState = tray.ConnectionStateErrorGeneral
	case state.StateFailed:
		trayState = tray.ConnectionStateFailed
	default:
		trayState = tray.ConnectionStateDisconnected
	}

	cpl.trayApp.SetConnectionState(trayState)
}

// safeHandleLaunchCore wraps handleLaunchCore with panic recovery
func (cpl *CoreProcessLauncher) safeHandleLaunchCore(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("panic in handleLaunchCore: %v", r)
			cpl.logger.Error("PANIC recovered in handleLaunchCore", "panic", r, "error", err)
			cpl.stateMachine.SetError(err)
			cpl.stateMachine.SendEvent(state.EventGeneralError)
		}
	}()
	cpl.handleLaunchCore(ctx)
}

// safeHandleWaitForCore wraps handleWaitForCore with panic recovery
func (cpl *CoreProcessLauncher) safeHandleWaitForCore(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("panic in handleWaitForCore: %v", r)
			cpl.logger.Error("PANIC recovered in handleWaitForCore", "panic", r, "error", err)
			cpl.stateMachine.SetError(err)
			cpl.stateMachine.SendEvent(state.EventGeneralError)
		}
	}()
	cpl.handleWaitForCore(ctx)
}

// safeHandleConnectAPI wraps handleConnectAPI with panic recovery
func (cpl *CoreProcessLauncher) safeHandleConnectAPI(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("panic in handleConnectAPI: %v", r)
			cpl.logger.Error("PANIC recovered in handleConnectAPI", "panic", r, "error", err)
			cpl.stateMachine.SetError(err)
			cpl.stateMachine.SendEvent(state.EventConnectionLost)
		}
	}()
	cpl.handleConnectAPI(ctx)
}

// safeHandleReconnecting wraps handleReconnecting with panic recovery
func (cpl *CoreProcessLauncher) safeHandleReconnecting(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("panic in handleReconnecting: %v", r)
			cpl.logger.Error("PANIC recovered in handleReconnecting", "panic", r, "error", err)
			cpl.stateMachine.SetError(err)
			cpl.stateMachine.SendEvent(state.EventConnectionLost)
		}
	}()
	cpl.handleReconnecting(ctx)
}

// handleLaunchCore handles launching the core process
func (cpl *CoreProcessLauncher) handleLaunchCore(ctx context.Context) {
	cpl.logger.Info("Launching mcpproxy core process")

	// Stop any pending Docker retry loop before attempting a new launch
	cpl.cancelDockerRetry()

	// Ensure Docker engine is available before launching core (most upstreams depend on it)
	if runtime.GOOS == platformDarwin || runtime.GOOS == platformWindows {
		if err := cpl.ensureDockerAvailable(ctx); err != nil {
			cpl.logger.Error("Docker engine unavailable", zap.Error(err))
			cpl.stateMachine.SetError(err)
			cpl.stateMachine.SendEvent(state.EventDockerUnavailable)
			return
		}
	}

	// Stop existing process monitor if running
	if cpl.processMonitor != nil {
		cpl.processMonitor.Shutdown()
		cpl.processMonitor = nil
	}

	// Resolve core binary path
	coreBinary, err := resolveCoreBinary(cpl.logger.Desugar())
	if err != nil {
		cpl.logger.Error("Failed to resolve core binary", "error", err)
		cpl.stateMachine.SetError(err)
		cpl.stateMachine.SendEvent(state.EventGeneralError)
		return
	}

	// Build command arguments and environment
	args := buildCoreArgs(cpl.coreURL)
	env := cpl.buildCoreEnvironment()

	launchBinary := coreBinary
	launchArgs := args
	wrappedWithShell := false

	if shellBinary, shellArgs, err := wrapCoreLaunchWithShell(coreBinary, args); err != nil {
		cpl.logger.Warn("Falling back to direct core launch", "error", err)
	} else {
		launchBinary = shellBinary
		launchArgs = shellArgs
		wrappedWithShell = true
	}

	cpl.logger.Info("Starting core process",
		"binary", launchBinary,
		"args", cpl.maskSensitiveArgs(launchArgs),
		"env_count", len(env),
		"wrapped_with_shell", wrappedWithShell)

	if wrappedWithShell {
		cpl.logger.Debug("Wrapped core command",
			"core_binary", coreBinary,
			"core_args", cpl.maskSensitiveArgs(args))
	}

	// Create process configuration
	// Note: CaptureOutput is false because core logs to its own files
	// Tray only monitors exit codes for failure detection
	processConfig := monitor.ProcessConfig{
		Binary:        launchBinary,
		Args:          launchArgs,
		Env:           env,
		StartTimeout:  cpl.coreTimeout,
		CaptureOutput: false,
	}

	// Create process monitor
	cpl.processMonitor = monitor.NewProcessMonitor(&processConfig, cpl.logger, cpl.stateMachine)

	// Start the process
	if err := cpl.processMonitor.Start(); err != nil {
		cpl.logger.Error("Failed to start core process", "error", err)
		cpl.stateMachine.SetError(err)
		cpl.stateMachine.SendEvent(state.EventGeneralError)
		return
	}

	// The process monitor will send EventCoreStarted when the process starts successfully
}

// handleWaitForCore handles waiting for the core to become ready
func (cpl *CoreProcessLauncher) handleWaitForCore(_ context.Context) {
	cpl.logger.Info("Waiting for core to become ready")

	// Create health monitor if not exists
	if cpl.healthMonitor == nil {
		cpl.healthMonitor = monitor.NewHealthMonitor(cpl.coreURL, cpl.logger, cpl.stateMachine)
		cpl.healthMonitor.Start()
	}

	// Wait for core to become ready
	go func() {
		if err := cpl.healthMonitor.WaitForReady(); err != nil {
			cpl.logger.Error("Core failed to become ready", "error", err)
			cpl.stateMachine.SetError(err)
			cpl.stateMachine.SendEvent(state.EventTimeout)
		}
		// If successful, the health monitor will send EventCoreReady
	}()
}

// handleConnectAPI handles connecting to the core API
func (cpl *CoreProcessLauncher) handleConnectAPI(ctx context.Context) {
	cpl.logger.Info("Connecting to core API")

	// First, do a quick readiness check to verify the API is reachable
	// This provides instant feedback to the user
	if err := cpl.verifyAPIReadiness(ctx); err != nil {
		cpl.logger.Error("API readiness check failed", "error", err)
		cpl.stateMachine.SetError(err)
		cpl.stateMachine.SendEvent(state.EventConnectionLost)
		return
	}

	// API is ready! Send EventAPIConnected immediately for fast status update
	cpl.logger.Info("API is ready, transitioning to connected state")
	cpl.stateMachine.SendEvent(state.EventAPIConnected)

	// Start SSE connection in background for real-time updates
	if err := cpl.apiClient.StartSSE(ctx); err != nil {
		cpl.logger.Error("Failed to start SSE connection", "error", err)
		// Don't send EventConnectionLost here - we're already connected via HTTP
		// Just log the error and SSE will retry in the background
	}

	// Subscribe to API client connection state changes
	// Pass alreadyConnected=true since we verified API is ready via HTTP
	// This tells the monitor to ignore SSE connection failures
	go cpl.monitorAPIConnection(ctx, true)
}

// verifyAPIReadiness does a quick check to verify the core API is responding
func (cpl *CoreProcessLauncher) verifyAPIReadiness(ctx context.Context) error {
	// Try up to 3 times with short delays
	for attempt := 1; attempt <= 3; attempt++ {
		// Simple GET /ready check
		err := cpl.apiClient.GetReady(ctx)
		if err == nil {
			cpl.logger.Infow("API readiness verified", "attempt", attempt)
			return nil
		}

		cpl.logger.Warn("API readiness check failed",
			"attempt", attempt,
			"error", err)

		if attempt < 3 {
			// Short delay before retry
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(500 * time.Millisecond):
				// Continue to next attempt
			}
		}
	}

	return fmt.Errorf("API not ready after 3 attempts")
}

// monitorAPIConnection monitors the API client connection state
// When alreadyConnected is true, this only monitors for successful SSE connections
// and ignores connection failures (since we're already connected via HTTP)
func (cpl *CoreProcessLauncher) monitorAPIConnection(ctx context.Context, alreadyConnected bool) {
	connectionStateCh := cpl.apiClient.ConnectionStateChannel()

	for {
		select {
		case <-ctx.Done():
			return
		case connState, ok := <-connectionStateCh:
			if !ok {
				return
			}
			switch connState {
			case tray.ConnectionStateConnected:
				// SSE connection established successfully
				// If we weren't already connected, send EventAPIConnected now
				if !alreadyConnected {
					cpl.stateMachine.SendEvent(state.EventAPIConnected)
				}
			case tray.ConnectionStateReconnecting, tray.ConnectionStateDisconnected:
				// SSE connection lost or reconnecting
				// Only send EventConnectionLost if we were relying on SSE for connection
				// If we're already connected via HTTP, ignore SSE failures
				if !alreadyConnected {
					cpl.stateMachine.SendEvent(state.EventConnectionLost)
				}
			}
		}
	}
}

// handleConnected handles the connected state
func (cpl *CoreProcessLauncher) handleConnected() {
	cpl.logger.Info("Core process fully connected and operational")

	if cpl.consumeDockerReconnectPending() {
		go cpl.triggerForceReconnect("docker_recovered")
	}
}

// handleReconnecting handles reconnection attempts
func (cpl *CoreProcessLauncher) handleReconnecting(_ context.Context) {
	cpl.logger.Info("Attempting to reconnect to core")
	// The state machine will handle retry logic automatically
}

// handlePortConflictError handles port conflict errors
func (cpl *CoreProcessLauncher) handlePortConflictError() {
	cpl.logger.Warn("Core failed due to port conflict")
	// Attempt automatic port resolution on Windows/macOS
	// 1) Parse current coreURL and extract port
	u, err := url.Parse(cpl.coreURL)
	if err != nil {
		cpl.logger.Error("Failed to parse coreURL for port conflict handling", "core_url", cpl.coreURL, "error", err)
		return
	}
	portStr := u.Port()
	if portStr == "" {
		portStr = "8080"
	}
	baseHost := u.Hostname()
	if baseHost == "" {
		baseHost = "127.0.0.1"
	}
	// 2) Find next available port
	startPort, _ := strconv.Atoi(portStr)
	newPort, err := findNextAvailablePort(startPort+1, startPort+50)
	if err != nil {
		cpl.logger.Error("Failed to find available port after conflict", "start_port", startPort, "error", err)
		return
	}
	// 3) Update coreURL and restart flow
	u.Host = net.JoinHostPort(baseHost, strconv.Itoa(newPort))
	cpl.coreURL = u.String()
	cpl.logger.Info("Auto-selected alternate port after conflict", "new_core_url", cpl.coreURL)

	// Stop monitors so they can be recreated with new URL
	if cpl.healthMonitor != nil {
		cpl.healthMonitor.Stop()
		cpl.healthMonitor = nil
	}
	if cpl.processMonitor != nil {
		cpl.processMonitor.Shutdown()
		cpl.processMonitor = nil
	}
	// Trigger retry which will launch core with updated args based on coreURL
	cpl.stateMachine.SendEvent(state.EventRetry)
}

// findNextAvailablePort scans a range and returns the first free port on localhost
func findNextAvailablePort(start, end int) (int, error) {
	if start < 1 {
		start = 1
	}
	if end <= start {
		end = start + 50
	}
	for p := start; p <= end; p++ {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
		if err == nil {
			_ = ln.Close()
			return p, nil
		}
	}
	return 0, fmt.Errorf("no free port in range %d-%d", start, end)
}

// handleDockerRecovering handles the Docker recovery state
// This state provides user feedback while we're restarting the core after Docker recovery
func (cpl *CoreProcessLauncher) handleDockerRecovering(ctx context.Context) {
	cpl.logger.Info("Docker recovery state - preparing to launch core")

	// Small delay to ensure Docker is fully stable
	time.Sleep(500 * time.Millisecond)

	// Transition to launching core
	cpl.stateMachine.SendEvent(state.EventRetry)
}

// handleDockerUnavailable handles scenarios where Docker Desktop is paused or unavailable.
// Uses exponential backoff to avoid wasting resources while waiting for Docker recovery.
func (cpl *CoreProcessLauncher) handleDockerUnavailable(ctx context.Context) {
	lastErr := cpl.stateMachine.GetLastError()
	if lastErr != nil {
		cpl.logger.Warn("Docker engine unavailable - waiting for recovery", zap.Error(lastErr))
	} else {
		cpl.logger.Warn("Docker engine unavailable - waiting for recovery")
	}

	// Show notification that Docker recovery has started
	if err := tray.ShowDockerRecoveryStarted(); err != nil {
		cpl.logger.Warn("Failed to show Docker recovery notification", zap.Error(err))
	}

	cpl.dockerRetryMu.Lock()
	if cpl.dockerRetryCancel != nil {
		cpl.dockerRetryCancel()
	}
	retryCtx, cancel := context.WithCancel(ctx)
	cpl.dockerRetryCancel = cancel
	cpl.dockerRetryMu.Unlock()

	go func() {
		// Exponential backoff intervals: fast when Docker just paused, slower when off for longer
		intervals := []time.Duration{
			2 * time.Second,   // Immediate retry (Docker just paused)
			5 * time.Second,   // Quick retry
			10 * time.Second,  // Normal retry
			30 * time.Second,  // Slow retry
			60 * time.Second,  // Very slow retry (max backoff)
		}

		attempt := 0
		startTime := time.Now()

		for {
			// Calculate current interval (stay at max after reaching it)
			currentInterval := intervals[min(attempt, len(intervals)-1)]

			select {
			case <-retryCtx.Done():
				return
			case <-time.After(currentInterval):
				if err := cpl.ensureDockerAvailable(retryCtx); err == nil {
					elapsed := time.Since(startTime)
					cpl.logger.Info("Docker engine available - transitioning to recovery state",
						zap.Int("attempts", attempt+1),
						zap.Duration("total_wait", elapsed))

					// Show notification that Docker is back online
					// Note: Full recovery notification will be shown after servers reconnect
					cpl.setDockerReconnectPending(true)
					cpl.cancelDockerRetry()
					// Transition to recovering state instead of directly retrying
					cpl.stateMachine.SendEvent(state.EventDockerRecovered)
					return
				} else if err != nil {
					cpl.logger.Debug("Docker still unavailable",
						zap.Int("attempt", attempt+1),
						zap.Duration("next_check_in", intervals[min(attempt+1, len(intervals)-1)]),
						zap.Error(err))
				}
				attempt++
			}
		}
	}()
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// cancelDockerRetry stops any pending Docker retry loop.
func (cpl *CoreProcessLauncher) cancelDockerRetry() {
	cpl.dockerRetryMu.Lock()
	if cpl.dockerRetryCancel != nil {
		cpl.dockerRetryCancel()
		cpl.dockerRetryCancel = nil
	}
	cpl.dockerRetryMu.Unlock()
}

func (cpl *CoreProcessLauncher) setDockerReconnectPending(pending bool) {
	cpl.dockerRetryMu.Lock()
	cpl.dockerReconnectPending = pending
	cpl.dockerRetryMu.Unlock()
}

func (cpl *CoreProcessLauncher) consumeDockerReconnectPending() bool {
	cpl.dockerRetryMu.Lock()
	pending := cpl.dockerReconnectPending
	if pending {
		cpl.dockerReconnectPending = false
	}
	cpl.dockerRetryMu.Unlock()
	return pending
}

// ensureDockerAvailable verifies Docker Desktop is running and responsive.
func (cpl *CoreProcessLauncher) ensureDockerAvailable(ctx context.Context) error {
	checkCtx := ctx
	if checkCtx == nil {
		checkCtx = context.Background()
	}
	timeoutCtx, cancel := context.WithTimeout(checkCtx, 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "docker", "info", "--format", "{{json .ServerVersion}}")
	var stderr bytes.Buffer
	cmd.Stdout = &bytes.Buffer{}
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		lower := strings.ToLower(stderrStr)

		switch {
		case strings.Contains(lower, "docker desktop is manually paused"):
			if stderrStr == "" {
				stderrStr = "Docker Desktop is manually paused"
			}
			return fmt.Errorf("%w: %s", errDockerPaused, stderrStr)
		case strings.Contains(lower, "is the docker daemon running"),
			strings.Contains(lower, "cannot connect to the docker daemon"),
			strings.Contains(lower, "error during connect"),
			strings.Contains(lower, "connectex"),
			errors.Is(err, context.DeadlineExceeded):
			if stderrStr == "" {
				stderrStr = "Docker daemon is not responding"
			}
			return fmt.Errorf("%w: %s", errDockerUnavailable, stderrStr)
		}

		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return fmt.Errorf("%w: %v", errDockerUnavailable, execErr)
		}

		if exitErr, ok := err.(*exec.ExitError); ok {
			if stderrStr == "" {
				stderrStr = exitErr.Error()
			}
			return fmt.Errorf("%w: %s", errDockerUnavailable, stderrStr)
		}

		if stderrStr != "" {
			return fmt.Errorf("%w: %s", errDockerUnavailable, stderrStr)
		}

		return fmt.Errorf("%w: %v", errDockerUnavailable, err)
	}

	return nil
}

func (cpl *CoreProcessLauncher) triggerForceReconnect(reason string) {
	if cpl.apiClient == nil {
		return
	}

	const maxAttempts = 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := cpl.apiClient.ForceReconnectAllServers(reason); err != nil {
			cpl.logger.Warn("Failed to trigger upstream reconnection after Docker recovery",
				zap.String("reason", reason),
				zap.Int("attempt", attempt),
				zap.Error(err))
			time.Sleep(2 * time.Second)
			continue
		}

		cpl.logger.Info("Triggered upstream reconnection after Docker recovery",
			zap.String("reason", reason),
			zap.Int("attempt", attempt))

		// Show success notification
		if err := tray.ShowDockerRecoverySuccess(0); err != nil {
			cpl.logger.Warn("Failed to show recovery success notification", zap.Error(err))
		}
		return
	}

	cpl.logger.Error("Exhausted attempts to trigger upstream reconnection after Docker recovery",
		zap.String("reason", reason),
		zap.Int("attempts", maxAttempts))

	// Show failure notification
	if err := tray.ShowDockerRecoveryFailed("Max reconnection attempts exceeded"); err != nil {
		cpl.logger.Warn("Failed to show recovery failure notification", zap.Error(err))
	}
}

// handleDBLockedError handles database locked errors
func (cpl *CoreProcessLauncher) handleDBLockedError() {
	cpl.logger.Warn("Core failed due to database lock")
	// Could implement automatic stale lock cleanup here
}

// handleConfigError handles configuration errors
func (cpl *CoreProcessLauncher) handleConfigError() {
	cpl.logger.Error("Core failed due to configuration error")
	// Configuration errors are usually not recoverable without user intervention
}

// handleGeneralError handles general errors with retry logic
func (cpl *CoreProcessLauncher) handleGeneralError() {
	currentState := cpl.stateMachine.GetCurrentState()
	cpl.logger.Error("Core failed with general error", "state", currentState)

	// Check if we should retry
	if cpl.stateMachine.ShouldRetry(currentState) {
		retryCount := cpl.stateMachine.GetRetryCount(currentState)
		retryDelay := cpl.stateMachine.GetRetryDelay(currentState)

		cpl.logger.Info("Will retry after delay",
			"state", currentState,
			"retry_attempt", retryCount+1,
			"delay", retryDelay)

		// Wait for retry delay
		time.Sleep(retryDelay)

		// Send retry event
		cpl.stateMachine.SendEvent(state.EventRetry)
	} else {
		cpl.logger.Error("Max retries exceeded, giving up", "state", currentState)
	}
}

// handleShutdown handles graceful shutdown
func (cpl *CoreProcessLauncher) handleShutdown() {
	cpl.logger.Info("Core process launcher shutting down")

	// CRITICAL: Disable menu sync FIRST to prevent API calls after shutdown
	// This prevents the menu sync from trying to fetch servers after core is killed
	if cpl.trayApp != nil {
		cpl.logger.Info("Disabling menu synchronization")
		cpl.trayApp.SetConnectionState(tray.ConnectionStateDisconnected)
	}

	// Stop any Docker retry loop
	cpl.cancelDockerRetry()

	// Stop SSE connection before killing core
	// This prevents SSE from detecting disconnection and trying to reconnect
	cpl.logger.Info("Stopping SSE connection")
	cpl.apiClient.StopSSE()

	// Give SSE goroutine a moment to see cancellation and exit cleanly
	time.Sleep(100 * time.Millisecond)

	// Stop health monitor before killing core
	if cpl.healthMonitor != nil {
		cpl.logger.Info("Stopping health monitor")
		cpl.healthMonitor.Stop()
	}

	// Finally, kill the core process
	if cpl.processMonitor != nil {
		cpl.logger.Info("Shutting down core process")
		cpl.processMonitor.Shutdown()
	}
}

// buildCoreEnvironment builds the environment for the core process
func (cpl *CoreProcessLauncher) buildCoreEnvironment() []string {
	env := os.Environ()

	// Filter out any existing MCPPROXY_API_KEY to avoid conflicts
	filtered := make([]string, 0, len(env))
	for _, envVar := range env {
		if !strings.HasPrefix(envVar, "MCPPROXY_API_KEY=") {
			filtered = append(filtered, envVar)
		}
	}

	// Add our environment variables
	filtered = append(filtered,
		"MCPPROXY_ENABLE_TRAY=false",
		fmt.Sprintf("MCPPROXY_API_KEY=%s", trayAPIKey))

	// Pass through TLS configuration if set
	if tlsEnabled := strings.TrimSpace(os.Getenv("MCPPROXY_TLS_ENABLED")); tlsEnabled != "" {
		filtered = append(filtered, fmt.Sprintf("MCPPROXY_TLS_ENABLED=%s", tlsEnabled))
	}

	return filtered
}

// maskSensitiveArgs masks sensitive command line arguments
func (cpl *CoreProcessLauncher) maskSensitiveArgs(args []string) []string {
	masked := make([]string, len(args))
	copy(masked, args)

	for i, arg := range masked {
		if strings.Contains(strings.ToLower(arg), "key") ||
			strings.Contains(strings.ToLower(arg), "secret") ||
			strings.Contains(strings.ToLower(arg), "token") ||
			strings.Contains(strings.ToLower(arg), "password") {
			masked[i] = maskAPIKey(arg)
		}
	}

	return masked
}
