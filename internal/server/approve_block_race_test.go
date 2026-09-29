package server

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
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

	// The shared fixture starts approved so it can connect. Turn the server and
	// its one tool into the actual pre-approval state after connection setup.
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name: "filesystem", URL: up.URL, Protocol: "streamable-http", Enabled: true, Quarantined: true,
	}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "filesystem", ToolName: "delete_0", Status: storage.ToolApprovalStatusPending,
		CurrentHash: "delete-0-hash", CurrentDescription: "Delete a file",
	}))
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
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			request := mcp.CallToolRequest{}
			request.Params.Name = contracts.ToolVariantDestructive
			request.Params.Arguments = map[string]interface{}{"name": "filesystem:delete_0"}
			for ctx.Err() == nil {
				result, err := proxy.handleCallToolVariant(adminCtx(), request, contracts.ToolVariantDestructive)
				if err != nil || result == nil {
					t.Errorf("blocked call returned err=%v result=%v", err, result)
					return
				}
			}
		}()
	}

	// Let callers observe the quarantined state, then approve while they are
	// still dispatching. Force avoids adding unrelated scan setup to this race.
	time.Sleep(20 * time.Millisecond)
	require.NoError(t, svc.ApproveServerWithBlocks(context.Background(), "filesystem", true, "reviewer", []string{"delete_0"}))
	time.Sleep(20 * time.Millisecond)
	cancel()
	workers.Wait()

	blocked, err := proxy.storage.GetToolApproval("filesystem", "delete_0")
	require.NoError(t, err)
	require.Equal(t, storage.ToolApprovalStatusApproved, blocked.Status)
	require.True(t, blocked.Disabled)
	require.Equal(t, int64(0), up.count.Load(), "blocked tool must never reach the counting upstream")
}
