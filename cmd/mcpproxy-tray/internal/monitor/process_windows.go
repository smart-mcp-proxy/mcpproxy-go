//go:build windows

package monitor

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sys/windows"

	"github.com/smart-mcp-proxy/mcpproxy-go/cmd/mcpproxy-tray/internal/state"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/shutdown"
)

// Windows-specific ProcessMonitor implementation without POSIX process groups.

// ProcessStatus represents the status of a monitored process
type ProcessStatus string

const (
	ProcessStatusStopped  ProcessStatus = "stopped"
	ProcessStatusStarting ProcessStatus = "starting"
	ProcessStatusRunning  ProcessStatus = "running"
	ProcessStatusFailed   ProcessStatus = "failed"
	ProcessStatusCrashed  ProcessStatus = "crashed"
)

// ProcessEvent represents events from the process monitor
type ProcessEvent struct {
	Type      ProcessEventType
	Data      map[string]interface{}
	Error     error
	Timestamp time.Time
}

type ProcessEventType string

const (
	ProcessEventStarted ProcessEventType = "started"
	ProcessEventExited  ProcessEventType = "exited"
	ProcessEventError   ProcessEventType = "error"
	ProcessEventOutput  ProcessEventType = "output"
)

// ExitInfo contains information about process exit
type ExitInfo struct {
	Code      int
	Signal    string
	Timestamp time.Time
	Error     error
}

// ProcessConfig contains configuration for process monitoring
type ProcessConfig struct {
	Binary        string
	Args          []string
	Env           []string
	WorkingDir    string
	StartTimeout  time.Duration
	CaptureOutput bool
}

// ProcessMonitor monitors a subprocess and reports its status (Windows)
type ProcessMonitor struct {
	config       ProcessConfig
	logger       *zap.SugaredLogger
	stateMachine *state.Machine

	mu        sync.RWMutex
	cmd       *exec.Cmd
	status    ProcessStatus
	pid       int
	exitInfo  *ExitInfo
	startTime time.Time

	// Channels
	eventCh    chan ProcessEvent
	shutdownCh chan struct{}
	doneCh     chan struct{} // Closed when monitor() exits

	// Output capture
	stdoutBuf strings.Builder
	stderrBuf strings.Builder
	outputMu  sync.Mutex

	// Context for cancellation
	ctx    context.Context
	cancel context.CancelFunc
}

// NewProcessMonitor creates a new process monitor (Windows)
func NewProcessMonitor(config *ProcessConfig, logger *zap.SugaredLogger, stateMachine *state.Machine) *ProcessMonitor {
	ctx, cancel := context.WithCancel(context.Background())

	// Set default timeout if not specified
	if config.StartTimeout == 0 {
		config.StartTimeout = 30 * time.Second
	}

	return &ProcessMonitor{
		config:       *config,
		logger:       logger,
		stateMachine: stateMachine,
		status:       ProcessStatusStopped,
		eventCh:      make(chan ProcessEvent, 50),
		shutdownCh:   make(chan struct{}),
		doneCh:       make(chan struct{}),
		ctx:          ctx,
		cancel:       cancel,
	}
}

