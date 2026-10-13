package launcher

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// DefaultStopGrace is the time we wait between SIGTERM and SIGKILL when
// stopping a launched child. Tunable via Handle.SetStopGrace if a particular
// upstream needs longer (e.g. a Docker container with a slow shutdown
// hook). Five seconds is a common default for "graceful" in this codebase
// (see processGracefulTimeout in internal/upstream/core).
const DefaultStopGrace = 5 * time.Second

// Spec describes the command to launch.
//
// Spec is intentionally narrow: it doesn't replicate the full ServerConfig
// surface — the caller is responsible for assembling the final exec.Cmd
// (env, working dir, Docker shell-wrap, process-group attrs). The launcher
// only owns the lifecycle once Cmd is started.
type Spec struct {
	// Cmd is the command to start. The launcher will set Stdout/Stderr
	// pipes and call cmd.Start(). Callers MUST NOT have started cmd
	// already; calling Start again is undefined.
	Cmd *exec.Cmd

	// LogSink receives the child's combined stdout+stderr, line by line.
	// May be nil to discard output (tests).
	LogSink io.Writer

	// Name is used in log messages to identify which upstream's launcher
	// is doing the talking. Optional; defaults to cmd.Path basename.
	Name string

	// StopGrace overrides DefaultStopGrace if non-zero.
	StopGrace time.Duration

	// RedactArgs masks the child's argv for the startup banner written to
	// LogSink (issue #1158).
	//
	// The banner previously wrote cmd.Args verbatim into
	// ~/.mcpproxy/logs/server-<name>.log on every spawn - fully transformed,
	// i.e. AFTER the docker env->argv injection has turned every configured
	// env var into `-e KEY=VALUE`, and after a login-shell wrap has folded the
	// whole command line into one `-c "..."` element.
	//
	// The redactor is injected rather than imported because this package is
	// deliberately dependency-free (`go list -deps` returns only itself);
	// importing internal/oauth would drag config/security/storage in with it.
	//
	// nil means the banner OMITS argv entirely. That default is fail-CLOSED on
	// purpose: a future caller that forgets to set the field loses a
	// diagnostic, not a secret.
	RedactArgs func([]string) []string
}

// Handle represents a running child managed by the launcher. Stop, Wait,
// and Done are safe to call from multiple goroutines.
type Handle interface {
	// Stop signals the child to exit (SIGTERM → grace → SIGKILL on
	// timeout). Blocks until the child is reaped or ctx fires. Calling
	// Stop more than once is safe — subsequent calls return the result
	// of the first.
	Stop(ctx context.Context) error

	// Wait blocks until the child exits. Returns the exit error from
	// exec.Cmd.Wait() (nil on clean exit, *exec.ExitError on non-zero).
	Wait() error

	// Done is closed when the child has exited for any reason and been
	// reaped. Useful for select{}-driven supervision. On Unix (Linux and
	// macOS) the reaper keeps an exited leader unreaped while another
	// member of its process group runs (Spec 116 PR4, review round 6), so
	// Done stays open while a descendant the launcher left behind runs.
	Done() <-chan struct{}

	// Pid returns the OS process id, or 0 if the child has exited.
	Pid() int

	// Kill hard-kills the child and everything it spawned (the process
	// group on Unix, the Job Object on Windows) at once, without a grace
	// period and without waiting for the exit. It is the retirement kill
	// of Spec 116 PR4: safe to call more than once and after the child
	// exited, while a descendant it left in its process group runs.
	// The reaper reaps the child; Stop and Wait wait for that.
	Kill() error
}

