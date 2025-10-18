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
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
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
	trayAPIKey       = "" // API key generated for core communication
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

	// Setup API key for secure communication between tray and core
	if trayAPIKey == "" {
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
	}

	// Create state machine
	stateMachine := state.NewMachine(logger.Sugar())

	// Create enhanced API client with better connection management
	apiClient := api.NewClient(coreURL, logger.Sugar())
	apiClient.SetAPIKey(trayAPIKey)

	// Create tray application early so icon appears
	shutdownFunc := func() {
		logger.Info("Tray shutdown requested")
		stateMachine.Shutdown()
		cancel()
	}

	trayApp := tray.NewWithAPIClient(api.NewServerAdapter(apiClient), apiClient, logger.Sugar(), version, shutdownFunc)

	// Start the state machine (without automatic initial event)
	stateMachine.Start()

	// Launch core management with state machine
	launcher := NewCoreProcessLauncher(
		coreURL,
		logger.Sugar(),
		stateMachine,
		apiClient,
		trayApp,
		coreTimeout,
	)

	// Send the appropriate initial event based on SKIP_CORE flag
	if shouldSkipCoreLaunch() {
		logger.Info("Skipping core launch, will connect to existing core")
		stateMachine.SendEvent(state.EventSkipCore)
	} else {
		logger.Info("Will launch new core process")
		stateMachine.SendEvent(state.EventStart)
	}

	go launcher.Start(ctx)

	// Handle signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Info("Received shutdown signal")
		stateMachine.SendEvent(state.EventShutdown)
		cancel()
	}()

	logger.Info("Starting tray event loop")
	if err := trayApp.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("Tray application error", zap.Error(err))
	}

	// Wait for state machine to shut down gracefully
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
			cpl.logger.Info("State transition",
				"from", transition.From,
				"to", transition.To,
				"event", transition.Event,
				"timestamp", transition.Timestamp.Format(time.RFC3339))

			// Update tray connection state based on machine state
			cpl.updateTrayConnectionState(transition.To)

			// Handle specific state entries
			switch transition.To {
			case state.StateLaunchingCore:
				go cpl.handleLaunchCore(ctx)

			case state.StateWaitingForCore:
				go cpl.handleWaitForCore(ctx)

			case state.StateConnectingAPI:
				go cpl.handleConnectAPI(ctx)

			case state.StateConnected:
				cpl.handleConnected()

			case state.StateReconnecting:
				go cpl.handleReconnecting(ctx)

			case state.StateCoreErrorPortConflict:
				cpl.handlePortConflictError()

			case state.StateCoreErrorDBLocked:
				cpl.handleDBLockedError()

			case state.StateCoreErrorConfig:
				cpl.handleConfigError()

			case state.StateCoreErrorGeneral:
				cpl.handleGeneralError()

			case state.StateShuttingDown:
				cpl.handleShutdown()
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

// handleLaunchCore handles launching the core process
func (cpl *CoreProcessLauncher) handleLaunchCore(_ context.Context) {
	cpl.logger.Info("Launching mcpproxy core process")

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
	processConfig := monitor.ProcessConfig{
		Binary:        launchBinary,
		Args:          launchArgs,
		Env:           env,
		StartTimeout:  cpl.coreTimeout,
		CaptureOutput: true,
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

	// Start SSE connection
	if err := cpl.apiClient.StartSSE(ctx); err != nil {
		cpl.logger.Error("Failed to start SSE connection", "error", err)
		cpl.stateMachine.SetError(err)
		cpl.stateMachine.SendEvent(state.EventConnectionLost)
		return
	}

	// Subscribe to API client connection state changes
	go cpl.monitorAPIConnection(ctx)
}

// monitorAPIConnection monitors the API client connection state
func (cpl *CoreProcessLauncher) monitorAPIConnection(ctx context.Context) {
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
				cpl.stateMachine.SendEvent(state.EventAPIConnected)
			case tray.ConnectionStateReconnecting, tray.ConnectionStateDisconnected:
				cpl.stateMachine.SendEvent(state.EventConnectionLost)
			}
		}
	}
}

// handleConnected handles the connected state
func (cpl *CoreProcessLauncher) handleConnected() {
	cpl.logger.Info("Core process fully connected and operational")
}

// handleReconnecting handles reconnection attempts
func (cpl *CoreProcessLauncher) handleReconnecting(_ context.Context) {
	cpl.logger.Info("Attempting to reconnect to core")
	// The state machine will handle retry logic automatically
}

// handlePortConflictError handles port conflict errors
func (cpl *CoreProcessLauncher) handlePortConflictError() {
	cpl.logger.Warn("Core failed due to port conflict")
	// Could implement automatic port resolution here
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

// handleGeneralError handles general errors
func (cpl *CoreProcessLauncher) handleGeneralError() {
	cpl.logger.Error("Core failed with general error")
}

// handleShutdown handles graceful shutdown
func (cpl *CoreProcessLauncher) handleShutdown() {
	cpl.logger.Info("Core process launcher shutting down")

	if cpl.processMonitor != nil {
		cpl.processMonitor.Shutdown()
	}

	if cpl.healthMonitor != nil {
		cpl.healthMonitor.Stop()
	}

	cpl.apiClient.StopSSE()
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
