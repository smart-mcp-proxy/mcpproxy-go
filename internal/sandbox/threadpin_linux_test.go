//go:build linux

package sandbox

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Child-process protocol for the thread-pinning regression (see escapeChild).
const (
	envEscapeChild = "MCPPROXY_SANDBOX_TEST_ESCAPE_CHILD"
	envTIDChild    = "MCPPROXY_SANDBOX_TEST_TID_CHILD"
	envOutsideDir  = "MCPPROXY_SANDBOX_TEST_OUTSIDE"
)

// escapeChild reproduces, deliberately, the shape that let an untrusted command
// run UNCONFINED even though Apply reported "Landlock enforced":
//
//	Apply → (goroutine parks and may resume on a different OS thread) → execve
//
// Landlock domains are per-THREAD: landlock_restrict_self(2) commits the new
// credentials on `current` only. The production wrapper does a blocking diag
// write, a privilege drop and os.Environ() between Apply and syscall.Exec, each
// of which can park the goroutine; on a loaded machine the Go scheduler then
// resumes it on an OS thread that never entered the Landlock domain, and the
// execve from that thread runs the untrusted command with no confinement at all.
//
// Here we reproduce that window deliberately (see provokeThreadMigration),
// turning a load-dependent race into a near-certain one. Apply must pin the
// thread so the exec is unconditionally confined.
func escapeChild() int {
	rwDir := os.Getenv(envRWDir)
	outside := os.Getenv(envOutsideDir)

	spec := Spec{
		// RO "/" so /bin/sh and the loader stay reachable; the only writable
		// subtrees are rwDir and /dev. outside is under neither.
		ReadOnlyPaths:  []string{"/"},
		ReadWritePaths: []string{rwDir, "/dev"},
	}
	rep, err := Apply(spec)
	if err != nil {
		os.Stderr.WriteString("escape-child: Apply failed: " + err.Error() + "\n")
		return 12
	}
	if rep.LandlockABI < 1 {
		os.Stderr.WriteString("escape-child: Landlock not enforced: " + rep.LandlockNote + "\n")
		return 12
	}

	provokeThreadMigration()

	// Exec directly rather than through RunChild: this test must exercise the
	// thread pin itself, not the fail-closed tid guard that backstops it.
	script := "echo escaped > " + shellQuoteTest(filepath.Join(outside, "escaped.txt"))
	err = syscall.Exec("/bin/sh", []string{"/bin/sh", "-c", script}, os.Environ())
	os.Stderr.WriteString("escape-child: exec failed: " + err.Error() + "\n")
	return 126
}

// TestApplyPinsThreadAcrossReschedule is the regression guard for the per-thread
// Landlock escape. Each iteration re-execs this test binary into escapeChild,
// which confines itself, deliberately invites a thread migration, and then execs
// a shell that writes OUTSIDE the allowlist. A single surviving write is a
// security failure, so the tolerance is zero.
func TestApplyPinsThreadAcrossReschedule(t *testing.T) {
	if !Available() {
		t.Skip("Landlock unavailable on this kernel (needs 5.13+ with Landlock LSM enabled)")
	}

	const iterations = 20
	escapes := 0
	for i := 0; i < iterations; i++ {
		rwDir := t.TempDir()
		outside := t.TempDir()

		cmd := exec.Command(os.Args[0]) //nolint:gosec // re-exec of this test binary by design
		cmd.Env = append(os.Environ(),
			envEscapeChild+"=1",
			envRWDir+"="+rwDir,
			envOutsideDir+"="+outside,
		)
		var errb bytes.Buffer
		cmd.Stderr = &errb
		runErr := cmd.Run()

		// exit 12 means the child could not confine itself at all — that is a
		// broken test environment, not a pass.
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) && exitErr.ExitCode() == 12 {
			t.Fatalf("iteration %d: child could not enforce Landlock:\n%s", i, errb.String())
		}

		escaped := filepath.Join(outside, "escaped.txt")
		if _, err := os.Stat(escaped); err == nil {
			escapes++
			t.Errorf("iteration %d: command exec'd after Apply wrote OUTSIDE the allowlist (%s) — "+
				"it ran unconfined; child stderr:\n%s", i, escaped, errb.String())
		}
	}
	if escapes > 0 {
		t.Fatalf("%d/%d exec'd commands escaped the Landlock domain; want 0", escapes, iterations)
	}
}

// TestApplyReportsRestrictedThread pins the fail-closed guard's input: Apply
// must report the tid it actually confined, and must still be on that thread
// when it returns.
func TestApplyReportsRestrictedThread(t *testing.T) {
	if !Available() {
		t.Skip("Landlock unavailable on this kernel (needs 5.13+ with Landlock LSM enabled)")
	}
	// Apply is irreversible for the calling thread, so this assertion has to run
	// in the same throwaway child machinery as the enforcement tests.
	rwDir := t.TempDir()
	outside := t.TempDir()
	cmd := exec.Command(os.Args[0]) //nolint:gosec // re-exec of this test binary by design
	cmd.Env = append(os.Environ(),
		envTIDChild+"=1",
		envRWDir+"="+rwDir,
		envOutsideDir+"="+outside,
	)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("tid child failed: %v\nchild stderr:\n%s", err, errb.String())
	}
}

// tidChild asserts Report.LandlockTID is populated and still matches the running
// thread after a deliberate reschedule window.
func tidChild() int {
	rwDir := os.Getenv(envRWDir)
	rep, err := Apply(Spec{ReadOnlyPaths: []string{"/"}, ReadWritePaths: []string{rwDir}})
	if err != nil {
		os.Stderr.WriteString("tid-child: Apply failed: " + err.Error() + "\n")
		return 12
	}
	if rep.LandlockTID == 0 {
		os.Stderr.WriteString("tid-child: Report.LandlockTID not populated after enforcement\n")
		return 11
	}
	provokeThreadMigration()

	if now := currentTID(); now != rep.LandlockTID {
		os.Stderr.WriteString("tid-child: thread moved after Apply: confined tid differs from current\n")
		return 10
	}
	return 0
}

func shellQuoteTest(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// provokeThreadMigration parks the calling goroutine in a blocking syscall while
// every P is saturated. That is precisely the mechanism by which the production
// wrapper used to lose its Landlock-restricted thread: sysmon retakes the P
// during the blocking call, and on exitsyscall the M cannot reacquire one, so
// the goroutine is handed to a different M — a different OS thread, outside the
// domain.
//
// The syscall shape is load-bearing. Measured on a 14-CPU Linux 6.12 kernel over
// 50 unpinned runs each: a blocking pipe read moved the goroutine 50/50 times,
// while a plain time.Sleep of the same duration moved it 0/50 (the runtime hands
// a sleeping goroutine back to the M it parked on). A sleep-based version of
// this test would be a vacuous guard.
func provokeThreadMigration() {
	var fds [2]int
	if err := unix.Pipe(fds[:]); err != nil {
		return
	}
	defer unix.Close(fds[0])
	defer unix.Close(fds[1])

	// A genuine spin (not a yielding loop) is what keeps every P occupied, so
	// the blocking read below cannot get its P back on exitsyscall.
	var stop atomic.Bool
	for i := 0; i < runtime.NumCPU()*4; i++ {
		go func() {
			for !stop.Load() { //nolint:revive // deliberate busy-wait: saturating every P is the point
			}
		}()
	}
	defer stop.Store(true)

	go func() {
		time.Sleep(2 * time.Millisecond)
		_, _ = unix.Write(fds[1], []byte{'x'})
	}()
	buf := make([]byte, 1)
	_, _ = syscall.Read(fds[0], buf)
}
