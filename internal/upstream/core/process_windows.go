//go:build windows

package core

import (
	"context"
	"errors"
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

// windowsJob is one registered Job Object plus the identity of the
// exec.Cmd it was created for. The cmd pointer is what lets a delayed
// release tell "my process" from "a newer process that reused my PID".
type windowsJob struct {
	job *winjob.Job
	cmd *exec.Cmd
}

// windowsJobs maps the "process group ID" mcpproxy uses on Windows (the
// immediate child's PID — see extractProcessGroupID) to the Job Object that
// PID was assigned to. killProcessGroup only receives that int, so this is
// how it finds the Job to terminate.
var (
	windowsJobsMu sync.Mutex
	windowsJobs   = map[int]windowsJob{}
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
// cmd is the owning exec.Cmd (see releaseProcessGroup for why identity
// matters: the PID may already belong to a newer connection). Only a Job
// registered for this cmd is terminated. If the slot is held by someone
// else, this connection's process is already gone and nothing is killed.
//
// Falls back to a plain single-process kill if no job was ever registered
// for this PID (e.g. job-object setup itself failed) — degrades to the old
// behaviour rather than doing nothing. The fallback goes through cmd's own
// process handle when available (PID-reuse-proof; Windows keeps a PID
// reserved while a handle to it is open) and only resolves by PID for the
// legacy nil-cmd case.
func killProcessGroup(pgid int, cmd *exec.Cmd, logger *zap.Logger, serverName string) error {
	if pgid <= 0 {
		return nil
	}

	job := takeWindowsJob(pgid, cmd)
	if job == nil {
		if cmd != nil && windowsJobOwnedByOther(pgid, cmd) {
			logger.Debug("PID now belongs to a newer connection's Job; nothing to kill for this one",
				zap.String("server", serverName),
				zap.Int("pid", pgid))
			return nil
		}
		logger.Warn("No Windows Job Object tracked for this process; falling back to killing only the immediate process (any grandchildren will leak)",
			zap.String("server", serverName),
			zap.Int("pid", pgid))
		return killImmediateProcess(pgid, cmd)
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

	registerWindowsJob(pid, cmd, job, logger, serverName)

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
//
// cmd identifies the connection's own process: by the time this runs the
// graceful close may already have reaped cmd.exe, and Windows reuses PIDs
// eagerly, so a concurrently connecting server can have registered a NEW
// Job under the same PID. Only the entry registered for this exact cmd is
// released; if the slot now belongs to someone else, ours was already
// closed by registerWindowsJob when it took the slot over. A nil cmd
// disables the identity check (not used by production callers).
func releaseProcessGroup(pgid int, cmd *exec.Cmd, logger *zap.Logger, serverName string) {
	if pgid <= 0 {
		return
	}
	job := takeWindowsJob(pgid, cmd)
	if job == nil {
		// Already killed via killProcessGroup, job setup failed at connect
		// time (extractProcessGroupID logged a Warn then), or the PID slot
		// was taken over by a newer process (see above).
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
// When cmd is non-nil the entry is only taken if it was registered for
// that same cmd. Popping under the lock gives the caller sole ownership
// of the Close.
func takeWindowsJob(pid int, cmd *exec.Cmd) *winjob.Job {
	windowsJobsMu.Lock()
	defer windowsJobsMu.Unlock()
	entry, ok := windowsJobs[pid]
	if !ok || (cmd != nil && entry.cmd != cmd) {
		return nil
	}
	delete(windowsJobs, pid)
	return entry.job
}

// windowsJobOwnedByOther reports whether pid has a registered Job that
// belongs to a different cmd — i.e. Windows reused the PID for a newer
// connection after this one's process exited.
func windowsJobOwnedByOther(pid int, cmd *exec.Cmd) bool {
	windowsJobsMu.Lock()
	defer windowsJobsMu.Unlock()
	entry, ok := windowsJobs[pid]
	return ok && entry.cmd != cmd
}

// killImmediateProcess is the no-Job fallback: kill just the one process.
// With a cmd we use its own process handle, which cannot alias a reused
// PID; an already-exited child is not an error (ErrProcessDone).
func killImmediateProcess(pid int, cmd *exec.Cmd) error {
	if cmd != nil && cmd.Process != nil {
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil // already gone
	}
	if err := proc.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

// registerWindowsJob records the Job for pid. If an entry already exists —
// Windows reused a PID whose earlier Job has not been released yet — the
// old Job is closed first rather than silently dropped with its handle
// (and any straggler still inside it) left open forever. The previous
// owner's later releaseProcessGroup then finds the slot belongs to a
// different cmd and does nothing.
func registerWindowsJob(pid int, cmd *exec.Cmd, job *winjob.Job, logger *zap.Logger, serverName string) {
	windowsJobsMu.Lock()
	prev, hadPrev := windowsJobs[pid]
	windowsJobs[pid] = windowsJob{job: job, cmd: cmd}
	windowsJobsMu.Unlock()

	if hadPrev {
		logger.Warn("Windows Job Object already registered for reused PID; closing the stale one",
			zap.String("server", serverName),
			zap.Int("pid", pid))
		_ = prev.job.Close()
	}
}
