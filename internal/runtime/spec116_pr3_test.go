package runtime

import (
	"context"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/supervisor"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// Spec 116 PR3 (#1563, quickstart.md §T3): every connect/disconnect worker,
// RestartServer worker and supervisor action is bound to the server's commit
// generation (and, for an action on an existing instance, to that instance),
// and re-checks it inside the manager lock before it acts. A stale one is
// inert. Each test parks a REAL production path at its seam, lets a newer
// commit (or restart) return, then releases it and asserts on the manager.

// pr3Upstream is a streamable-HTTP MCP upstream listing one tool.
func pr3Upstream(t *testing.T, name, tool string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(captureTestServer(name, tool))
	t.Cleanup(srv.Close)
	return srv
}

func pr3Server(name, url string) *config.ServerConfig {
	return explicitServer(&config.ServerConfig{Name: name, URL: url, Protocol: "streamable-http", Enabled: true})
}

// pr3WorkerPark parks the first post-commit worker for name at the
// beforeWorkerAct seam and reports when workers of name finished.
type pr3WorkerPark struct {
	parked   chan struct{}
	release  chan struct{}
	done     chan struct{}
	gen      atomic.Int64
	once     sync.Once
	relOnce  sync.Once
	doneOnce sync.Once
	mu       sync.Mutex
	finished map[int64]chan struct{}
}

func installPR3WorkerPark(rt *Runtime, name string) *pr3WorkerPark {
	p := &pr3WorkerPark{parked: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{}), finished: map[int64]chan struct{}{}}
	parkedGoroutine := atomic.Int64{}
	before := func(n string, gen int64) {
		if n != name {
			return
		}
		first := false
		p.once.Do(func() { first = true })
		if !first {
			return
		}
		p.gen.Store(gen)
		parkedGoroutine.Store(currentGoroutineID())
		close(p.parked)
		<-p.release
	}
	after := func(n string, gen int64) {
		if n != name {
			return
		}
		if parkedGoroutine.Load() == currentGoroutineID() {
			p.doneOnce.Do(func() { close(p.done) })
			return
		}
		p.mu.Lock()
		ch, ok := p.finished[gen]
		if !ok {
			ch = make(chan struct{})
			p.finished[gen] = ch
		}
		select {
		case <-ch:
		default:
			close(ch)
		}
		p.mu.Unlock()
	}
	rt.SetWorkerActHooksForTest(before, after)
	return p
}

// finishedAt returns a channel closed when a (non-parked) worker of the
// server at gen has finished.
func (p *pr3WorkerPark) finishedAt(gen int64) <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	ch, ok := p.finished[gen]
	if !ok {
		ch = make(chan struct{})
		p.finished[gen] = ch
	}
	return ch
}

func (p *pr3WorkerPark) releaseParked() { p.relOnce.Do(func() { close(p.release) }) }

func pr3Gen(t *testing.T, rt *Runtime, name string) int64 {
	t.Helper()
	sg, ok := rt.configSvc.Current().ServerGeneration(name)
	require.True(t, ok, "server %s has a committed generation", name)
	return sg.Gen
}

func pr3Create(t *testing.T, rt *Runtime, sc *config.ServerConfig) {
	t.Helper()
	_, err := rt.Commit("pr3_create", func(tx *CommitTx) error { return tx.CreateServer(sc) })
	require.NoError(t, err)
}

func pr3Update(t *testing.T, rt *Runtime, name string, fn func(cur *config.ServerConfig)) {
	t.Helper()
	_, err := rt.Commit("pr3_update", func(tx *CommitTx) error {
		return tx.UpdateServer(name, func(cur *config.ServerConfig) error {
			fn(cur)
			return nil
		})
	})
	require.NoError(t, err)
}

func pr3Remove(t *testing.T, rt *Runtime, name string) {
	t.Helper()
	_, err := rt.Commit("pr3_remove", func(tx *CommitTx) error { return tx.RemoveServer(name) })
	require.NoError(t, err)
}

