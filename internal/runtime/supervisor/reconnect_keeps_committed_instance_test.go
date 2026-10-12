package supervisor

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/configsvc"
)

// instanceUpstream models the manager's registration semantics that matter to
// ActionReconnect: AddServer keeps a registered instance whose connection
// config is equivalent (config.ConnectionEquivalent, as
// Manager.AddServerConfigAt does) and replaces (and disconnects) one that is
// not; DisconnectServer and ConnectServer act on whatever instance is
// registered under the name when they run.
type instanceUpstream struct {
	mu      sync.Mutex
	nextID  int
	current map[string]*fakeInstance
	// disconnected records every instance id that was disconnected.
	disconnected []int
	eventCh      chan Event
}

type fakeInstance struct {
	id        int
	cfg       *config.ServerConfig
	connected bool
}

func newInstanceUpstream() *instanceUpstream {
	return &instanceUpstream{current: map[string]*fakeInstance{}, eventCh: make(chan Event, 10)}
}

func (u *instanceUpstream) AddServer(name string, cfg *config.ServerConfig) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if inst, ok := u.current[name]; ok {
		if config.ConnectionEquivalent(inst.cfg, cfg) {
			inst.cfg = cfg
			return nil
		}
		inst.connected = false
		u.disconnected = append(u.disconnected, inst.id)
	}
	u.nextID++
	u.current[name] = &fakeInstance{id: u.nextID, cfg: cfg}
	return nil
}

func (u *instanceUpstream) RemoveServer(name string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.current, name)
	return nil
}

func (u *instanceUpstream) ConnectServer(_ context.Context, name string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if inst, ok := u.current[name]; ok {
		inst.connected = true
	}
	return nil
}

func (u *instanceUpstream) DisconnectServer(name string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if inst, ok := u.current[name]; ok {
		inst.connected = false
		u.disconnected = append(u.disconnected, inst.id)
	}
	return nil
}

func (u *instanceUpstream) ConnectAll(context.Context) error { return nil }

func (u *instanceUpstream) GetServerState(name string) (*ServerState, error) {
	return u.GetAllStates()[name], nil
}

func (u *instanceUpstream) GetAllStates() map[string]*ServerState {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := map[string]*ServerState{}
	for name, inst := range u.current {
		out[name] = &ServerState{Name: name, Config: inst.cfg, Enabled: inst.cfg.Enabled, Connected: inst.connected}
	}
	return out
}

func (u *instanceUpstream) IsUserLoggedOut(string) bool { return false }
func (u *instanceUpstream) Subscribe() <-chan Event     { return u.eventCh }
func (u *instanceUpstream) Unsubscribe(<-chan Event)    {}
func (u *instanceUpstream) Close()                      {}

// TestReconnect_DoesNotDisconnectTheCommittedInstance is the regression guard
// for the re-enable failure after Spec 116 PR1 (#1590): every config commit
// publishes to configsvc, so the supervisor reconciles a change the commit's
// own worker has already applied. Re-enabling a server makes the supervisor
// plan ActionReconnect (its snapshot still holds the disabled entry), and the
// reconnect's first leg used to resolve the client by name and disconnect the
// instance the commit worker had just installed and connected with the
// enabled config. That disconnect raced the worker's Connect and left the
// server with no tools until the next periodic reconcile.
//
// ActionReconnect must act only on an instance whose connection config
// differs from the desired one; AddServer already replaces exactly those.
func TestReconnect_DoesNotDisconnectTheCommittedInstance(t *testing.T) {
	disabled := &config.ServerConfig{Name: "s", URL: "http://127.0.0.1:1/mcp", Protocol: "streamable-http", Enabled: false}
	enabled := &config.ServerConfig{Name: "s", URL: "http://127.0.0.1:1/mcp", Protocol: "streamable-http", Enabled: true}

	cfg := &config.Config{Listen: "127.0.0.1:0", Servers: []*config.ServerConfig{disabled}}
	svc := configsvc.NewService(cfg, "", zap.NewNop())
	defer svc.Close()

	up := newInstanceUpstream()
	sup := New(svc, up, zap.NewNop())

	// The supervisor's snapshot holds the disabled entry. reconcile dispatches
	// its actions asynchronously; drain them so none lands after the steps
	// below.
	require.NoError(t, sup.reconcile(svc.Current()))
	sup.actionWg.Wait()

	// The re-enable commit's worker installs and connects the enabled instance
	// (runCommitWorkers → Manager.AddServerAt).
	require.NoError(t, up.AddServer("s", enabled))
	require.NoError(t, up.ConnectServer(context.Background(), "s"))
	committed := up.current["s"].id

	// The commit's publish reaches the supervisor.
	next := &config.Config{Listen: "127.0.0.1:0", Servers: []*config.ServerConfig{enabled}}
	require.NoError(t, svc.Update(next, configsvc.UpdateTypeModify, "test_enable"))
	snap := svc.Current()

	plan := sup.computeReconcilePlan(snap, up.GetAllStates(), map[string]bool{})
	require.Equal(t, ActionReconnect, plan.Actions["s"], "scenario precondition: the stale supervisor snapshot plans a reconnect")

	require.NoError(t, sup.executeAction("s", ActionReconnect, snap))

	up.mu.Lock()
	defer up.mu.Unlock()
	require.NotContains(t, up.disconnected, committed,
		"ActionReconnect disconnected the instance the commit had already installed with the desired config")
	require.Equal(t, committed, up.current["s"].id, "the committed instance must stay registered")
	require.True(t, up.current["s"].connected, "the committed instance must stay connected")
}

// TestReconnect_ReplacesAnInstanceWithAnOldConnectionConfig keeps the
// reconnect's purpose: an instance still running a connection config that
// differs from the desired one is replaced and the replacement connects.
func TestReconnect_ReplacesAnInstanceWithAnOldConnectionConfig(t *testing.T) {
	oldCfg := &config.ServerConfig{Name: "s", URL: "http://127.0.0.1:1/old", Protocol: "streamable-http", Enabled: true}
	newCfg := &config.ServerConfig{Name: "s", URL: "http://127.0.0.1:1/new", Protocol: "streamable-http", Enabled: true}

	svc := configsvc.NewService(&config.Config{Listen: "127.0.0.1:0", Servers: []*config.ServerConfig{oldCfg}}, "", zap.NewNop())
	defer svc.Close()

	up := newInstanceUpstream()
	sup := New(svc, up, zap.NewNop())
	require.NoError(t, up.AddServer("s", oldCfg))
	require.NoError(t, up.ConnectServer(context.Background(), "s"))
	old := up.current["s"].id
	// Drain the reconcile's asynchronous actions: a late ActionConnect from
	// the old snapshot would otherwise re-install the old config after the
	// reconnect below (seen on the Windows runner).
	require.NoError(t, sup.reconcile(svc.Current()))
	sup.actionWg.Wait()

	require.NoError(t, svc.Update(&config.Config{Listen: "127.0.0.1:0", Servers: []*config.ServerConfig{newCfg}}, configsvc.UpdateTypeModify, "test_edit"))
	require.NoError(t, sup.executeAction("s", ActionReconnect, svc.Current()))

	up.mu.Lock()
	defer up.mu.Unlock()
	require.Contains(t, up.disconnected, old, "the old-config instance must be disconnected")
	require.NotEqual(t, old, up.current["s"].id, "the old-config instance must be replaced")
	require.Equal(t, "http://127.0.0.1:1/new", up.current["s"].cfg.URL)
	require.True(t, up.current["s"].connected, "the replacement must connect")
}
