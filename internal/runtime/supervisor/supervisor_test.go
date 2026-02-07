package supervisor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/configsvc"
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
	// Return deep copies to prevent data races with concurrent goroutines
	statesCopy := make(map[string]*ServerState, len(m.states))
	for k, v := range m.states {
		stateCopy := *v // Deep copy the struct
		statesCopy[k] = &stateCopy
	}
	return statesCopy
}

func (m *MockUpstreamAdapter) IsUserLoggedOut(name string) bool {
	// Mock always returns false - tests can override behavior if needed
	return false
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

// SetServerTools sets tools for a specific server (for testing)
func (m *MockUpstreamAdapter) SetServerTools(name string, tools []*config.ToolMetadata) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if state, ok := m.states[name]; ok {
		state.Tools = tools
		state.ToolCount = len(tools)
	}
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
	require.NotNil(t, snapshot, "Expected non-nil snapshot")

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
	require.NotNil(t, snapshot, "Expected non-nil snapshot")

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

func TestSupervisor_RefreshToolsFromDiscovery(t *testing.T) {
	cfg := &config.Config{
		Listen: "127.0.0.1:8080",
		Servers: []*config.ServerConfig{
			{Name: "server1", Enabled: true},
			{Name: "server2", Enabled: true},
		},
	}

	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	defer configSvc.Close()

	mockUpstream := NewMockUpstreamAdapter()
	defer mockUpstream.Close()

	supervisor := New(configSvc, mockUpstream, zap.NewNop())

	// Reconcile to populate initial state
	_ = supervisor.reconcile(configSvc.Current())
	time.Sleep(50 * time.Millisecond)

	// Create discovered tools
	tools := []*config.ToolMetadata{
		{
			Name:        "tool1",
			ServerName:  "server1",
			Description: "Test tool 1",
			ParamsJSON:  `{"type":"object","properties":{"arg1":{"type":"string"}}}`,
		},
		{
			Name:        "tool2",
			ServerName:  "server1",
			Description: "Test tool 2",
			ParamsJSON:  `{"type":"object","properties":{"arg2":{"type":"number"}}}`,
		},
		{
			Name:        "tool3",
			ServerName:  "server2",
			Description: "Test tool 3",
			ParamsJSON:  `{"type":"object","properties":{"arg3":{"type":"boolean"}}}`,
		},
	}

	// Refresh tools from discovery
	err := supervisor.RefreshToolsFromDiscovery(tools)
	if err != nil {
		t.Fatalf("RefreshToolsFromDiscovery failed: %v", err)
	}

	// Verify StateView was updated
	snapshot := supervisor.StateView().Snapshot()

	// Check server1 has 2 tools
	if server1, ok := snapshot.Servers["server1"]; ok {
		if server1.ToolCount != 2 {
			t.Errorf("Expected server1 to have 2 tools, got %d", server1.ToolCount)
		}
		if len(server1.Tools) != 2 {
			t.Errorf("Expected server1 Tools array to have 2 items, got %d", len(server1.Tools))
		}
		if server1.Tools[0].Name != "tool1" {
			t.Errorf("Expected first tool to be 'tool1', got '%s'", server1.Tools[0].Name)
		}
		if server1.Tools[0].Description != "Test tool 1" {
			t.Errorf("Expected first tool description to be 'Test tool 1', got '%s'", server1.Tools[0].Description)
		}
	} else {
		t.Error("Expected server1 in StateView snapshot")
	}

	// Check server2 has 1 tool
	if server2, ok := snapshot.Servers["server2"]; ok {
		if server2.ToolCount != 1 {
			t.Errorf("Expected server2 to have 1 tool, got %d", server2.ToolCount)
		}
		if len(server2.Tools) != 1 {
			t.Errorf("Expected server2 Tools array to have 1 item, got %d", len(server2.Tools))
		}
		if server2.Tools[0].Name != "tool3" {
			t.Errorf("Expected tool to be 'tool3', got '%s'", server2.Tools[0].Name)
		}
	} else {
		t.Error("Expected server2 in StateView snapshot")
	}
}

func TestSupervisor_RefreshToolsFromDiscovery_EmptyTools(t *testing.T) {
	cfg := &config.Config{
		Listen:  "127.0.0.1:8080",
		Servers: []*config.ServerConfig{},
	}

	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	defer configSvc.Close()

	mockUpstream := NewMockUpstreamAdapter()
	defer mockUpstream.Close()

	supervisor := New(configSvc, mockUpstream, zap.NewNop())

	// Test with nil tools
	err := supervisor.RefreshToolsFromDiscovery(nil)
	if err != nil {
		t.Errorf("Expected no error with nil tools, got %v", err)
	}

	// Test with empty tools slice
	err = supervisor.RefreshToolsFromDiscovery([]*config.ToolMetadata{})
	if err != nil {
		t.Errorf("Expected no error with empty tools, got %v", err)
	}
}

func TestSupervisor_InspectionExemption_GrantAndRevoke(t *testing.T) {
	cfg := &config.Config{
		Listen:  "127.0.0.1:8080",
		Servers: []*config.ServerConfig{},
	}

	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	defer configSvc.Close()

	mockUpstream := NewMockUpstreamAdapter()
	defer mockUpstream.Close()

	supervisor := New(configSvc, mockUpstream, zap.NewNop())

	// Test: Server starts with no exemptions
	if supervisor.IsInspectionExempted("test-server") {
		t.Error("Expected no exemption initially")
	}

	// Test: Grant exemption
	err := supervisor.RequestInspectionExemption("test-server", 5*time.Second)
	if err != nil {
		t.Fatalf("RequestInspectionExemption failed: %v", err)
	}

	// Test: Exemption is active
	if !supervisor.IsInspectionExempted("test-server") {
		t.Error("Expected exemption to be active after request")
	}

	// Test: Revoke exemption
	supervisor.RevokeInspectionExemption("test-server")

	// Test: Exemption is revoked
	if supervisor.IsInspectionExempted("test-server") {
		t.Error("Expected exemption to be revoked")
	}
}

func TestSupervisor_InspectionExemption_AutoExpiry(t *testing.T) {
	cfg := &config.Config{
		Listen:  "127.0.0.1:8080",
		Servers: []*config.ServerConfig{},
	}

	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	defer configSvc.Close()

	mockUpstream := NewMockUpstreamAdapter()
	defer mockUpstream.Close()

	supervisor := New(configSvc, mockUpstream, zap.NewNop())

	// Grant short-lived exemption (100ms)
	err := supervisor.RequestInspectionExemption("test-server", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("RequestInspectionExemption failed: %v", err)
	}

	// Exemption should be active immediately
	if !supervisor.IsInspectionExempted("test-server") {
		t.Error("Expected exemption to be active")
	}

	// Wait for expiry
	time.Sleep(150 * time.Millisecond)

	// Exemption should be expired now
	if supervisor.IsInspectionExempted("test-server") {
		t.Error("Expected exemption to be expired")
	}
}

func TestSupervisor_InspectionExemption_QuarantinedServerConnects(t *testing.T) {
	cfg := &config.Config{
		Listen: "127.0.0.1:8080",
		Servers: []*config.ServerConfig{
			{Name: "quarantined-server", Enabled: true, Quarantined: true},
		},
	}

	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	defer configSvc.Close()

	mockUpstream := NewMockUpstreamAdapter()
	defer mockUpstream.Close()

	supervisor := New(configSvc, mockUpstream, zap.NewNop())

	// Initial reconciliation - quarantined server should NOT connect (no exemption)
	_ = supervisor.reconcile(configSvc.Current())
	time.Sleep(50 * time.Millisecond)

	mockUpstream.mu.Lock()
	connected := mockUpstream.connected["quarantined-server"]
	mockUpstream.mu.Unlock()

	if connected {
		t.Error("Expected quarantined server NOT to connect initially without exemption")
	}

	// Grant exemption
	err := supervisor.RequestInspectionExemption("quarantined-server", 5*time.Second)
	if err != nil {
		t.Fatalf("RequestInspectionExemption failed: %v", err)
	}

	// Wait for reconciliation triggered by RequestInspectionExemption to complete
	time.Sleep(100 * time.Millisecond)

	// Now quarantined server SHOULD be connected due to exemption
	mockUpstream.mu.Lock()
	connected = mockUpstream.connected["quarantined-server"]
	mockUpstream.mu.Unlock()

	if !connected {
		t.Error("Expected quarantined server to connect with active exemption")
	}

	// Revoke exemption
	supervisor.RevokeInspectionExemption("quarantined-server")

	// Exemption should no longer be active
	if supervisor.IsInspectionExempted("quarantined-server") {
		t.Error("Expected exemption to be revoked")
	}

	// Note: In a unit test, we can verify the exemption logic works correctly.
	// Full disconnection behavior is best tested in integration tests where
	// the supervisor's event loop and state synchronization are fully active.
}

func TestSupervisor_InspectionExemption_MultipleServers(t *testing.T) {
	cfg := &config.Config{
		Listen:  "127.0.0.1:8080",
		Servers: []*config.ServerConfig{},
	}

	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	defer configSvc.Close()

	mockUpstream := NewMockUpstreamAdapter()
	defer mockUpstream.Close()

	supervisor := New(configSvc, mockUpstream, zap.NewNop())

	// Grant exemptions for multiple servers
	_ = supervisor.RequestInspectionExemption("server1", 5*time.Second)
	_ = supervisor.RequestInspectionExemption("server2", 5*time.Second)
	_ = supervisor.RequestInspectionExemption("server3", 5*time.Second)

	// All should be exempted
	if !supervisor.IsInspectionExempted("server1") {
		t.Error("Expected server1 to be exempted")
	}
	if !supervisor.IsInspectionExempted("server2") {
		t.Error("Expected server2 to be exempted")
	}
	if !supervisor.IsInspectionExempted("server3") {
		t.Error("Expected server3 to be exempted")
	}

	// Revoke one exemption
	supervisor.RevokeInspectionExemption("server2")

	// server2 should be revoked, others still active
	if !supervisor.IsInspectionExempted("server1") {
		t.Error("Expected server1 to still be exempted")
	}
	if supervisor.IsInspectionExempted("server2") {
		t.Error("Expected server2 exemption to be revoked")
	}
	if !supervisor.IsInspectionExempted("server3") {
		t.Error("Expected server3 to still be exempted")
	}
}
