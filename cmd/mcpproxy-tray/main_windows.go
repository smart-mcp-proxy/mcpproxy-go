//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"go.uber.org/zap"
)

// Windows doesn't have POSIX signals, so we create dummy constants for compatibility
const (
	_SIGTERM = syscall.Signal(0x1) // Dummy signal for graceful shutdown
	_SIGKILL = syscall.Signal(0x2) // Dummy signal for force kill
)

// forceKillCore forcefully kills the core process (Windows)
func (cpl *CoreProcessLauncher) forceKillCore() {
	pid := cpl.processMonitor.GetPID()
	if pid <= 0 {
		cpl.logger.Warn("Cannot force kill: invalid PID")
		return
	}

	cpl.logger.Warn("Force killing core process", zap.Int("pid", pid))

	// Kill the entire process tree using taskkill /T /F
	killCmd := exec.Command("taskkill", "/PID", fmt.Sprint(pid), "/T", "/F")
	if err := killCmd.Run(); err != nil {
		cpl.logger.Error("Failed to force kill process", zap.Int("pid", pid), zap.Error(err))
	}
}

// signalProcessTree sends a signal to the process tree (Windows)
// On Windows, we use taskkill instead of signals
func (cpl *CoreProcessLauncher) signalProcessTree(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}

	var killCmd *exec.Cmd

	// Map Unix signals to Windows taskkill behavior
	if sig == _SIGKILL {
		// Force kill
		killCmd = exec.Command("taskkill", "/PID", fmt.Sprint(pid), "/T", "/F")
	} else {
		// Graceful shutdown (SIGTERM equivalent)
		killCmd = exec.Command("taskkill", "/PID", fmt.Sprint(pid), "/T")
	}

	if err := killCmd.Run(); err != nil {
		// taskkill returns error if process doesn't exist
		return fmt.Errorf("taskkill failed: %w", err)
	}
	return nil
}

// waitForProcessExit waits for a process to exit (Windows)
func (cpl *CoreProcessLauncher) waitForProcessExit(pid int, timeout time.Duration) bool {
	if pid <= 0 {
		return true
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Check if process exists using tasklist
		checkCmd := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH")
		output, err := checkCmd.Output()
		if err != nil || len(output) == 0 {
			return true // Process doesn't exist
		}
		// Check if output indicates no process found
		outputStr := string(output)
		if outputStr == "" || outputStr == "INFO: No tasks are running which match the specified criteria.\r\n" {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// isProcessAlive returns true if the OS reports the PID as running (Windows)
func (cpl *CoreProcessLauncher) isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}

	checkCmd := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH")
	output, err := checkCmd.Output()
	if err != nil || len(output) == 0 {
		return false
	}
	outputStr := string(output)
	return outputStr != "" && outputStr != "INFO: No tasks are running which match the specified criteria.\r\n"
}

// shutdownExternalCoreFallback attempts to terminate an externally managed core process (Windows)
func (cpl *CoreProcessLauncher) shutdownExternalCoreFallback() error {
	pid, err := cpl.lookupExternalCorePID()
	if err != nil {
		return fmt.Errorf("failed to discover core PID: %w", err)
	}
	if pid <= 0 {
		return fmt.Errorf("invalid PID discovered (%d)", pid)
	}

	cpl.logger.Info("Attempting graceful shutdown for external core", zap.Int("pid", pid))
	if err := cpl.signalProcessTree(pid, _SIGTERM); err != nil {
		cpl.logger.Warn("Failed to send graceful shutdown to external core", zap.Int("pid", pid), zap.Error(err))
	}

	if !cpl.waitForProcessExit(pid, 30*time.Second) {
		cpl.logger.Warn("External core did not exit after graceful shutdown, force killing", zap.Int("pid", pid))
		if err := cpl.signalProcessTree(pid, _SIGKILL); err != nil {
			return fmt.Errorf("failed to force kill external core: %w", err)
		}
		_ = cpl.waitForProcessExit(pid, 5*time.Second)
	}

	return nil
}

// ensureCoreTermination double-checks that no core processes remain and performs a safety cleanup (Windows)
func (cpl *CoreProcessLauncher) ensureCoreTermination() error {
	candidates := cpl.collectCorePIDs()
	if len(candidates) == 0 {
		// Note: pgrep is Unix-only, Windows would need tasklist parsing
		cpl.logger.Debug("No candidate PIDs found for termination verification")
	}
	candidateList := make([]int, 0, len(candidates))
	for pid := range candidates {
		candidateList = append(candidateList, pid)
	}
	cpl.logger.Infow("Ensuring core termination",
		"candidate_count", len(candidateList),
		"candidates", candidateList)

	for pid := range candidates {
		if !cpl.isProcessAlive(pid) {
			cpl.logger.Debug("Candidate PID already exited", zap.Int("pid", pid))
			continue
		}

		cpl.logger.Warn("Additional core termination attempt", zap.Int("pid", pid))
		if err := cpl.signalProcessTree(pid, _SIGTERM); err != nil {
			cpl.logger.Warn("Failed to send graceful shutdown during verification", zap.Int("pid", pid), zap.Error(err))
		}

		if !cpl.waitForProcessExit(pid, 10*time.Second) {
			cpl.logger.Warn("Core still alive after graceful shutdown verification, force killing", zap.Int("pid", pid))
			if err := cpl.signalProcessTree(pid, _SIGKILL); err != nil {
				cpl.logger.Error("Failed to force kill during verification", zap.Int("pid", pid), zap.Error(err))
			}
			_ = cpl.waitForProcessExit(pid, 3*time.Second)
		}
	}

	return nil
}
