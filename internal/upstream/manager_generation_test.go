package upstream

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/callstats"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// Spec 116 PR3 manager tests (tasks.md T064, T3.8, T3.10): the *At methods,
// AdvanceGenerations (unregister vs restamp), the removal tombstone, instance
// binding and the generation-bound ForceReconnectAll.

func newGenTestManager(t *testing.T) *Manager {
	t.Helper()
	db, err := storage.NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	m := &Manager{
		clients:        make(map[string]*managed.Client),
		desired:        make(map[string]desiredGen),
		logger:         zap.NewNop(),
		storage:        db,
		secretResolver: secret.NewResolver(),
		callStats:      callstats.NewRegistry(),
	}
	m.globalConfig.Store(&config.Config{})
	return m
}

func genCfg(name, url string) *config.ServerConfig {
	return &config.ServerConfig{Name: name, URL: url, Protocol: "streamable-http", Enabled: false}
}

func present(name string, gen int64, cfg *config.ServerConfig) GenerationChange {
	return GenerationChange{Name: name, Gen: gen, Present: true, Config: cfg}
}

func registered(t *testing.T, m *Manager, name string) *managed.Client {
	t.Helper()
	c, ok := m.GetClient(name)
	require.True(t, ok, "%s must be registered", name)
	return c
}

func TestManagerGenerations_StaleAddIsRefused(t *testing.T) {
	m := newGenTestManager(t)
	cfgA := genCfg("a", "http://127.0.0.1:1/a")

	// No commit has recorded "a" yet: a bound add is refused.
	assert.ErrorIs(t, m.AddServerConfigAt("a", cfgA, 5), ErrStaleGeneration)
	_, ok := m.GetClient("a")
	assert.False(t, ok)

	m.AdvanceGenerations([]GenerationChange{present("a", 5, cfgA)})
	require.NoError(t, m.AddServerConfigAt("a", cfgA, 5))
	i1 := registered(t, m, "a")
	assert.Equal(t, int64(5), i1.Generation(), "an instance is stamped with its generation from construction")
	assert.NotZero(t, i1.InstanceID())

	// A worker for an older generation never touches the registry.
	assert.ErrorIs(t, m.AddServerConfigAt("a", genCfg("a", "http://127.0.0.1:1/old"), 4), ErrStaleGeneration)
	assert.Same(t, i1, registered(t, m, "a"))
	assert.ErrorIs(t, m.RemoveServerAt("a", 4), ErrStaleGeneration)
	assert.Same(t, i1, registered(t, m, "a"))
	assert.ErrorIs(t, m.ConnectServerAt(context.Background(), "a", 4), ErrStaleGeneration)
}

func TestManagerGenerations_AdvanceRestampsOrUnregisters(t *testing.T) {
	m := newGenTestManager(t)
	cfgA := genCfg("a", "http://127.0.0.1:1/a")
	m.AdvanceGenerations([]GenerationChange{present("a", 5, cfgA)})
	require.NoError(t, m.AddServerConfigAt("a", cfgA, 5))
	i1 := registered(t, m, "a")

	// A non-connection edit: the instance is kept and restamped in place.
	edited := genCfg("a", cfgA.URL)
	edited.DisabledTools = []string{"x"}
	res := m.AdvanceGenerations([]GenerationChange{present("a", 6, edited)})
	assert.Equal(t, []string{"a"}, res.Restamped)
	assert.Empty(t, res.Unregistered)
	assert.Same(t, i1, registered(t, m, "a"))
	assert.Equal(t, int64(6), i1.Generation())
	assert.Equal(t, []string{"x"}, i1.GetConfig().DisabledTools)

	// An unchanged server (generation did not move) is left alone.
	res = m.AdvanceGenerations([]GenerationChange{present("a", 6, edited)})
	assert.Empty(t, res.Restamped)
	assert.Empty(t, res.Unregistered)

	// A connection change: the instance is unregistered before the commit
	// publishes, and it never connects again.
	moved := genCfg("a", "http://127.0.0.1:1/moved")
	res = m.AdvanceGenerations([]GenerationChange{{Name: "a", Gen: 7, Present: true, Reconnect: true, Config: moved}})
	require.Len(t, res.Unregistered, 1)
	assert.Same(t, i1, res.Unregistered[0])
	assert.True(t, i1.Unregistered())
	_, ok := m.GetClient("a")
	assert.False(t, ok, "the superseded instance is unregistered by the commit itself")
	assert.ErrorIs(t, i1.Connect(context.Background()), managed.ErrInstanceUnregistered)

	// The superseded commit's worker is refused; the current one installs.
	assert.ErrorIs(t, m.AddServerConfigAt("a", edited, 6), ErrStaleGeneration)
	require.NoError(t, m.AddServerConfigAt("a", moved, 7))
	i2 := registered(t, m, "a")
	assert.NotEqual(t, i1.InstanceID(), i2.InstanceID())
	assert.Equal(t, moved.URL, i2.GetConfig().URL)
	assert.Equal(t, int64(7), i2.Generation())
}

