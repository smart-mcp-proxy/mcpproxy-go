package runtime

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"go.uber.org/zap"

	"mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestApplyConfig_ListenAddressChange tests that listen address changes are saved to disk
// even though they require a restart
func TestApplyConfig_ListenAddressChange(t *testing.T) {
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
	initialCfg.TopK = 5

	// Save initial config
	err := config.SaveConfig(initialCfg, cfgPath)
	require.NoError(t, err)

	// Create runtime
	rt, err := New(initialCfg, cfgPath, zap.NewNop())
	require.NoError(t, err)
	defer func() {
		_ = rt.Close()
	}()

	// Create new config with different TopK (hot-reloadable)
	newCfg := config.DefaultConfig()
	newCfg.Listen = "127.0.0.1:8080" // Same listen address
	newCfg.DataDir = tmpDir
	newCfg.TopK = 10 // Changed TopK

	// Apply the new config
	result, err := rt.ApplyConfig(newCfg, cfgPath)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify that restart is NOT required (TopK is hot-reloadable)
	assert.False(t, result.RequiresRestart, "TopK change should not require restart")
	assert.True(t, result.AppliedImmediately, "TopK change should be applied immediately")

	// Verify config was saved to disk
	savedCfg, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err)

	assert.Equal(t, 10, savedCfg.TopK, "Config file should be updated with new TopK value")
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
