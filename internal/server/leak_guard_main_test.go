package server

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// networkRecoveryFrame is the frame every upstream.Manager's connectivity
// watcher (GH #1929) runs in until the manager is shut down.
const networkRecoveryFrame = "upstream.(*networkRecovery).run("

// leakGuardSettle is how long the guard waits for watchers to exit after the
// last test. A watcher leaves as soon as its manager's shutdown context is
// cancelled, at most one probe dial (3s) later; ShutdownAll stops waiting for
// it after 3s, so a watcher may still be on its way out when a test returns.
const leakGuardSettle = 10 * time.Second

// TestMain fails the package run when a test left an upstream.Manager
// running. Such a manager keeps its network-recovery watcher and its clients
// for the rest of the run: the watchers add up (967 of them by the end of a
// full run before this guard), and the clients keep reconnecting to their
// fixture's closed port, sending requests to whatever test listens on it next
// (the stray traffic behind #1918 and #1934). Shut a fixture's manager down
// with newTestUpstreamManager / shutdownUpstreamOnCleanup, or close the
// runtime or server that owns it.
//
// The check runs only after a passing run, so a failing or timed-out test is
// reported as itself rather than as a leak.
func TestMain(m *testing.M) {
	code := m.Run()
	if code == 0 {
		if leaked, stacks := waitForNoNetworkRecoveryWatchers(leakGuardSettle); leaked > 0 {
			fmt.Fprintf(os.Stderr, "FAIL: %d upstream.Manager network-recovery watcher(s) still running after the last test: "+
				"a test did not shut down an upstream manager, runtime or server it created "+
				"(use newTestUpstreamManager or shutdownUpstreamOnCleanup).\n\n%s\n", leaked, stacks)
			code = 1
		}
	}
	os.Exit(code)
}

// waitForNoNetworkRecoveryWatchers polls until no goroutine runs the
// network-recovery watcher or settle elapses, and returns the number still
// running with their stacks.
func waitForNoNetworkRecoveryWatchers(settle time.Duration) (int, string) {
	deadline := time.Now().Add(settle)
	for {
		stacks := networkRecoveryWatcherStacks()
		if len(stacks) == 0 || time.Now().After(deadline) {
			const maxShown = 5
			shown := stacks
			if len(shown) > maxShown {
				shown = shown[:maxShown]
			}
			return len(stacks), strings.Join(shown, "\n\n")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// networkRecoveryWatcherStacks returns the stack of every goroutine that runs
// the network-recovery watcher.
func networkRecoveryWatcherStacks() []string {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	var out []string
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, networkRecoveryFrame) {
			out = append(out, g)
		}
	}
	return out
}
