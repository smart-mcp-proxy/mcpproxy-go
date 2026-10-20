package runtime

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage/purgetest"
)

// Spec 116 PR2 tests of the APIs PR2 adds (they do not compile on the
// parent): the *At approval writers bound to an explicit expected
// incarnation, and the purge report on the commit result.

// T2.4 for the block writer and an explicit expected incarnation (FR-017): a
// write bound to the old incarnation writes nothing on the replacement; the
// current incarnation succeeds.
func TestApproval_T2_4_ExplicitExpectedIncarnation(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil, manualStdio("acme", false))
	old, ok := rt.ConfigSnapshot().ServerGeneration("acme")
	require.True(t, ok)
	replacement := manualStdio("acme", false)
	replacement.DisabledTools = []string{"echo"}
	removeAndReAdd(t, rt, replacement)

	// Review round 3: a bulk toggle on a replacement with no tool inventory
	// writes nothing either way, but a stale request still reports the
	// replacement instead of a successful no-op.
	_, err := rt.SetAllToolsEnabledAt("acme", true, "api", old.Incarnation)
	require.ErrorIs(t, err, ErrServerReplaced, "empty inventory")
	_, err = rt.SetAllToolsEnabledAt("acme", false, "api", old.Incarnation)
	require.ErrorIs(t, err, ErrServerReplaced, "empty inventory")

	seedPendingTool(t, rt, "acme", "echo", "new")

	// Every tool is config-denied, so enable-all attempts no write.
	_, err = rt.SetAllToolsEnabledAt("acme", true, "api", old.Incarnation)
	require.ErrorIs(t, err, ErrServerReplaced, "all tools config-denied")

	_, err = rt.BlockToolsAt("acme", []string{"echo"}, "api", old.Incarnation)
	require.ErrorIs(t, err, ErrServerReplaced)
	assert.Equal(t, "server_replaced: server 'acme' was removed or replaced since it was reviewed", err.Error())
	_, err = rt.BlockAllToolsAt("acme", "api", old.Incarnation)
	require.ErrorIs(t, err, ErrServerReplaced)
	_, err = rt.ApproveAllToolsAt("acme", "api", old.Incarnation)
	require.ErrorIs(t, err, ErrServerReplaced)
	require.ErrorIs(t, rt.ApproveToolsAt("acme", []string{"echo"}, "api", old.Incarnation), ErrServerReplaced)
	require.ErrorIs(t, rt.SetToolEnabledAt("acme", "echo", false, "api", old.Incarnation), ErrServerReplaced)
	_, err = rt.SetAllToolsEnabledAt("acme", false, "api", old.Incarnation)
	require.ErrorIs(t, err, ErrServerReplaced)
	require.ErrorIs(t, rt.QuarantineServerAt("acme", true, old.Incarnation), ErrServerReplaced)
	assert.False(t, spec116Live(rt, "acme").Quarantined, "a rejected quarantine changes nothing")
	rec, err := rt.storageManager.GetToolApproval("acme", "echo")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusPending, rec.Status)
	assert.False(t, rec.Disabled)

	cur, ok := rt.ConfigSnapshot().ServerGeneration("acme")
	require.True(t, ok)
	n, err := rt.SetAllToolsEnabledAt("acme", true, "api", cur.Incarnation)
	require.NoError(t, err, "the current incarnation's no-op bulk toggle succeeds")
	assert.Zero(t, n)
	require.NoError(t, rt.ApproveToolsAt("acme", []string{"echo"}, "api", cur.Incarnation))
	assert.Equal(t, storage.ToolApprovalStatusApproved, toolStatus(t, rt, "acme", "echo"))
}

// An approval of a server that is not configured fails with
// ErrServerNotFound instead of writing a record no purge will ever see.
func TestApproval_UnknownServerRejected(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil, manualStdio("acme", false))
	err := rt.ApproveTools("ghost", []string{"echo"}, "api")
	require.ErrorIs(t, err, ErrServerNotFound)
	_, err = rt.storageManager.GetToolApproval("ghost", "echo")
	assert.ErrorIs(t, err, storage.ErrToolApprovalNotFound)
}

// The commit result reports what the removal purged, per bucket.
func TestRemoval_CommitResultReportsPurge(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil, disabledStdio("acme"))
	purgetest.Seed(t, rt.storageManager, "acme")
	res, err := rt.Commit("remove_server", func(tx *CommitTx) error { return tx.RemoveServer("acme") })
	require.NoError(t, err)
	rep, ok := res.Purged["acme"]
	require.True(t, ok)
	assert.Equal(t, 1, rep.Deleted[storage.UpstreamsBucket])
	assert.Equal(t, 2, rep.Deleted[storage.PromptApprovalBucket])
	assert.Equal(t, 1, rep.Deleted[storage.OAuthTokenBucket])
	assert.Positive(t, rep.Total())
	counts, err := rt.storageManager.PurgeSetCounts("acme")
	require.NoError(t, err)
	assert.Empty(t, counts, "PurgeSetCounts agrees nothing is left")
}

// The startup empty-set guard: a daemon that boots on a configuration listing
// no server deletes the stale rows but keeps their approvals and credentials,
// as the boot-time orphan GC does; a later explicit removal purges.
func TestRemoval_BootWithEmptyConfigKeepsTrustState(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcp_config.json")
	initial := config.DefaultConfig()
	initial.DataDir = dir
	initial.Listen = "127.0.0.1:0"
	initial.Servers = []*config.ServerConfig{disabledStdio("acme")}
	require.NoError(t, config.SaveConfig(initial, cfgPath))
	boot := func() *Runtime {
		cfg, err := config.LoadFromFile(cfgPath)
		require.NoError(t, err)
		rt, err := New(cfg, cfgPath, zap.NewNop())
		require.NoError(t, err)
		rt.gateInitialConfig()
		require.NoError(t, rt.bootReconcile())
		return rt
	}
	rt := boot()
	purgetest.Seed(t, rt.storageManager, "acme")
	require.NoError(t, rt.Close())

	empty := config.DefaultConfig()
	empty.DataDir = dir
	empty.Listen = "127.0.0.1:0"
	require.NoError(t, config.SaveConfig(empty, cfgPath))
	rt = boot()
	defer func() { _ = rt.Close() }()
	c := purgetest.Counts(t, rt.storageManager, "acme")
	assert.Zero(t, c[purgetest.Upstream], "the stale row is deleted")
	assert.Equal(t, 1, c[purgetest.ToolApprovals], "approvals survive a startup config with no server")
	assert.Equal(t, 1, c[purgetest.OAuthTokens], "credentials survive a startup config with no server")
}
