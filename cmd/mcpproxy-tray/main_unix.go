//go:build darwin || linux

package main

import (
	"errors"
	"fmt"
	"syscall"
	"time"

	"go.uber.org/zap"
)

// forceKillCore forcefully kills the core process (Unix)
func (cpl *CoreProcessLauncher) forceKillCore() {
	pid := cpl.processMonitor.GetPID()
	if pid <= 0 {
		cpl.logger.Warn("Cannot force kill: invalid PID")
		return
	}

	cpl.logger.Warn("Force killing core process", zap.Int("pid", pid))

	// Kill the entire process group
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		if !errors.Is(err, syscall.ESRCH) {
			cpl.logger.Error("Failed to send SIGKILL", zap.Int("pid", pid), zap.Error(err))
		}
	}
}

// signalProcessTree sends a signal to the process tree (Unix)
func (cpl *CoreProcessLauncher) signalProcessTree(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}

	// Try to signal the process group first (negative PID)
	if err := syscall.Kill(-pid, sig); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return err
		}
		if !errors.Is(err, syscall.EPERM) {
			cpl.logger.Debug("Failed to signal process group, trying single process", zap.Int("pid", pid), zap.Error(err))
		}
		// Fallback to signaling just the process
		if err := syscall.Kill(pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		return nil
	}
	// Also signal the main process directly
	if err := syscall.Kill(pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		cpl.logger.Debug("Failed to signal main process after group", zap.Int("pid", pid), zap.Error(err))
	}
	return nil
}

// waitForProcessExit waits for a process to exit (Unix)
func (cpl *CoreProcessLauncher) waitForProcessExit(pid int, timeout time.Duration) bool {
	if pid <= 0 {
		return true
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Check if process exists (signal 0 doesn't kill, just checks)
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return true // Process doesn't exist
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// isProcessAlive returns true if the OS reports the PID as running (Unix)
func (cpl *CoreProcessLauncher) isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}

	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}

	// EPERM means the process exists but we lack permissions
	return !errors.Is(err, syscall.ESRCH)
}

// shutdownExternalCoreFallback attempts to terminate an externally managed core process (Unix)
func (cpl *CoreProcessLauncher) shutdownExternalCoreFallback() error {
	pid, err := cpl.lookupExternalCorePID()
	if err != nil {
		return fmt.Errorf("failed to discover core PID: %w", err)
	}
	if pid <= 0 {
		return fmt.Errorf("invalid PID discovered (%d)", pid)
	}

	cpl.logger.Info("Attempting graceful shutdown for external core", zap.Int("pid", pid))
	if err := cpl.signalProcessTree(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		cpl.logger.Warn("Failed to send SIGTERM to external core", zap.Int("pid", pid), zap.Error(err))
	}

	if !cpl.waitForProcessExit(pid, 30*time.Second) {
		cpl.logger.Warn("External core did not exit after SIGTERM, sending SIGKILL", zap.Int("pid", pid))
		if err := cpl.signalProcessTree(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("failed to force kill external core: %w", err)
		}
		_ = cpl.waitForProcessExit(pid, 5*time.Second)
	}

	return nil
}

// ensureCoreTermination double-checks that no core processes remain and performs a safety cleanup (Unix)
func (cpl *CoreProcessLauncher) ensureCoreTermination() error {
	candidates := cpl.collectCorePIDs()
	if len(candidates) == 0 {
		if extra, err := cpl.findCorePIDsViaPgrep(); err == nil {
			cpl.logger.Infow("No monitor/status PIDs found, using pgrep results",
				"pgrep_count", len(extra),
				"pgrep_pids", extra)
			for _, pid := range extra {
				if pid > 0 {
					candidates[pid] = struct{}{}
				}
			}
		} else {
			cpl.logger.Debug("pgrep PID discovery failed", zap.Error(err))
		}
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
		if err := cpl.signalProcessTree(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			cpl.logger.Warn("Failed to send SIGTERM during verification", zap.Int("pid", pid), zap.Error(err))
		}

		if !cpl.waitForProcessExit(pid, 10*time.Second) {
			cpl.logger.Warn("Core still alive after SIGTERM verification, sending SIGKILL", zap.Int("pid", pid))
			if err := cpl.signalProcessTree(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				cpl.logger.Error("Failed to force kill during verification", zap.Int("pid", pid), zap.Error(err))
			}
			_ = cpl.waitForProcessExit(pid, 3*time.Second)
		}
	}

	return nil
}
