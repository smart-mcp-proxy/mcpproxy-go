package server

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"testing"
	"time"
)

// allocsFreshProcessEnv marks the child process runInFreshProcess starts.
const allocsFreshProcessEnv = "MCPPROXY_TEST_ALLOCS_FRESH_PROCESS"

// runInFreshProcess makes an allocation-parity test measure in a process of
// its own. testing.AllocsPerRun counts every malloc in the process, and by the
// time such a test runs in a full package run, earlier tests have left
// hundreds of live upstream managers behind: on CI, 967 network-recovery
// watchers each ticking every 10s, plus clients reconnecting to closed
// fixtures. That background work adds allocations to whichever case it
// overlaps, on every retry, so exact parity reads 1 / 47 / 35 when the code
// does 1 / 1 / 1. A fresh copy of the test binary running only this test has
// none of it, so the parity is checked at the same tolerance against the
// code alone.
//
// In the parent it reruns the calling test alone, reports the child's
// outcome, and returns true: the caller returns at once. In the child it
// returns false and the test measures as usual.
func runInFreshProcess(t *testing.T) bool {
	t.Helper()
	if os.Getenv(allocsFreshProcessEnv) != "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0],
		"-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), allocsFreshProcessEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed in a fresh process: %v\n%s", t.Name(), err, out)
	}
	if !regexp.MustCompile(`(?m)^--- PASS: ` + regexp.QuoteMeta(t.Name()) + ` `).Match(out) {
		t.Fatalf("%s did not run in the fresh process:\n%s", t.Name(), out)
	}
	return true
}
