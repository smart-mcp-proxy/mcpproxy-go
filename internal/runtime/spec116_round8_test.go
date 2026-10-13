package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/updatecheck"
)

// Review round 8 (finding 1): the disk reload applies update_check to the
// checker in its publish step, under the commit lock, as ApplyConfig does. Run
// after the commit, a reload paused past publication (here on profileIndexMu)
// could apply its older update_check over a newer commit's.
func TestCommit_R8_ReloadUpdateCheckFollowsCommitOrder(t *testing.T) {
	t.Setenv(updatecheck.EnvDisableAutoUpdate, "")
	rt := newFunnelRuntime(t)
	rt.SetVersion("v0.0.1")
	require.NotNil(t, rt.updateChecker)
	require.True(t, rt.updateChecker.Enabled(), "fixture starts with update checks enabled")

	// The hand edit disables update checks and changes a profile, so the
	// reload calls reconcileProfileIndexes after its commit.
	edited := nextConfig(t, rt)
	edited.UpdateCheck = &config.UpdateCheckConfig{Enabled: r8Bool(false)}
	setProfileServers(edited, "ro", []string{"a", "b"})
	require.NoError(t, config.SaveConfig(edited, rt.ConfigPath()))

	rt.profileIndexMu.Lock()
	locked := true
	defer func() {
		if locked {
			rt.profileIndexMu.Unlock()
		}
	}()
	reloadDone := make(chan error, 1)
	go func() { reloadDone <- rt.ReloadConfiguration() }()
	require.Eventually(t, func() bool {
		return !rt.Config().UpdateCheck.IsEnabled()
	}, 10*time.Second, 2*time.Millisecond, "the reload publishes its configuration")

	// A newer commit re-enables update checks without touching profiles.
	next := nextConfig(t, rt)
	next.UpdateCheck = &config.UpdateCheckConfig{Enabled: r8Bool(true)}
	_, err := rt.ApplyConfig(next, rt.ConfigPath())
	require.NoError(t, err)
	require.True(t, rt.Config().UpdateCheck.IsEnabled())

	rt.profileIndexMu.Unlock()
	locked = false
	select {
	case err := <-reloadDone:
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("reload did not finish")
	}
	assert.True(t, rt.Config().UpdateCheck.IsEnabled())
	assert.True(t, rt.updateChecker.Enabled(), "the checker follows the newest published configuration")
}

// Review round 8 (finding 2): the startup admission commit is a startup commit
// too; an empty server list at startup must not prune every approval before
// the boot commit's guard sees it. An explicit removal of the last server
// still prunes.
func TestCommit_R8_StartupEmptySetKeepsApprovals(t *testing.T) {
	seed := func(t *testing.T, rt *Runtime) {
		t.Helper()
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "srv", ToolName: "tool", Status: storage.ToolApprovalStatusApproved,
			ApprovedHash: "h", CurrentHash: "h", CurrentDescription: "d",
		}))
	}
	approvalExists := func(rt *Runtime) bool {
		_, err := rt.storageManager.GetToolApproval("srv", "tool")
		return err == nil
	}

	t.Run("startup with an empty server list", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "mcp_config.json")
		raw, err := json.Marshal(map[string]any{
			"listen": "127.0.0.1:0", "data_dir": dir, "api_key": "spec116-r8-key", "mcpServers": []any{},
		})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(cfgPath, raw, 0o600))
		cfg, err := config.LoadFromFile(cfgPath)
		require.NoError(t, err)
		rt, err := New(cfg, cfgPath, zap.NewNop())
		require.NoError(t, err)
		t.Cleanup(func() { _ = rt.Close() })
		seed(t, rt)

		rt.gateInitialConfig()
		assert.True(t, approvalExists(rt), "the startup admission commit keeps approvals under an empty server list")
		require.NoError(t, rt.bootReconcile())
		assert.True(t, approvalExists(rt), "the boot commit keeps approvals under an empty server list")
	})

	t.Run("explicit removal of the last server", func(t *testing.T) {
		rt, cfgPath := newSpec116Runtime(t, nil, disabledStdio("srv"))
		seed(t, rt)
		next := cloneRuntimeConfig(t, rt.Config())
		next.Servers = nil
		_, err := rt.ApplyConfig(next, cfgPath)
		require.NoError(t, err)
		assert.False(t, approvalExists(rt), "removing the last server prunes its approvals")
	})
}

func r8Bool(v bool) *bool { return &v }