// Start starts the monitored process (Windows)
func (pm *ProcessMonitor) Start() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.status == ProcessStatusRunning || pm.status == ProcessStatusStarting {
		return fmt.Errorf("process already running or starting")
	}

	pm.logger.Infow("Starting process",
		"binary", pm.config.Binary,
		"args", pm.maskSensitiveArgs(pm.config.Args),
		"env_count", len(pm.config.Env),
		"working_dir", pm.config.WorkingDir)

	// Create command
	pm.cmd = exec.CommandContext(pm.ctx, pm.config.Binary, pm.config.Args...)
	if pm.config.WorkingDir != "" {
		pm.cmd.Dir = pm.config.WorkingDir
	}
	if len(pm.config.Env) > 0 {
		pm.cmd.Env = pm.config.Env
		pm.logger.Debug("Process environment",
			"env_vars", pm.maskSensitiveEnv(pm.config.Env))
	}

	// Hide the console window; a process group of its own lets Stop() send it CTRL_BREAK.
	pm.cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP,
	}

	// Set up output capture if enabled
	if pm.config.CaptureOutput {
		if err := pm.setupOutputCapture(); err != nil {
			return fmt.Errorf("failed to set up output capture: %w", err)
		}
	}

	pm.status = ProcessStatusStarting
	pm.startTime = time.Now()
	if err := pm.cmd.Start(); err != nil {
		pm.status = ProcessStatusFailed
		pm.logger.Error("Failed to start process", "error", err)
		return fmt.Errorf("failed to start process: %w", err)
	}

	pm.pid = pm.cmd.Process.Pid
	pm.status = ProcessStatusRunning
	pm.logger.Infow("Process started successfully",
		"pid", pm.pid,
		"startup_time", time.Since(pm.startTime))

	go pm.monitor()

	pm.sendEvent(ProcessEvent{
		Type: ProcessEventStarted,
		Data: map[string]interface{}{
			"pid":          pm.pid,
			"startup_time": time.Since(pm.startTime),
		},
		Timestamp: time.Now(),
	})

	if pm.stateMachine != nil {
		pm.stateMachine.SendEvent(state.EventCoreStarted)
	}
	return nil
}

// Stop stops the monitored process (Windows)
func (pm *ProcessMonitor) Stop() error {
	pm.mu.Lock()
	pid := pm.pid
	pm.mu.Unlock()

	if pid <= 0 {
		return fmt.Errorf("no process to stop")
	}

	pm.logger.Infow("Stopping process", "pid", pid)

	// Force kill only a wedged core: it ends by itself after 60 seconds at the latest.
	grace := shutdown.Tray
	if err := requestGracefulStop(pid); err != nil {
		pm.logger.Warnw("Could not ask process to stop gracefully, force killing", "pid", pid, "error", err)
		grace = 0
	}

	// Wait for monitor goroutine to detect exit (via doneCh)
	// This avoids the cmd.Wait() race condition
	exited := func() error {
		// Monitor has detected process exit
		pm.mu.RLock()
		exitInfo := pm.exitInfo
		pm.mu.RUnlock()

		if exitInfo != nil {
			pm.logger.Infow("Process stopped", "pid", pid, "exit_code", exitInfo.Code)
			return exitInfo.Error
		}
		pm.logger.Infow("Process stopped (no exit info)", "pid", pid)
		return nil
	}

	// select picks at random when both cases are ready, and with grace == 0 the timer is ready
	// at once: look at doneCh first so a child the monitor has already seen exit is not
	// force-killed. A child that exits in the instant before the monitor closes doneCh can
	// still get a harmless taskkill (the tray holds its process handle until Wait returns).
	select {
	case <-pm.doneCh:
		return exited()
	default:
	}

	select {
	case <-pm.doneCh:
		return exited()

	case <-time.After(grace):
		select {
		case <-pm.doneCh: // exited just as the timer fired
			return exited()
		default:
		}
		pm.logger.Warnw("Process did not stop gracefully, sending force kill", "pid", pid, "grace", grace)
		killCmd := HiddenCommand("taskkill", "/PID", fmt.Sprint(pid), "/T", "/F")
		_ = killCmd.Run()

		// Wait for monitor to detect the kill
		<-pm.doneCh
		pm.logger.Info("Process force killed", "pid", pid)
		return fmt.Errorf("process force killed after timeout")
	}
}

var (
	kernel32          = windows.NewLazySystemDLL("kernel32.dll")
	procAttachConsole = kernel32.NewProc("AttachConsole")
	procFreeConsole   = kernel32.NewProc("FreeConsole")
	consoleMu         sync.Mutex // the console a process is attached to is process-wide
)

