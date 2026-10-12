package scanner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// rt06DockerImage is a small image the real-Docker RT-06 tests run as the
// "scanner". The tests skip unless it is already present locally, so they
// never pull from the network.
const rt06DockerImage = "alpine:latest"

func requireLocalDockerImage(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("real Docker test skipped in -short mode")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not available")
	}
	if err := exec.Command("docker", "image", "inspect", rt06DockerImage).Run(); err != nil {
		t.Skipf("docker image %s not available locally: %v", rt06DockerImage, err)
	}
}

// rt06SnoopScanner is a Docker "scanner" that waits, then copies the
// tools.json it sees into its report dir (seen.json) and writes a clean report.
func rt06SnoopScanner() *ScannerPlugin {
	return &ScannerPlugin{
		ID:          "mcp-scan",
		DockerImage: rt06DockerImage,
		Command: []string{"sh", "-c",
			"sleep 6; cat /scan/source/tools.json > /scan/report/seen.json; echo '" + genericCleanReport + "' > /scan/report/results.json"},
		Timeout: "60s",
	}
}

// TestRT06_RealDocker_SymlinkedToolsJSONRetargetedMidRun (Sol r20 P2): when
// tools.json in the upstream-writable source dir is a symlink, Docker lays the
// snapshot over the symlink's TARGET. Retargeting the symlink at clean
// metadata while the scanner runs, and restoring it before the post-run check,
// let the scanner read the clean file while both content checks passed. The
// scanner must read the immutable snapshot or the scan must fail closed.
func TestRT06_RealDocker_SymlinkedToolsJSONRetargetedMidRun(t *testing.T) {
	requireLocalDockerImage(t)

	source := writeToolsJSON(t, []map[string]interface{}{rt06Tool(rt06Poisoned)})
	toolsPath := filepath.Join(source, "tools.json")
	snapshot, err := os.ReadFile(toolsPath)
	require.NoError(t, err)
	// tools.json becomes a symlink to a file holding the exported contracts.
	require.NoError(t, os.Rename(toolsPath, filepath.Join(source, "real.json")))
	require.NoError(t, os.WriteFile(filepath.Join(source, "clean.json"), []byte(staleCleanToolsJSON), 0o644))
	require.NoError(t, os.Symlink("real.json", toolsPath))

	logger := zap.NewNop()
	docker := NewDockerRunner(logger)
	engine := NewEngine(docker, NewRegistry(t.TempDir(), logger), t.TempDir(), logger)
	ran := false
	var seen []byte
	engine.runScannerContainer = func(ctx context.Context, cfg ScannerRunConfig) (string, string, int, error) {
		ran = true
		done := make(chan struct{})
		go func() {
			defer close(done)
			time.Sleep(3 * time.Second) // the container has started and mounted
			_ = os.Remove(toolsPath)
			_ = os.Symlink("clean.json", toolsPath)
		}()
		stdout, stderr, code, runErr := docker.RunScanner(ctx, cfg)
		<-done
		// Restore the original link before the post-run integrity check.
		_ = os.Remove(toolsPath)
		_ = os.Symlink("real.json", toolsPath)
		seen, _ = os.ReadFile(filepath.Join(cfg.ReportDir, "seen.json"))
		return stdout, stderr, code, runErr
	}

	report, _, err := engine.runSingleScanner(context.Background(), rt06SnoopScanner(),
		ScanRequest{ServerName: "rt06-symlink", SourceDir: source, ToolDefinitions: snapshot})
	if err == nil {
		require.True(t, ran)
		require.NotNil(t, report)
		assert.Equal(t, string(snapshot), string(seen),
			"a scan that succeeds must have read the exported snapshot, not the retargeted clean file")
		return
	}
	assert.Contains(t, err.Error(), "tools.json", "the scan fails closed on the tools.json integrity check")
}

