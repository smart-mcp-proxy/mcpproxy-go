package management

import (
	"context"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// The REST/tray `isolation_defaults.image` placeholder exists so a user can see
// the image the container will really run before deciding whether to override
// it. ResolveDefaults picks the git-capable image from the server's ARGS
// (#1143), so a call site that builds a throwaway ServerConfig without them
// resolves a different image than the spawn path — the placeholder shows the
// slim image while the container runs the git one, and the two surfaces
// disagree with no way for the user to tell which is right.
func TestListServers_IsolationDefaultsReflectGitDependency(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	cfg := &config.Config{DockerIsolation: config.DefaultDockerIsolationConfig()}
	cfg.DockerIsolation.Enabled = true

	rt := newMockRuntime()
	rt.servers = []map[string]interface{}{
		{
			"id": "git-server", "name": "git-server", "protocol": "stdio",
			"command": "uvx", "enabled": true,
			"args": []string{"--from", "srv@git+https://github.com/o/r", "srv"},
		},
		{
			"id": "plain", "name": "plain", "protocol": "stdio",
			"command": "uvx", "enabled": true,
			"args": []string{"mcp-server-fetch"},
		},
	}

	svc := NewService(rt, cfg, "", &mockEventEmitter{}, nil, logger)
	servers, _, err := svc.ListServers(context.Background())
	require.NoError(t, err)
	require.Len(t, servers, 2)

	byName := map[string]int{}
	for i, s := range servers {
		byName[s.Name] = i
	}

	gitSrv := servers[byName["git-server"]]
	require.NotNil(t, gitSrv.IsolationDefaults)
	if got, want := gitSrv.IsolationDefaults.Image, config.DefaultGitCapableImage; got != want {
		t.Errorf("git-dependency placeholder image = %q, want %q (what the spawn path resolves)", got, want)
	}

	plain := servers[byName["plain"]]
	require.NotNil(t, plain.IsolationDefaults)
	if got, want := plain.IsolationDefaults.Image, cfg.DockerIsolation.DefaultImages["uvx"]; got != want {
		t.Errorf("plain server placeholder image = %q, want the slim runtime default %q", got, want)
	}
}
