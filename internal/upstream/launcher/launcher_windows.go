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
// actual MCP server — dies when the returned Closer's Close is called
// (wired into launcher.go's reap(), which runs on every exit path). Prior
// to this, launcher_windows.go only ever reached the immediate child
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

// terminateProcess signals the child directly. The Job Object (if createJob
// succeeded) is what actually reaches grandchildren — it is closed in
// launcher.go's reap() once the child has exited, not here, since
// terminating the job immediately would kill descendants before giving the
// immediate child a chance to shut down gracefully.
func terminateProcess(cmd *exec.Cmd, _ *zap.Logger) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// killProcess is the hard-kill fallback after the grace period. Same
// reasoning as terminateProcess: the immediate child dies here, the wider
// tree is reaped via the Job Object in reap() once Wait() returns.
func killProcess(cmd *exec.Cmd, _ *zap.Logger) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
