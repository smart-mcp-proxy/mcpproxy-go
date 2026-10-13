package upstream

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
