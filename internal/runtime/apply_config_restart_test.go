package runtime

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestApplyConfig_ListenAddressChange tests that listen address changes are saved to disk
// even though they require a restart
func TestApplyConfig_ListenAddressChange(t *testing.T) {
	if testing.Short() {
		t.Skip("server restart timing test (~18s under -race); runs in the stress-tests CI job")
	}
	// Create temp directory and config file
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	// Initial config with port 8080
	initialCfg := config.DefaultConfig()
	initialCfg.Listen = "127.0.0.1:8080"
	initialCfg.DataDir = tmpDir

	// Save initial config
	err := config.SaveConfig(initialCfg, cfgPath)
	require.NoError(t, err)

	// Create runtime with initial config
	rt, err := New(initialCfg, cfgPath, zap.NewNop())
	require.NoError(t, err)
	defer func() {
		_ = rt.Close()
	}()

	// Create new config with different listen address
	newCfg := config.DefaultConfig()
	newCfg.Listen = "127.0.0.1:30080" // Changed port
	newCfg.DataDir = tmpDir

	// Apply the new config
	result, err := rt.ApplyConfig(newCfg, cfgPath)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify that restart is required (listen address changes require restart)
	assert.True(t, result.RequiresRestart, "Listen address change should require restart")
	assert.Contains(t, result.ChangedFields, "listen", "Should detect listen address change")

	// The critical test: verify config was saved to disk despite requiring restart
	savedCfg, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err)

	assert.Equal(t, "127.0.0.1:30080", savedCfg.Listen,
		"Config file should be updated with new listen address even though restart is required")
}

// TestApplyConfig_HotReloadableChange tests that hot-reloadable changes work correctly
func TestApplyConfig_HotReloadableChange(t *testing.T) {
	// Create temp directory and config file
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	// Initial config
	initialCfg := config.DefaultConfig()
	initialCfg.Listen = "127.0.0.1:8080"
	initialCfg.DataDir = tmpDir
	initialCfg.ToolsLimit = 15

	// Save initial config
	err := config.SaveConfig(initialCfg, cfgPath)
	require.NoError(t, err)

	// Create runtime
	rt, err := New(initialCfg, cfgPath, zap.NewNop())
	require.NoError(t, err)
	defer func() {
		_ = rt.Close()
	}()

	// Create new config with different ToolsLimit (hot-reloadable)
	newCfg := config.DefaultConfig()
	newCfg.Listen = "127.0.0.1:8080" // Same listen address
	newCfg.DataDir = tmpDir
	newCfg.ToolsLimit = 20 // Changed ToolsLimit

	// Apply the new config
	result, err := rt.ApplyConfig(newCfg, cfgPath)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify that restart is NOT required (ToolsLimit is hot-reloadable)
	assert.False(t, result.RequiresRestart, "ToolsLimit change should not require restart")
	assert.True(t, result.AppliedImmediately, "ToolsLimit change should be applied immediately")

	// Verify config was saved to disk
	savedCfg, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err)

	assert.Equal(t, 20, savedCfg.ToolsLimit, "Config file should be updated with new ToolsLimit value")
}

// TestApplyConfig_ObservabilityHotReload (MCP-835 / Codex finding #3): changing
// the observability usage persist interval must hot-reload into the running
// ActivityService — previously ApplyConfig only handled logging/truncator, so
// SetUsagePersistInterval's "hot-reloadable" promise was unfulfilled.
func TestApplyConfig_ObservabilityHotReload(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	initialCfg := config.DefaultConfig()
	initialCfg.Listen = "127.0.0.1:8080"
	initialCfg.DataDir = tmpDir
	require.NoError(t, config.SaveConfig(initialCfg, cfgPath))

	rt, err := New(initialCfg, cfgPath, zap.NewNop())
	require.NoError(t, err)
	defer func() { _ = rt.Close() }()

	// Default cadence is 30s before the reload.
	require.Equal(t, DefaultUsagePersistInterval, rt.ActivityService().usagePersistInterval())

	newCfg := config.DefaultConfig()
	newCfg.Listen = "127.0.0.1:8080"
	newCfg.DataDir = tmpDir
	newCfg.Observability.UsagePersistInterval = config.Duration(10 * time.Second)

	result, err := rt.ApplyConfig(newCfg, cfgPath)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.False(t, result.RequiresRestart, "observability cadence change is hot-reloadable")
	assert.Contains(t, result.ChangedFields, "observability")
	assert.Equal(t, 10*time.Second, rt.ActivityService().usagePersistInterval(),
		"new persist interval must be applied to the running ActivityService")
}

