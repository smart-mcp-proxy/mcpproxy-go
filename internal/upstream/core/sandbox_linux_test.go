//go:build linux

package core

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/sandbox"
)

// TestSandboxWrapper_EndToEnd drives the full launcher integration on a real
// kernel: it builds the wrapped argv with sandbox.WrapCommand (as connectStdio
// does), re-execs this test binary (TestMain dispatches into sandbox.RunChild),
// and asserts the four behaviors MCP-34.3 requires:
//
//  1. cmd construction — the wrapper actually runs and reaches the target;
//  2. rlimit application — RLIMIT_NOFILE is lowered inside the confined child;
//  3. write-allowlist enforcement — a write inside the allowlist succeeds and a
//     write OUTSIDE it is denied;
//  4. stdin→stdout passthrough — JSON-RPC framing survives confinement (no mux).
func TestSandboxWrapper_EndToEnd(t *testing.T) {
	if !sandbox.Available() {
		t.Skip("Landlock unavailable on this kernel (needs 5.13+ with Landlock LSM enabled)")
	}

	rw := t.TempDir()
	outside := t.TempDir()

	// The confined shell: read system files (RO "/"), write only under rw.
	spec := sandbox.Spec{
		ReadOnlyPaths:  []string{"/"},
		ReadWritePaths: []string{rw, "/dev"}, // /dev so the shell can open /dev/null
		Rlimits:        []sandbox.Rlimit{{Resource: unix.RLIMIT_NOFILE, Cur: 64, Max: 64}},
		BestEffort:     false, // fail-closed: this test requires real enforcement
	}

	// Script: echo stdin back (passthrough), report the fd limit, write inside
	// the allowlist, then try to write OUTSIDE it (must fail).
	script := fmt.Sprintf(`
read line
echo "$line"
ulimit -n
echo allowed > %s/inside.txt
if echo denied > %s/outside.txt 2>/dev/null; then echo WROTE_OUTSIDE; fi
`, shellQuote(rw), shellQuote(outside))

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd, args, extraEnv, err := sandbox.WrapCommand(self, spec, "/bin/sh", []string{"-c", script})
	if err != nil {
		t.Fatalf("WrapCommand: %v", err)
	}

	c := exec.Command(cmd, args...) //nolint:gosec // re-exec of this test binary by design
	c.Env = append(os.Environ(), extraEnv...)
	c.Stdin = strings.NewReader("PING-3234\n")
	out, runErr := c.CombinedOutput()
	if runErr != nil {
		t.Fatalf("confined wrapper failed: %v\noutput:\n%s", runErr, out)
	}
	got := string(out)

	// (4) passthrough
	if !strings.Contains(got, "PING-3234") {
		t.Errorf("stdin→stdout passthrough broken; output:\n%s", got)
	}
	// (2) rlimit applied
	if !strings.Contains(got, "64") {
		t.Errorf("expected RLIMIT_NOFILE=64 in output; got:\n%s", got)
	}
	// (3a) allowlisted write succeeded
	if _, err := os.Stat(filepath.Join(rw, "inside.txt")); err != nil {
		t.Errorf("expected write inside allowlist to succeed: %v", err)
	}
	// (3b) write outside the allowlist denied
	if strings.Contains(got, "WROTE_OUTSIDE") {
		t.Errorf("write OUTSIDE the allowlist was NOT denied; output:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(outside, "outside.txt")); err == nil {
		t.Errorf("file written outside the allowlist exists; sandbox did not deny the write")
	}
}

// TestSandboxWrapper_FailClosed proves the fallback contract's strict side: when
// the spec asks for confinement that the wrapper cannot honor and BestEffort is
// false, the child refuses to exec (non-zero) rather than run the server
// unconfined. We simulate "cannot honor" by stripping the spec env so
// SpecFromEnv fails — RunChild must not fall through to an unconfined exec.
func TestSandboxWrapper_FailClosed(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Build wrapped argv but deliberately DROP extraEnv (no spec reaches child).
	cmd, args, _, err := sandbox.WrapCommand(self, sandbox.Spec{}, "/bin/sh", []string{"-c", "echo SHOULD_NOT_RUN"})
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Command(cmd, args...) //nolint:gosec // re-exec of this test binary by design
	// Scrub any inherited spec env so the child sees it as absent.
	c.Env = scrubEnv(os.Environ(), sandbox.EnvSpec)
	out, runErr := c.CombinedOutput()
	if runErr == nil {
		t.Fatalf("expected non-zero exit when spec is missing, got success; output:\n%s", out)
	}
	if strings.Contains(string(out), "SHOULD_NOT_RUN") {
		t.Errorf("child exec'd the command unconfined despite missing spec; output:\n%s", out)
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func scrubEnv(env []string, key string) []string {
	out := env[:0:0]
	for _, e := range env {
		if strings.HasPrefix(e, key+"=") {
			continue
		}
		out = append(out, e)
	}
	return out
}
