package scanner

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// TestFirstContainerIDPicksValidHexID covers the ID-shape validation
// firstContainerID performs on `docker ps --format {{.ID}}` output: IDs are
// the one field Docker guarantees is a safe opaque hex token, but the line
// parser still checks the shape rather than trusting the first line blindly.
func TestFirstContainerIDPicksValidHexID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"short id", "deadbeef0123\n", "deadbeef0123"},
		{"full id", strings.Repeat("a", 64) + "\n", strings.Repeat("a", 64)},
		{"multiple lines picks first", "abc123456789\ndef987654321\n", "abc123456789"},
		{"empty input", "", ""},
		{"blank lines only", "\n\n", ""},
		{"rejects too-short garbage", "abc\n", ""},
		{"rejects non-hex garbage", strings.Repeat("g", 12) + "\n", ""},
		{"skips a bad line then picks a good one", "not-an-id\n" + strings.Repeat("b", 12) + "\n", strings.Repeat("b", 12)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstContainerID(tc.in); got != tc.want {
				t.Errorf("firstContainerID(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// dockerAvailableForTest reports whether a real Docker daemon can be reached,
// matching the check DockerRunner.IsDockerAvailable performs elsewhere in this
// package. The regression test below needs a genuine daemon — the
// vulnerability it proves is in how Docker itself renders/matches labels, not
// in a fake shim's approximation of that behavior.
func dockerAvailableForTest(t *testing.T) bool {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "docker", "info")
	return cmd.Run() == nil
}

// TestFindServerContainerRejectsLabelInjection is a regression test for the
// vulnerability opencode's cross-review of the original substring-collision
// fix found: re-verifying `docker ps --filter name=` candidates by rendering
// `{{.Label "com.mcpproxy.server"}}` into tab/newline-delimited --format text
// and parsing it back is itself exploitable. mcpproxy validates server names
// only for non-empty and no ':' (internal/config/config.go), so a name
// containing an embedded newline+tab forges an extra "record" that a naive
// line parser attributes to whatever text follows the tab — letting a
// same-Docker-daemon server redirect scanning to an arbitrary container ID of
// its choosing.
//
// findServerContainer no longer parses rendered label text at all: ownership
// is established purely through `docker ps --filter label=key=value`, which
// Docker matches against the raw label bytes server-side. This test proves
// that against a REAL daemon (skipped if Docker is unavailable) — the fix
// here is exactly Docker's own filter semantics, which a fake shim cannot
// stand in for.
func TestFindServerContainerRejectsLabelInjection(t *testing.T) {
	if !dockerAvailableForTest(t) {
		t.Skip("docker daemon not available")
	}
	ctx := context.Background()

	// A server named "a-b" whose label value embeds a newline + a fabricated
	// container ID + a tab + the victim server name "a". If this were ever
	// rendered into `{{.ID}}\t{{.Label ...}}` text and parsed line-by-line, it
	// would produce a spoofed second line "<injectedID>\ta" — an exact match
	// for server "a".
	injectedID := "fake" + strings.Repeat("0", 60) // looks like a 64-char container ID
	maliciousLabel := "a-b\n" + injectedID + "\ta"
	containerName := "mcpproxy-a-b-" + t.Name() + "-poc"

	runCmd := exec.CommandContext(ctx, "docker", "run", "-d", "--rm",
		"--label", "com.mcpproxy.managed=true",
		"--label", "com.mcpproxy.server="+maliciousLabel,
		"--name", containerName,
		"alpine", "sleep", "60")
	var runOut bytes.Buffer
	runCmd.Stdout = &runOut
	if err := runCmd.Run(); err != nil {
		t.Skipf("could not start docker PoC container (offline image pull?): %v", err)
	}
	realContainerID := strings.TrimSpace(runOut.String())
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", containerName).Run()
	})

	r := NewSourceResolver(zap.NewNop())

	// Scanning victim server "a" must find NOTHING — not the real container
	// (whose true label is "a-b\n...", not "a"), and definitely not the
	// forged in-band container ID.
	id, err := r.findServerContainer(ctx, "a")
	if err == nil {
		t.Fatalf("findServerContainer(%q) = %q, nil error; want an error (no container owned by %q)", "a", id, "a")
	}
	if id == injectedID {
		t.Fatalf("findServerContainer(%q) returned the INJECTED container id %q — label injection succeeded", "a", injectedID)
	}

	// Scanning the genuine owner must find the real container by its exact,
	// full (newline-containing) label value.
	id, err = r.findServerContainer(ctx, maliciousLabel)
	if err != nil {
		t.Fatalf("findServerContainer(%q) unexpected error: %v", maliciousLabel, err)
	}
	if id != realContainerID {
		t.Errorf("findServerContainer(%q) = %q, want the real container id %q", maliciousLabel, id, realContainerID)
	}
}

// TestFindServerContainerInstanceScoping proves the second review finding is
// closed: two "instances" (simulated by two different com.mcpproxy.instance
// label values on containers that otherwise share the same managed+server
// labels) must not be able to select each other's container once
// SetInstanceID pins the resolver to one of them.
func TestFindServerContainerInstanceScoping(t *testing.T) {
	if !dockerAvailableForTest(t) {
		t.Skip("docker daemon not available")
	}
	ctx := context.Background()
	serverName := "shared-name-" + t.Name()
	otherContainerName := "mcpproxy-" + serverName + "-other-instance"

	runCmd := exec.CommandContext(ctx, "docker", "run", "-d", "--rm",
		"--label", "com.mcpproxy.managed=true",
		"--label", "com.mcpproxy.server="+serverName,
		"--label", "com.mcpproxy.instance=other-instance-id",
		"--name", otherContainerName,
		"alpine", "sleep", "60")
	if err := runCmd.Run(); err != nil {
		t.Skipf("could not start docker PoC container (offline image pull?): %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", otherContainerName).Run()
	})

	r := NewSourceResolver(zap.NewNop())
	r.SetInstanceID("this-instance-id")

	if id, err := r.findServerContainer(ctx, serverName); err == nil {
		t.Fatalf("findServerContainer(%q) = %q, nil error; want an error (container belongs to a different instance)", serverName, id)
	}

	// Without an instance pinned, the same server+managed labels are enough
	// (back-compat for callers — e.g. existing tests — that never call
	// SetInstanceID).
	r2 := NewSourceResolver(zap.NewNop())
	id, err := r2.findServerContainer(ctx, serverName)
	if err != nil {
		t.Fatalf("findServerContainer(%q) with no instance pinned: unexpected error: %v", serverName, err)
	}
	if id == "" {
		t.Errorf("findServerContainer(%q) with no instance pinned returned empty id", serverName)
	}
}
