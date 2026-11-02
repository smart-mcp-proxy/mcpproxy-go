package supervisor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/configsvc"
)

// deadlineUpstream records the deadline of the context ConnectServerAt gets.
type deadlineUpstream struct {
	*instanceUpstream
	mu       sync.Mutex
	deadline time.Time
	hasDL    bool
	called   bool
}

func (d *deadlineUpstream) ConnectServerAt(ctx context.Context, name string, gen int64) error {
	d.mu.Lock()
	d.deadline, d.hasDL = ctx.Deadline()
	d.called = true
	d.mu.Unlock()
	return d.instanceUpstream.ConnectServerAt(ctx, name, gen)
}

// TestExecuteAction_ConnectDoesNotCapInitTimeout is the regression guard for
// #1772: the supervisor wrapped every action in a 30s context, and the
// manager derives the per-server connect deadline from the context it is
// handed, so an init_timeout above 30s could never take effect. The connect
// context must leave the (always applied) per-instance init_timeout in charge:
// no deadline, or one no shorter than the configured init_timeout.
func TestExecuteAction_ConnectDoesNotCapInitTimeout(t *testing.T) {
	const initTimeout = 120 * time.Second
	initDur := config.Duration(initTimeout)
	srv := &config.ServerConfig{
		Name: "s", URL: "http://127.0.0.1:1/mcp", Protocol: "streamable-http",
		Enabled: true, InitTimeout: &initDur,
	}
	svc := configsvc.NewService(&config.Config{Listen: "127.0.0.1:0", Servers: []*config.ServerConfig{srv}}, "", zap.NewNop())
	defer svc.Close()

	for _, action := range []ReconcileAction{ActionConnect, ActionReconnect} {
		t.Run(string(action), func(t *testing.T) {
			up := &deadlineUpstream{instanceUpstream: newInstanceUpstream()}
			sup := New(svc, up, zap.NewNop())

			require.NoError(t, sup.executeAction("s", plannedAction{Action: action}, svc.Current()))

			up.mu.Lock()
			defer up.mu.Unlock()
			require.True(t, up.called, "the connect leg must run")
			if up.hasDL {
				require.GreaterOrEqual(t, time.Until(up.deadline), initTimeout-20*time.Second,
					"the supervisor's connect context expires before the server's init_timeout")
			}
		})
	}
}