// requestGracefulStop sends CTRL_BREAK to the process group led by pid; Go turns it into
// SIGINT, so the core runs its normal graceful shutdown. taskkill without /F cannot do this:
// it only sends WM_CLOSE, which a windowless console process never receives.
func requestGracefulStop(pid int) error {
	consoleMu.Lock()
	defer consoleMu.Unlock()
	_, _, _ = procFreeConsole.Call() // a tray started from a terminal must leave it to attach
	if r, _, err := procAttachConsole.Call(uintptr(pid)); r == 0 {
		return fmt.Errorf("attach to console of pid %d: %w", pid, err)
	}
	defer procFreeConsole.Call()
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(pid))
}

// HiddenCommand is exec.Command without the console window a GUI process would otherwise flash.
func HiddenCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd
}

// setupOutputCapture sets up stdout/stderr capture (Windows shares POSIX impl)
func (pm *ProcessMonitor) setupOutputCapture() error {
	stdoutPipe, err := pm.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderrPipe, err := pm.cmd.StderrPipe()
	if err != nil {
		stdoutPipe.Close()
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}
	go pm.captureOutput(stdoutPipe, &pm.stdoutBuf, "stdout")
	go pm.captureOutput(stderrPipe, &pm.stderrBuf, "stderr")
	return nil
}

// captureOutput mirrors POSIX version
func (pm *ProcessMonitor) captureOutput(pipe io.ReadCloser, buf *strings.Builder, streamName string) {
	defer pipe.Close()
	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		line := scanner.Text()
		pm.outputMu.Lock()
		buf.WriteString(line)
		buf.WriteString("\n")
		pm.outputMu.Unlock()
		select {
		case pm.eventCh <- ProcessEvent{Type: ProcessEventOutput, Data: map[string]interface{}{"stream": streamName, "line": line}, Timestamp: time.Now()}:
		default:
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "failed to bind") || strings.Contains(lower, "address already in use") || strings.Contains(lower, "only one usage of each socket") || (strings.Contains(lower, "bind:") && strings.Contains(lower, "forbidden by its access permissions")) {
			// Proactively notify about port conflict
			if pm.stateMachine != nil {
				pm.stateMachine.SendEvent(state.EventPortConflict)
			}
		}

		if strings.Contains(lower, "error") || strings.Contains(lower, "failed") || strings.Contains(lower, "panic") {
			pm.logger.Warn("Process error output", "stream", streamName, "line", line)
		} else {
			pm.logger.Debug("Process output", "stream", streamName, "line", line)
		}
	}
	if err := scanner.Err(); err != nil {
		pm.logger.Warn("Error reading process output", "stream", streamName, "error", err)
	}
}

// monitor waits for process exit (Windows)
func (pm *ProcessMonitor) monitor() {
	defer close(pm.eventCh)
	defer close(pm.doneCh)
	err := pm.cmd.Wait()
	pm.mu.Lock()
	pm.exitInfo = &ExitInfo{Timestamp: time.Now(), Error: err}
	if err != nil {
		pm.status = ProcessStatusFailed
		// Extract exit code on Windows
		if exitErr, ok := err.(*exec.ExitError); ok {
			code := exitErr.ExitCode()
			pm.exitInfo.Code = code
			pm.logger.Error("Process exited with error", "pid", pm.pid, "error", err, "exit_code", code, "runtime", time.Since(pm.startTime))
		} else {
			pm.logger.Error("Process exited with error", "pid", pm.pid, "error", err, "runtime", time.Since(pm.startTime))
		}
	} else {
		pm.status = ProcessStatusStopped
		pm.exitInfo.Code = 0
		pm.logger.Infow("Process exited normally", "pid", pm.pid, "runtime", time.Since(pm.startTime))
	}
	exitInfo := *pm.exitInfo
	pm.mu.Unlock()
	pm.sendEvent(ProcessEvent{Type: ProcessEventExited, Data: map[string]interface{}{"exit_code": exitInfo.Code, "runtime": time.Since(pm.startTime)}, Error: exitInfo.Error, Timestamp: exitInfo.Timestamp})
	if pm.stateMachine != nil {
		if exitInfo.Error == nil {
			return
		}
		// Map Windows exit codes similar to POSIX implementation
		switch exitInfo.Code {
		case 2:
			pm.stateMachine.SendEvent(state.EventPortConflict)
		case 3:
			pm.stateMachine.SendEvent(state.EventDBLocked)
		case 4:
			pm.stateMachine.SendEvent(state.EventConfigError)
		case 5:
			pm.stateMachine.SendEvent(state.EventPermissionError)
		default:
			pm.stateMachine.SendEvent(state.EventGeneralError)
		}
	}
}

