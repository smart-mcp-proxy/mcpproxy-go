package supervisor

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/runtime/configsvc"
)

// MockUpstreamAdapter is a test double for UpstreamAdapter
type MockUpstreamAdapter struct {
	addedServers    map[string]*config.ServerConfig
	removedServers  []string
	connected       map[string]bool
	disconnected    []string
	eventCh         chan Event
	states          map[string]*ServerState
}

func NewMockUpstreamAdapter() *MockUpstreamAdapter {
	return &MockUpstreamAdapter{
		addedServers:   make(map[string]*config.ServerConfig),
		removedServers: make([]string, 0),
		connected:      make(map[string]bool),
		disconnected:   make([]string, 0),
		eventCh:        make(chan Event, 100),
		states:         make(map[string]*ServerState),
	}
}

func (m *MockUpstreamAdapter) AddServer(name string, cfg *config.ServerConfig) error {
	m.addedServers[name] = cfg
	m.states[name] = &ServerState{
		Name:      name,
		Config:    cfg,
		Enabled:   cfg.Enabled,
		Connected: false,
	}
	return nil
}

func (m *MockUpstreamAdapter) RemoveServer(name string) error {
	m.removedServers = append(m.removedServers, name)
	delete(m.states, name)
	return nil
}

func (m *MockUpstreamAdapter) ConnectServer(ctx context.Context, name string) error {
	m.connected[name] = true
	if state, ok := m.states[name]; ok {
		state.Connected = true
	}
	return nil
}

func (m *MockUpstreamAdapter) DisconnectServer(name string) error {
	m.disconnected = append(m.disconnected, name)
	if state, ok := m.states[name]; ok {
		state.Connected = false
	}
	return nil
}

func (m *MockUpstreamAdapter) ConnectAll(ctx context.Context) error {
	for name := range m.states {
		m.connected[name] = true
	}
	return nil
}

func (m *MockUpstreamAdapter) GetServerState(name string) (*ServerState, error) {
	if state, ok := m.states[name]; ok {
		return state, nil
	}
	return nil, nil
}

func (m *MockUpstreamAdapter) GetAllStates() map[string]*ServerState {
	return m.states
}

func (m *MockUpstreamAdapter) Subscribe() <-chan Event {
	return m.eventCh
}

func (m *MockUpstreamAdapter) Unsubscribe(ch <-chan Event) {
	close(m.eventCh)
}

func (m *MockUpstreamAdapter) Close() {
	close(m.eventCh)
}

func TestSupervisor_New(t *testing.T) {
	cfg := &config.Config{
		Listen:  "127.0.0.1:8080",
		Servers: []*config.ServerConfig{},
	}

	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	defer configSvc.Close()

	mockUpstream := NewMockUpstreamAdapter()
	defer mockUpstream.Close()

	supervisor := New(configSvc, mockUpstream, zap.NewNop())
	if supervisor == nil {
		t.Fatal("Expected non-nil supervisor")
	}

	snapshot := supervisor.CurrentSnapshot()
	if snapshot == nil {
		t.Fatal("Expected non-nil snapshot")
	}

	if snapshot.Version != 0 {
		t.Errorf("Expected version 0, got %d", snapshot.Version)
	}
}

func TestSupervisor_Reconcile_AddServer(t *testing.T) {
	cfg := &config.Config{
		Listen: "127.0.0.1:8080",
		Servers: []*config.ServerConfig{
			{Name: "test-server", Enabled: true, Quarantined: false},
		},
	}

	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	defer configSvc.Close()

	mockUpstream := NewMockUpstreamAdapter()
	defer mockUpstream.Close()

	supervisor := New(configSvc, mockUpstream, zap.NewNop())

	// Trigger reconciliation
	configSnapshot := configSvc.Current()
	err := supervisor.reconcile(configSnapshot)
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// Verify server was added
	if _, ok := mockUpstream.addedServers["test-server"]; !ok {
		t.Error("Expected server to be added")
	}

	// Verify server was connected
	if !mockUpstream.connected["test-server"] {
		t.Error("Expected server to be connected")
	}
}

func TestSupervisor_Reconcile_RemoveServer(t *testing.T) {
	cfg := &config.Config{
		Listen: "127.0.0.1:8080",
		Servers: []*config.ServerConfig{
			{Name: "test-server", Enabled: true},
		},
	}

	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	defer configSvc.Close()

	mockUpstream := NewMockUpstreamAdapter()
	defer mockUpstream.Close()

	supervisor := New(configSvc, mockUpstream, zap.NewNop())

	// First reconciliation - add server
	configSnapshot := configSvc.Current()
	_ = supervisor.reconcile(configSnapshot)

	// Update config to remove server
	newCfg := &config.Config{
		Listen:  "127.0.0.1:8080",
		Servers: []*config.ServerConfig{}, // No servers
	}

	_ = configSvc.Update(newCfg, configsvc.UpdateTypeModify, "test")

	// Second reconciliation - remove server
	newSnapshot := configSvc.Current()
	err := supervisor.reconcile(newSnapshot)
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// Verify server was removed
	found := false
	for _, name := range mockUpstream.removedServers {
		if name == "test-server" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Expected server to be removed")
	}
}

