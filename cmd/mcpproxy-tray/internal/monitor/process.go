//go:build darwin

package monitor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"

	"mcpproxy-go/cmd/mcpproxy-tray/internal/state"
)

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

// ProcessMonitor monitors a subprocess and reports its status
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

	// Output capture
	stdoutBuf strings.Builder
	stderrBuf strings.Builder
	outputMu  sync.Mutex

	// Context for cancellation
	ctx    context.Context
	cancel context.CancelFunc
}

// NewProcessMonitor creates a new process monitor
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
		ctx:          ctx,
		cancel:       cancel,
	}
}

// Start starts the monitored process
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

	// Set environment
	if len(pm.config.Env) > 0 {
		pm.cmd.Env = pm.config.Env
		pm.logger.Debug("Process environment",
			"env_vars", pm.maskSensitiveEnv(pm.config.Env))
	}

	// Set up process group for clean termination
	pm.cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Set up output capture if enabled
	if pm.config.CaptureOutput {
		if err := pm.setupOutputCapture(); err != nil {
			return fmt.Errorf("failed to set up output capture: %w", err)
		}
	}

	// Start the process
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

	// Start monitoring in background
	go pm.monitor()

	// Send started event
	pm.sendEvent(ProcessEvent{
		Type: ProcessEventStarted,
		Data: map[string]interface{}{
			"pid":          pm.pid,
			"startup_time": time.Since(pm.startTime),
		},
		Timestamp: time.Now(),
	})

	// Notify state machine
	if pm.stateMachine != nil {
		pm.stateMachine.SendEvent(state.EventCoreStarted)
	}

	return nil
}

// Stop stops the monitored process
func (pm *ProcessMonitor) Stop() error {
	pm.mu.Lock()
	cmd := pm.cmd
	pid := pm.pid
	pm.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("no process to stop")
	}

	pm.logger.Infow("Stopping process", "pid", pid)

	// Send SIGTERM to process group
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		pm.logger.Warn("Failed to send SIGTERM", "pid", pid, "error", err)
	}

	// Wait for graceful shutdown with timeout
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		pm.logger.Infow("Process stopped gracefully", "pid", pid, "error", err)
		return err
	case <-time.After(10 * time.Second):
		// Force kill
		pm.logger.Warn("Process did not stop gracefully, sending SIGKILL", "pid", pid)
		if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			pm.logger.Error("Failed to send SIGKILL", "pid", pid, "error", err)
		}
		<-done // Wait for process to exit
		return fmt.Errorf("process force killed")
	}
}

// GetStatus returns the current process status
func (pm *ProcessMonitor) GetStatus() ProcessStatus {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.status
}

// GetPID returns the process ID
func (pm *ProcessMonitor) GetPID() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.pid
}

// GetExitInfo returns information about process exit
func (pm *ProcessMonitor) GetExitInfo() *ExitInfo {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.exitInfo
}

// GetOutput returns captured stdout and stderr
func (pm *ProcessMonitor) GetOutput() (stdout, stderr string) {
	pm.outputMu.Lock()
	defer pm.outputMu.Unlock()
	return pm.stdoutBuf.String(), pm.stderrBuf.String()
}

// EventChannel returns a channel for receiving process events
func (pm *ProcessMonitor) EventChannel() <-chan ProcessEvent {
	return pm.eventCh
}

// Shutdown gracefully shuts down the process monitor
func (pm *ProcessMonitor) Shutdown() {
	pm.logger.Info("Process monitor shutting down")
	pm.cancel()

	// Stop the process if it's still running
	if pm.GetStatus() == ProcessStatusRunning {
		_ = pm.Stop() // Ignore error during shutdown
	}

	close(pm.shutdownCh)
}

// monitor watches the process in a background goroutine
func (pm *ProcessMonitor) monitor() {
	defer close(pm.eventCh)

	// Wait for process to exit
	err := pm.cmd.Wait()

	pm.mu.Lock()

	// Determine exit information
	pm.exitInfo = &ExitInfo{
		Timestamp: time.Now(),
		Error:     err,
	}

	if err != nil {
		pm.status = ProcessStatusFailed

		// Try to extract exit code and signal
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				pm.exitInfo.Code = status.ExitStatus()
				if status.Signaled() {
					pm.exitInfo.Signal = status.Signal().String()
					pm.status = ProcessStatusCrashed
				}
			}
		}

		pm.logger.Error("Process exited with error",
			"pid", pm.pid,
			"error", err,
			"exit_code", pm.exitInfo.Code,
			"signal", pm.exitInfo.Signal,
			"runtime", time.Since(pm.startTime))
	} else {
		pm.status = ProcessStatusStopped
		pm.exitInfo.Code = 0
		pm.logger.Info("Process exited normally",
			"pid", pm.pid,
			"runtime", time.Since(pm.startTime))
	}

	exitInfo := *pm.exitInfo
	pm.mu.Unlock()

	// Send exit event
	pm.sendEvent(ProcessEvent{
		Type: ProcessEventExited,
		Data: map[string]interface{}{
			"exit_code": exitInfo.Code,
			"signal":    exitInfo.Signal,
			"runtime":   time.Since(pm.startTime),
		},
		Error:     exitInfo.Error,
		Timestamp: exitInfo.Timestamp,
	})

	// Notify state machine based on exit code
	if pm.stateMachine != nil {
		pm.handleProcessExit(exitInfo.Code)
	}
}