func pr3WaitConnected(t *testing.T, rt *Runtime, name, url string) *managed.Client {
	t.Helper()
	var got *managed.Client
	require.Eventually(t, func() bool {
		c, ok := rt.upstreamManager.GetClient(name)
		if !ok || c == nil || !c.IsConnected() || c.GetConfig().URL != url {
			return false
		}
		got = c
		return true
	}, 15*time.Second, 5*time.Millisecond, "%s must connect to %s", name, url)
	return got
}

// T3.1: the enabled worker for (s, g1), parked before it acts, is inert once a
// newer commit (a) removed s, (b) re-pointed it at another endpoint or (c)
// quarantined it. On main the released worker installs its old config.
func TestSpec116_T3_1_StaleEnabledWorkerIsInert(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	for _, variant := range []string{"remove", "replace", "quarantine"} {
		t.Run(variant, func(t *testing.T) {
			first := pr3Upstream(t, "first", "first_tool")
			second := pr3Upstream(t, "second", "second_tool")
			rt, _ := newSpec116Runtime(t, nil)
			park := installPR3WorkerPark(rt, "s")

			pr3Create(t, rt, pr3Server("s", first.URL))
			spec116Await(t, park.parked, "the g1 connect worker to park")
			g1 := park.gen.Load()

			switch variant {
			case "remove":
				pr3Remove(t, rt, "s")
			case "replace":
				pr3Update(t, rt, "s", func(cur *config.ServerConfig) { cur.URL = second.URL })
				spec116Await(t, park.finishedAt(pr3Gen(t, rt, "s")), "the replacement's worker")
				pr3WaitConnected(t, rt, "s", second.URL)
			case "quarantine":
				pr3Update(t, rt, "s", func(cur *config.ServerConfig) { cur.Quarantined = true })
				spec116Await(t, park.finishedAt(pr3Gen(t, rt, "s")), "the quarantine commit's worker")
			}
			if variant != "remove" {
				require.NotEqual(t, g1, pr3Gen(t, rt, "s"), "precondition: the competing commit moved the generation")
			}

			park.releaseParked()
			spec116Await(t, park.done, "the released stale worker")

			c, ok := rt.upstreamManager.GetClient("s")
			switch variant {
			case "remove":
				assert.False(t, ok, "a stale worker must not re-create a removed server")
			case "replace":
				require.True(t, ok)
				assert.Equal(t, second.URL, c.GetConfig().URL, "a stale worker must not re-install the old endpoint")
				assert.Equal(t, pr3Gen(t, rt, "s"), c.Generation())
			case "quarantine":
				require.True(t, ok)
				assert.True(t, c.GetConfig().Quarantined, "a stale worker must not replace the quarantined config with the unquarantined one")
				assert.Equal(t, pr3Gen(t, rt, "s"), c.Generation())
			}
		})
	}
}

// T3.2: the disabled worker for (s, g1), parked before it acts, must not tear
// down the instance a later enable (g2) installed and connected.
func TestSpec116_T3_2_StaleDisableWorkerKeepsReenabledInstance(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	up := pr3Upstream(t, "up", "up_tool")
	rt, _ := newSpec116Runtime(t, nil)
	pr3Create(t, rt, pr3Server("s", up.URL))
	pr3WaitConnected(t, rt, "s", up.URL)

	park := installPR3WorkerPark(rt, "s")
	pr3Update(t, rt, "s", func(cur *config.ServerConfig) { cur.Enabled = false })
	spec116Await(t, park.parked, "the disable worker to park")

	pr3Update(t, rt, "s", func(cur *config.ServerConfig) { cur.Enabled = true })
	spec116Await(t, park.finishedAt(pr3Gen(t, rt, "s")), "the re-enable worker")
	fresh := pr3WaitConnected(t, rt, "s", up.URL)

	park.releaseParked()
	spec116Await(t, park.done, "the released stale disable worker")

	c, ok := rt.upstreamManager.GetClient("s")
	require.True(t, ok, "the stale disable worker must not remove the re-enabled instance")
	assert.Same(t, fresh, c)
	assert.True(t, c.IsConnected(), "the re-enabled instance stays connected")
}