// Spawn starts spec.Cmd and returns a Handle owning its lifecycle.
//
// stdout+stderr are line-buffered into spec.LogSink (or discarded if nil).
// On Unix, the child is placed in its own process group so Stop can signal
// the entire group, not just the immediate child — this matters when the
// command shells out (e.g. `sh -c 'docker run …'`) and the actual server
// is a grandchild.
//
// The supplied ctx is NOT used to bound the child's runtime — once Spawn
// returns, the child outlives ctx. ctx is only used to abort cmd.Start()
// itself if it's slow (rare). Callers who want ctx-tied lifetime should
// call Stop from a goroutine watching ctx.Done().
func Spawn(ctx context.Context, spec *Spec, log *zap.Logger) (Handle, error) {
	if spec == nil || spec.Cmd == nil {
		return nil, errors.New("launcher.Spawn: spec.Cmd is required")
	}
	if log == nil {
		log = zap.NewNop()
	}

	cmd := spec.Cmd
	name := spec.Name
	if name == "" {
		name = cmd.Path
	}

	// Apply Unix process-group attrs (no-op on Windows). Callers that
	// already set SysProcAttr win — we don't override their fields.
	applyProcAttrs(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("launcher: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("launcher: stderr pipe: %w", err)
	}

	// Note: we don't honor ctx for Start() because exec.Cmd.Start() is
	// synchronous and short — there's no useful way to interrupt it. We
	// could plumb cmd.Cancel but that's also tied to the cmd's
	// lifetime, not just startup.
	_ = ctx

	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("launcher: start %s: %w", name, err)
	}

	// Contain the tree before the handle exists (Windows: a Job Object; a
	// no-op on Unix, where the process group set up by applyProcAttrs does
	// it). A child that cannot be contained is killed and the launch fails
	// (Spec 116 FR-025a, review round 3): the retirement kill could
	// otherwise reach only the immediate process and leave its
	// descendants running.
	job, err := containTree(cmd)
	if err != nil {
		_ = killProcess(cmd, log)
		_ = cmd.Wait() // reaps the child and closes both pipes
		return nil, fmt.Errorf("launcher: contain %s: %w", name, err)
	}

	h := &handle{
		cmd:       cmd,
		name:      name,
		log:       log.With(zap.String("launcher_server", name)),
		done:      make(chan struct{}),
		stopGrace: spec.StopGrace,
	}
	if h.stopGrace <= 0 {
		h.stopGrace = DefaultStopGrace
	}
	h.pid.Store(int64(cmd.Process.Pid))
	h.pgid = startedGroupID(cmd)

	// On Windows the just-started process is in a Job Object (containTree
	// above) so that terminate/kill (and the natural-exit path in reap)
	// reach the grandchildren it spawns — Process.Kill() alone only ever
	// reaches this one PID. nil on Unix, where the process-group attrs
	// applied via applyProcAttrs already cover this.
	h.job = job

	// Pump stdout+stderr to LogSink. Both streams write to the same
	// sink, prefixed so they remain distinguishable. Discard mode is
	// supported via io.Discard. We wrap LogSink in a small mutex so
	// the stdout pump, the stderr pump, and the startup banner can
	// share an arbitrary io.Writer (bytes.Buffer in tests, zap-bridge
	// in production) without racing on Write.
	rawSink := spec.LogSink
	if rawSink == nil {
		rawSink = io.Discard
	}
	sink := newSerializedWriter(rawSink)

	if rawSink != io.Discard {
		args := cmd.Args
		if len(args) > 0 {
			args = args[1:]
		}
		if spec.RedactArgs == nil {
			// Fail closed: no redactor, no argv. See Spec.RedactArgs.
			_, _ = fmt.Fprintf(sink, "[launcher] starting: %s (%d args, redacted) (pid=%d)\n",
				cmd.Path, len(args), cmd.Process.Pid)
		} else {
			_, _ = fmt.Fprintf(sink, "[launcher] starting: %s %v (pid=%d)\n",
				cmd.Path, spec.RedactArgs(args), cmd.Process.Pid)
		}
	}

	h.pumpWG.Add(2)
	go pumpLines(&h.pumpWG, stdout, sink, "[launcher stdout] ")
	go pumpLines(&h.pumpWG, stderr, sink, "[launcher stderr] ")

	// Reaper goroutine: blocks on cmd.Wait, captures the exit error,
	// then closes the done channel exactly once.
	go h.reap()

	h.log.Info("upstream child process started",
		zap.Int("pid", cmd.Process.Pid),
		zap.String("command", cmd.Path))

	return h, nil
}

