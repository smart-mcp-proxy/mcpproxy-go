package upstream

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// TestResolveConnectTimeout proves the connect-path deadline applied to a
// server's MCP `initialize` handshake (MCP-3322 / GH #760): unset → 30s (no
// regression), a per-server init_timeout is honored, and Docker-isolated
// servers keep a 3-minute floor while still letting a larger override win.
// This is the connect-path assertion without actually sleeping — it checks the
// exact value handed to context.WithTimeout in AddServer/ConnectAll.
func TestResolveConnectTimeout(t *testing.T) {
	logger := zap.NewNop()

	t.Run("unset → 30s default (no regression)", func(t *testing.T) {
		m := NewManager(logger, &config.Config{}, nil, secret.NewResolver(), nil)
		sc := &config.ServerConfig{Name: "s"}
		assert.Equal(t, 30*time.Second, m.resolveConnectTimeout(sc, false))
	})

	t.Run("per-server init_timeout honored on un-isolated stdio (the #760 bite)", func(t *testing.T) {
		m := NewManager(logger, &config.Config{}, nil, secret.NewResolver(), nil)
		sc := &config.ServerConfig{Name: "slack", InitTimeout: durPtr(120 * time.Second)}
		assert.Equal(t, 120*time.Second, m.resolveConnectTimeout(sc, false))
	})

	t.Run("global init_timeout applies when server unset", func(t *testing.T) {
		m := NewManager(logger, &config.Config{InitTimeout: durPtr(90 * time.Second)}, nil, secret.NewResolver(), nil)
		sc := &config.ServerConfig{Name: "s"}
		assert.Equal(t, 90*time.Second, m.resolveConnectTimeout(sc, false))
	})

	t.Run("per-server overrides global", func(t *testing.T) {
		m := NewManager(logger, &config.Config{InitTimeout: durPtr(90 * time.Second)}, nil, secret.NewResolver(), nil)
		sc := &config.ServerConfig{Name: "s", InitTimeout: durPtr(45 * time.Second)}
		assert.Equal(t, 45*time.Second, m.resolveConnectTimeout(sc, false))
	})

	t.Run("docker-isolated keeps 3m floor for small init_timeout", func(t *testing.T) {
		m := NewManager(logger, &config.Config{}, nil, secret.NewResolver(), nil)
		sc := &config.ServerConfig{Name: "s", InitTimeout: durPtr(30 * time.Second)}
		assert.Equal(t, 3*time.Minute, m.resolveConnectTimeout(sc, true))
	})

	t.Run("docker-isolated honors a larger init_timeout above the floor", func(t *testing.T) {
		m := NewManager(logger, &config.Config{}, nil, secret.NewResolver(), nil)
		sc := &config.ServerConfig{Name: "s", InitTimeout: durPtr(10 * time.Minute)}
		assert.Equal(t, 10*time.Minute, m.resolveConnectTimeout(sc, true))
	})
}

// TestConnectPaths_InitTimeoutAboveThirtySeconds covers the manager half of
// #1772: with a caller context that carries no deadline of its own, every
// connect path hands the handshake the server's resolved init_timeout, not the
// 30s default. (The 30s ceilings were in the callers; see the supervisor and
// runtime tests for those.)
func TestConnectPaths_InitTimeoutAboveThirtySeconds(t *testing.T) {
	const initTimeout = 120 * time.Second
	initDur := config.Duration(initTimeout)

	// connectOnce runs connect against a fresh manager holding one server with
	// init_timeout 120s, and returns how long the handshake context had left.
	connectOnce := func(t *testing.T, connect func(m *Manager) error) time.Duration {
		t.Helper()
		var mu sync.Mutex
		var left []time.Duration
		m := newGenTestManager(t)
		m.SetConnectDeadlineHookForTest(func(_ string, ctx context.Context) {
			dl, ok := ctx.Deadline()
			mu.Lock()
			defer mu.Unlock()
			if ok {
				left = append(left, time.Until(dl))
			} else {
				left = append(left, 0)
			}
		})
		cfg := &config.ServerConfig{
			Name: "slow", URL: "http://127.0.0.1:1/mcp", Protocol: "streamable-http",
			Enabled: true, InitTimeout: &initDur,
		}
		m.AdvanceGenerations([]GenerationChange{present("slow", 3, cfg)})
		require.NoError(t, m.AddServerConfigAt("slow", cfg, 3))
		_ = connect(m)
		mu.Lock()
		defer mu.Unlock()
		require.Len(t, left, 1)
		return left[0]
	}

	t.Run("ConnectServerAt", func(t *testing.T) {
		d := connectOnce(t, func(m *Manager) error { return m.ConnectServerAt(context.Background(), "slow", 3) })
		assert.GreaterOrEqual(t, d, initTimeout-10*time.Second)
	})

	t.Run("ConnectAll", func(t *testing.T) {
		d := connectOnce(t, func(m *Manager) error { return m.ConnectAll(context.Background()) })
		assert.GreaterOrEqual(t, d, initTimeout-10*time.Second)
	})
}