// T3.4: RestartServer's asynchronous worker, parked before it acts, must not
// bring back a server a later commit removed.
func TestSpec116_T3_4_StaleRestartWorkerDoesNotResurrect(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	up := pr3Upstream(t, "up", "up_tool")
	rt, _ := newSpec116Runtime(t, nil)
	pr3Create(t, rt, pr3Server("s", up.URL))
	pr3WaitConnected(t, rt, "s", up.URL)

	park := installPR3WorkerPark(rt, "s")
	require.NoError(t, rt.RestartServer("s"))
	spec116Await(t, park.parked, "the restart worker to park")

	pr3Remove(t, rt, "s")
	park.releaseParked()
	spec116Await(t, park.done, "the released restart worker")

	_, ok := rt.upstreamManager.GetClient("s")
	assert.False(t, ok, "a stale restart worker must not re-create a removed server")
}

// pr3ActionPark parks the first supervisor action for name that match accepts.
type pr3ActionPark struct {
	parked  chan struct{}
	release chan struct{}
	relOnce sync.Once
}

func installPR3ActionPark(rt *Runtime, name string, match func(supervisor.ReconcileAction) bool) *pr3ActionPark {
	p := &pr3ActionPark{parked: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	rt.supervisor.SetBeforeExecuteActionHookForTest(func(n string, action supervisor.ReconcileAction) {
		if n != name || !match(action) {
			return
		}
		first := false
		once.Do(func() { first = true })
		if !first {
			return
		}
		close(p.parked)
		<-p.release
	})
	return p
}

func (p *pr3ActionPark) releaseParked() { p.relOnce.Do(func() { close(p.release) }) }

func pr3AwaitQuiescent(t *testing.T, rt *Runtime) {
	t.Helper()
	require.Eventually(t, rt.supervisor.ReconcileQuiescent, 20*time.Second, 5*time.Millisecond,
		"supervisor actions must finish")
}

// T3.3: a supervisor ActionConnect, planned from a snapshot in which s was
// enabled and parked before it touches the manager, is a no-op once a newer
// commit removed, replaced or quarantined s. The REAL ActorPoolSimple and
// upstream.Manager are used; the manager is not settled before the action.
func TestSpec116_T3_3_StaleSupervisorConnectIsInert(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	for _, variant := range []string{"remove", "replace", "quarantine"} {
		t.Run(variant, func(t *testing.T) {
			first := pr3Upstream(t, "first", "first_tool")
			second := pr3Upstream(t, "second", "second_tool")
			rt, _ := newSpec116Runtime(t, nil)
			pr3Create(t, rt, pr3Server("s", first.URL))
			pr3WaitConnected(t, rt, "s", first.URL)

			park := installPR3ActionPark(rt, "s", func(a supervisor.ReconcileAction) bool {
				return a == supervisor.ActionConnect || a == supervisor.ActionReconnect
			})
			rt.supervisor.Start()
			spec116Await(t, park.parked, "the supervisor connect action to park")

			switch variant {
			case "remove":
				pr3Remove(t, rt, "s")
			case "replace":
				pr3Update(t, rt, "s", func(cur *config.ServerConfig) { cur.URL = second.URL })
				pr3WaitConnected(t, rt, "s", second.URL)
			case "quarantine":
				pr3Update(t, rt, "s", func(cur *config.ServerConfig) { cur.Quarantined = true })
				require.Eventually(t, func() bool {
					c, ok := rt.upstreamManager.GetClient("s")
					return ok && c.GetConfig().Quarantined
				}, 15*time.Second, 5*time.Millisecond)
			}
			park.releaseParked()
			pr3AwaitQuiescent(t, rt)

			c, ok := rt.upstreamManager.GetClient("s")
			switch variant {
			case "remove":
				assert.False(t, ok, "a stale supervisor action must not re-create a removed server")
			case "replace":
				require.True(t, ok)
				assert.Equal(t, second.URL, c.GetConfig().URL, "a stale supervisor action must not re-install the old endpoint")
			case "quarantine":
				require.True(t, ok)
				assert.True(t, c.GetConfig().Quarantined, "a stale supervisor action must not unquarantine the instance")
			}
		})
	}
}

// pr3ParkedQuarantinedDisconnect sets up a connected, quarantined (not
// exempted) server s and parks the supervisor ActionDisconnect the next
// reconcile plans for it, captured for the instance connected now.
func pr3ParkedQuarantinedDisconnect(t *testing.T, url string) (*Runtime, *pr3ActionPark, *managed.Client) {
	t.Helper()
	rt, _ := newSpec116Runtime(t, nil)
	sc := pr3Server("s", url)
	sc.Quarantined = true
	pr3Create(t, rt, sc)
	old := pr3WaitConnected(t, rt, "s", url)

	park := installPR3ActionPark(rt, "s", func(a supervisor.ReconcileAction) bool { return a == supervisor.ActionDisconnect })
	rt.supervisor.Start()
	// The startup reconcile (or the config subscription's first one) plans
	// the disconnect once the supervisor has seen s; nudge it if not.
	select {
	case <-park.parked:
	case <-time.After(3 * time.Second):
		rt.supervisor.ReconcileAfterExemptionChange()
	}
	spec116Await(t, park.parked, "the supervisor disconnect action to park")
	return rt, park, old
}

// T3.6: an old ActionDisconnect, parked, must not disconnect the replacement a
// newer commit installed and connected.
func TestSpec116_T3_6_StaleSupervisorDisconnectSparesReplacement(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	first := pr3Upstream(t, "first", "first_tool")
	second := pr3Upstream(t, "second", "second_tool")
	rt, park, old := pr3ParkedQuarantinedDisconnect(t, first.URL)

	pr3Update(t, rt, "s", func(cur *config.ServerConfig) { cur.URL = second.URL })
	fresh := pr3WaitConnected(t, rt, "s", second.URL)
	require.NotSame(t, old, fresh)

	park.releaseParked()
	pr3AwaitQuiescent(t, rt)
	c, ok := rt.upstreamManager.GetClient("s")
	require.True(t, ok)
	assert.Same(t, fresh, c)
	assert.True(t, c.IsConnected(), "the stale disconnect must not reach the replacement")
}

// T3.9 (review R5.2): a disconnect captured for instance I1, parked, must not
// disconnect I2 that RestartServer installed at the SAME generation.
func TestSpec116_T3_9_StaleDisconnectSparesSameGenerationRestart(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	up := pr3Upstream(t, "up", "up_tool")
	rt, park, old := pr3ParkedQuarantinedDisconnect(t, up.URL)
	g := pr3Gen(t, rt, "s")

	require.NoError(t, rt.RestartServer("s"))
	var fresh *managed.Client
	require.Eventually(t, func() bool {
		c, ok := rt.upstreamManager.GetClient("s")
		if !ok || c == old || !c.IsConnected() {
			return false
		}
		fresh = c
		return true
	}, 15*time.Second, 5*time.Millisecond, "the restart must install and connect a new instance")
	require.Equal(t, g, pr3Gen(t, rt, "s"), "precondition: the restart did not change the generation")

	park.releaseParked()
	pr3AwaitQuiescent(t, rt)
	c, ok := rt.upstreamManager.GetClient("s")
	require.True(t, ok)
	assert.Same(t, fresh, c)
	assert.True(t, c.IsConnected(), "a disconnect captured for the old instance must not reach the restarted one")
}

// T3.7 (FR-021b): an approval-driven reindex must not reapply a last-good
// snapshot captured from an instance of an older generation after the endpoint
// changed; it re-lists the current instance instead.
func TestSpec116_T3_7_LastGoodSnapshotOfOldGenerationIsNotReapplied(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	first := pr3Upstream(t, "first", "first_tool")
	second := pr3Upstream(t, "second", "second_tool")
	rt, _ := newSpec116Runtime(t, nil)
	pr3Create(t, rt, pr3Server("s", first.URL))
	pr3WaitConnected(t, rt, "s", first.URL)
	require.NoError(t, rt.DiscoverAndIndexToolsForServer(context.Background(), "s"))
	require.Len(t, rt.lastGoodToolsSnapshot("s"), 1, "precondition: the last-good snapshot holds the first endpoint's tool")

	pr3Update(t, rt, "s", func(cur *config.ServerConfig) { cur.URL = second.URL })
	pr3WaitConnected(t, rt, "s", second.URL)

	rt.reindexServerToolsAfterApprovalChange("s")

	indexed, err := rt.indexManager.GetToolsByServer("s")
	require.NoError(t, err)
	got := make([]string, 0, len(indexed))
	for _, tool := range indexed {
		got = append(got, config.RawToolName(tool))
	}
	assert.NotContains(t, got, "first_tool", "the old generation's snapshot must not be reapplied")
	// The current instance was re-listed: its (new, so pending review on a
	// trusted server, Spec 032) tool now has a record, and the last-good
	// snapshot is the current endpoint's.
	_, err = rt.storageManager.GetToolApproval("s", "second_tool")
	assert.NoError(t, err, "the current instance is re-listed")
	snap := rt.lastGoodToolsSnapshot("s")
	require.Len(t, snap, 1)
	assert.Equal(t, "second_tool", config.RawToolName(snap[0]))
}

// T3.5 (b): a quarantined capture holding the capture section (inside
// WithCurrentClientOnEpoch) orders the endpoint-replacing commit after it:
// the commit waits for the section (CaptureReplacementQueued) instead of
// publishing past it.
func TestSpec116_T3_5b_CommitWaitsForCaptureSection(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	first := pr3Upstream(t, "first", "old_definition")
	second := pr3Upstream(t, "second", "current_definition")
	rt, _ := newSpec116Runtime(t, nil)
	rt.skipQuarantinedContractVerify = true
	sc := pr3Server("q", first.URL)
	sc.Quarantined = true
	pr3Create(t, rt, sc)
	client := pr3WaitConnected(t, rt, "q", first.URL)

	tools, err := client.ListToolsFresh(context.Background())
	require.NoError(t, err)
	inSection := make(chan struct{})
	releaseSection := make(chan struct{})
	var once sync.Once
	rt.quarantinedCaptureDuringPersist = func() {
		once.Do(func() {
			close(inSection)
			<-releaseSection
		})
	}
	captureDone := make(chan error, 1)
	go func() {
		_, err := rt.captureQuarantinedToolDefinitionsFromCurrentClient("q", client, rt.discoveryGeneration("q"), client.Generation(), tools, managed.InventoryCapture{})
		captureDone <- err
	}()
	spec116Await(t, inSection, "the capture section")

	// The replacement's connect worker is parked, so the only capture-lock
	// waiter the probe below can see is the commit itself.
	park := installPR3WorkerPark(rt, "q")
	defer park.releaseParked()
	commitDone := make(chan error, 1)
	go func() {
		_, err := rt.Commit("pr3_replace", func(tx *CommitTx) error {
			return tx.UpdateServer("q", func(cur *config.ServerConfig) error {
				cur.URL = second.URL
				return nil
			})
		})
		commitDone <- err
	}()
	returned, err := spec116AwaitReturnOrBlocked(t, commitDone, rt.upstreamManager.CaptureReplacementQueued)
	close(releaseSection)
	require.NoError(t, spec116AwaitErr(t, captureDone, "the capture"))
	if !returned {
		require.NoError(t, spec116AwaitErr(t, commitDone, "the commit"))
	}
	require.NoError(t, err)
	assert.False(t, returned, "the replacing commit must wait for the capture section, not publish past it")
}
