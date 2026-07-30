//go:build unix

package sandbox

import (
	"bytes"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestThreadLost pins the fail-closed guard's decision table. RunChild consults
// it immediately before execve: a true verdict must abort the exec, because
// exec'ing from a thread outside the Landlock domain runs the untrusted command
// with no confinement at all.
func TestThreadLost(t *testing.T) {
	cases := []struct {
		name   string
		rep    Report
		nowTID int
		want   bool
	}{
		{
			name:   "same thread — safe to exec",
			rep:    Report{LandlockABI: 5, LandlockTID: 4242},
			nowTID: 4242,
			want:   false,
		},
		{
			name:   "thread changed after confinement — must refuse",
			rep:    Report{LandlockABI: 5, LandlockTID: 4242},
			nowTID: 4243,
			want:   true,
		},
		{
			name:   "no domain enforced (rlimits only) — nothing to verify",
			rep:    Report{LandlockTID: 0},
			nowTID: 4243,
			want:   false,
		},
		{
			name:   "Landlock unavailable under BestEffort — nothing to verify",
			rep:    Report{LandlockABI: -1, LandlockTID: 0},
			nowTID: 99,
			want:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := threadLost(c.rep, c.nowTID); got != c.want {
				t.Errorf("threadLost(%+v, %d) = %v, want %v", c.rep, c.nowTID, got, c.want)
			}
		})
	}
}

// fakeConfinement makes RunChild's Apply step return rep, so the fail-closed
// guard can be driven from a test process that must not actually confine itself.
func fakeConfinement(t *testing.T, rep Report) {
	t.Helper()
	prev := applyConfinement
	applyConfinement = func(Spec) (Report, error) { return rep, nil }
	t.Cleanup(func() { applyConfinement = prev })
}

// unreachableTarget is an absolute path that does not exist. Absolute so
// RunChild skips the PATH lookup and gets as far as the guard; non-existent so
// that a RunChild which reached syscall.Exec fails with ENOENT (126) instead of
// replacing this test binary's process image. The two failure modes are
// therefore distinguishable: 4 means the guard refused before exec, 126 means it
// did not.
func unreachableTarget(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "never-exec-me")
}

// TestRunChildRefusesExecWhenThreadLost covers the guard where it actually
// matters — inside RunChild, before execve. TestThreadLost alone would stay
// green if the call were deleted from RunChild or moved below syscall.Exec, and
// either edit re-opens the escape this whole change exists to close.
func TestRunChildRefusesExecWhenThreadLost(t *testing.T) {
	t.Setenv(EnvSpec, `{}`)
	// currentTID()+1 is never the running thread, on Linux (real tid) or
	// elsewhere (currentTID is a constant 0 off Linux).
	fakeConfinement(t, Report{LandlockABI: 5, LandlockTID: currentTID() + 1})

	target := unreachableTarget(t)
	var diag bytes.Buffer
	code := RunChild([]string{target}, &diag)

	if code != 4 {
		t.Fatalf("RunChild with a lost Landlock thread = %d, want 4 (refuse before execve); diag:\n%s",
			code, diag.String())
	}
	if strings.Contains(diag.String(), "sandbox: exec ") {
		t.Errorf("RunChild attempted the execve despite the lost thread; diag:\n%s", diag.String())
	}
	if !strings.Contains(diag.String(), "refusing to exec") || !strings.Contains(diag.String(), "UNCONFINED") {
		t.Errorf("refusal must say what went wrong and why it matters; diag:\n%s", diag.String())
	}
}

// TestRunChildExecsWhenThreadHeld is the other half of the pin: the guard must
// not refuse when the domain is still on this thread, or it would block every
// legitimate launch. Without this, "return 4 unconditionally" would pass the
// test above.
func TestRunChildExecsWhenThreadHeld(t *testing.T) {
	t.Setenv(EnvSpec, `{}`)
	// Hold the thread so the tid recorded here is still the tid RunChild sees.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	fakeConfinement(t, Report{LandlockABI: 5, LandlockTID: currentTID()})

	target := unreachableTarget(t)
	var diag bytes.Buffer
	code := RunChild([]string{target}, &diag)

	if code != 126 {
		t.Fatalf("RunChild on the confined thread = %d, want 126 (execve reached, target absent); diag:\n%s",
			code, diag.String())
	}
	if !strings.Contains(diag.String(), "sandbox: exec ") {
		t.Errorf("expected the diag to show execve was attempted; diag:\n%s", diag.String())
	}
}
