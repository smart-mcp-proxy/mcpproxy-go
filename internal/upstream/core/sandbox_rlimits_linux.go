//go:build linux

package core

import (
	"golang.org/x/sys/unix"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/sandbox"
)

// defaultSandboxRlimits returns the resource limits applied to a sandboxed
// stdio server. Kept deliberately safe and per-process: RLIMIT_CORE=0 disables
// core dumps (which could otherwise leak in-memory secrets to disk) and
// RLIMIT_NOFILE caps the descriptor table. We intentionally avoid RLIMIT_NPROC
// (counted per real-uid, so a low value would starve the user's other
// processes) and RLIMIT_CPU (cumulative — it would kill long-lived servers).
func defaultSandboxRlimits() []sandbox.Rlimit {
	return []sandbox.Rlimit{
		{Resource: unix.RLIMIT_CORE, Cur: 0, Max: 0},
		{Resource: unix.RLIMIT_NOFILE, Cur: 4096, Max: 4096},
	}
}
