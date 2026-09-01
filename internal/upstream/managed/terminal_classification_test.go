package managed

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

func newClientForConnect(t *testing.T, cfg *config.ServerConfig) *Client {
	t.Helper()
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	mc, err := NewClient(cfg.Name, cfg, zap.NewNop(), nil, &config.Config{}, nil, secret.NewResolver())
	require.NoError(t, err)
	return mc
}

// TestConnect_PermanentFailureParksTerminal is the GH #1145 acceptance at the
// connect site: a package runner with no package to run cannot start no matter
// how many times we try, so the failure must park the connection terminally
// instead of feeding the retry ladder forever.
//
// This particular fault is chosen because it is decided by mcpproxy's own
// pre-spawn validation (core.validateStdioConfig), so the error text is exact
// every time. A missing-binary ENOENT would exercise the same code path but its
// wording depends on capturing the child's stderr before the transport closes,
// which is a race under load.
func TestConnect_PermanentFailureParksTerminal(t *testing.T) {
	mc := newClientForConnect(t, &config.ServerConfig{
		Name:     "runner-without-package",
		Protocol: "stdio",
		Command:  "uvx",
		Enabled:  true,
		Created:  time.Now(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.Error(t, mc.Connect(ctx))

	info := mc.StateManager.GetConnectionInfo()
	require.True(t, info.Terminal, "an unrunnable command must classify as permanent, got code %q err %v", info.TerminalCode, info.LastError)
	assert.Equal(t, string(diagnostics.ConfigInvalidCommand), info.TerminalCode)
	assert.True(t, diagnostics.IsPermanent(diagnostics.Code(info.TerminalCode)),
		"the recorded code %q must be one the catalog declares permanent", info.TerminalCode)

	// One confirmation attempt, then the supervisor gate closes for good.
	// (Backdate the attempt so only the terminal park, not the ordinary
	// exponential backoff, can be the reason a reconnect is refused.)
	elapsed := info
	elapsed.LastRetryTime = time.Now().Add(-time.Hour)
	assert.True(t, elapsed.ShouldAutoReconnect(time.Now()), "the first failure still owes a confirmation attempt")

	require.Error(t, mc.Connect(ctx))
	info = mc.StateManager.GetConnectionInfo()
	elapsed = info
	elapsed.LastRetryTime = time.Now().Add(-time.Hour)
	assert.False(t, elapsed.ShouldAutoReconnect(time.Now()), "a confirmed permanent failure must stop being re-dialed")
	assert.False(t, mc.StateManager.ShouldRetry(), "the in-client ladder must stop too")
	assert.Equal(t, 2, info.RetryCount, "exactly two attempts, not 55")

	// Recovery contract: an explicit disconnect (the manual restart path) un-parks.
	require.NoError(t, mc.Disconnect())
	assert.False(t, mc.StateManager.GetConnectionInfo().Terminal,
		"a user who fixes the problem and restarts must get the server back")
}

// TestConnect_TransientFailureStaysRetryable is the anti-case: an unreachable
// endpoint is not proof of a config fault, so nothing may be parked.
func TestConnect_TransientFailureStaysRetryable(t *testing.T) {
	// Bind then release a port so it is almost certainly closed.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())

	mc := newClientForConnect(t, &config.ServerConfig{
		Name:     "unreachable",
		Protocol: "http",
		URL:      "http://" + addr + "/mcp",
		Enabled:  true,
		Created:  time.Now(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.Error(t, mc.Connect(ctx))

	info := mc.StateManager.GetConnectionInfo()
	assert.False(t, info.Terminal, "an unreachable endpoint must stay retryable (code %q)", info.TerminalCode)
	assert.Empty(t, info.TerminalCode)
}

// TestConnect_ChildStderrNeverParks is the regression for the review of GH
// #1145. The stdio classifier's substring fallbacks read an error string that
// embeds the child's captured stderr, so a server that merely crashed while
// printing "no such file or directory" was classified MCPX_STDIO_SPAWN_ENOENT —
// a code declared permanent — and parked for good after two attempts. The
// classification is still the most useful message we have; what it must not do
// is stop automatic recovery on the child's own words.
func TestConnect_ChildStderrNeverParks(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
	}{
		{"ENOENT on a data file", "Error: ENOENT: no such file or directory, open '/tmp/agent-state.json'"},
		{"EACCES on a cache dir", "EACCES: permission denied, mkdir '/var/cache/agent'"},
		{"a helper it shelled out to is missing", "sh: line 1: helper-tool: command not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := newClientForConnect(t, &config.ServerConfig{
				Name:     "transiently-crashing",
				Protocol: "stdio",
				Command:  "sh",
				Args:     []string{"-c", "echo " + strconv.Quote(tt.stderr) + " >&2; exit 1"},
				Enabled:  true,
				Created:  time.Now(),
			})

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			require.Error(t, mc.Connect(ctx))

			info := mc.StateManager.GetConnectionInfo()
			assert.False(t, info.Terminal,
				"a crash whose stderr merely contains a spawn-like phrase must stay retryable (code %q, err %v)",
				info.TerminalCode, info.LastError)
			assert.Empty(t, info.TerminalCode)

			backdated := info
			backdated.LastRetryTime = time.Now().Add(-time.Hour)
			assert.True(t, backdated.ShouldAutoReconnect(time.Now()),
				"the supervisor must keep probing an unproven failure")
		})
	}
}
