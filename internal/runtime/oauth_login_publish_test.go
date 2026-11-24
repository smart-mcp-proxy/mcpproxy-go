package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// A switch-off whose publish fails leaves the feature on: the disable epoch is
// NOT counted (live streams must not end), the lifecycle lock is released, and
// a later successful switch-off counts once.
func TestDisableEpoch_NotCountedWhenPublishFails(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil)
	svc := rt.OAuthLoginService()
	setEnabled := func(on bool) func(*CommitTx) error {
		return func(tx *CommitTx) error {
			return tx.MutateGlobal(func(c *config.Config) error { c.MCPOAuthEnabled = on; return nil })
		}
	}
	_, err := rt.Commit("on", setEnabled(true))
	require.NoError(t, err)
	require.True(t, svc.Enabled())

	// A concurrent publish moves the version between prepare and publish.
	rt.testHooks.afterAdvance = func(string) { rt.configSvc.SeedVersion(rt.ConfigSnapshot().Version + 1000) }
	_, err = rt.Commit("off", setEnabled(false))
	require.Error(t, err, "the publish fails")
	assert.True(t, svc.Enabled(), "the feature stayed on")
	assert.Zero(t, svc.DisableEpoch(), "a failed switch-off does not end live requests")
	require.True(t, svc.lifecycleMu.TryLock(), "the lifecycle lock was released")
	svc.lifecycleMu.Unlock()

	rt.testHooks.afterAdvance = nil
	_, err = rt.Commit("off", setEnabled(false))
	require.NoError(t, err)
	assert.False(t, svc.Enabled())
	assert.Equal(t, uint64(1), svc.DisableEpoch())
}