type handle struct {
	cmd       *exec.Cmd
	name      string
	log       *zap.Logger
	done      chan struct{}
	stopGrace time.Duration
	job       io.Closer // Windows Job Object wrapper; nil on Unix (see createJob)
	// pgid is the process group the child leads (Unix; 0 on Windows),
	// recorded at spawn (Spec 116 PR4, review round 2): the retirement kill
	// and the reaper's killGroupBeforeReap signal it without a Getpgid
	// lookup. It is never signalled after the leader was reaped (review
	// round 6).
	pgid int

	pid    atomic.Int64 // 0 once exited
	pumpWG sync.WaitGroup

	// reapMu orders the retirement kill with the reaper (review rounds 3
	// and 4): reap calls cmd.Wait and sets reaped under it, and retireKill
	// reads reaped and signals under the same lock. A kill that sees
	// reaped == false therefore signals while the leader (alive or a
	// zombie) still holds the group ID, so the ID cannot have been reused.
	reapMu sync.Mutex
	reaped bool

	stopOnce sync.Once
	stopErr  error

	waitErrMu sync.Mutex
	waitErr   error
}

func (h *handle) Pid() int {
	return int(h.pid.Load())
}

// Kill implements Handle.Kill: the same hard-kill step stopLocked
// escalates to, without the SIGTERM and the grace period. Unlike Stop it
// still acts after the leader exited: a descendant the launcher left in its
// process group is killed too (see retireKill in the platform files).
func (h *handle) Kill() error {
	if h.cmd.Process == nil {
		return nil
	}
	return h.retireKill()
}

func (h *handle) Done() <-chan struct{} {
	return h.done
}

// Wait waits for the child to exit. Multiple callers see the same error.
func (h *handle) Wait() error {
	<-h.done
	h.waitErrMu.Lock()
	defer h.waitErrMu.Unlock()
	return h.waitErr
}

