package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// ClassifyDisabledTool must map (server, tool) to exactly one status by fixed
// first-match precedence: server-off → config → user → pending → unknown.
func TestClassifyDisabledTool_Precedence(t *testing.T) {
	rt := setupConfigFilterRuntime(t, []*config.ServerConfig{
		{Name: "off", Enabled: false},
		{Name: "github", Enabled: true, EnabledTools: []string{"list_issues"}},
		{Name: "plain", Enabled: true},
	})

	// server_disabled wins even over a config that would also deny.
	assert.Equal(t, contracts.DisabledStatusServerDisabled,
		rt.ClassifyDisabledTool("off", "anything"))

	// config-denied (not in allowlist) outranks any user/pending state.
	assert.Equal(t, contracts.DisabledStatusByConfig,
		rt.ClassifyDisabledTool("github", "create_issue"))

	// config + user-disabled on the same tool → config wins (precedence).
	require.NoError(t, rt.SetToolEnabled("github", "create_issue", false, "user"))
	assert.Equal(t, contracts.DisabledStatusByConfig,
		rt.ClassifyDisabledTool("github", "create_issue"))

	// user-disabled, no config filter on this server.
	require.NoError(t, rt.SetToolEnabled("plain", "do_thing", false, "user"))
	assert.Equal(t, contracts.DisabledStatusByUser,
		rt.ClassifyDisabledTool("plain", "do_thing"))
}

func TestClassifyDisabledTool_Unknown(t *testing.T) {
	rt := setupConfigFilterRuntime(t, []*config.ServerConfig{
		{Name: "plain", Enabled: true},
	})

	// Server not in config at all → unknown (never a misleading remediation).
	assert.Equal(t, contracts.DisabledStatusUnknown,
		rt.ClassifyDisabledTool("nonexistent", "x"))

	// Enabled server, no config filter, no approval record, tool not otherwise
	// blocked → indeterminate → unknown (classifier is only called for
	// non-callable tools; absent a concrete reason it must not lie).
	assert.Equal(t, contracts.DisabledStatusUnknown,
		rt.ClassifyDisabledTool("plain", "no_record_tool"))
}

func TestClassifyDisabledTool_PendingApproval(t *testing.T) {
	rt := setupConfigFilterRuntime(t, []*config.ServerConfig{
		{Name: "plain", Enabled: true},
	})
	// A pending approval record (not user-disabled) → pending_approval.
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "plain", ToolName: "pending_tool",
		Status: storage.ToolApprovalStatusPending,
	}))
	assert.Equal(t, contracts.DisabledStatusPendingApproval,
		rt.ClassifyDisabledTool("plain", "pending_tool"))
}

// BenchmarkClassifyDisabledTool guards Constitution I: the classify path must
// stay far under the 100ms/1k-tools discovery budget.
func BenchmarkClassifyDisabledTool(b *testing.B) {
	rt := setupConfigFilterRuntime(&testing.T{}, []*config.ServerConfig{
		{Name: "github", Enabled: true, DisabledTools: []string{"delete_repo"}},
	})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = rt.ClassifyDisabledTool("github", "delete_repo")
	}
}

// Regression for the #476 follow-up: config-file disabled_tools live ONLY in
// the runtime config, not always in the storage copy. ClassifyDisabledTool
// must read the live config (r.Config()) so disabled_by_config is correct for
// config-file stdio servers — the server-layer classifier delegates its
// config-denied leg to this authority.
func TestClassifyDisabledTool_ConfigFromLiveConfig(t *testing.T) {
	rt := setupConfigFilterRuntime(t, []*config.ServerConfig{
		{Name: "cfgsrv", Enabled: true, DisabledTools: []string{"locked"}},
	})
	// No storage approval record and no storage.SaveUpstreamServer call:
	// disabled_tools exists only in the live config here.
	assert.Equal(t, contracts.DisabledStatusByConfig,
		rt.ClassifyDisabledTool("cfgsrv", "locked"))
	assert.Equal(t, contracts.DisabledStatusUnknown,
		rt.ClassifyDisabledTool("cfgsrv", "allowed")) // callable → not a disable reason
}
