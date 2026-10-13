package managed

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// pingInstanceID calls the fixture's deterministic `ping` tool and returns the
// per-process instance_id, which changes on every process start.
func pingInstanceID(ctx context.Context, mc *Client) (string, error) {
	res, err := mc.CallTool(ctx, "ping", nil)
	if err != nil {
		return "", err
	}
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			var body struct {
				InstanceID string `json:"instance_id"`
			}
			if jerr := json.Unmarshal([]byte(tc.Text), &body); jerr == nil && body.InstanceID != "" {
				return body.InstanceID, nil
			}
		}
	}
	return "", nil
}

// TestStdioChildKilled_ClientLeavesReadyAndRespawns is the end-to-end
// RC4-STDIO-001 regression: an admitted stdio upstream (cmd/mcpfixture) whose
// child process is SIGKILLed must stop reporting Ready on the first failed
// call ("transport closed") and be respawned by the managed client's own
// health loop + backoff, without any explicit restart.
func TestStdioChildKilled_ClientLeavesReadyAndRespawns(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and spawns the mcpfixture stdio binary")
	}
	if runtime.GOOS == "windows" {
		t.Skip("uses pkill to SIGKILL the child")
	}
	if _, err := exec.LookPath("pkill"); err != nil {
		t.Skip("pkill not available")
	}

	bin := filepath.Join(t.TempDir(), "mcpfixture-deadtransport")
	build := exec.Command("go", "build", "-o", bin, "github.com/smart-mcp-proxy/mcpproxy-go/cmd/mcpfixture")
	out, err := build.CombinedOutput()
	require.NoError(t, err, "build mcpfixture: %s", out)

	interval := config.Duration(200 * time.Millisecond)
	cfg := &config.ServerConfig{
		Name:                "stdio-dead-transport",
		Command:             bin,
		Args:                []string{"--transport", "stdio"},
		Protocol:            "stdio",
		Enabled:             true,
		HealthCheckInterval: &interval,
	}
	mc, err := NewClient(cfg.Name, cfg, zap.NewNop(), nil, &config.Config{}, nil, secret.NewResolver())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	require.NoError(t, mc.Connect(ctx))
	t.Cleanup(func() { _ = mc.Disconnect() })

	firstID, err := pingInstanceID(ctx, mc)
	require.NoError(t, err)
	require.NotEmpty(t, firstID)
	epochBefore := mc.ConnectionEpoch()

	// SIGKILL only the child: the proxy-side client is left untouched.
	require.NoError(t, exec.Command("pkill", "-KILL", "-f", bin).Run())

	// The dead transport must surface as a failed call, and that call must
	// stop the client from reporting Ready on the dead generation.
	require.Eventually(t, func() bool {
		_, callErr := mc.CallTool(ctx, "ping", nil)
		return callErr != nil
	}, 5*time.Second, 20*time.Millisecond, "calls must start failing once the child is dead")
	require.True(t, !mc.IsConnected() || mc.ConnectionEpoch() != epochBefore,
		"after a call failed on the dead transport the client must not still report Ready on that connection (state=%s)",
		mc.GetState())

	// ...and the managed client must respawn the process on its own.
	var newID string
	require.Eventually(t, func() bool {
		if !mc.IsConnected() {
			return false
		}
		id, callErr := pingInstanceID(ctx, mc)
		if callErr != nil || id == "" {
			return false
		}
		newID = id
		return true
	}, 20*time.Second, 100*time.Millisecond, "the client must reconnect a fresh stdio process (state=%s)", mc.GetState())
	require.NotEqual(t, firstID, newID, "the reconnected upstream must be a new process")
}
