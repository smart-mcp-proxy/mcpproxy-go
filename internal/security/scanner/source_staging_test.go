package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestRT06_StagedSource_UpstreamPathsCannotRedirectScanner (Sol r20 P2): a
// metadata-dependent Docker scanner mounts a proxy-owned copy of the source
// dir, never the upstream-writable dir itself, so no symlink or rename in the
// upstream's dir decides what the container reads. Symlinks are copied as
// symlinks (never followed out of the source dir on the host), special files
// are skipped, and the copy is removed after the run.
func TestRT06_StagedSource_UpstreamPathsCannotRedirectScanner(t *testing.T) {
	source := writeToolsJSON(t, []map[string]interface{}{rt06Tool(rt06Poisoned)})
	snapshot, err := os.ReadFile(filepath.Join(source, "tools.json"))
	require.NoError(t, err)

	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "host-secret.txt"), []byte("host secret"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(source, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(source, "sub", "a.txt"), []byte("original"), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(source, "link-out")))
	require.NoError(t, os.Symlink(filepath.Join("sub", "a.txt"), filepath.Join(source, "link-in")))
	makeTestFIFO(t, filepath.Join(source, "fifo"))

	logger := zap.NewNop()
	dataDir := t.TempDir()
	engine := NewEngine(NewDockerRunner(logger), NewRegistry(t.TempDir(), logger), dataDir, logger)
	var staged string
	engine.runScannerContainer = func(_ context.Context, cfg ScannerRunConfig) (string, string, int, error) {
		staged = cfg.SourceDir
		require.NotEqual(t, source, staged, "the upstream's dir is never mounted for a metadata-dependent scanner")
		assert.True(t, strings.HasPrefix(staged, filepath.Join(dataDir, "scanner-snapshots")+string(os.PathSeparator)),
			"the mounted source is proxy-owned")
		for _, m := range cfg.ExtraMounts {
			assert.NotContains(t, m, source, "no mount reaches into the upstream's dir")
		}

		info, err := os.Lstat(filepath.Join(staged, "tools.json"))
		require.NoError(t, err)
		assert.True(t, info.Mode().IsRegular(), "the staged tools.json is a regular file")
		got, err := os.ReadFile(filepath.Join(staged, "tools.json"))
		require.NoError(t, err)
		assert.Equal(t, string(snapshot), string(got))

		// The upstream now swaps a directory for a symlink to clean content.
		clean := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(clean, "a.txt"), []byte("swapped"), 0o644))
		require.NoError(t, os.RemoveAll(filepath.Join(source, "sub")))
		require.NoError(t, os.Symlink(clean, filepath.Join(source, "sub")))
		got, err = os.ReadFile(filepath.Join(staged, "sub", "a.txt"))
		require.NoError(t, err)
		assert.Equal(t, "original", string(got), "the staged copy does not follow the upstream's later changes")

		linfo, err := os.Lstat(filepath.Join(staged, "link-out"))
		require.NoError(t, err)
		assert.NotZero(t, linfo.Mode()&os.ModeSymlink, "a symlink is copied as a symlink, not followed on the host")
		_, err = os.Lstat(filepath.Join(staged, "fifo"))
		assert.True(t, os.IsNotExist(err), "special files are not staged")
		target, err := os.Readlink(filepath.Join(staged, "link-in"))
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("sub", "a.txt"), target)

		require.NoError(t, os.WriteFile(filepath.Join(cfg.ReportDir, "results.json"), []byte(genericCleanReport), 0o644))
		return "", "", 0, nil
	}

	plugin := &ScannerPlugin{ID: "mcp-scan", DockerImage: "example/mcp-scan:test", Command: []string{"scan"}}
	report, _, err := engine.runSingleScanner(context.Background(), plugin,
		ScanRequest{ServerName: "rt06-staged", SourceDir: source, ToolDefinitions: snapshot})
	require.NoError(t, err)
	require.NotNil(t, report)
	_, statErr := os.Stat(staged)
	assert.True(t, os.IsNotExist(statErr), "the staged copy is removed after the run")
}