// Shutdown gracefully shuts down the monitor
func (pm *ProcessMonitor) Shutdown() {
	pm.logger.Info("Process monitor shutting down")

	// IMPORTANT: Do NOT cancel context before stopping process!
	// Cancelling the context sends SIGKILL immediately via exec.CommandContext,
	// preventing graceful shutdown. We must call Stop() first, which sends SIGTERM
	// and waits for graceful termination.

	// Stop the process if it's still running
	status := pm.GetStatus()
	pm.logger.Infow("Process monitor status before stop", "status", status)
	if status == ProcessStatusRunning || status == ProcessStatusStarting {
		if err := pm.Stop(); err != nil {
			pm.logger.Warn("Process stop returned error during shutdown", "error", err)
		} else {
			pm.logger.Info("Process stop completed during shutdown")
		}
	}

	// Now it's safe to cancel the context after the process has stopped
	pm.cancel()

	close(pm.shutdownCh)
}

// GetStatus returns the current process status (Windows)
func (pm *ProcessMonitor) GetStatus() ProcessStatus {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.status
}

// sendEvent sends an event to the channel (Windows)
func (pm *ProcessMonitor) sendEvent(event ProcessEvent) {
	select {
	case pm.eventCh <- event:
	default:
		pm.logger.Warn("Process event channel full, dropping event", "event_type", event.Type)
	}
}

// maskSensitiveArgs masks sensitive data in command arguments (Windows)
func (pm *ProcessMonitor) maskSensitiveArgs(args []string) []string {
	masked := make([]string, len(args))
	copy(masked, args)
	for i, arg := range masked {
		low := strings.ToLower(arg)
		if strings.Contains(low, "key") || strings.Contains(low, "secret") || strings.Contains(low, "token") || strings.Contains(low, "password") {
			if len(arg) > 8 {
				masked[i] = arg[:4] + "****" + arg[len(arg)-4:]
			} else {
				masked[i] = "****"
			}
		}
	}
	return masked
}

// maskSensitiveEnv masks sensitive data in environment variables (Windows)
func (pm *ProcessMonitor) maskSensitiveEnv(env []string) []string {
	masked := make([]string, len(env))
	for i, envVar := range env {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) != 2 {
			masked[i] = envVar
			continue
		}
		keyLower := strings.ToLower(parts[0])
		value := parts[1]
		if strings.Contains(keyLower, "key") || strings.Contains(keyLower, "secret") || strings.Contains(keyLower, "token") || strings.Contains(keyLower, "password") {
			if len(value) > 8 {
				masked[i] = parts[0] + "=" + value[:4] + "****" + value[len(value)-4:]
			} else {
				masked[i] = parts[0] + "=****"
			}
		} else {
			masked[i] = envVar
		}
	}
	return masked
}

// GetPID returns the process ID (Windows)
func (pm *ProcessMonitor) GetPID() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.pid
}

// GetExitInfo returns information about process exit (Windows)
func (pm *ProcessMonitor) GetExitInfo() *ExitInfo {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.exitInfo
}

// GetOutput returns captured stdout and stderr (Windows)
func (pm *ProcessMonitor) GetOutput() (stdout, stderr string) {
	pm.outputMu.Lock()
	defer pm.outputMu.Unlock()
	return pm.stdoutBuf.String(), pm.stderrBuf.String()
}

// EventChannel returns a channel for receiving process events (Windows)
func (pm *ProcessMonitor) EventChannel() <-chan ProcessEvent {
	return pm.eventCh
}
