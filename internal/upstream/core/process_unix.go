//go:build unix

package core

import (
	"context"
	"os/exec"
	"syscall"
	"time"

	"go.uber.org/zap"
)

// ProcessGroup represents a Unix process group for proper child process management
type ProcessGroup struct {
	PGID int
}

// createProcessGroupCommandFunc creates a custom CommandFunc that sets process groups for Unix systems
// This ensures that child processes can be properly cleaned up when the parent exits
func createProcessGroupCommandFunc(client *Client, workingDir string, logger *zap.Logger) func(ctx context.Context, command string, env []string, args []string) (*exec.Cmd, error) {
	return func(ctx context.Context, command string, env []string, args []string) (*exec.Cmd, error) {
		cmd := exec.CommandContext(ctx, command, args...)
		cmd.Env = env

		// Set working directory if specified
		if workingDir != "" {
			cmd.Dir = workingDir
		}

		// CRITICAL FIX: Set process group attributes for proper child process management
		// This ensures that when mcpproxy exits, all child processes can be terminated properly
		cmd.SysProcAttr = &syscall.SysProcAttr{
			// Create a new process group for this command and its children
			Setpgid: true,
			// Make this process the group leader
			Pgid: 0,
		}

		logger.Debug("Process group configuration applied",
			zap.String("command", command),
			zap.Strings("args", args),
			zap.String("working_dir", workingDir))

		if client != nil {
			client.processCmd = cmd
			if pgid := extractProcessGroupID(cmd, logger, client.config.Name); pgid > 0 {
				client.processGroupID = pgid
				logger.Info("Process group ID tracked for cleanup",
					zap.String("server", client.config.Name),
					zap.Int("pgid", pgid))
			}
		}

		return cmd, nil
	}
}

// killProcessGroup terminates an entire process group on Unix systems
// This is the proper way to clean up child processes and prevent zombies
func killProcessGroup(pgid int, logger *zap.Logger, serverName string) error {
	if pgid <= 0 {
		return nil
	}

	logger.Info("Terminating process group",
		zap.String("server", serverName),
		zap.Int("pgid", pgid))

	// Step 1: Send SIGTERM to the entire process group
	err := syscall.Kill(-pgid, syscall.SIGTERM)
	if err != nil {
		logger.Warn("Failed to send SIGTERM to process group",
			zap.String("server", serverName),
			zap.Int("pgid", pgid),
			zap.Error(err))
	} else {
		logger.Debug("SIGTERM sent to process group",
			zap.String("server", serverName),
			zap.Int("pgid", pgid))
	}

	// Step 2: Wait a bit for graceful termination
	time.Sleep(2 * time.Second)

	// Step 3: Check if processes are still running and send SIGKILL if needed
	if err := syscall.Kill(-pgid, 0); err == nil {
		// Processes still exist, force kill them
		logger.Warn("Process group still running after SIGTERM, sending SIGKILL",
			zap.String("server", serverName),
			zap.Int("pgid", pgid))

		if killErr := syscall.Kill(-pgid, syscall.SIGKILL); killErr != nil {
			logger.Error("Failed to send SIGKILL to process group",
				zap.String("server", serverName),
				zap.Int("pgid", pgid),
				zap.Error(killErr))
			return killErr
		}

		logger.Info("SIGKILL sent to process group",
			zap.String("server", serverName),
			zap.Int("pgid", pgid))
	} else {
		logger.Info("Process group terminated successfully",
			zap.String("server", serverName),
			zap.Int("pgid", pgid))
	}

	return nil
}

// extractProcessGroupID extracts the process group ID from a running command
func extractProcessGroupID(cmd *exec.Cmd, logger *zap.Logger, serverName string) int {
	if cmd == nil || cmd.Process == nil {
		return 0
	}

	// Get the process group ID from the process
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		logger.Warn("Failed to get process group ID",
			zap.String("server", serverName),
			zap.Int("pid", cmd.Process.Pid),
			zap.Error(err))
		return 0
	}

	logger.Debug("Process group ID extracted",
		zap.String("server", serverName),
		zap.Int("pid", cmd.Process.Pid),
		zap.Int("pgid", pgid))

	return pgid
}
