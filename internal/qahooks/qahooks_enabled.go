//go:build qa_hooks

package qahooks

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// With -tags qa_hooks:
//
//	MCPPROXY_QA_PARK_WORKER=<server>                  park that server's post-commit workers
//	MCPPROXY_QA_PARK_SUPERVISOR_DISCONNECT=<server>   park that server's supervisor disconnects
//	MCPPROXY_QA_RELEASE_DIR=<dir>                     (default: the OS temp dir)
//
// A parked call writes <dir>/parked-<kind>-<server> and waits until
// <dir>/release-<server> exists (at most 10 minutes). Only one call per park
// point is parked at a time: a call that arrives while another is parked
// passes through, so a later commit's worker is not held behind the parked
// one. Removing the release file re-arms the park point for the next call.
func init() {
	dir := os.Getenv("MCPPROXY_QA_RELEASE_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	if name := os.Getenv("MCPPROXY_QA_PARK_WORKER"); name != "" {
		ParkWorker = parker(dir, "worker", name)
	}
	if name := os.Getenv("MCPPROXY_QA_PARK_SUPERVISOR_DISCONNECT"); name != "" {
		ParkSupervisorDisconnect = parker(dir, "supervisor-disconnect", name)
	}
}

func parker(dir, kind, target string) func(server string) {
	var active atomic.Bool
	return func(server string) {
		if server != target {
			return
		}
		release := filepath.Join(dir, "release-"+server)
		if _, err := os.Stat(release); err == nil {
			return
		}
		if !active.CompareAndSwap(false, true) {
			fmt.Fprintf(os.Stderr, "QA_HOOK passing %s for %s (another call is parked)\n", kind, server)
			return
		}
		defer active.Store(false)
		marker := filepath.Join(dir, "parked-"+kind+"-"+server)
		_ = os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o600)
		fmt.Fprintf(os.Stderr, "QA_HOOK parked %s for %s until %s exists\n", kind, server, release)
		deadline := time.Now().Add(10 * time.Minute)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(release); err == nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		_ = os.Remove(marker)
		fmt.Fprintf(os.Stderr, "QA_HOOK released %s for %s\n", kind, server)
	}
}
