package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestScanDefaultNotice_OncePerInstallation (spec 120 FR-011): the first start
// with configured servers names the defaulted ones and writes the marker; the
// second start is silent.
func TestScanDefaultNotice_OncePerInstallation(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{
		{Name: "github", Enabled: true},
		{Name: "jira", Enabled: true, TrustMode: string(config.TrustModeManual)},
		{Name: "slack", Enabled: false},
	})
	// setupQuarantineRuntime pins unset modes to manual for the fixtures; the
	// notice is about the UNSET ones, so clear the pin on github.
	rt.Config().Servers[0].TrustMode = ""
	rt.Config().Servers[0].AutoApproveToolChanges = nil
	require.True(t, rt.Config().Servers[0].TrustModeDefaulted())

	state, err := rt.storageManager.LoadScanDefaultNoticeState()
	require.NoError(t, err)
	require.Nil(t, state, "fresh installation: no marker")

	rt.warnScanDefaultOnce()
	state, err = rt.storageManager.LoadScanDefaultNoticeState()
	require.NoError(t, err)
	require.NotNil(t, state, "the first start with servers writes the marker")
	assert.Equal(t, []string{"github"}, state.Servers, "only the enabled, defaulted server is named")
	first := state.NoticedAt

	rt.warnScanDefaultOnce()
	state, err = rt.storageManager.LoadScanDefaultNoticeState()
	require.NoError(t, err)
	assert.Equal(t, first, state.NoticedAt, "a second start leaves the marker as it was")
}

// TestScanDefaultNotice_FreshInstallWritesNothing: no servers, no marker.
func TestScanDefaultNotice_FreshInstallWritesNothing(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, nil)
	rt.warnScanDefaultOnce()
	state, err := rt.storageManager.LoadScanDefaultNoticeState()
	require.NoError(t, err)
	assert.Nil(t, state)
}

// TestScanDefaultNotice_NothingDefaultedStillMarks: servers exist but every
// one states its mode: the marker is written (nothing to say later either).
func TestScanDefaultNotice_NothingDefaultedStillMarks(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "jira", Enabled: true, TrustMode: string(config.TrustModeManual)}})
	rt.warnScanDefaultOnce()
	state, err := rt.storageManager.LoadScanDefaultNoticeState()
	require.NoError(t, err)
	require.NotNil(t, state)
	assert.Empty(t, state.Servers)
}
