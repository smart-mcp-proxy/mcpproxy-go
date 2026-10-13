package runtime

import (
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

// SC-004 (Spec 116 PR2/PR4, review round 4): the three commit latency
// budgets, measured in-process on a 25-server configuration (the qa070 `big`
// profile size) so that no HTTP, JSON or scheduling noise of a live daemon is
// counted:
//
//   - a commit with no index work: p99 < 50 ms;
//   - a removal whose index purge drops a 320-tool server: p99 < 500 ms;
//   - the retirement of one connected instance inside a removal commit adds
//     < 100 ms (p99 of the Retire call itself, timed in unregisterLocked).
//
// It is a measurement, not a unit test: it runs only with
// MCPPROXY_SC004_MEASURE=1 and prints the percentiles it asserts. As of
// review round 4 the 320-tool purge misses its budget (about 5.5 s: one
// bleve transaction per deleted tool, which predates this PR); see #1617.
func TestSC004_CommitLatencyBudgets(t *testing.T) {
	if os.Getenv("MCPPROXY_SC004_MEASURE") != "1" {
		t.Skip("SC-004 measurement: set MCPPROXY_SC004_MEASURE=1")
	}
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	servers := make([]*config.ServerConfig, 0, 25)
	for i := 0; i < 25; i++ {
		servers = append(servers, disabledStdio(fmt.Sprintf("big-%02d", i)))
	}
	rt, _ := newSpec116Runtime(t, nil, servers...)

	const (
		noIndexRuns = 300
		purgeRuns   = 40
		retireRuns  = 40
		purgeTools  = 320
	)

	// 1. No index work: an argument change of a disabled server.
	noIndex := make([]time.Duration, 0, noIndexRuns)
	for i := 0; i < noIndexRuns; i++ {
		name := fmt.Sprintf("big-%02d", i%25)
		start := time.Now()
		_, err := rt.Commit("sc004-update", func(tx *CommitTx) error {
			return tx.UpdateServer(name, func(cur *config.ServerConfig) error {
				cur.Args = []string{fmt.Sprintf("--run=%d", i)}
				return nil
			})
		})
		noIndex = append(noIndex, time.Since(start))
		require.NoError(t, err)
	}

	// 2. Removal with an index purge of a 320-tool server.
	purge := make([]time.Duration, 0, purgeRuns)
	tools := make([]*config.ToolMetadata, 0, purgeTools)
	for j := 0; j < purgeTools; j++ {
		tools = append(tools, &config.ToolMetadata{
			Name:        fmt.Sprintf("tool_%03d", j),
			ServerName:  "big320",
			Description: fmt.Sprintf("tool %d of the 320-tool server: reads and writes records", j),
			ParamsJSON:  `{"type":"object","properties":{"id":{"type":"string"}}}`,
			Hash:        fmt.Sprintf("h%03d", j),
		})
	}
	for i := 0; i < purgeRuns; i++ {
		_, err := rt.Commit("sc004-create", func(tx *CommitTx) error {
			return tx.CreateServer(disabledStdio("big320"))
		})
		require.NoError(t, err)
		require.NoError(t, rt.indexManager.BatchIndexTools(tools))
		indexed, err := rt.indexManager.GetToolsByServer("big320")
		require.NoError(t, err)
		require.Len(t, indexed, purgeTools, "control: the server has 320 indexed tools")

		start := time.Now()
		_, err = rt.Commit("sc004-remove", func(tx *CommitTx) error { return tx.RemoveServer("big320") })
		purge = append(purge, time.Since(start))
		require.NoError(t, err)
		left, err := rt.indexManager.GetToolsByServer("big320")
		require.NoError(t, err)
		require.Empty(t, left, "the removal purged the server's tools")
	}

	// 3. Retirement of one connected instance inside a removal commit.
	up := newNamedToolUpstream(t, "live_tool")
	var mu sync.Mutex
	retire := make([]time.Duration, 0, retireRuns)
	restore := upstream.SetRetireObserverForTest(func(server string, d time.Duration) {
		if server == "live" {
			mu.Lock()
			retire = append(retire, d)
			mu.Unlock()
		}
	})
	defer restore()
	removeLive := make([]time.Duration, 0, retireRuns)
	for i := 0; i < retireRuns; i++ {
		_, err := rt.Commit("sc004-create-live", func(tx *CommitTx) error {
			return tx.CreateServer(explicitServer(&config.ServerConfig{
				Name: "live", URL: up.srv.URL, Protocol: "streamable-http", Enabled: true,
			}))
		})
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			c, ok := rt.upstreamManager.GetClient("live")
			return ok && c.IsConnected()
		}, 10*time.Second, 5*time.Millisecond, "the live instance connects")

		start := time.Now()
		_, err = rt.Commit("sc004-remove-live", func(tx *CommitTx) error { return tx.RemoveServer("live") })
		removeLive = append(removeLive, time.Since(start))
		require.NoError(t, err)
	}
	mu.Lock()
	retired := append([]time.Duration(nil), retire...)
	mu.Unlock()
	require.Len(t, retired, retireRuns, "every removal retired the connected instance once")

	report := func(label string, v []time.Duration) time.Duration {
		s := append([]time.Duration(nil), v...)
		sort.Slice(s, func(a, b int) bool { return s[a] < s[b] })
		p := func(q float64) time.Duration {
			k := int(q*float64(len(s))+0.999999) - 1
			if k < 0 {
				k = 0
			}
			return s[k]
		}
		t.Logf("SC-004 %-40s n=%-3d p50=%-12v p90=%-12v p99=%-12v max=%v",
			label, len(s), p(0.50), p(0.90), p(0.99), s[len(s)-1])
		return p(0.99)
	}
	p99NoIndex := report("commit, no index work (25 servers)", noIndex)
	p99Purge := report("removal, 320-tool index purge", purge)
	p99Retire := report("Retire of one connected instance", retired)
	report("removal commit incl. that retirement", removeLive)

	require.Less(t, p99NoIndex, 50*time.Millisecond, "SC-004: a commit with no index work, p99")
	require.Less(t, p99Purge, 500*time.Millisecond, "SC-004: a removal with a 320-tool purge, p99")
	require.Less(t, p99Retire, 100*time.Millisecond, "SC-004: the retirement overhead, p99")
}
