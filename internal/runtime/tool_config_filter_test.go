package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func setupConfigFilterRuntime(t *testing.T, servers []*config.ServerConfig) *Runtime {
	t.Helper()
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir: tempDir,
		Listen:  "127.0.0.1:0",
		Servers: servers,
	}
	rt, err := New(cfg, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func TestIsToolConfigDenied_AllowList(t *testing.T) {
	rt := setupConfigFilterRuntime(t, []*config.ServerConfig{
		{Name: "github", Enabled: true, EnabledTools: []string{"list_issues", "get_issue"}},
	})

	assert.False(t, rt.IsToolConfigDenied("github", "list_issues"), "list_issues should be allowed")
	assert.False(t, rt.IsToolConfigDenied("github", "get_issue"), "get_issue should be allowed")
	assert.True(t, rt.IsToolConfigDenied("github", "create_issue"), "create_issue not in allowlist → denied")
	assert.True(t, rt.IsToolConfigDenied("github", "delete_issue"), "delete_issue not in allowlist → denied")
}

func TestIsToolConfigDenied_DenyList(t *testing.T) {
	rt := setupConfigFilterRuntime(t, []*config.ServerConfig{
		{Name: "github", Enabled: true, DisabledTools: []string{"delete_repo", "force_push"}},
	})

	assert.True(t, rt.IsToolConfigDenied("github", "delete_repo"), "delete_repo in denylist → denied")
	assert.True(t, rt.IsToolConfigDenied("github", "force_push"), "force_push in denylist → denied")
	assert.False(t, rt.IsToolConfigDenied("github", "list_repos"), "list_repos not in denylist → allowed")
}

func TestIsToolConfigDenied_NoFilter(t *testing.T) {
	rt := setupConfigFilterRuntime(t, []*config.ServerConfig{
		{Name: "github", Enabled: true},
	})

	assert.False(t, rt.IsToolConfigDenied("github", "any_tool"), "no filter → all tools allowed")
}

func TestIsToolConfigDenied_UnknownServer(t *testing.T) {
	rt := setupConfigFilterRuntime(t, nil)

	assert.False(t, rt.IsToolConfigDenied("nonexistent", "any_tool"))
}

// TestIsToolConfigDenied_UserDisabledPreserved verifies that setting Disabled in BBolt
// does NOT affect IsToolConfigDenied — the two layers are independent.
func TestIsToolConfigDenied_UserDisabledPreserved(t *testing.T) {
	rt := setupConfigFilterRuntime(t, []*config.ServerConfig{
		{Name: "github", Enabled: true, EnabledTools: []string{"list_issues"}},
	})

	// User disables list_issues manually
	err := rt.SetToolEnabled("github", "list_issues", false, "user")
	require.NoError(t, err)

	// Config still allows it — user preference is separate
	assert.False(t, rt.IsToolConfigDenied("github", "list_issues"),
		"config allows list_issues; user-disabled state must not affect IsToolConfigDenied")
}

func TestSetAllToolsEnabled_SkipsConfigDenied(t *testing.T) {
	rt := setupConfigFilterRuntime(t, []*config.ServerConfig{
		{Name: "github", Enabled: true, EnabledTools: []string{"list_issues"}},
	})

	// Seed approval records so SetAllToolsEnabled has tools to iterate over.
	require.NoError(t, rt.SetToolEnabled("github", "list_issues", false, "user"))
	require.NoError(t, rt.SetToolEnabled("github", "create_issue", false, "user"))

	changed, err := rt.SetAllToolsEnabled("github", true, "user")
	require.NoError(t, err)

	// Only list_issues should flip — create_issue is config-denied.
	assert.Equal(t, 1, changed)

	// create_issue must remain disabled in BBolt
	record, err := rt.storageManager.GetToolApproval("github", "create_issue")
	require.NoError(t, err)
	assert.True(t, record.Disabled, "create_issue must remain disabled; config denies it")
}