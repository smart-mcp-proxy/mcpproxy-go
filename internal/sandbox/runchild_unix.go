//go:build unix

package sandbox

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// RunChild is the entrypoint for the hidden `__sandbox_exec` subcommand. argv is
// the command line after the `--` separator: argv[0] is the program to run and
// argv[1:] are its arguments. It decodes the Spec from EnvSpec, applies the
// confinement, performs a best-effort uid/gid drop, then execs the target,
// replacing this process image — so the untrusted server inherits the locked
// Landlock domain and its stdin/stdout/stderr stay wired straight through to the
// parent with no mux.
//
// On success it never returns (execve replaces the image). It returns a non-zero
// exit code on any failure; diag receives human-readable confinement notes and
// errors (os.Stderr in production, so they land in the per-server upstream log).
// Exit codes: 2 bad invocation or missing spec, 3 confinement unavailable while
// fail-closed, 4 the Landlock domain's thread was lost before execve, 126 execve
// failed, 127 target not found on PATH.
func RunChild(argv []string, diag io.Writer) int {
	if diag == nil {
		diag = io.Discard
	}
	if len(argv) == 0 {
		fmt.Fprintln(diag, "sandbox: no command to exec")
		return 2
	}

	spec, ok, err := SpecFromEnv()
	if err != nil {
		fmt.Fprintln(diag, err)
		return 2
	}
	if !ok {
		// The wrapper must never run a command unconfined just because the spec
		// went missing — that would silently defeat the isolation request.
		fmt.Fprintln(diag, "sandbox: missing spec env; refusing to run unconfined")
		return 2
	}

	// Resolve the target before confinement so a bare command name can be looked
	// up on PATH while the filesystem is still fully visible.
	target := argv[0]
	if filepath.Base(target) == target {
		resolved, lerr := exec.LookPath(target)
		if lerr != nil {
			fmt.Fprintf(diag, "sandbox: lookup %q: %v\n", target, lerr)
			return 127
		}
		target = resolved
	}

	rep, err := applyConfinement(spec)
	if err != nil {
		// fail-closed: BestEffort was false and the primitive is unavailable.
		fmt.Fprintf(diag, "sandbox: confinement unavailable and fail-closed: %v\n", err)
		return 3
	}
	fmt.Fprintf(diag, "sandbox: %s\n", describeReport(rep))

	dropPrivilegesBestEffort(diag)

	// Everything between Apply and here — the diag write above, the privilege
	// drop, os.Environ() — can park this goroutine. Apply pins it to the thread
	// that carries the Landlock domain, so a mismatch here means that pin failed
	// and an execve now would run the untrusted command unconfined. Refuse
	// instead: an MCP server that does not start is recoverable, one that starts
	// outside its sandbox is not.
	if now := currentTID(); threadLost(rep, now) {
		fmt.Fprintf(diag, "sandbox: refusing to exec %q — thread changed after confinement "+
			"(Landlock domain is on tid %d, now running on tid %d); the command would run UNCONFINED\n",
			target, rep.LandlockTID, now)
		return 4
	}

	if err := syscall.Exec(target, argv, os.Environ()); err != nil {
		fmt.Fprintf(diag, "sandbox: exec %q: %v\n", target, err)
		return 126
	}
	return 0 // unreachable: Exec replaced the image on success.
}

// applyConfinement indirects Apply for RunChild. Production never reassigns it;
// it exists so the test suite can drive RunChild's fail-closed thread guard,
// whose refusal path is otherwise untestable: Apply is irreversible for the
// calling thread (so the test process cannot call it for real) and a genuine
// thread migration is a race that cannot be provoked on demand. Without the
// seam, deleting the guard from RunChild — or moving it after syscall.Exec —
// would leave every test still green.
var applyConfinement = Apply

// threadLost reports whether the Landlock domain Apply committed lives on a
// different thread than the one now about to execve — in which case the exec
// would run the untrusted command outside the domain entirely. A zero
// LandlockTID means no domain was enforced (rlimits-only, or Landlock
// unavailable under BestEffort), so there is nothing to verify and nothing to
// lose by exec'ing.
func threadLost(rep Report, nowTID int) bool {
	return rep.LandlockTID != 0 && nowTID != rep.LandlockTID
}

// describeReport renders a one-line honest summary of what Apply enforced.
func describeReport(rep Report) string {
	switch {
	case rep.LandlockABI >= 1:
		s := fmt.Sprintf("Landlock enforced (ABI %d), %d rlimit(s) set", rep.LandlockABI, rep.RlimitsSet)
		if rep.LandlockNote != "" {
			s += "; " + rep.LandlockNote
		}
		return s
	case rep.LandlockABI < 0:
		return fmt.Sprintf("running DEGRADED/unconfined — %s (%d rlimit(s) set)", rep.LandlockNote, rep.RlimitsSet)
	default:
		return fmt.Sprintf("%s (%d rlimit(s) set)", rep.LandlockNote, rep.RlimitsSet)
	}
}

// dropPrivilegesBestEffort drops to the real uid/gid when the wrapper is running
// as root with a non-root real user (e.g. a setuid/elevated launch). This is a
// best-effort defense-in-depth step: in the personal edition mcpproxy runs as
// the user, not root, so this is a documented no-op. A real, unconditional
// privilege drop requires root/CAP_SETUID and an explicit target uid/gid, which
// is out of scope here.
func dropPrivilegesBestEffort(diag io.Writer) {
	euid, ruid := os.Geteuid(), os.Getuid()
	if euid != 0 || ruid == 0 {
		return // not privileged, or already the real user — nothing to drop.
	}
	rgid := os.Getgid()
	if err := syscall.Setgid(rgid); err != nil {
		fmt.Fprintf(diag, "sandbox: best-effort setgid(%d) failed: %v\n", rgid, err)
		return
	}
	if err := syscall.Setuid(ruid); err != nil {
		fmt.Fprintf(diag, "sandbox: best-effort setuid(%d) failed: %v\n", ruid, err)
		return
	}
	fmt.Fprintf(diag, "sandbox: dropped privileges to uid=%d gid=%d\n", ruid, rgid)
}
