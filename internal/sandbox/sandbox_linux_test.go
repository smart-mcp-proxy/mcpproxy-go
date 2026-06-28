//go:build linux

package sandbox

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// Child-process protocol (see sandboxChild). The enforcement test re-execs this
// same test binary with envChild set; Landlock confinement is irreversible, so
// it MUST run in a throwaway subprocess rather than the test process itself.
const (
	envChild  = "MCPPROXY_SANDBOX_TEST_CHILD"
	envRWDir  = "MCPPROXY_SANDBOX_TEST_RW"
	envSecret = "MCPPROXY_SANDBOX_TEST_SECRET"
)

func TestMain(m *testing.M) {
	if os.Getenv(envChild) == "1" {
		os.Exit(sandboxChild())
	}
	os.Exit(m.Run())
}

// sandboxChild applies a Landlock allowlist confinement to itself, then proves:
//  1. raw stdin->stdout passthrough still works (JSON-RPC framing survives),
//  2. the read-write allowlist entry is readable AND writable,
//  3. a path OUTSIDE the allowlist is DENIED.
//
// Exit codes are the assertion channel back to the parent test.
func sandboxChild() int {
	rwDir := os.Getenv(envRWDir)
	secret := os.Getenv(envSecret)

	spec := Spec{
		// Generous system RO set so the Go runtime/loader keep working; none of
		// these cover the secret (which lives under its own temp dir).
		ReadOnlyPaths:  []string{"/usr", "/lib", "/lib64", "/bin", "/etc", "/proc", "/sys", "/dev"},
		ReadWritePaths: []string{rwDir},
	}
	if rep, err := Apply(spec); err != nil {
		fmt.Fprintln(os.Stderr, "child: Apply failed:", err)
		return 12
	} else if rep.LandlockABI < 1 {
		fmt.Fprintln(os.Stderr, "child: Landlock not enforced:", rep.LandlockNote)
		return 12
	}

	// (1) passthrough
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	fmt.Fprint(os.Stdout, line)

	// (2) RW allowlist works
	if _, err := os.ReadFile(filepath.Join(rwDir, "allowed.txt")); err != nil {
		fmt.Fprintln(os.Stderr, "child: allowed read failed:", err)
		return 11
	}
	if err := os.WriteFile(filepath.Join(rwDir, "written.txt"), []byte("ok"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "child: allowed write failed:", err)
		return 11
	}

	// (3) outside the allowlist must be denied
	if _, err := os.ReadFile(secret); err == nil {
		fmt.Fprintln(os.Stderr, "child: SECRET WAS READABLE — sandbox did not deny")
		return 10
	}
	return 0
}

func TestLandlockEnforcesFilesystemAllowlist(t *testing.T) {
	if abi, err := landlockABI(); err != nil || abi < 1 {
		t.Skipf("Landlock unavailable (abi=%d, err=%v); needs kernel 5.13+ with Landlock LSM enabled", abi, err)
	}

	rwDir := t.TempDir()
	secretDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(rwDir, "allowed.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(secretDir, "secret.txt")
	if err := os.WriteFile(secret, []byte("TOPSECRET"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0]) //nolint:gosec // re-exec of this test binary by design
	cmd.Env = append(os.Environ(),
		envChild+"=1",
		envRWDir+"="+rwDir,
		envSecret+"="+secret,
	)
	cmd.Stdin = strings.NewReader("PING-1234\n")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	runErr := cmd.Run()

	// stdin->stdout framing must survive confinement.
	if got := strings.TrimSpace(out.String()); got != "PING-1234" {
		t.Errorf("stdio passthrough broken: got %q (stderr: %s)", got, errb.String())
	}
	// exit 0 == secret denied AND allowlist usable.
	if runErr != nil {
		t.Fatalf("confined child failed: %v\nchild stderr:\n%s", runErr, errb.String())
	}
	// the child should have been able to write inside the RW allowlist.
	if _, err := os.Stat(filepath.Join(rwDir, "written.txt")); err != nil {
		t.Errorf("expected child to write inside RW allowlist: %v", err)
	}
}

func TestHandledAccessFSMasksByABI(t *testing.T) {
	// ABI 1 must not advertise rights introduced by later ABIs.
	h1 := handledAccessFS(1)
	for _, bit := range []struct {
		name string
		v    uint64
	}{
		{"REFER (ABI2)", unix.LANDLOCK_ACCESS_FS_REFER},
		{"TRUNCATE (ABI3)", unix.LANDLOCK_ACCESS_FS_TRUNCATE},
		{"IOCTL_DEV (ABI5)", unix.LANDLOCK_ACCESS_FS_IOCTL_DEV},
	} {
		if h1&bit.v != 0 {
			t.Errorf("handledAccessFS(1) must not include %s", bit.name)
		}
	}
	// ABI 5 must advertise the full set.
	if h5 := handledAccessFS(5); h5 != uint64(accessFSReadWrite) {
		t.Errorf("handledAccessFS(5) = %#x, want full set %#x", h5, uint64(accessFSReadWrite))
	}
}