func TestSupervisor_Reconcile_DisableServer(t *testing.T) {
	cfg := &config.Config{
		Listen: "127.0.0.1:8080",
		Servers: []*config.ServerConfig{
			{Name: "test-server", Enabled: true},
		},
	}

	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	defer configSvc.Close()

	mockUpstream := NewMockUpstreamAdapter()
	defer mockUpstream.Close()

	supervisor := New(configSvc, mockUpstream, zap.NewNop())

	// First reconciliation - add and connect
	_ = supervisor.reconcile(configSvc.Current())

	// Mark as connected in mock
	if state, ok := mockUpstream.states["test-server"]; ok {
		state.Connected = true
	}

	// Update config to disable server
	newCfg := &config.Config{
		Listen: "127.0.0.1:8080",
		Servers: []*config.ServerConfig{
			{Name: "test-server", Enabled: false}, // Disabled
		},
	}

	_ = configSvc.Update(newCfg, configsvc.UpdateTypeModify, "test")

	// Second reconciliation - should disconnect
	err := supervisor.reconcile(configSvc.Current())
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// Verify server was disconnected
	found := false
	for _, name := range mockUpstream.disconnected {
		if name == "test-server" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Expected server to be disconnected")
	}
}

func TestSupervisor_CurrentSnapshot(t *testing.T) {
	cfg := &config.Config{
		Listen: "127.0.0.1:8080",
		Servers: []*config.ServerConfig{
			{Name: "server1", Enabled: true},
			{Name: "server2", Enabled: false},
		},
	}

	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	defer configSvc.Close()

	mockUpstream := NewMockUpstreamAdapter()
	defer mockUpstream.Close()

	supervisor := New(configSvc, mockUpstream, zap.NewNop())

	// Reconcile to populate snapshot
	_ = supervisor.reconcile(configSvc.Current())

	snapshot := supervisor.CurrentSnapshot()
	if snapshot == nil {
		t.Fatal("Expected non-nil snapshot")
	}

	if len(snapshot.Servers) != 2 {
		t.Errorf("Expected 2 servers, got %d", len(snapshot.Servers))
	}

	// Verify server states
	if state, ok := snapshot.Servers["server1"]; ok {
		if !state.Enabled {
			t.Error("Expected server1 to be enabled")
		}
	} else {
		t.Error("Expected server1 in snapshot")
	}

	if state, ok := snapshot.Servers["server2"]; ok {
		if state.Enabled {
			t.Error("Expected server2 to be disabled")
		}
	} else {
		t.Error("Expected server2 in snapshot")
	}
}

func TestSupervisor_SnapshotClone(t *testing.T) {
	original := &ServerStateSnapshot{
		Servers: map[string]*ServerState{
			"test": {
				Name:    "test",
				Enabled: true,
				Config:  &config.ServerConfig{Name: "test", Enabled: true},
			},
		},
		Timestamp: time.Now(),
		Version:   1,
	}

	cloned := original.Clone()

	// Verify deep copy
	if cloned == original {
		t.Error("Clone returned same pointer")
	}

	// Modify original
	original.Servers["test"].Enabled = false
	original.Servers["test"].Config.Enabled = false

	// Cloned should be unchanged
	if !cloned.Servers["test"].Enabled {
		t.Error("Clone was mutated")
	}

	if !cloned.Servers["test"].Config.Enabled {
		t.Error("Clone config was mutated")
	}
}

func TestSupervisor_Subscribe(t *testing.T) {
	cfg := &config.Config{
		Listen:  "127.0.0.1:8080",
		Servers: []*config.ServerConfig{},
	}

	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	defer configSvc.Close()

	mockUpstream := NewMockUpstreamAdapter()
	defer mockUpstream.Close()

	supervisor := New(configSvc, mockUpstream, zap.NewNop())

	eventCh := supervisor.Subscribe()

	// Emit an event
	supervisor.emitEvent(Event{
		Type:       EventReconciliationComplete,
		Timestamp:  time.Now(),
		ServerName: "",
		Payload:    map[string]interface{}{"version": int64(1)},
	})

	// Should receive event
	select {
	case event := <-eventCh:
		if event.Type != EventReconciliationComplete {
			t.Errorf("Expected EventReconciliationComplete, got %s", event.Type)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Did not receive event")
	}

	supervisor.Unsubscribe(eventCh)
}
