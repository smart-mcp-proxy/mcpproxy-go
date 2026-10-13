// Package sandbox is a spike (MCP-3232) proving an *unprivileged*, non-Docker
// confinement primitive for stdio MCP servers on hosts where Docker is
// unavailable or broken — notably Ubuntu 24.04 with snap-installed Docker under
// AppArmor, where the deb's systemd hardening fights snap-confine (see
// internal repo issue #457 / cmd/mcpproxy/doctor_env_snapdocker.go).
//
// The mechanism chosen by the spike is the Linux Landlock LSM (kernel 5.13+).
// Unlike user-namespace / bubblewrap sandboxes, Landlock does NOT require
// unprivileged user namespaces and is therefore NOT blocked by
// `kernel.apparmor_restrict_unprivileged_userns=1`, which Ubuntu 23.10+/24.04
// enable by default. See docs/development/sandbox-spike-mcp-34.md for the full
// recommendation and the honest limits (no uid/gid separation without
// privilege, filesystem-allowlist + rlimits only).
//
// This package is intentionally minimal: it confines the *current thread* and,
// because a Landlock domain is inherited across execve, every process that
// thread then execs (the npx/uvx server and its descendants).
//
// The per-thread scope is load-bearing, not a footnote: landlock_restrict_self(2)
// commits the new credentials on the calling thread only. A multithreaded Go
// process therefore CANNOT be confined by calling Apply in-process — the other
// threads keep full filesystem access, and even the calling goroutine may be
// rescheduled onto an unrestricted thread. The only sound shape is the one this
// package implements: a tiny re-exec wrapper that calls Apply and immediately
// execs the untrusted command from the very thread Apply restricted. Apply pins
// that thread (runtime.LockOSThread) and RunChild verifies the thread identity
// again just before execve; the package test exercises exactly that shape.
package sandbox

import "errors"

// ErrUnsupported is returned by Apply on platforms or kernels that do not
// provide the requested confinement primitive (e.g. non-Linux, or a kernel
// without Landlock). Callers that want fail-open behaviour set Spec.BestEffort.
var ErrUnsupported = errors.New("sandbox: confinement primitive unavailable on this platform/kernel")

// Spec describes the confinement to apply to the current process before it
// execs an untrusted stdio MCP server. The zero value applies no filesystem
// restriction (only rlimits, if any).
type Spec struct {
	// ReadOnlyPaths are filesystem subtrees the confined process may read and
	// execute. Anything not covered by a ReadOnlyPaths/ReadWritePaths entry is
	// denied. Missing paths are skipped (best-effort) and noted in the Report.
	ReadOnlyPaths []string

	// ReadWritePaths are subtrees the confined process may read, execute, and
	// write (create/delete/truncate within).
	ReadWritePaths []string

	// Rlimits are resource limits applied via setrlimit before confinement.
	Rlimits []Rlimit

	// BestEffort, when true, downgrades "primitive unavailable" from an error
	// to a no-op recorded in Report.LandlockNote, mirroring go-landlock's
	// BestEffort semantics. When false (the default, fail-closed), Apply
	// returns ErrUnsupported if Landlock cannot be enforced — a security
	// boundary should fail closed rather than silently run unconfined.
	BestEffort bool
}

// Rlimit is a single setrlimit request. Resource is one of the unix.RLIMIT_*
// constants (e.g. RLIMIT_AS, RLIMIT_NOFILE, RLIMIT_NPROC, RLIMIT_CPU).
type Rlimit struct {
	Resource int
	Cur      uint64
	Max      uint64
}

// Report records what Apply actually enforced, so callers can log an honest
// account of the confinement (important because Apply is best-effort across
// kernels with different Landlock ABI levels).
type Report struct {
	// LandlockABI is the kernel's reported Landlock ABI version that was
	// enforced (>=1), 0 if Landlock was not requested, or -1 if Landlock is
	// unavailable on this kernel/platform.
	LandlockABI int
	// LandlockNote is a human-readable note (e.g. why Landlock was skipped, or
	// which paths were missing).
	LandlockNote string
	// RlimitsSet is the number of rlimits successfully applied.
	RlimitsSet int
	// NoNewPrivs reports whether PR_SET_NO_NEW_PRIVS was set (always true when
	// Landlock is enforced; Landlock requires it).
	NoNewPrivs bool
	// LandlockTID is the OS thread id (Linux tid) the Landlock domain was
	// committed on, or 0 when no domain was enforced. A caller about to execve
	// must confirm it is still running on this thread: an execve from any other
	// thread runs the target unconfined.
	LandlockTID int
}

// wantsLandlock reports whether the spec asks for any filesystem confinement.
func (s Spec) wantsLandlock() bool {
	return len(s.ReadOnlyPaths) > 0 || len(s.ReadWritePaths) > 0
}
