package server

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// TestApproveWithBlockNeverDispatchesBlockedTool races real destructive calls
// with the scanner approval path. The counting upstream is the proof: a
// quarantined server blocks before approval, and the atomically persisted
// approved+disabled record blocks after unquarantine. There must be no
// dispatch interval between those states.
func TestApproveWithBlockNeverDispatchesBlockedTool(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{
		Name: "filesystem", Enabled: true, Quarantined: true,
	}})
	up := startCountingUpstream(t, proxy, rt, "filesystem", destructiveSpec("delete_0"))
	discoveryConfig := rt.ConfigSnapshot().Clone()
	for _, server := range discoveryConfig.Servers {
		if server.Name == "filesystem" {
			server.Quarantined = false
		}
	}
	rt.UpdateConfig(discoveryConfig, "")
	runRuntimeDiscovery(t, proxy, rt, up)

	// The shared fixture starts approved so it can connect. Turn the server and
	// its one tool into the actual pre-approval state after connection setup.
	// Keep the contract discovery persisted: the unquarantiner rediscovering the
	// same tool must not turn this test's synthetic pending state into a change.
	approval, err := proxy.storage.GetToolApproval("filesystem", "delete_0")
	require.NoError(t, err)
	require.NotEmpty(t, approval.CurrentHash)
	require.NotEmpty(t, approval.CurrentDescription)
	require.NotEmpty(t, approval.CurrentSchema)
	approval.Status = storage.ToolApprovalStatusPending
	approval.ApprovedHash = ""
	approval.ApprovedAt = time.Time{}
	approval.ApprovedBy = ""
	approval.PreviousDescription = ""
	approval.PreviousAnnotations = nil
	approval.PreviousSchema = ""
	approval.PreviousOutputSchema = ""
	approval.Disabled = false
	approval.ClearScanHold()
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name: "filesystem", URL: up.URL, Protocol: "streamable-http", Enabled: true, Quarantined: true,
	}))
	require.NoError(t, proxy.storage.SaveToolApproval(approval))
	rt.Supervisor().StateView().UpdateServer("filesystem", func(s *stateview.ServerStatus) {
		s.Quarantined = true
	})
	configPath := filepath.Join(t.TempDir(), "mcp_config.json")
	require.NoError(t, config.SaveConfig(rt.Config(), configPath))
	rt.UpdateConfig(rt.Config(), configPath)

	// This is the production scanner operation, including the real runtime
	// unquarantiner. SaveIntegrityBaselineWithBlocks commits the disabled record
	// before that unquarantiner can clear quarantine.
	svc := scanner.NewService(rt.StorageManager(), scanner.NewRegistry(t.TempDir(), zap.NewNop()), scanner.NewDockerRunner(zap.NewNop()), t.TempDir(), zap.NewNop())
	svc.SetServerUnquarantiner(&serverUnquarantinerAdapter{server: proxy.mainServer})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var preApprovalCalls atomic.Int64
	var postApprovalCalls atomic.Int64
	var approvalReturned atomic.Bool
	quarantineResponse := func(result *mcp.CallToolResult) bool {
		if result == nil || result.IsError || len(result.Content) == 0 {
			return false
		}
		text, ok := result.Content[0].(mcp.TextContent)
		return ok && strings.Contains(text.Text, "QUARANTINED_SERVER_BLOCKED")
	}
	blockedResponse := func(result *mcp.CallToolResult) bool {
		if result == nil || !result.IsError || len(result.Content) == 0 {
			return false
		}
		text, ok := result.Content[0].(mcp.TextContent)
		return ok && strings.Contains(text.Text, "TOOL_BLOCKED")
	}
	callRequest := func() mcp.CallToolRequest {
		request := mcp.CallToolRequest{}
		request.Params.Name = contracts.ToolVariantDestructive
		request.Params.Arguments = map[string]interface{}{"name": "filesystem:delete_0"}
		return request
	}
	require.Equal(t, preflight.ToolClassServerQuarantined,
		proxy.evaluateToolGate("filesystem", "delete_0").class,
		"pre-approval policy must be server quarantine")
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for ctx.Err() == nil {
				postApproval := approvalReturned.Load()
				result, err := proxy.handleCallToolVariant(adminCtx(), callRequest(), contracts.ToolVariantDestructive)
				if err != nil || result == nil {
					t.Errorf("blocked call returned err=%v result=%v", err, result)
					return
				}
				if postApproval {
					if blockedResponse(result) {
						postApprovalCalls.Add(1)
					}
				} else if quarantineResponse(result) {
					preApprovalCalls.Add(1)
				}
			}
		}()
	}

	// A completed pre-approval call proves the workers exercised the
	// quarantined gate before the scanner operation starts.
	require.Eventually(t, func() bool { return preApprovalCalls.Load() > 0 }, 15*time.Second, time.Millisecond)
	// Keep the workers running through the storage commit and unquarantine; the
	// counting upstream below catches any transient callable window between them.
	require.NoError(t, svc.ApproveServerWithBlocks(context.Background(), "filesystem", true, "reviewer", []string{"delete_0"}))
	approvalReturned.Store(true)
	storedServer, err := proxy.storage.GetUpstreamServer("filesystem")
	require.NoError(t, err)
	require.False(t, storedServer.Quarantined, "approval must clear persisted quarantine")
	require.Equal(t, preflight.ToolClassBlockedByUser,
		proxy.evaluateToolGate("filesystem", "delete_0").class,
		"the post-approval policy must be the persisted disabled tool, not quarantine")
	// The unquarantiner starts a background full discovery pass. Complete a
	// synchronous discovery against its settled runtime client before releasing
	// callers, then prove the real disabled-tool refusal from that snapshot.
	runRuntimeDiscovery(t, proxy, rt, up)
	// The unquarantiner's background discovery pass may reconnect the client
	// after the synchronous discovery above, which advances the connection
	// epoch and makes the stamped discovery read as a previous connection's.
	// Re-stamp the live epoch on each poll so the assertion measures the
	// disabled-tool refusal rather than that reconnect timing.
	require.Eventually(t, func() bool {
		if client, ok := proxy.upstreamManager.GetClient("filesystem"); ok && client.IsConnected() {
			epoch := client.ConnectionEpoch()
			rt.Supervisor().StateView().UpdateServer("filesystem", func(s *stateview.ServerStatus) {
				s.DiscoveryEpoch = epoch
			})
		}
		result, callErr := proxy.handleCallToolVariant(adminCtx(), callRequest(), contracts.ToolVariantDestructive)
		return callErr == nil && blockedResponse(result)
	}, 15*time.Second, time.Millisecond)
	// A completed post-approval call proves the disabled approval record gates
	// callers after unquarantine, rather than only the original quarantine.
	require.Eventually(t, func() bool { return postApprovalCalls.Load() > 0 }, 15*time.Second, time.Millisecond)
	cancel()
	workers.Wait()

	blocked, err := proxy.storage.GetToolApproval("filesystem", "delete_0")
	require.NoError(t, err)
	require.Equal(t, storage.ToolApprovalStatusApproved, blocked.Status)
	require.True(t, blocked.Disabled)
	require.Equal(t, int64(0), up.count.Load(), "blocked tool must never reach the counting upstream")
}
