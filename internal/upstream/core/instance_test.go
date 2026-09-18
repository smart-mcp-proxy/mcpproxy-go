package core

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

func TestAdoptLegacyInstanceIDConcurrentClaimIsExclusive(t *testing.T) {
	legacyPath := filepath.Join(t.TempDir(), "mcpproxy-instance-id")
	withLegacyInstanceIDPath(t, legacyPath)

	legacyID := uuid.New().String()
	if err := os.WriteFile(legacyPath, []byte(legacyID), 0o600); err != nil {
		t.Fatalf("failed to seed legacy instance id file: %v", err)
	}

	const racers = 8
	dataDirs := make([]string, racers)
	for i := range dataDirs {
		dataDirs[i] = t.TempDir()
	}

	start := make(chan struct{})
	results := make([]string, racers)
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = adoptLegacyInstanceID(dataDirs[i])
		}(i)
	}
	close(start)
	wg.Wait()

	adopters := 0
	for _, id := range results {
		if id == legacyID {
			adopters++
		} else if id != "" {
			t.Errorf("adoptLegacyInstanceID returned an unexpected non-empty, non-legacy id %q", id)
		}
	}
	if adopters != 1 {
		t.Fatalf("expected exactly one of %d concurrent racers to adopt the legacy id, got %d", racers, adopters)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("expected the legacy shared file to be gone after the race, stat err = %v", err)
	}
}

func TestAdoptLegacyInstanceIDPreservesClaimWhenSaveFails(t *testing.T) {
	legacyPath := filepath.Join(t.TempDir(), "mcpproxy-instance-id")
	withLegacyInstanceIDPath(t, legacyPath)

	legacyID := uuid.New().String()
	if err := os.WriteFile(legacyPath, []byte(legacyID), 0o600); err != nil {
		t.Fatalf("failed to seed legacy instance id file: %v", err)
	}

	// A plain file (not a directory) as the "data dir" makes saveInstanceID's
	// os.WriteFile(filepath.Join(dataDir, instanceIDFileName), ...) fail on
	// every OS, without relying on permission bits that behave differently
	// on Windows.
	unwritableDataDir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(unwritableDataDir, []byte("x"), 0o600); err != nil {
		t.Fatalf("failed to seed the not-a-directory stand-in: %v", err)
	}

	id := adoptLegacyInstanceID(unwritableDataDir)
	if id != legacyID {
		t.Fatalf("expected adoptLegacyInstanceID to still return the legacy id %q despite the failed save, got %q", legacyID, id)
	}

	matches, err := filepath.Glob(legacyPath + ".claimed-*")
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected the claimed legacy content to survive a failed save (found %d claim files, want 1): %v", len(matches), matches)
	}
	claimed, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("failed to read the surviving claim file: %v", err)
	}
	if strings.TrimSpace(string(claimed)) != legacyID {
		t.Fatalf("surviving claim file content = %q, want %q", claimed, legacyID)
	}
}

// helperProcessEnvVar and friends drive a re-exec of this test binary as a
// standalone helper process, so GetInstanceID's process-wide sync.Once is
// exercised fresh -- other tests in this package (e.g. isolation_*_test.go,
// via BuildDockerArgs) already call GetInstanceID indirectly, which would
// otherwise cache a result before this test runs and make in-process testing
// of the singleton order-dependent.
//
// helperProcessLegacyPathEnvVar is required, not optional: without it the
// helper process would fall through to the real legacyInstanceIDPath()
// default (the actual host-wide os.TempDir() file), and could adopt-and-delete
// a genuine machine's pre-upgrade migration file as a side effect of running
// this test suite.
const (
	helperProcessEnvVar           = "MCPPROXY_INSTANCE_ID_TEST_HELPER"
	helperProcessDataDirEnvVar    = "MCPPROXY_INSTANCE_ID_TEST_DATA_DIR"
	helperProcessLegacyPathEnvVar = "MCPPROXY_INSTANCE_ID_TEST_LEGACY_PATH"
)

