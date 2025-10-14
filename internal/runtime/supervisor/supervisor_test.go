package supervisor

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/runtime/configsvc"
)

// MockUpstreamAdapter is a test double for UpstreamAdapter
type MockUpstreamAdapter struct {
	mu              sync.Mutex
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
	m.mu.Lock()
	defer m.mu.Unlock()
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
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removedServers = append(m.removedServers, name)
	delete(m.states, name)
	return nil
}

func (m *MockUpstreamAdapter) ConnectServer(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected[name] = true
	if state, ok := m.states[name]; ok {
		state.Connected = true
	}
	return nil
}

func (m *MockUpstreamAdapter) DisconnectServer(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.disconnected = append(m.disconnected, name)
	if state, ok := m.states[name]; ok {
		state.Connected = false
	}
	return nil
}

func (m *MockUpstreamAdapter) ConnectAll(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name := range m.states {
		m.connected[name] = true
	}
	return nil
}

func (m *MockUpstreamAdapter) GetServerState(name string) (*ServerState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if state, ok := m.states[name]; ok {
		return state, nil
	}
	return nil, nil
}

func (m *MockUpstreamAdapter) GetAllStates() map[string]*ServerState {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Return a copy to prevent data races
	statesCopy := make(map[string]*ServerState, len(m.states))
	for k, v := range m.states {
		statesCopy[k] = v
	}
	return statesCopy
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

	// Wait a bit for goroutines to complete
	time.Sleep(50 * time.Millisecond)

	// Verify server was added (with lock)
	mockUpstream.mu.Lock()
	_, addedOk := mockUpstream.addedServers["test-server"]
	connectedOk := mockUpstream.connected["test-server"]
	mockUpstream.mu.Unlock()

	if !addedOk {
		t.Error("Expected server to be added")
	}

	// Verify server was connected
	if !connectedOk {
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

	// Wait for first reconciliation to complete
	time.Sleep(50 * time.Millisecond)

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

	// Wait for goroutines to complete
	time.Sleep(50 * time.Millisecond)

	// Verify server was removed (with lock)
	mockUpstream.mu.Lock()
	removedServers := make([]string, len(mockUpstream.removedServers))
	copy(removedServers, mockUpstream.removedServers)
	mockUpstream.mu.Unlock()

	found := false
	for _, name := range removedServers {
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

	// Wait for first reconciliation to complete
	time.Sleep(50 * time.Millisecond)

	// Mark as connected in mock (with lock)
	mockUpstream.mu.Lock()
	if state, ok := mockUpstream.states["test-server"]; ok {
		state.Connected = true
	}
	mockUpstream.mu.Unlock()

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

	// Wait for goroutines to complete
	time.Sleep(50 * time.Millisecond)

	// Verify server was disconnected (with lock)
	mockUpstream.mu.Lock()
	disconnected := make([]string, len(mockUpstream.disconnected))
	copy(disconnected, mockUpstream.disconnected)
	mockUpstream.mu.Unlock()

	found := false
	for _, name := range disconnected {
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