// TestRT06_RealDocker_ToolsJSONReplacedByRenameMidRun (Sol r20 P2): an
// upstream that replaces tools.json with a new file (rename over it) while the
// scanner runs, and renames the original contents back before the post-run
// check, changes the file identity even though the bytes match again. The
// result is discarded.
func TestRT06_RealDocker_ToolsJSONReplacedByRenameMidRun(t *testing.T) {
	requireLocalDockerImage(t)

	source := writeToolsJSON(t, []map[string]interface{}{rt06Tool(rt06Poisoned)})
	toolsPath := filepath.Join(source, "tools.json")
	snapshot, err := os.ReadFile(toolsPath)
	require.NoError(t, err)

	logger := zap.NewNop()
	docker := NewDockerRunner(logger)
	engine := NewEngine(docker, NewRegistry(t.TempDir(), logger), t.TempDir(), logger)
	engine.runScannerContainer = func(ctx context.Context, cfg ScannerRunConfig) (string, string, int, error) {
		done := make(chan struct{})
		go func() {
			defer close(done)
			time.Sleep(3 * time.Second)
			clean := filepath.Join(source, "swap.json")
			_ = os.WriteFile(clean, []byte(staleCleanToolsJSON), 0o644)
			_ = os.Rename(clean, toolsPath)
		}()
		stdout, stderr, code, runErr := docker.RunScanner(ctx, cfg)
		<-done
		restore := filepath.Join(source, "restore.json")
		_ = os.WriteFile(restore, snapshot, 0o644)
		_ = os.Rename(restore, toolsPath)
		return stdout, stderr, code, runErr
	}

	_, _, err = engine.runSingleScanner(context.Background(), rt06SnoopScanner(),
		ScanRequest{ServerName: "rt06-rename", SourceDir: source, ToolDefinitions: snapshot})
	require.Error(t, err, "a tools.json replaced during the run is not the file the snapshot was laid over")
	assert.Contains(t, err.Error(), "tools.json")
}

// TestRT06_RealDocker_InPlaceSwapReadsSnapshot is the real-Docker control for
// the unit test of the same name: an in-place rewrite and restore of a regular
// tools.json never reaches the scanner, which reads the snapshot, and the
// scan succeeds.
func TestRT06_RealDocker_InPlaceSwapReadsSnapshot(t *testing.T) {
	requireLocalDockerImage(t)

	source := writeToolsJSON(t, []map[string]interface{}{rt06Tool(rt06Poisoned)})
	toolsPath := filepath.Join(source, "tools.json")
	snapshot, err := os.ReadFile(toolsPath)
	require.NoError(t, err)

	logger := zap.NewNop()
	docker := NewDockerRunner(logger)
	engine := NewEngine(docker, NewRegistry(t.TempDir(), logger), t.TempDir(), logger)
	var seen []byte
	engine.runScannerContainer = func(ctx context.Context, cfg ScannerRunConfig) (string, string, int, error) {
		done := make(chan struct{})
		go func() {
			defer close(done)
			time.Sleep(3 * time.Second)
			_ = os.WriteFile(toolsPath, []byte(staleCleanToolsJSON), 0o644)
		}()
		stdout, stderr, code, runErr := docker.RunScanner(ctx, cfg)
		<-done
		_ = os.WriteFile(toolsPath, snapshot, 0o644)
		seen, _ = os.ReadFile(filepath.Join(cfg.ReportDir, "seen.json"))
		return stdout, stderr, code, runErr
	}

	report, _, err := engine.runSingleScanner(context.Background(), rt06SnoopScanner(),
		ScanRequest{ServerName: "rt06-inplace", SourceDir: source, ToolDefinitions: snapshot})
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.Equal(t, string(snapshot), string(seen), "the scanner read the exported snapshot")
}

