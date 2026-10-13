package upstream

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// TestConnectBudget_ServerRunningDockerItself is the composed regression test
// for the connect budget of a server whose OWN command is `docker`.
//
// Such a server is never CONTAINERISED by us — the resolver's already-docker
// structural gate correctly refuses to double-wrap it — but it still shells out
// to the Docker daemon and still pays image-pull latency before it answers
// `initialize`. The 3-minute floor must therefore cover it, exactly as it
// covers a server we containerise ourselves. Routing the timeout predicate
// through ResolveIsolation alone dropped it to the resolved init_timeout (30s
// by default), which can kill the server mid-pull (GH #1142).
func TestConnectBudget_ServerRunningDockerItself(t *testing.T) {
	logger := zap.NewNop()
	gc := &config.Config{DockerIsolation: &config.DockerIsolationConfig{Enabled: true}}
	m := NewManager(logger, gc, nil, secret.NewResolver(), nil)

	sc := &config.ServerConfig{
		Name:     "already-dockerised",
		Command:  "docker",
		Args:     []string{"run", "-i", "--rm", "mcp/foo"},
		Protocol: "stdio",
		Enabled:  true,
	}
	client, err := managed.NewClient("already-dockerised", sc, logger, nil, gc, nil, secret.NewResolver())
	require.NoError(t, err)

	assert.Equal(t, 3*time.Minute, m.resolveConnectTimeout(sc, client.DependsOnDocker()),
		"a server whose own command is docker must keep the 3-minute image-pull budget")
}

// TestShouldEnableDockerRecovery_HonorsIsolationModes covers the third
// classification site: the Docker recovery monitor (and, through
// UsesDockerIsolation, shutdown container-cleanup verification) was still
// hand-rolled from the two LEGACY booleans and never consulted isolation MODES.
//
// So a config that really does containerise its servers — a global
// `{mode:"docker", enabled:false}`, or a per-server `isolation.mode:"docker"`
// while the global legacy flag is off — left recovery monitoring off and let
// shutdown skip container cleanup, leaking containers (GH #1142).
func TestShouldEnableDockerRecovery_HonorsIsolationModes(t *testing.T) {
	newManager := func(global *config.DockerIsolationConfig, servers ...*config.ServerConfig) *Manager {
		m := &Manager{logger: zap.NewNop()}
		m.globalConfig.Store(&config.Config{DockerIsolation: global, Servers: servers})
		return m
	}
	dockerMode := config.IsolationModeDocker

	t.Run("global mode docker with the legacy bool false", func(t *testing.T) {
		m := newManager(
			&config.DockerIsolationConfig{Mode: config.IsolationModeDocker, Enabled: false},
			&config.ServerConfig{Name: "npx-server", Command: "npx"},
		)
		assert.True(t, m.shouldEnableDockerRecovery(),
			"global mode:docker containerises servers even with the legacy enabled flag false")
	})

	t.Run("per-server mode docker with global isolation off", func(t *testing.T) {
		m := newManager(
			&config.DockerIsolationConfig{Enabled: false},
			&config.ServerConfig{
				Name:      "moded",
				Command:   "npx",
				Isolation: &config.IsolationConfig{Mode: &dockerMode},
			},
		)
		assert.True(t, m.shouldEnableDockerRecovery(),
			"a per-server mode:docker override is honoured at spawn, so its containers need recovery and cleanup")
	})

	t.Run("per-server sandbox mode under global docker still needs recovery for its siblings", func(t *testing.T) {
		sandboxMode := config.IsolationModeSandbox
		m := newManager(
			&config.DockerIsolationConfig{Enabled: true},
			&config.ServerConfig{
				Name:      "sandboxed",
				Command:   "npx",
				Isolation: &config.IsolationConfig{Mode: &sandboxMode},
			},
		)
		assert.True(t, m.shouldEnableDockerRecovery(),
			"the global mode is docker, so any server added later is containerised")
	})
}