// T3.10 (review R8.2): a one-commit remove and re-create of an EQUAL entry
// unregisters the old instance; the new generation gets a new instance.
func TestManagerGenerations_T3_10_ReplacedEqualEntryGetsNewInstance(t *testing.T) {
	m := newGenTestManager(t)
	cfg := genCfg("acme", "http://127.0.0.1:1/acme")
	m.AdvanceGenerations([]GenerationChange{present("acme", 3, cfg)})
	require.NoError(t, m.AddServerConfigAt("acme", cfg, 3))
	old := registered(t, m, "acme")

	same := genCfg("acme", cfg.URL)
	res := m.AdvanceGenerations([]GenerationChange{{Name: "acme", Gen: 8, Present: true, Reconnect: true, Replaced: true, Config: same}})
	require.Len(t, res.Unregistered, 1)
	assert.Same(t, old, res.Unregistered[0])
	gen, ok := m.DesiredGeneration("acme")
	assert.True(t, ok)
	assert.Equal(t, int64(8), gen)

	require.NoError(t, m.AddServerConfigAt("acme", same, 8))
	fresh := registered(t, m, "acme")
	assert.NotEqual(t, old.InstanceID(), fresh.InstanceID(), "the replacement is a new instance")
}

func TestManagerGenerations_RemovalTombstone(t *testing.T) {
	m := newGenTestManager(t)
	cfg := genCfg("a", "http://127.0.0.1:1/a")
	m.AdvanceGenerations([]GenerationChange{present("a", 2, cfg)})
	require.NoError(t, m.AddServerConfigAt("a", cfg, 2))
	inst := registered(t, m, "a")

	// gen-0 removal is refused while the name is configured.
	assert.ErrorIs(t, m.RemoveServerAt("a", 0), ErrStaleGeneration)

	res := m.AdvanceGenerations([]GenerationChange{{Name: "a"}})
	require.Len(t, res.Unregistered, 1)
	assert.Same(t, inst, res.Unregistered[0])
	_, isPresent := m.DesiredGeneration("a")
	assert.False(t, isPresent)

	// A removed name refuses re-registration at its old generation...
	assert.ErrorIs(t, m.AddServerConfigAt("a", cfg, 2), ErrStaleGeneration)
	_, ok := m.GetClient("a")
	assert.False(t, ok)
	// ...a gen-0 removal (the supervisor's ActionRemove) is accepted...
	assert.NoError(t, m.RemoveServerAt("a", 0))
	// ...and a later create overwrites the tombstone.
	m.AdvanceGenerations([]GenerationChange{present("a", 9, cfg)})
	require.NoError(t, m.AddServerConfigAt("a", cfg, 9))
	assert.Equal(t, int64(9), registered(t, m, "a").Generation())
}

// Review R5.2: an action captured for instance I1 never reaches I2 that a
// same-generation replacement installed.
func TestManagerGenerations_InstanceBoundDisconnectAndReplace(t *testing.T) {
	m := newGenTestManager(t)
	cfg := genCfg("s", "http://127.0.0.1:1/s")
	m.AdvanceGenerations([]GenerationChange{present("s", 4, cfg)})
	require.NoError(t, m.AddServerConfigAt("s", cfg, 4))
	i1 := registered(t, m, "s")

	i2, err := m.ReplaceInstanceAt("s", 4, i1, nil)
	require.NoError(t, err)
	assert.Same(t, i2, registered(t, m, "s"))
	assert.Equal(t, int64(4), i2.Generation())
	assert.True(t, i1.Unregistered())

	assert.ErrorIs(t, m.DisconnectServerAt("s", 4, i1.InstanceID()), ErrStaleGeneration)
	assert.NoError(t, m.DisconnectServerAt("s", 4, i2.InstanceID()))
	assert.ErrorIs(t, m.DisconnectServerAt("s", 3, i2.InstanceID()), ErrStaleGeneration)

	// A second replacement aimed at the old instance is refused.
	_, err = m.ReplaceInstanceAt("s", 4, i1, nil)
	assert.ErrorIs(t, err, ErrStaleGeneration)
	assert.Same(t, i2, registered(t, m, "s"))
}

