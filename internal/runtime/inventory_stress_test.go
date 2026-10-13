package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 117 PR-3 T310: concurrent single-server discovery (lenient and
// authoritative), sweeps, approval reindex, operator approve/block/enable,
// quarantine toggles (Spec 116 commits) and upstream inventory changes for two seconds under -race. Nothing may
// deadlock (goroutine dump on timeout), and once the writers stop and one
// final capture is applied, the index holds exactly the approved, enabled
// tools: no pending, changed or blocked tool is indexed.
func TestInventoryOrder_StressNoDeadlockAndConsistent(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}
	up := newInventoryUpstream(t)
	rt := newInventoryRuntime(t, up)
	ctx := context.Background()
	require.NoError(t, rt.discoverAndIndexToolsForServer(ctx, "inv", false))

	stop := make(chan struct{})
	var wg sync.WaitGroup
	loop := func(fn func(i int)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				fn(i)
			}
		}()
	}
	loop(func(int) { _ = rt.discoverAndIndexToolsForServer(ctx, "inv", false) })
	loop(func(int) { _ = rt.discoverAndIndexToolsForServer(ctx, "inv", true) })
	loop(func(int) { _ = rt.DiscoverAndIndexTools(ctx) })
	loop(func(int) { rt.reindexServerToolsAfterApprovalChange("inv") })
	loop(func(i int) {
		switch i % 4 {
		case 0:
			_ = rt.ApproveTools("inv", []string{"x"}, "operator")
		case 1:
			_, _ = rt.BlockTools("inv", []string{"x"}, "operator")
		case 2:
			_ = rt.SetToolEnabled("inv", "x", true, "operator")
		case 3:
			_ = rt.ApproveTools("inv", []string{"t"}, "operator")
		}
	})
	loop(func(i int) {
		_ = rt.QuarantineServer("inv", i%2 == 0)
		time.Sleep(50 * time.Millisecond)
	})
	loop(func(i int) {
		up.withX.Store(i%2 == 0)
		up.destructive.Store(i%3 == 0)
		time.Sleep(time.Millisecond)
	})

	time.Sleep(2 * time.Second)
	close(stop)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		buf := make([]byte, 1<<22)
		n := runtimeStack(buf)
		t.Fatalf("writers did not stop within 30s (deadlock?)\n%s", buf[:n])
	}

	// Trusted again, then one final authoritative capture; the background
	// reindexes spawned by the approvals settle on it.
	require.NoError(t, rt.QuarantineServer("inv", false))
	c, ok := rt.upstreamManager.GetClient("inv")
	require.True(t, ok)
	require.Eventually(t, c.IsConnected, 10*time.Second, 10*time.Millisecond)
	require.NoError(t, rt.discoverAndIndexToolsForServer(ctx, "inv", true))
	require.Eventually(t, func() bool {
		indexed := invIndexed(t, rt)
		for name := range indexed {
			rec := invRecord(t, rt, name)
			if rec == nil || rec.Status != storage.ToolApprovalStatusApproved || rec.Disabled {
				return false
			}
		}
		for _, name := range []string{"t", "x"} {
			rec := invRecord(t, rt, name)
			if rec == nil {
				continue
			}
			_, isIndexed := indexed[name]
			listed := name == "t" || up.withX.Load()
			if rec.Status == storage.ToolApprovalStatusApproved && !rec.Disabled && listed && !isIndexed {
				return false
			}
		}
		return true
	}, 15*time.Second, 20*time.Millisecond, "index and approval records must agree after the final capture")
}