// TestHelperProcess is not a real test; it's invoked as a subprocess by the
// tests below. See https://pkg.go.dev/os/exec#Command for this pattern.
func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperProcessEnvVar) != "1" {
		return
	}
	legacyPath := os.Getenv(helperProcessLegacyPathEnvVar)
	if legacyPath == "" {
		t.Fatal("helper process requires " + helperProcessLegacyPathEnvVar + " to avoid touching the real host-wide legacy file")
	}
	legacyInstanceIDPath = func() string { return legacyPath }
	SetInstanceDataDir(os.Getenv(helperProcessDataDirEnvVar))
	fmt.Print(GetInstanceID())
	os.Exit(0)
}

// runInstanceIDHelperProcess runs GetInstanceID() (via SetInstanceDataDir) in
// a fresh subprocess scoped to dataDir, with the legacy shared-id path
// scoped to a scratch location under dataDir so the helper never touches a
// real machine's host-wide legacy file. Returns what it printed.
func runInstanceIDHelperProcess(t *testing.T, dataDir string) string {
	t.Helper()
	out, err := runInstanceIDHelperProcessWithLegacyPath(dataDir, filepath.Join(dataDir, "no-legacy-file"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	return out
}

// runInstanceIDHelperProcessWithLegacyPath is like runInstanceIDHelperProcess
// but lets the caller point multiple helper processes at the SAME legacy
// path, to exercise the real cross-process legacy-claim race. It returns an
// error instead of calling t.Fatalf directly so it's safe to invoke from a
// spawned goroutine (testing.T.Fatal/FailNow must only be called from the
// test's own goroutine).
func runInstanceIDHelperProcessWithLegacyPath(dataDir, legacyPath string) (string, error) {
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
	cmd.Env = append(os.Environ(),
		helperProcessEnvVar+"=1",
		helperProcessDataDirEnvVar+"="+dataDir,
		helperProcessLegacyPathEnvVar+"="+legacyPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("helper process failed: %w\noutput: %s", err, out)
	}
	return string(out), nil
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

	var id1, id2 string
	var err1, err2 error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		id1, err1 = runInstanceIDHelperProcessWithLegacyPath(dir1, filepath.Join(dir1, "no-legacy-file"))
	}()
	go func() {
		defer wg.Done()
		id2, err2 = runInstanceIDHelperProcessWithLegacyPath(dir2, filepath.Join(dir2, "no-legacy-file"))
	}()
	wg.Wait()

	if err1 != nil {
		t.Fatalf("%v", err1)
	}
	if err2 != nil {
		t.Fatalf("%v", err2)
	}
	if id1 == id2 {
		t.Fatalf("expected two mcpproxy processes with distinct data dirs to get distinct instance ids, got %q for both", id1)
	}
}

// TestGetInstanceIDCrossProcessLegacyClaimIsExclusive is the real-world
// counterpart to TestAdoptLegacyInstanceIDConcurrentClaimIsExclusive: that
// test proves the claim is exclusive between goroutines in one process
// (which, unrealistically, all share one PID); this one starts two genuinely
// separate OS processes racing over the SAME seeded legacy file and checks
// exactly one of them adopts it.
func TestGetInstanceIDCrossProcessLegacyClaimIsExclusive(t *testing.T) {
	legacyPath := filepath.Join(t.TempDir(), "mcpproxy-instance-id")
	legacyID := uuid.New().String()
	if err := os.WriteFile(legacyPath, []byte(legacyID), 0o600); err != nil {
		t.Fatalf("failed to seed legacy instance id file: %v", err)
	}

	dir1 := t.TempDir()
	dir2 := t.TempDir()

	var id1, id2 string
	var err1, err2 error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); id1, err1 = runInstanceIDHelperProcessWithLegacyPath(dir1, legacyPath) }()
	go func() { defer wg.Done(); id2, err2 = runInstanceIDHelperProcessWithLegacyPath(dir2, legacyPath) }()
	wg.Wait()

	if err1 != nil {
		t.Fatalf("%v", err1)
	}
	if err2 != nil {
		t.Fatalf("%v", err2)
	}

	adopters := 0
	for _, id := range []string{id1, id2} {
		if id == legacyID {
			adopters++
		}
	}
	if adopters != 1 {
		t.Fatalf("expected exactly one of 2 concurrent mcpproxy processes to adopt the legacy id %q, got %d (id1=%q id2=%q)", legacyID, adopters, id1, id2)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("expected the legacy shared file to be gone after the race, stat err = %v", err)
	}
}