// TestRT06_StagedSource_TooLargeFailsClosed: a source dir over the staging
// limits is not handed to a metadata-dependent scanner at all; a
// metadata-independent scanner still mounts it directly.
func TestRT06_StagedSource_TooLargeFailsClosed(t *testing.T) {
	source := writeToolsJSON(t, []map[string]interface{}{rt06Tool(rt06Poisoned)})
	snapshot, err := os.ReadFile(filepath.Join(source, "tools.json"))
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(source, "f"+string(rune('a'+i))+".txt"), []byte("0123456789"), 0o644))
	}

	logger := zap.NewNop()
	engine := NewEngine(NewDockerRunner(logger), NewRegistry(t.TempDir(), logger), t.TempDir(), logger)
	ran := 0
	engine.runScannerContainer = func(_ context.Context, cfg ScannerRunConfig) (string, string, int, error) {
		ran++
		require.NoError(t, os.WriteFile(filepath.Join(cfg.ReportDir, "results.json"), []byte(genericCleanReport), 0o644))
		return "", "", 0, nil
	}
	req := ScanRequest{ServerName: "rt06-large", SourceDir: source, ToolDefinitions: snapshot}
	plugin := &ScannerPlugin{ID: "mcp-scan", DockerImage: "example/mcp-scan:test", Command: []string{"scan"}}

	for name, limit := range map[string]func(){
		"entries": func() { stagedSourceMaxEntries = 3 },
		"bytes":   func() { stagedSourceMaxBytes = 25 },
	} {
		t.Run(name, func(t *testing.T) {
			oldEntries, oldBytes := stagedSourceMaxEntries, stagedSourceMaxBytes
			t.Cleanup(func() { stagedSourceMaxEntries, stagedSourceMaxBytes = oldEntries, oldBytes })
			limit()
			before := ran
			_, _, err := engine.runSingleScanner(context.Background(), plugin, req)
			require.Error(t, err)
			assert.ErrorIs(t, err, errStagedSourceTooLarge)
			assert.Equal(t, before, ran, "the container is never created")

			_, _, err = engine.runSingleScanner(context.Background(),
				&ScannerPlugin{ID: "trivy-mcp", DockerImage: "example/trivy:test", Command: []string{"scan"}}, req)
			require.NoError(t, err, "a metadata-independent scanner is not staged")
			assert.Equal(t, before+1, ran)
		})
	}
}