// TestApplyConfig_DeepScanLegacyMigration (Spec 077 US3 / Codex finding #2): the
// /api/v1/config/apply path bypasses LoadFromFile, so applying a config that
// carries the deprecated security.scanner_* keys must still fold them into
// security.deep_scan before saving — the saved file must expose ONLY the unified
// deep_scan surface, matching a file load (SC-007).
func TestApplyConfig_DeepScanLegacyMigration(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	initialCfg := config.DefaultConfig()
	initialCfg.Listen = "127.0.0.1:8080"
	initialCfg.DataDir = tmpDir
	require.NoError(t, config.SaveConfig(initialCfg, cfgPath))

	rt, err := New(initialCfg, cfgPath, zap.NewNop())
	require.NoError(t, err)
	defer func() { _ = rt.Close() }()

	// Submit a config carrying ONLY the deprecated top-level keys (as an older
	// API client / stored config would), no deep_scan block.
	fetchOff := false
	newCfg := config.DefaultConfig()
	newCfg.Listen = "127.0.0.1:8080"
	newCfg.DataDir = tmpDir
	newCfg.Security = &config.SecurityConfig{
		ScannerFetchPackageSource:     &fetchOff,
		ScannerDisableNoNewPrivileges: true,
	}

	result, err := rt.ApplyConfig(newCfg, cfgPath)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.RequiresRestart, "security change is hot-reloadable")
	assert.Contains(t, result.ChangedFields, "security")

	// Read the SAVED file back and assert it normalized identically to a file
	// load: legacy keys cleared, only deep_scan.* present.
	saved, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err)
	require.NotNil(t, saved.Security)
	assert.Nil(t, saved.Security.ScannerFetchPackageSource,
		"deprecated scanner_fetch_package_source must be cleared after apply")
	assert.False(t, saved.Security.ScannerDisableNoNewPrivileges,
		"deprecated scanner_disable_no_new_privileges must be cleared after apply")
	require.NotNil(t, saved.Security.DeepScan, "legacy keys must fold into deep_scan")
	require.NotNil(t, saved.Security.DeepScan.FetchPackageSource)
	assert.False(t, *saved.Security.DeepScan.FetchPackageSource,
		"fetch_package_source=false migrated into deep_scan")
	assert.True(t, saved.Security.DeepScan.DisableNoNewPrivileges,
		"disable_no_new_privileges=true migrated into deep_scan")

	// The in-memory runtime config must also be normalized (not just disk).
	live, err := rt.GetConfig()
	require.NoError(t, err)
	require.NotNil(t, live.Security)
	assert.Nil(t, live.Security.ScannerFetchPackageSource, "runtime config normalized too")
	require.NotNil(t, live.Security.DeepScan)
	assert.True(t, live.Security.DeepScan.DisableNoNewPrivileges)
}

// TestApplyConfig_SaveFailure tests handling of save errors
func TestApplyConfig_SaveFailure(t *testing.T) {
	// Skip on Windows: chmod on directories doesn't reliably prevent file creation
	// Windows has different permission semantics (ACLs vs POSIX permissions)
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows: directory chmod doesn't reliably prevent file creation")
	}

	// Create temp directory and config file
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	// Initial config
	initialCfg := config.DefaultConfig()
	initialCfg.Listen = "127.0.0.1:8080"
	initialCfg.DataDir = tmpDir

	// Save initial config
	err := config.SaveConfig(initialCfg, cfgPath)
	require.NoError(t, err)

	// Create runtime
	rt, err := New(initialCfg, cfgPath, zap.NewNop())
	require.NoError(t, err)
	defer func() {
		_ = rt.Close()
	}()

	// Make directory read-only to force save failure
	// (With atomic writes, making the file read-only doesn't prevent writes
	// because we create a new temp file. We need to make the directory read-only.)
	err = os.Chmod(tmpDir, 0555)
	require.NoError(t, err)
	defer func() {
		_ = os.Chmod(tmpDir, 0700)
	}()

	// Create new config
	newCfg := config.DefaultConfig()
	newCfg.Listen = "127.0.0.1:30080"
	newCfg.DataDir = tmpDir

	// Apply should fail because config can't be saved (directory is read-only)
	result, err := rt.ApplyConfig(newCfg, cfgPath)
	assert.Error(t, err, "Should fail when config cannot be saved")
	assert.NotNil(t, result)
	assert.False(t, result.Success, "Result should indicate failure")
}
