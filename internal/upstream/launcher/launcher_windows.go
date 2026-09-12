//go:build windows

package launcher

import (
	"io"
	"os/exec"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/winjob"
)

// applyProcAttrs is a no-op on Windows. Job Object assignment (createJob,
// below) is what does the equivalent work here, and it has to happen AFTER
// Start() — a Job Object is assigned to a live PID, there's no SysProcAttr
// field to pre-configure the way Setpgid works on Unix.
func applyProcAttrs(_ *exec.Cmd) {}

// createJob assigns the just-started process to a fresh Windows Job Object
// (JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE) so that everything it spawns
// afterwards — e.g. `cmd.exe /c npx ...` spawning node.exe spawning the
// actual MCP server — dies when the returned Closer's Close is called.
// Prior to this, launcher_windows.go only ever reached the immediate child
// (terminateProcess/killProcess below called cmd.Process.Kill()), so a
// launcher-managed server on Windows leaked every grandchild it spawned —
// the same class of leak process_windows.go had on the stdio path.
func createJob(cmd *exec.Cmd) io.Closer {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	job, err := winjob.New()
	if err != nil {
		// Degrade to the old single-process behaviour rather than failing
		// the whole launch over a diagnostics-only feature.
		return nil
	}
	if err := job.Assign(cmd.Process.Pid); err != nil {
		_ = job.Close()
		return nil
	}
	return job
}

// terminate is stopLocked's first escalation step. On Windows there is no
// graceful phase to protect: Process.Kill() is TerminateProcess, so the
// immediate child gets no chance to shut down cleanly either way. What
// matters is reaching the WHOLE tree right here, not in reap(): the
// grandchildren inherit the child's stdout/stderr pipe handles, so if only
// cmd.exe dies the log pumps never see EOF, reap() never runs, Done()
// never closes and Stop() blocks until the caller's ctx expires — the Job
// would only ever have been closed once the tree was already gone (#1234
// follow-up, F2). Closing the job kills every member, the pipes close, the
// pumps drain, and reap() proceeds.
func (h *handle) terminate() error {
	if h.job != nil {
		return h.job.Close()
	}
	return terminateProcess(h.cmd, h.log)
}

// kill is the hard-kill step after the grace period. With a Job the tree is
// already gone (terminate closed it, and Close is idempotent), so this is a
// no-op there — falling through to Process.Kill() on the dead-but-unreaped
// child would only yield a misleading "kill failed" error. It does real
// work only when createJob failed and we are on the single-process fallback.
func (h *handle) kill() error {
	if h.job != nil {
		return h.job.Close()
	}
	return killProcess(h.cmd, h.log)
}

// terminateProcess kills the immediate child only. Used when no Job Object
// could be created (see createJob) — grandchildren will leak in that case.
func terminateProcess(cmd *exec.Cmd, _ *zap.Logger) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// killProcess is the single-process fallback for the hard-kill step.
func killProcess(cmd *exec.Cmd, _ *zap.Logger) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
