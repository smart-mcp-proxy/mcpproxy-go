//go:build windows

package core

import (
	"context"
	"os"
	"os/exec"
	"sync"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/winjob"
)

// ProcessGroup represents a Windows process group for proper child process management
type ProcessGroup struct {
	PGID   int
	logger *zap.Logger
}

// windowsJobs maps the "process group ID" mcpproxy uses on Windows (the
// immediate child's PID — see extractProcessGroupID) to the Job Object that
// PID was assigned to. killProcessGroup only receives that int, so this is
// how it finds the Job to terminate.
var (
	windowsJobsMu sync.Mutex
	windowsJobs   = map[int]*winjob.Job{}
)

// createProcessGroupCommandFunc creates a custom CommandFunc for Windows systems.
// Job Object assignment happens later, in extractProcessGroupID, once the
// process actually exists (a Job Object is assigned to a PID after start,
// not configured via SysProcAttr before start).
func createProcessGroupCommandFunc(client *Client, workingDir string, logger *zap.Logger) func(ctx context.Context, command string, env []string, args []string) (*exec.Cmd, error) {
	return func(ctx context.Context, command string, env []string, args []string) (*exec.Cmd, error) {
		cmd := exec.CommandContext(ctx, command, args...)
		cmd.Env = env

		if workingDir != "" {
			cmd.Dir = workingDir
		}

		logger.Debug("Process group configuration applied (Windows)",
			zap.String("command", command),
			zap.Strings("args", logSafeArgs(args)),
			zap.String("working_dir", workingDir))

		if client != nil {
			client.processCmd = cmd
		}

		return cmd, nil
	}
}

// killProcessGroup terminates a process AND every descendant it has spawned
// (e.g. cmd.exe -> node.exe -> the actual MCP server) via the Windows Job
// Object that PID was assigned to in extractProcessGroupID. This is what
// process_windows.go never did before: previously this function was a
// no-op placeholder, so restarting/disconnecting a stdio server on Windows
// left every grandchild process running forever (confirmed: 413 leaked
// node.exe/python.exe/qmcp.exe processes accumulated from normal use in
// under an hour on this host).
//
// Falls back to a plain single-process kill if no job was ever registered
// for this PID (e.g. job-object setup itself failed) — degrades to the old
// behaviour rather than doing nothing.
func killProcessGroup(pgid int, logger *zap.Logger, serverName string) error {
	if pgid <= 0 {
		return nil
	}

	job := takeWindowsJob(pgid)
	if job == nil {
		logger.Warn("No Windows Job Object tracked for this process; falling back to killing only the immediate process (any grandchildren will leak)",
			zap.String("server", serverName),
			zap.Int("pid", pgid))
		proc, err := os.FindProcess(pgid)
		if err != nil {
			return nil // already gone
		}
		return proc.Kill()
	}

	logger.Info("Terminating process tree via Windows Job Object",
		zap.String("server", serverName),
		zap.Int("pid", pgid))

	if err := job.Close(); err != nil {
		logger.Warn("Failed to close Windows Job Object cleanly",
			zap.String("server", serverName),
			zap.Int("pid", pgid),
			zap.Error(err))
		return err
	}

	logger.Info("Process tree terminated",
		zap.String("server", serverName),
		zap.Int("pid", pgid))
	return nil
}

// extractProcessGroupID assigns the just-started process to a fresh Windows
// Job Object configured with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, then
// returns its PID as the "process group ID" — the identifier killProcessGroup
// expects. Everything the process spawns AFTER this point automatically
// joins the same job (Windows job membership is inherited), so a later
// killProcessGroup call reaches the whole tree.
func extractProcessGroupID(cmd *exec.Cmd, logger *zap.Logger, serverName string) int {
	if cmd == nil || cmd.Process == nil {
		return 0
	}
	pid := cmd.Process.Pid

	job, err := winjob.New()
	if err != nil {
		logger.Warn("Failed to create Windows Job Object; child processes spawned by this server will not be cleaned up on restart/disconnect",
			zap.String("server", serverName),
			zap.Int("pid", pid),
			zap.Error(err))
		return pid
	}
	if err := job.Assign(pid); err != nil {
		logger.Warn("Failed to assign process to Windows Job Object; child processes spawned by this server will not be cleaned up on restart/disconnect",
			zap.String("server", serverName),
			zap.Int("pid", pid),
			zap.Error(err))
		_ = job.Close()
		return pid
	}

	registerWindowsJob(pid, job, logger, serverName)

	logger.Debug("Process group ID extracted and assigned to Windows Job Object",
		zap.String("server", serverName),
		zap.Int("pid", pid))

	return pid
}

// releaseProcessGroup is the platform hook DisconnectWithContext calls on
// EVERY non-Docker stdio disconnect, whether or not the graceful MCP close
// succeeded. On Unix it is a no-op. Here it terminates whatever is still
// in the Job and drops the windowsJobs entry.
//
// Why it must run on the graceful path too: mcp-go's Stdio.Close() only
// ever kills the immediate child (cmd.exe — every Windows stdio command
// is shell-wrapped) and always returns within ~8s, under the 10s
// mcpClientCloseTimeout, so Disconnect's force-kill step (which is where
// killProcessGroup lives) is effectively unreachable on Windows. Without
// this hook the grandchildren a stdin-EOF-ignoring node.exe/python.exe
// left behind survived every restart, and the Job handle + map entry
// leaked per disconnect until mcpproxy exited (#1234 follow-up, F1).
func releaseProcessGroup(pgid int, logger *zap.Logger, serverName string) {
	if pgid <= 0 {
		return
	}
	job := takeWindowsJob(pgid)
	if job == nil {
		// Already killed via killProcessGroup, or job setup failed at
		// connect time (extractProcessGroupID logged a Warn then).
		return
	}
	if err := job.Close(); err != nil {
		logger.Warn("Failed to close Windows Job Object on disconnect",
			zap.String("server", serverName),
			zap.Int("pid", pgid),
			zap.Error(err))
		return
	}
	logger.Debug("Released Windows Job Object; any surviving grandchildren terminated",
		zap.String("server", serverName),
		zap.Int("pid", pgid))
}

// takeWindowsJob removes and returns the Job registered for pid, or nil.
// Popping under the lock gives the caller sole ownership of the Close.
func takeWindowsJob(pid int) *winjob.Job {
	windowsJobsMu.Lock()
	defer windowsJobsMu.Unlock()
	job := windowsJobs[pid]
	delete(windowsJobs, pid)
	return job
}

// registerWindowsJob records the Job for pid. If an entry already exists —
// only possible when Windows reused a PID whose earlier Job was never
// released — the old Job is closed first rather than silently dropped
// with its handle (and any straggler still inside it) left open forever.
func registerWindowsJob(pid int, job *winjob.Job, logger *zap.Logger, serverName string) {
	windowsJobsMu.Lock()
	prev := windowsJobs[pid]
	windowsJobs[pid] = job
	windowsJobsMu.Unlock()

	if prev != nil {
		logger.Warn("Windows Job Object already registered for reused PID; closing the stale one",
			zap.String("server", serverName),
			zap.Int("pid", pid))
		_ = prev.Close()
	}
}