// TestRT06_RealDocker_SymlinkRetargetedAroundContainerStart (Sol r20 P2):
// tools.json is a regular file when it is pinned. In the window before the
// container is created the upstream swaps in a symlink (a bind mount laid
// over <source>/tools.json would land on the link's target), retargets the
// link at other clean metadata once the container runs, and renames the
// original back after the run. The scanner must read the exported snapshot;
// the swap is still seen, and the result is discarded.
func TestRT06_RealDocker_SymlinkRetargetedAroundContainerStart(t *testing.T) {
	requireLocalDockerImage(t)

	source := writeToolsJSON(t, []map[string]interface{}{rt06Tool(rt06Poisoned)})
	toolsPath := filepath.Join(source, "tools.json")
	snapshot, err := os.ReadFile(toolsPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(source, "decoy.json"), []byte(`{"decoy":true}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(source, "clean.json"), []byte(staleCleanToolsJSON), 0o644))
	aside := filepath.Join(source, "aside.json")

	logger := zap.NewNop()
	docker := NewDockerRunner(logger)
	engine := NewEngine(docker, NewRegistry(t.TempDir(), logger), t.TempDir(), logger)
	var seen []byte
	engine.runScannerContainer = func(ctx context.Context, cfg ScannerRunConfig) (string, string, int, error) {
		require.NoError(t, os.Rename(toolsPath, aside))
		require.NoError(t, os.Symlink("decoy.json", toolsPath))
		done := make(chan struct{})
		go func() {
			defer close(done)
			time.Sleep(3 * time.Second) // the container has started and mounted
			_ = os.Remove(toolsPath)
			_ = os.Symlink("clean.json", toolsPath)
		}()
		stdout, stderr, code, runErr := docker.RunScanner(ctx, cfg)
		<-done
		_ = os.Remove(toolsPath)
		_ = os.Rename(aside, toolsPath)
		seen, _ = os.ReadFile(filepath.Join(cfg.ReportDir, "seen.json"))
		return stdout, stderr, code, runErr
	}

	_, _, err = engine.runSingleScanner(context.Background(), rt06SnoopScanner(),
		ScanRequest{ServerName: "rt06-prestart", SourceDir: source, ToolDefinitions: snapshot})
	require.NotEmpty(t, seen, "the container ran and reported what it read")
	assert.Equal(t, string(snapshot), string(seen),
		"the container read the exported snapshot, not the retargeted clean file")
	require.Error(t, err, "the swap is tampering evidence; the result is discarded")
	assert.Contains(t, err.Error(), "tools.json")
}

// TestRT06_StagedSource_DirSwappedForFIFOAfterLstat (Sol r21 P2): an
// upstream that replaces a directory with a FIFO between its Lstat and its
// open must not block staging (a blocking open waits for a writer forever).
func TestRT06_StagedSource_DirSwappedForFIFOAfterLstat(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no FIFOs on Windows")
	}
	source := writeToolsJSON(t, []map[string]interface{}{rt06Tool(rt06Poisoned)})
	snapshot, err := os.ReadFile(filepath.Join(source, "tools.json"))
	require.NoError(t, err)
	sub := filepath.Join(source, "sub")
	require.NoError(t, os.Mkdir(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(source, "keep.txt"), []byte("keep"), 0o644))

	stagingAfterLstat = func(rel string) {
		if rel == "sub" {
			require.NoError(t, os.RemoveAll(sub))
			makeTestFIFO(t, sub)
		}
	}
	t.Cleanup(func() { stagingAfterLstat = nil })

	logger := zap.NewNop()
	engine := NewEngine(NewDockerRunner(logger), NewRegistry(t.TempDir(), logger), t.TempDir(), logger)
	type result struct {
		dir     string
		cleanup func()
		err     error
	}
	done := make(chan result, 1)
	go func() {
		dir, cleanup, err := engine.stageScannerSource(context.Background(),
			&ScannerPlugin{ID: "mcp-scan"}, ScanRequest{ServerName: "rt06-fifo", SourceDir: source, ToolDefinitions: snapshot})
		done <- result{dir, cleanup, err}
	}()
	select {
	case r := <-done:
		require.NoError(t, r.err)
		defer r.cleanup()
		got, err := os.ReadFile(filepath.Join(r.dir, "keep.txt"))
		require.NoError(t, err)
		assert.Equal(t, "keep", string(got))
		got, err = os.ReadFile(filepath.Join(r.dir, "tools.json"))
		require.NoError(t, err)
		assert.Equal(t, string(snapshot), string(got))
	case <-time.After(10 * time.Second):
		// Unblock the stuck open so the goroutine can exit.
		if f, err := os.OpenFile(sub, os.O_WRONLY, 0); err == nil {
			_ = f.Close()
		}
		t.Fatal("staging blocked on a FIFO swapped in for a directory")
	}
}

// TestRT06_StagedSource_SourceContainingDataDir (Sol r21 P2): a source dir
// that contains the proxy's data dir (a stdio server whose working dir is
// $HOME, holding ~/.mcpproxy) must not copy the staging area into itself.
func TestRT06_StagedSource_SourceContainingDataDir(t *testing.T) {
	source := writeToolsJSON(t, []map[string]interface{}{rt06Tool(rt06Poisoned)})
	snapshot, err := os.ReadFile(filepath.Join(source, "tools.json"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(source, "main.py"), []byte("print(1)"), 0o644))
	dataDir := filepath.Join(source, ".mcpproxy")
	require.NoError(t, os.Mkdir(dataDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "mcp_config.json"), []byte("{}"), 0o600))

	logger := zap.NewNop()
	engine := NewEngine(NewDockerRunner(logger), NewRegistry(t.TempDir(), logger), dataDir, logger)
	dir, cleanup, err := engine.stageScannerSource(context.Background(),
		&ScannerPlugin{ID: "mcp-scan"}, ScanRequest{ServerName: "rt06-home", SourceDir: source, ToolDefinitions: snapshot})
	require.NoError(t, err)
	defer cleanup()

	got, err := os.ReadFile(filepath.Join(dir, "main.py"))
	require.NoError(t, err)
	assert.Equal(t, "print(1)", string(got))
	got, err = os.ReadFile(filepath.Join(dir, "tools.json"))
	require.NoError(t, err)
	assert.Equal(t, string(snapshot), string(got))
	_, err = os.Lstat(filepath.Join(dir, ".mcpproxy", "scanner-snapshots"))
	assert.True(t, os.IsNotExist(err), "the staging area is not copied into the staged source")
	_, err = os.Lstat(filepath.Join(dir, ".mcpproxy", "mcp_config.json"))
	assert.NoError(t, err, "the rest of the data dir is copied as ordinary source")
}

// TestStagedSource_EntryCapEnforcedMidEnumeration (#1602): a directory far
// over the cap fails closed after reading only a bounded number of entries.
func TestStagedSource_EntryCapEnforcedMidEnumeration(t *testing.T) {
	src := t.TempDir()
	for i := 0; i < 200; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(src, fmt.Sprintf("f%03d", i)), nil, 0o644))
	}
	oldE, oldB := stagedSourceMaxEntries, stagedSourceReadBatch
	t.Cleanup(func() { stagedSourceMaxEntries, stagedSourceReadBatch, stagingAfterReadBatch = oldE, oldB, nil })
	stagedSourceMaxEntries, stagedSourceReadBatch = 5, 4
	read := 0
	stagingAfterReadBatch = func(n int) { read += n }

	err := copySourceTree(context.Background(), src, t.TempDir(), nil)
	require.ErrorIs(t, err, errStagedSourceTooLarge)
	assert.LessOrEqual(t, read, stagedSourceMaxEntries+stagedSourceReadBatch, "entries read stay bounded by cap plus one batch")
}

// TestStagedSource_CopyCancelledByDeadline (#1602): staging stops when the
// context is done instead of copying on.
func TestStagedSource_CopyCancelledByDeadline(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "big.bin"), make([]byte, 2*copyChunkSize), 0o644))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root, err := os.OpenRoot(src)
	require.NoError(t, err)
	defer root.Close()
	budget := int64(1 << 30)
	err = copyRegularFromRoot(ctx, root, "big.bin", filepath.Join(t.TempDir(), "out"), 0o644, &budget)
	require.ErrorIs(t, err, context.Canceled)
}

// TestStagedSource_ScanDeadlineCoversStaging (#1602): runSingleScanner
// applies the scan timeout before staging, so staging sees a deadline even
// when the caller passed context.Background().
func TestStagedSource_ScanDeadlineCoversStaging(t *testing.T) {
	source := writeToolsJSON(t, []map[string]interface{}{rt06Tool(rt06Poisoned)})
	require.NoError(t, os.WriteFile(filepath.Join(source, "main.js"), []byte("x"), 0o644)) // a non-skipped entry for staging to check ctx on
	snapshot, err := os.ReadFile(filepath.Join(source, "tools.json"))
	require.NoError(t, err)
	logger := zap.NewNop()
	engine := NewEngine(NewDockerRunner(logger), NewRegistry(t.TempDir(), logger), t.TempDir(), logger)
	engine.runScannerContainer = func(ctx context.Context, cfg ScannerRunConfig) (string, string, int, error) {
		_, ok := ctx.Deadline()
		assert.True(t, ok, "scan context carries a deadline")
		require.NoError(t, os.WriteFile(filepath.Join(cfg.ReportDir, "results.json"), []byte(genericCleanReport), 0o644))
		return "", "", 0, nil
	}
	req := ScanRequest{ServerName: "dl", SourceDir: source, ToolDefinitions: snapshot}
	// A 1ns timeout is not reliably expired on coarse-clock platforms
	// (Windows), so staging could finish before the deadline was observable.
	// Use a short timeout and stall inside staging, after the first directory
	// batch, until the deadline has certainly passed: the next ctx check in
	// staging must then fail. This proves staging runs under the scan deadline.
	t.Cleanup(func() { stagingAfterReadBatch = nil })
	stagingAfterReadBatch = func(int) { time.Sleep(300 * time.Millisecond) }
	plugin := &ScannerPlugin{ID: "mcp-scan", DockerImage: "example/mcp-scan:test", Command: []string{"scan"}, Timeout: "20ms"}
	_, _, err = engine.runSingleScanner(context.Background(), plugin, req)
	require.Error(t, err, "an expired scan deadline stops staging")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestStagedSource_NonPositiveTimeoutMeansNoLimit: a "0s"/negative plugin
// timeout keeps its pre-#1602 meaning (no limit) instead of expiring the scan.
func TestStagedSource_NonPositiveTimeoutMeansNoLimit(t *testing.T) {
	for _, tmo := range []string{"0s", "-1s"} {
		t.Run(tmo, func(t *testing.T) {
			source := writeToolsJSON(t, []map[string]interface{}{rt06Tool(rt06Poisoned)})
			snapshot, err := os.ReadFile(filepath.Join(source, "tools.json"))
			require.NoError(t, err)
			logger := zap.NewNop()
			engine := NewEngine(NewDockerRunner(logger), NewRegistry(t.TempDir(), logger), t.TempDir(), logger)
			engine.runScannerContainer = func(ctx context.Context, cfg ScannerRunConfig) (string, string, int, error) {
				_, ok := ctx.Deadline()
				assert.False(t, ok, "non-positive timeout adds no deadline")
				require.NoError(t, os.WriteFile(filepath.Join(cfg.ReportDir, "results.json"), []byte(genericCleanReport), 0o644))
				return "", "", 0, nil
			}
			req := ScanRequest{ServerName: "nl", SourceDir: source, ToolDefinitions: snapshot}
			plugin := &ScannerPlugin{ID: "mcp-scan", DockerImage: "example/mcp-scan:test", Command: []string{"scan"}, Timeout: tmo}
			_, _, err = engine.runSingleScanner(context.Background(), plugin, req)
			require.NoError(t, err)
		})
	}
}