// Stop is the one method users call to bring the child down deterministically.
// First call drives the actual signal sequence; subsequent calls just wait
// for it to finish.
func (h *handle) Stop(ctx context.Context) error {
	h.stopOnce.Do(func() {
		h.stopErr = h.stopLocked(ctx)
	})
	// Even if another goroutine drove the stop, we still want to wait
	// for the child to finish reaping before returning so the caller
	// can rely on "Stop returned → port is free".
	select {
	case <-h.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return h.stopErr
}

func (h *handle) stopLocked(ctx context.Context) error {
	// Already exited?
	select {
	case <-h.done:
		return nil
	default:
	}

	if h.cmd.Process == nil {
		return nil
	}

	h.log.Info("stopping upstream child process",
		zap.Int("pid", h.Pid()),
		zap.Duration("grace", h.stopGrace))

	// SIGTERM the process group (Unix) / terminate the Job Object tree
	// (Windows) — see the platform files for h.terminate.
	if err := h.terminate(); err != nil {
		h.log.Warn("terminate failed (will fall through to wait/kill)",
			zap.Error(err))
	}

	// Wait for graceful exit, falling back to SIGKILL after stopGrace.
	graceCtx, cancel := context.WithTimeout(ctx, h.stopGrace)
	defer cancel()

	select {
	case <-h.done:
		return nil
	case <-graceCtx.Done():
	}

	// Still alive — send SIGKILL.
	h.log.Warn("child did not exit within grace period; sending SIGKILL",
		zap.Int("pid", h.Pid()),
		zap.Duration("grace", h.stopGrace))
	if err := h.kill(); err != nil {
		h.log.Error("kill failed", zap.Error(err))
		// Fall through — we still wait for done.
	}

	select {
	case <-h.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *handle) reap() {
	// Drain log pumps FIRST. exec.Cmd.Wait() closes the parent-side stdout/stderr
	// pipes as part of its cleanup; if we call Wait() before the pumps have read
	// to EOF, the pump scanners can race against that close and observe EOF with
	// buffered child output still in flight, dropping it on the floor. Per the
	// exec.Cmd doc: "it is thus incorrect to call Wait before all reads from the
	// pipe have completed." Pumps naturally finish when the child exits and the
	// kernel propagates EOF — we don't need Wait() to reach that state.
	h.pumpWG.Wait()

	// Reap the leader under reapMu (review round 4). cmd.Wait releases the
	// leader's PID, and with it the process-group ID once the group is
	// empty; reaping outside the lock let a concurrent retireKill observe
	// reaped == false after the kernel had already released the ID, and
	// signal a group that reused it. waitExited first observes the exit
	// without reaping, so the locked cmd.Wait returns at once and Kill is
	// never held up by a live child.
	//
	// Where the exit cannot be observed that way (kqueue refused under fd
	// exhaustion, waitid denied, or no such call on the platform),
	// reapUnobserved still reaps only under reapMu (review round 5): on
	// Unix it polls a non-blocking wait4 under the lock, so the leader is
	// never released between a Kill's reaped check and its signal.
	//
	// Before the locked reap, awaitGroupEmpty keeps the exited leader a
	// zombie while another member of its process group runs, and
	// killGroupBeforeReap kills whatever is left while the zombie still
	// pins the group ID (review round 6): after the reap neither Kill nor
	// Stop signals the group at all.
	var err error
	exitCode := -1
	if observeExit(h.cmd.Process.Pid) {
		h.awaitGroupEmpty()
		h.reapMu.Lock()
		h.killGroupBeforeReap()
		err = h.cmd.Wait()
		if hook := reapedLockedHook.Load(); hook != nil {
			(*hook)(h)
		}
		h.reaped = true
		h.reapMu.Unlock()
	} else {
		exitCode, err = h.reapUnobserved()
	}
	if hook := afterReapHook.Load(); hook != nil {
		(*hook)(h)
	}

	// Release the Job Object (Windows only — see createJob/winjob). By the
	// time we get here every pipe holder has exited, so on the Stop path
	// this is a no-op (h.terminate/h.kill already closed the job — that is
	// what made the pumps reach EOF); it only does real work when the
	// whole tree exited on its own. Close is idempotent and goroutine-safe.
	if h.job != nil {
		_ = h.job.Close()
	}

	h.waitErrMu.Lock()
	h.waitErr = err
	h.waitErrMu.Unlock()

	h.pid.Store(0)

	if h.cmd.ProcessState != nil {
		exitCode = h.cmd.ProcessState.ExitCode()
	}
	h.log.Info("upstream child process exited",
		zap.Int("exit_code", exitCode),
		zap.Error(err))

	close(h.done)
}

// containTree contains a just-started child's process tree (createJob in
// the platform files). A test seam: tests replace it to inject a
// containment failure.
var containTree = createJob

// afterReapHook runs in the reaper after the leader was reaped and before
// Done closes. A test seam; nil in production. Atomic because reapers of
// earlier tests' handles may still read it while a test installs its own.
var afterReapHook atomic.Pointer[func(*handle)]

// reapedLockedHook runs in the reaper right after the leader was really
// reaped (cmd.Wait, or the non-blocking wait4 of reapUnobserved) and before
// reaped is published, with reapMu held. A test seam; nil in production.
var reapedLockedHook atomic.Pointer[func(*handle)]

// exitObserverHook replaces waitExited in reap. A test seam that injects an
// exit-observation failure; nil in production.
var exitObserverHook atomic.Pointer[func(pid int) bool]

func observeExit(pid int) bool {
	if hook := exitObserverHook.Load(); hook != nil {
		return (*hook)(pid)
	}
	return waitExited(pid)
}

// pumpLines reads r line-by-line and writes each line, prefixed, to dst.
// One Write per line so adapters that bridge io.Writer to a structured
// logger get one log entry per line (the obvious shape) instead of three.
func pumpLines(wg *sync.WaitGroup, r io.Reader, dst io.Writer, prefix string) {
	defer wg.Done()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		_, _ = dst.Write([]byte(prefix + line + "\n"))
	}
}

// serializedWriter wraps an io.Writer so concurrent Writes are atomic.
// Production sinks (per-server zap logger) are already thread-safe; we
// add this guard because we promise the launcher will accept an arbitrary
// io.Writer (notably *bytes.Buffer in tests), and bytes.Buffer is NOT
// safe for concurrent Write.
type serializedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func newSerializedWriter(w io.Writer) *serializedWriter {
	return &serializedWriter{w: w}
}

func (s *serializedWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