// TestRT06_SymlinkedToolsJSON_FailsClosedBeforeRun (Sol r20 P2, no Docker): a
// tools.json that is a symlink is refused before the container is created,
// even when the link's target holds the exported bytes.
func TestRT06_SymlinkedToolsJSON_FailsClosedBeforeRun(t *testing.T) {
	source := writeToolsJSON(t, []map[string]interface{}{rt06Tool(rt06Poisoned)})
	toolsPath := filepath.Join(source, "tools.json")
	snapshot, err := os.ReadFile(toolsPath)
	require.NoError(t, err)
	require.NoError(t, os.Rename(toolsPath, filepath.Join(source, "real.json")))
	require.NoError(t, os.Symlink("real.json", toolsPath))

	logger := zap.NewNop()
	engine := NewEngine(NewDockerRunner(logger), NewRegistry(t.TempDir(), logger), t.TempDir(), logger)
	ran := false
	engine.runScannerContainer = func(context.Context, ScannerRunConfig) (string, string, int, error) {
		ran = true
		return "", "", 0, nil
	}
	_, _, err = engine.runSingleScanner(context.Background(),
		&ScannerPlugin{ID: "mcp-scan", DockerImage: "example/mcp-scan:test", Command: []string{"scan"}},
		ScanRequest{ServerName: "rt06-symlink", SourceDir: source, ToolDefinitions: snapshot})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a regular file")
	assert.False(t, ran, "the container is never created")

	// A metadata-independent scanner never reads tools.json and still runs.
	_, _, err = engine.runSingleScanner(context.Background(),
		&ScannerPlugin{ID: "trivy-mcp", DockerImage: "example/trivy:test", Command: []string{"scan"}},
		ScanRequest{ServerName: "rt06-symlink", SourceDir: source, ToolDefinitions: snapshot})
	assert.True(t, ran)
	if err != nil {
		assert.NotContains(t, err.Error(), "tools.json")
	}
}

// TestRT06_ToolsJSONSwappedAndRestoredDuringRun_Discarded (Sol r20 P2, no
// Docker): a symlink or another file swapped in while the scanner runs and
// undone (bytes and all) before it ends is seen by the watcher, and the result
// is discarded.
func TestRT06_ToolsJSONSwappedAndRestoredDuringRun_Discarded(t *testing.T) {
	for name, swap := range map[string]func(t *testing.T, source, toolsPath string, snapshot []byte){
		"symlink then the original file renamed back": func(t *testing.T, source, toolsPath string, _ []byte) {
			aside := filepath.Join(source, "aside.json")
			require.NoError(t, os.WriteFile(filepath.Join(source, "clean.json"), []byte(staleCleanToolsJSON), 0o644))
			require.NoError(t, os.Rename(toolsPath, aside))
			require.NoError(t, os.Symlink("clean.json", toolsPath))
			time.Sleep(10 * toolDefinitionsWatchInterval)
			require.NoError(t, os.Remove(toolsPath))
			require.NoError(t, os.Rename(aside, toolsPath))
		},
		"another file with the same bytes": func(t *testing.T, source, toolsPath string, snapshot []byte) {
			other := filepath.Join(source, "other.json")
			require.NoError(t, os.WriteFile(other, snapshot, 0o644))
			require.NoError(t, os.Rename(other, toolsPath))
		},
	} {
		t.Run(name, func(t *testing.T) {
			source := writeToolsJSON(t, []map[string]interface{}{rt06Tool(rt06Poisoned)})
			toolsPath := filepath.Join(source, "tools.json")
			snapshot, err := os.ReadFile(toolsPath)
			require.NoError(t, err)

			logger := zap.NewNop()
			engine := NewEngine(NewDockerRunner(logger), NewRegistry(t.TempDir(), logger), t.TempDir(), logger)
			engine.runScannerContainer = func(_ context.Context, cfg ScannerRunConfig) (string, string, int, error) {
				swap(t, source, toolsPath, snapshot)
				require.NoError(t, os.WriteFile(filepath.Join(cfg.ReportDir, "results.json"), []byte(genericCleanReport), 0o644))
				return "", "", 0, nil
			}
			_, _, err = engine.runSingleScanner(context.Background(),
				&ScannerPlugin{ID: "mcp-scan", DockerImage: "example/mcp-scan:test", Command: []string{"scan"}},
				ScanRequest{ServerName: "rt06-swap", SourceDir: source, ToolDefinitions: snapshot})
			require.Error(t, err, "the restored bytes pass the content check, but the file was swapped")
			assert.Contains(t, err.Error(), "tools.json")
		})
	}
}
