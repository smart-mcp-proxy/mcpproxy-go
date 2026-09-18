package core

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

// withLegacyInstanceIDPath points the legacy (pre-fix, host-wide) instance id
// file at a path under the test's own temp dir instead of the real
// os.TempDir(), so tests don't read or clobber a real machine's legacy file
// and don't race other tests/processes that touch it.
func withLegacyInstanceIDPath(t *testing.T, path string) {
	t.Helper()
	original := legacyInstanceIDPath
	legacyInstanceIDPath = func() string { return path }
	t.Cleanup(func() { legacyInstanceIDPath = original })
}

func TestResolveInstanceIDUniquePerDataDir(t *testing.T) {
	withLegacyInstanceIDPath(t, filepath.Join(t.TempDir(), "no-legacy-file"))

	dir1 := t.TempDir()
	dir2 := t.TempDir()

	id1 := resolveInstanceID(dir1)
	id2 := resolveInstanceID(dir2)

	if id1 == id2 {
		t.Fatalf("expected distinct instance ids for distinct data dirs, got %q for both", id1)
	}
	if _, err := uuid.Parse(id1); err != nil {
		t.Errorf("id1 %q is not a valid UUID: %v", id1, err)
	}
	if _, err := uuid.Parse(id2); err != nil {
		t.Errorf("id2 %q is not a valid UUID: %v", id2, err)
	}
}

func TestResolveInstanceIDPersistsAcrossCalls(t *testing.T) {
	withLegacyInstanceIDPath(t, filepath.Join(t.TempDir(), "no-legacy-file"))

	dir := t.TempDir()

	first := resolveInstanceID(dir)
	second := resolveInstanceID(dir)

	if first != second {
		t.Fatalf("expected the same data dir to resolve the same instance id across calls (simulating a restart), got %q then %q", first, second)
	}
}

func TestResolveInstanceIDNoDataDirReturnsFreshID(t *testing.T) {
	withLegacyInstanceIDPath(t, filepath.Join(t.TempDir(), "no-legacy-file"))

	id1 := resolveInstanceID("")
	id2 := resolveInstanceID("")

	if id1 == id2 {
		t.Fatalf("expected fresh ids each time no data dir is available, got %q for both", id1)
	}
}

func TestResolveInstanceIDAdoptsLegacySharedFileOnce(t *testing.T) {
	legacyPath := filepath.Join(t.TempDir(), "mcpproxy-instance-id")
	withLegacyInstanceIDPath(t, legacyPath)

	legacyID := uuid.New().String()
	if err := os.WriteFile(legacyPath, []byte(legacyID), 0o600); err != nil {
		t.Fatalf("failed to seed legacy instance id file: %v", err)
	}

	dataDir := t.TempDir()
	adopted := resolveInstanceID(dataDir)

	if adopted != legacyID {
		t.Fatalf("expected the first data dir to adopt the legacy shared id %q, got %q", legacyID, adopted)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("expected the legacy shared file to be removed after adoption, stat err = %v", err)
	}

	// A second, distinct data dir must NOT also adopt the (now-consumed)
	// legacy id -- that would recreate the original host-wide-shared-id bug
	// for every data dir created after the upgrade.
	otherDataDir := t.TempDir()
	otherID := resolveInstanceID(otherDataDir)
	if otherID == legacyID {
		t.Fatalf("a second data dir must not also adopt the already-consumed legacy id %q", legacyID)
	}
}

// helperProcessEnvVar and helperProcessDataDirEnvVar drive a re-exec of this
// test binary as a standalone helper process, so GetInstanceID's
// process-wide sync.Once is exercised fresh -- other tests in this package
// (e.g. isolation_*_test.go, via BuildDockerArgs) already call GetInstanceID
// indirectly, which would otherwise cache a result before this test runs and
// make in-process testing of the singleton order-dependent.
const (
	helperProcessEnvVar        = "MCPPROXY_INSTANCE_ID_TEST_HELPER"
	helperProcessDataDirEnvVar = "MCPPROXY_INSTANCE_ID_TEST_DATA_DIR"
)

// TestHelperProcess is not a real test; it's invoked as a subprocess by the
// tests below. See https://pkg.go.dev/os/exec#Command for this pattern.
func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperProcessEnvVar) != "1" {
		return
	}
	SetInstanceDataDir(os.Getenv(helperProcessDataDirEnvVar))
	fmt.Print(GetInstanceID())
	os.Exit(0)
}

// runInstanceIDHelperProcess runs GetInstanceID() (via SetInstanceDataDir) in
// a fresh subprocess scoped to dataDir, returning what it printed.
func runInstanceIDHelperProcess(t *testing.T, dataDir string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
	cmd.Env = append(os.Environ(),
		helperProcessEnvVar+"=1",
		helperProcessDataDirEnvVar+"="+dataDir,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper process failed: %v\noutput: %s", err, out)
	}
	return string(out)
}

func TestGetInstanceIDReturnsValidUUIDPersistedUnderDataDir(t *testing.T) {
	dir := t.TempDir()

	id := runInstanceIDHelperProcess(t, dir)
	if _, err := uuid.Parse(id); err != nil {
		t.Fatalf("GetInstanceID() = %q is not a valid UUID: %v", id, err)
	}

	persisted, err := loadInstanceID(dir)
	if err != nil {
		t.Fatalf("expected instance id to be persisted under the data dir: %v", err)
	}
	if persisted != id {
		t.Fatalf("persisted instance id %q does not match GetInstanceID() %q", persisted, id)
	}
}

func TestGetInstanceIDDistinctAcrossConcurrentProcesses(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	id1 := runInstanceIDHelperProcess(t, dir1)
	id2 := runInstanceIDHelperProcess(t, dir2)

	if id1 == id2 {
		t.Fatalf("expected two mcpproxy processes with distinct data dirs to get distinct instance ids, got %q for both", id1)
	}
}
