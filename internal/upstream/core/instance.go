package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
)

const instanceIDFileName = "instance-id"

var (
	instanceID     string
	instanceIDOnce sync.Once

	dataDirMu sync.Mutex
	dataDir   string

	// claimSeq disambiguates legacy-id claim paths beyond os.Getpid(), which
	// is constant for the process's whole lifetime. Real racers are always
	// separate processes (distinct PIDs), so this never matters in
	// production, but it keeps the claim path collision-free for any
	// same-process caller too (e.g. concurrent test goroutines) instead of
	// relying on PID uniqueness alone.
	claimSeq atomic.Uint64

	// legacyInstanceIDPath is a var (not a const) so tests can point it at a
	// scratch path instead of the real host-wide file.
	legacyInstanceIDPath = func() string {
		return filepath.Join(os.TempDir(), "mcpproxy-instance-id")
	}
)

// SetInstanceDataDir records the data directory this process's instance ID
// should be persisted under (normally cfg.DataDir). Call it once at startup,
// before the first GetInstanceID() call — e.g. from upstream.NewManager,
// which every entry point constructs early with the loaded config.
//
// Concurrent mcpproxy processes on the same host must already use distinct
// data directories (BBolt takes an exclusive lock on config.db), so keying
// the instance ID off the data dir rather than a single shared file under
// os.TempDir() gives each process a genuinely distinct ID. A call after the
// ID has already been resolved, or no call at all, has no effect beyond that
// first read: GetInstanceID() then falls back to a fresh id for the
// process's lifetime instead of reusing a host-wide file.
func SetInstanceDataDir(dir string) {
	dataDirMu.Lock()
	dataDir = dir
	dataDirMu.Unlock()
}

// getInstanceID returns a unique identifier for this mcpproxy instance,
// resolved once per process and cached for the process's lifetime.
func getInstanceID() string {
	instanceIDOnce.Do(func() {
		dataDirMu.Lock()
		dir := dataDir
		dataDirMu.Unlock()
		instanceID = resolveInstanceID(dir)
	})
	return instanceID
}

// resolveInstanceID contains the actual id-resolution logic, kept free of
// package-level state so it can be exercised directly (and repeatedly, with
// different dirs) in tests without the sync.Once in getInstanceID hiding
// everything but the first call.
func resolveInstanceID(dir string) string {
	if dir == "" {
		// No data directory known at labeling time: use a fresh id for this
		// process's lifetime rather than the old host-wide shared temp file,
		// which made every mcpproxy process on a machine collide on one ID.
		return uuid.New().String()
	}

	if id, err := loadInstanceID(dir); err == nil && id != "" {
		return id
	}

	// First run under this data dir: adopt the legacy host-wide id if one is
	// still there, so containers created before this fix stay manageable by
	// whichever instance starts first after the upgrade. Then retire the
	// legacy file so no other data dir can adopt the same id afterwards --
	// otherwise every future data dir would keep re-adopting it forever,
	// recreating the exact bug this is fixing.
	if id := adoptLegacyInstanceID(dir); id != "" {
		return id
	}

	id := uuid.New().String()
	_ = saveInstanceID(dir, id) // Best effort save
	return id
}

// adoptLegacyInstanceID migrates the pre-fix, host-wide shared instance id
// (if present) into dataDir. Returns "" if there is no legacy file to adopt.
//
// Claiming the legacy file happens via os.Rename to a process-unique path
// rather than a plain read-then-remove: rename atomically fails if the
// source is already gone, so when two processes race to adopt the same
// legacy file at upgrade time, exactly one wins and the other correctly
// falls through to generating its own fresh id. A read-then-remove would let
// both processes read the same id before either removed the file,
// recreating the original host-wide-shared-id bug for that pair.
func adoptLegacyInstanceID(dataDir string) string {
	claimPath := fmt.Sprintf("%s.claimed-%d-%d", legacyInstanceIDPath(), os.Getpid(), claimSeq.Add(1))
	if err := os.Rename(legacyInstanceIDPath(), claimPath); err != nil {
		// No legacy file, or another process already claimed it.
		return ""
	}
	defer os.Remove(claimPath)

	data, err := os.ReadFile(claimPath)
	if err != nil {
		return ""
	}
	id := strings.TrimSpace(string(data))
	if id == "" {
		return ""
	}
	_ = saveInstanceID(dataDir, id) // Best effort save, same as the fresh-id path below
	return id
}

// GetInstanceID returns the unique identifier for this mcpproxy instance (exported for use by manager)
func GetInstanceID() string {
	return getInstanceID()
}

// loadInstanceID attempts to load the instance ID from disk under dataDir
func loadInstanceID(dataDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dataDir, instanceIDFileName))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// saveInstanceID persists the instance ID to disk under dataDir
func saveInstanceID(dataDir, id string) error {
	return os.WriteFile(filepath.Join(dataDir, instanceIDFileName), []byte(id), 0o600)
}

// formatContainerLabels returns Docker labels for container ownership tracking
func formatContainerLabels(serverName string) []string {
	instanceID := getInstanceID()
	return []string{
		"--label", "com.mcpproxy.managed=true",
		"--label", fmt.Sprintf("com.mcpproxy.instance=%s", instanceID),
		"--label", fmt.Sprintf("com.mcpproxy.server=%s", serverName),
		"--label", fmt.Sprintf("com.mcpproxy.created=%d", os.Getpid()),
	}
}
