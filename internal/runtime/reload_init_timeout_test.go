package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestPostConfigReload_HonoursInitTimeoutAboveThirtySeconds is the reload half
// of #1772: postConfigReload wrapped ConnectAll in a 30s context, and
// ConnectAll derives each client's connect deadline from the context it is
// handed, so an init_timeout above 30s never took effect after a reload.
func TestPostConfigReload_HonoursInitTimeoutAboveThirtySeconds(t *testing.T) {
	const initTimeout = 120 * time.Second
	initDur := config.Duration(initTimeout)
	serverCfg := &config.ServerConfig{
		Name: "slow", URL: "http://127.0.0.1:1/mcp", Protocol: "streamable-http",
		Enabled: true, InitTimeout: &initDur,
	}
	rt, err := New(&config.Config{
		DataDir: t.TempDir(), Listen: "127.0.0.1:0",
		Servers: []*config.ServerConfig{serverCfg},
	}, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	require.NoError(t, rt.UpstreamManager().AddServerConfig("slow", serverCfg))

	var mu sync.Mutex
	var left []time.Duration
	rt.UpstreamManager().SetConnectDeadlineHookForTest(func(_ string, ctx context.Context) {
		mu.Lock()
		defer mu.Unlock()
		if dl, ok := ctx.Deadline(); ok {
			left = append(left, time.Until(dl))
		} else {
			left = append(left, 0)
		}
	})

	rt.postConfigReload()

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, left, 1, "the reload must attempt the connect")
	require.GreaterOrEqual(t, left[0], initTimeout-10*time.Second,
		"the reload's connect context expires before the server's init_timeout")
}