// handleProcessExit sends appropriate events to state machine based on exit code
func (pm *ProcessMonitor) handleProcessExit(exitCode int) {
	switch exitCode {
	case 0: // Normal exit
		// Process exited normally, likely due to shutdown
		return
	case 2: // Port conflict
		pm.stateMachine.SendEvent(state.EventPortConflict)
	case 3: // Database locked
		pm.stateMachine.SendEvent(state.EventDBLocked)
	case 4: // Configuration error
		pm.stateMachine.SendEvent(state.EventConfigError)
	case 5: // Permission error
		pm.stateMachine.SendEvent(state.EventPermissionError)
	default: // General error
		pm.stateMachine.SendEvent(state.EventGeneralError)
	}
}

// setupOutputCapture sets up stdout/stderr capture
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

	// Use a WaitGroup to ensure capture goroutines are ready before starting process
	var captureReady sync.WaitGroup
	captureReady.Add(2) // stdout + stderr

	// Start output capture goroutines
	go pm.captureOutput(stdoutPipe, &pm.stdoutBuf, "stdout", &captureReady)
	go pm.captureOutput(stderrPipe, &pm.stderrBuf, "stderr", &captureReady)

	// Wait for both capture goroutines to be ready
	captureReady.Wait()
	pm.logger.Debug("Output capture goroutines ready")

	return nil
}

// captureOutput captures output from a pipe
func (pm *ProcessMonitor) captureOutput(pipe io.ReadCloser, buf *strings.Builder, streamName string, ready *sync.WaitGroup) {
	defer pipe.Close()

	// Signal that this goroutine is ready to read
	if ready != nil {
		pm.logger.Debug("Output capture goroutine starting", "stream", streamName)
		ready.Done()
	}

	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		line := scanner.Text()

		pm.outputMu.Lock()
		buf.WriteString(line)
		buf.WriteString("\n")
		pm.outputMu.Unlock()

		// Send output event (with rate limiting to avoid spam)
		select {
		case pm.eventCh <- ProcessEvent{
			Type: ProcessEventOutput,
			Data: map[string]interface{}{
				"stream": streamName,
				"line":   line,
			},
			Timestamp: time.Now(),
		}:
		default:
			// Channel full, drop output event
		}

		// Log significant output lines
		if strings.Contains(strings.ToLower(line), "error") ||
			strings.Contains(strings.ToLower(line), "failed") ||
			strings.Contains(strings.ToLower(line), "panic") {
			pm.logger.Warn("Process error output", "stream", streamName, "line", line)
		} else {
			pm.logger.Debug("Process output", "stream", streamName, "line", line)
		}
	}

	if err := scanner.Err(); err != nil {
		pm.logger.Warn("Error reading process output", "stream", streamName, "error", err)
	}
}

// sendEvent sends an event to the event channel
func (pm *ProcessMonitor) sendEvent(event ProcessEvent) {
	select {
	case pm.eventCh <- event:
	default:
		pm.logger.Warn("Process event channel full, dropping event", "event_type", event.Type)
	}
}

// maskSensitiveArgs masks sensitive data in command arguments
func (pm *ProcessMonitor) maskSensitiveArgs(args []string) []string {
	masked := make([]string, len(args))
	copy(masked, args)

	for i, arg := range masked {
		if strings.Contains(strings.ToLower(arg), "key") ||
			strings.Contains(strings.ToLower(arg), "secret") ||
			strings.Contains(strings.ToLower(arg), "token") ||
			strings.Contains(strings.ToLower(arg), "password") {
			if len(arg) > 8 {
				masked[i] = arg[:4] + "****" + arg[len(arg)-4:]
			} else {
				masked[i] = "****"
			}
		}
	}

	return masked
}

// maskSensitiveEnv masks sensitive data in environment variables
func (pm *ProcessMonitor) maskSensitiveEnv(env []string) []string {
	masked := make([]string, len(env))

	for i, envVar := range env {
		if parts := strings.SplitN(envVar, "=", 2); len(parts) == 2 {
			key := strings.ToLower(parts[0])
			value := parts[1]

			if strings.Contains(key, "key") ||
				strings.Contains(key, "secret") ||
				strings.Contains(key, "token") ||
				strings.Contains(key, "password") {
				if len(value) > 8 {
					masked[i] = parts[0] + "=" + value[:4] + "****" + value[len(value)-4:]
				} else {
					masked[i] = parts[0] + "=****"
				}
			} else {
				masked[i] = envVar
			}
		} else {
			masked[i] = envVar
		}
	}

	return masked
}