// T3.8 (review R4.1): ForceReconnectAll that captured A and B before a commit
// removed A and replaced B neither re-creates A nor tears down B's replacement.
func TestManagerGenerations_T3_8_ForceReconnectAllAfterCommit(t *testing.T) {
	m := newGenTestManager(t)
	dockerCfg := func(name string, args ...string) *config.ServerConfig {
		// A command containing "docker" makes the client a Docker server for
		// ForceReconnectAll; the path does not exist, so nothing is launched.
		return &config.ServerConfig{Name: name, Command: "/nonexistent/docker-fake", Args: args, Protocol: "stdio", Enabled: true}
	}
	a, b := dockerCfg("a", "one"), dockerCfg("b", "one")
	m.AdvanceGenerations([]GenerationChange{present("a", 1, a), present("b", 1, b)})
	require.NoError(t, m.AddServerConfigAt("a", a, 1))
	require.NoError(t, m.AddServerConfigAt("b", b, 1))

	b2 := dockerCfg("b", "two")
	var once sync.Once
	var bReplacement *managed.Client
	hook := func(string) {
		once.Do(func() {
			// The competing commit: remove A, replace B's command.
			res := m.AdvanceGenerations([]GenerationChange{
				{Name: "a"},
				{Name: "b", Gen: 2, Present: true, Reconnect: true, Config: b2},
			})
			for _, c := range res.Unregistered {
				_ = c.Disconnect()
			}
			require.NoError(t, m.AddServerConfigAt("b", b2, 2))
			bReplacement = registered(t, m, "b")
		})
	}
	m.forceReconnectBeforeReplace.Store(&hook)

	result := m.ForceReconnectAll("test")
	require.NotNil(t, bReplacement)
	_, ok := m.GetClient("a")
	assert.False(t, ok, "ForceReconnectAll must not re-create a server removed after its capture")
	assert.Same(t, bReplacement, registered(t, m, "b"), "ForceReconnectAll must not replace B's newer instance")
	assert.Equal(t, []string{"two"}, registered(t, m, "b").GetConfig().Args)
	assert.Contains(t, result.SkippedServers, "a")
	assert.Contains(t, result.SkippedServers, "b")
}

// T3.8 round 1 (Sol review P2): a non-connection commit that restamps the
// captured instance between ForceReconnectAll's config read and its
// replacement must not let recovery install the OLD config stamped with the
// NEW generation. Recovery either skips the server or installs the latest
// committed config; here it retries against the restamped instance.
func TestManagerGenerations_T3_8_ForceReconnectAllAfterRestamp(t *testing.T) {
	m := newGenTestManager(t)
	a := &config.ServerConfig{Name: "a", Command: "/nonexistent/docker-fake", Args: []string{"one"}, Protocol: "stdio", Enabled: true}
	m.AdvanceGenerations([]GenerationChange{present("a", 1, a)})
	require.NoError(t, m.AddServerConfigAt("a", a, 1))
	captured := registered(t, m, "a")

	a2 := cloneServerConfig(a)
	yes := true
	a2.ExposePrompts = &yes // not a connection setting: restamp, not replace
	var once sync.Once
	hook := func(string) {
		once.Do(func() {
			res := m.AdvanceGenerations([]GenerationChange{present("a", 2, a2)})
			require.Equal(t, []string{"a"}, res.Restamped)
			require.Same(t, captured, registered(t, m, "a"))
		})
	}
	m.forceReconnectBeforeReplace.Store(&hook)

	result := m.ForceReconnectAll("test")
	cur := registered(t, m, "a")
	assert.Equal(t, int64(2), cur.Generation())
	ep := cur.GetConfig().ExposePrompts
	assert.True(t, ep != nil && *ep,
		"recovery must not install the pre-restamp config at the restamped generation (result: %+v)", result)
}

type recordingNotifier struct{ ch chan *Notification }

func (r recordingNotifier) SendNotification(n *Notification) { r.ch <- n }

// A superseded instance's state transitions are not reported under the
// server's name: the Disconnect of an instance a commit unregistered must not
// overwrite the status of the instance that now serves the server.
func TestManagerGenerations_UnregisteredInstanceIsSilent(t *testing.T) {
	m := newGenTestManager(t)
	rec := recordingNotifier{ch: make(chan *Notification, 16)}
	m.notificationMgr = NewNotificationManager()
	m.notificationMgr.AddHandler(rec)
	cfg := genCfg("s", "http://127.0.0.1:1/s")
	m.AdvanceGenerations([]GenerationChange{present("s", 1, cfg)})
	require.NoError(t, m.AddServerConfigAt("s", cfg, 1))
	inst := registered(t, m, "s")

	inst.StateManager.TransitionTo(types.StateConnecting)
	select {
	case n := <-rec.ch:
		assert.Equal(t, "s", n.ServerName)
	case <-time.After(5 * time.Second):
		t.Fatal("a registered instance's transition must be notified")
	}

	res := m.AdvanceGenerations([]GenerationChange{{Name: "s", Gen: 2, Present: true, Reconnect: true, Config: genCfg("s", "http://127.0.0.1:1/other")}})
	require.Len(t, res.Unregistered, 1)
	require.NoError(t, inst.Disconnect())
	inst.StateManager.TransitionTo(types.StateConnecting)
	select {
	case n := <-rec.ch:
		t.Fatalf("an unregistered instance reported %q for %s", n.Title, n.ServerName)
	case <-time.After(300 * time.Millisecond):
	}
}
