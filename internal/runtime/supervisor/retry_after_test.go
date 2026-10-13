package supervisor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/configsvc"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// TestSupervisor_Reconcile_RespectsRetryAfter is the supervisor half of #1040's
// acceptance: while a rate-limited upstream's Retry-After window is open the
// periodic reconciliation must plan no ActionConnect, even though the ordinary
// exponential-backoff window has long since elapsed.
func TestSupervisor_Reconcile_RespectsRetryAfter(t *testing.T) {
	cfg := &config.Config{
		Listen: "127.0.0.1:8080",
		Servers: []*config.ServerConfig{
			{Name: "rate-limited-server", Enabled: true},
		},
	}

	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	defer configSvc.Close()

	mockUpstream := NewMockUpstreamAdapter()
	defer mockUpstream.Close()

	supervisor := New(configSvc, mockUpstream, zap.NewNop())

	require.NoError(t, supervisor.reconcile(configSvc.Current()))
	supervisor.actionWg.Wait()

	setConnectionState := func(info *types.ConnectionInfo) {
		mockUpstream.mu.Lock()
		defer mockUpstream.mu.Unlock()
		mockUpstream.connected["rate-limited-server"] = false
		if state, ok := mockUpstream.states["rate-limited-server"]; ok {
			state.Connected = false
			state.ConnectionInfo = info
		}
	}
	isConnected := func() bool {
		mockUpstream.mu.Lock()
		defer mockUpstream.mu.Unlock()
		return mockUpstream.connected["rate-limited-server"]
	}
	reconcileAndDrain := func() {
		t.Helper()
		require.NoError(t, supervisor.reconcile(configSvc.Current()))
		supervisor.actionWg.Wait()
	}

	// `Retry-After: 3600` on a 429: the ladder says "dial now" (its window
	// elapsed long ago), the upstream says "not for an hour". The upstream wins.
	setConnectionState(&types.ConnectionInfo{
		State:         types.StateError,
		RetryCount:    1,
		LastRetryTime: time.Now().Add(-time.Hour),
		RetryAfter:    time.Now().Add(time.Hour),
	})
	reconcileAndDrain()
	require.False(t, isConnected(), "supervisor re-dialed a server inside its Retry-After window")

	// Once the window has passed the normal ladder takes over again.
	setConnectionState(&types.ConnectionInfo{
		State:         types.StateError,
		RetryCount:    1,
		LastRetryTime: time.Now().Add(-time.Hour),
		RetryAfter:    time.Now().Add(-time.Second),
	})
	reconcileAndDrain()
	require.True(t, isConnected(), "supervisor never came back after the Retry-After window elapsed")
}
