package supervisor

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/configsvc"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// MockUpstreamAdapter is a test double for UpstreamAdapter
type MockUpstreamAdapter struct {
	mu             sync.Mutex
	addedServers   map[string]*config.ServerConfig
	removedServers []string
	connected      map[string]bool
	disconnected   []string
	eventCh        chan Event
	states         map[string]*ServerState
}

// setConnected mirrors managed.Client's connection token
// (connectionEpoch): it moves on every connection EDGE — a Connect that
// establishes a connection, a Disconnect that drops one — and not on a
// redundant call (a real Connect on a connected client returns early), so a
// test can settle the adapter with repeated ConnectServer calls and still
// model a reconnect the events never reported (astra r2 C3). Caller holds mu.
func (m *MockUpstreamAdapter) setConnected(name string, connected bool) {
	state, ok := m.states[name]
	if !ok {
		return
	}
	if state.Connected != connected {
		state.ConnectionEpoch++
	}
	state.Connected = connected
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
	m.setConnected(name, true)
	return nil
}

func (m *MockUpstreamAdapter) DisconnectServer(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.disconnected = append(m.disconnected, name)
	m.setConnected(name, false)
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
		stateCopy := *state
		return &stateCopy, nil
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
	_, err := supervisor.RefreshToolsFromDiscovery(tools, supervisor.DiscoveryGenerations())
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
	_, err := supervisor.RefreshToolsFromDiscovery(nil, supervisor.DiscoveryGenerations())
	if err != nil {
		t.Errorf("Expected no error with nil tools, got %v", err)
	}

	// Test with empty tools slice
	_, err = supervisor.RefreshToolsFromDiscovery([]*config.ToolMetadata{}, supervisor.DiscoveryGenerations())
	if err != nil {
		t.Errorf("Expected no error with empty tools, got %v", err)
	}
}

// TestSupervisor_ReconnectRepopulatesStateViewTools verifies that after a
// server disconnects (which clears the StateView per-server tool set) and then
// reconnects, the StateView tool set is repopulated from the retained
// Supervisor snapshot instead of being left empty until background discovery
// re-runs. This is the root-cause regression behind MCP-2083 (tracked as
// MCP-2094): StateView consumers that don't route through the #635 read
// fallback (tray counts, SSE servers.changed, health/diagnostics) would
// otherwise report 0 tools for a connected server that has tools.
func TestSupervisor_ReconnectRepopulatesStateViewTools(t *testing.T) {
	cfg := &config.Config{
		Listen: "127.0.0.1:8080",
		Servers: []*config.ServerConfig{
			{Name: "server1", Enabled: true},
		},
	}

	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	defer configSvc.Close()

	mockUpstream := NewMockUpstreamAdapter()
	defer mockUpstream.Close()
	_ = mockUpstream.AddServer("server1", cfg.Servers[0])

	sup := New(configSvc, mockUpstream, zap.NewNop())

	// Populate initial state + tools (simulating a connected server whose
	// background discovery has completed).
	_ = sup.reconcile(configSvc.Current())
	tools := []*config.ToolMetadata{
		{Name: "tool1", ServerName: "server1", Description: "Test tool 1"},
		{Name: "tool2", ServerName: "server1", Description: "Test tool 2"},
	}
	if _, err := sup.RefreshToolsFromDiscovery(tools, sup.DiscoveryGenerations()); err != nil {
		t.Fatalf("RefreshToolsFromDiscovery failed: %v", err)
	}
	mockUpstream.SetServerTools("server1", tools)

	// Sanity: StateView shows the 2 tools.
	if got := len(sup.StateView().Snapshot().Servers["server1"].Tools); got != 2 {
		t.Fatalf("setup: expected 2 tools in StateView, got %d", got)
	}

	// Disconnect: StateView deliberately clears the per-server tool set.
	sup.updateSnapshotFromEvent(Event{
		Type:       EventServerDisconnected,
		ServerName: "server1",
		Timestamp:  time.Now(),
		Payload:    map[string]interface{}{"connected": false},
	})
	if got := len(sup.StateView().Snapshot().Servers["server1"].Tools); got != 0 {
		t.Fatalf("after disconnect: expected StateView tools cleared, got %d", got)
	}

	// Reconnect: StateView must be repopulated from the retained Supervisor
	// snapshot immediately, without waiting for background discovery to re-run.
	sup.updateSnapshotFromEvent(Event{
		Type:       EventServerConnected,
		ServerName: "server1",
		Timestamp:  time.Now(),
		Payload:    map[string]interface{}{"connected": true},
	})

	status := sup.StateView().Snapshot().Servers["server1"]
	if status.ToolCount != 2 {
		t.Errorf("after reconnect: expected ToolCount 2, got %d", status.ToolCount)
	}
	if len(status.Tools) != 2 {
		t.Errorf("after reconnect: expected StateView repopulated with 2 tools, got %d", len(status.Tools))
	}
	if len(status.Tools) == 2 && status.Tools[0].Name != "tool1" {
		t.Errorf("after reconnect: expected first tool 'tool1', got %q", status.Tools[0].Name)
	}
}

// TestSupervisor_RefreshToolsFromDiscovery_ShrinkingToolSet verifies that a
// later discovery reporting fewer (but non-empty) tools updates StateView
// rather than being silently skipped. The old size-based guard pinned
// StateView to a stale higher count, diverging from the Supervisor snapshot
// (which is updated unconditionally). Part of MCP-2094.
func TestSupervisor_RefreshToolsFromDiscovery_ShrinkingToolSet(t *testing.T) {
	cfg := &config.Config{
		Listen: "127.0.0.1:8080",
		Servers: []*config.ServerConfig{
			{Name: "server1", Enabled: true},
		},
	}

	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	defer configSvc.Close()

	mockUpstream := NewMockUpstreamAdapter()
	defer mockUpstream.Close()

	sup := New(configSvc, mockUpstream, zap.NewNop())
	_ = sup.reconcile(configSvc.Current())

	threeTools := []*config.ToolMetadata{
		{Name: "tool1", ServerName: "server1"},
		{Name: "tool2", ServerName: "server1"},
		{Name: "tool3", ServerName: "server1"},
	}
	if _, err := sup.RefreshToolsFromDiscovery(threeTools, sup.DiscoveryGenerations()); err != nil {
		t.Fatalf("RefreshToolsFromDiscovery (3 tools) failed: %v", err)
	}

	// Upstream now legitimately exposes only one tool.
	oneTool := []*config.ToolMetadata{{Name: "tool1", ServerName: "server1"}}
	if _, err := sup.RefreshToolsFromDiscovery(oneTool, sup.DiscoveryGenerations()); err != nil {
		t.Fatalf("RefreshToolsFromDiscovery (1 tool) failed: %v", err)
	}

	status := sup.StateView().Snapshot().Servers["server1"]
	if status.ToolCount != 1 {
		t.Errorf("expected ToolCount 1 after shrink, got %d", status.ToolCount)
	}
	if len(status.Tools) != 1 {
		t.Errorf("expected StateView to reflect 1 tool after shrink, got %d", len(status.Tools))
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

// TestSupervisor_ErrorCodeNotifierSynchronous asserts Spec 080 FR-012: the
// error-code notifier fires synchronously at the classification site, so the
// pre-churn last_error_code write completes before updateStateView returns —
// a crash immediately after classification cannot lose the final pre-crash
// code. The unsynchronized `got` variable is deliberate: if the notifier were
// still dispatched on a goroutine, this assertion would flake and `go test
// -race` would flag the write.
func TestSupervisor_ErrorCodeNotifierSynchronous(t *testing.T) {
	cfg := &config.Config{
		Listen:  "127.0.0.1:8080",
		Servers: []*config.ServerConfig{},
	}
	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	defer configSvc.Close()

	mockUpstream := NewMockUpstreamAdapter()
	defer mockUpstream.Close()

	sup := New(configSvc, mockUpstream, zap.NewNop())

	var got string
	sup.SetErrorCodeNotifier(func(code string) { got = code })

	sup.updateStateView("srv", &ServerState{
		Name:    "srv",
		Config:  &config.ServerConfig{Name: "srv", URL: "http://127.0.0.1:1/mcp", Enabled: true},
		Enabled: true,
		ConnectionInfo: &types.ConnectionInfo{
			State:     types.StateError,
			LastError: errors.New("dial tcp 127.0.0.1:1: connect: connection refused"),
		},
	})

	if got == "" {
		t.Fatal("notifier did not fire synchronously during updateStateView")
	}
	if !strings.HasPrefix(got, "MCPX_") {
		t.Fatalf("notifier received a non-MCPX code: %q", got)
	}
}

// barrierUpstreamAdapter wraps MockUpstreamAdapter and counts any upstream
// call that happens after the barrier is armed. Used to prove Stop() is a
// write barrier for the delayed initial-reconciliation goroutine (Spec 080).
type barrierUpstreamAdapter struct {
	*MockUpstreamAdapter
	barrier    atomic.Bool
	violations atomic.Int32
}

func (b *barrierUpstreamAdapter) check() {
	if b.barrier.Load() {
		b.violations.Add(1)
	}
}

func (b *barrierUpstreamAdapter) AddServer(name string, cfg *config.ServerConfig) error {
	b.check()
	return b.MockUpstreamAdapter.AddServer(name, cfg)
}

func (b *barrierUpstreamAdapter) RemoveServer(name string) error {
	b.check()
	return b.MockUpstreamAdapter.RemoveServer(name)
}

func (b *barrierUpstreamAdapter) ConnectServer(ctx context.Context, name string) error {
	b.check()
	return b.MockUpstreamAdapter.ConnectServer(ctx, name)
}

func (b *barrierUpstreamAdapter) DisconnectServer(name string) error {
	b.check()
	return b.MockUpstreamAdapter.DisconnectServer(name)
}

func (b *barrierUpstreamAdapter) ConnectAll(ctx context.Context) error {
	b.check()
	return b.MockUpstreamAdapter.ConnectAll(ctx)
}

func (b *barrierUpstreamAdapter) GetServerState(name string) (*ServerState, error) {
	b.check()
	return b.MockUpstreamAdapter.GetServerState(name)
}

func (b *barrierUpstreamAdapter) GetAllStates() map[string]*ServerState {
	b.check()
	return b.MockUpstreamAdapter.GetAllStates()
}

func (b *barrierUpstreamAdapter) IsUserLoggedOut(name string) bool {
	b.check()
	return b.MockUpstreamAdapter.IsUserLoggedOut(name)
}

// TestSupervisor_StopBeforeInitialReconcileIsBarrier (Spec 080 FR-010/FR-011,
// review round 5): Start() arms a delayed (500ms) initial reconciliation.
// Stop() must be a barrier for it — the goroutine is registered in s.wg and
// waits on a ctx-aware timer, so Stop() (cancel + wg.Wait) deterministically
// either cancels it inside the window or waits for the reconcile to finish.
// Before the fix it was a bare `go func() { time.Sleep(500ms); reconcile() }`:
// Stop() returned with nothing to wait for, Runtime.Close resolved the clean-
// shutdown marker, and the goroutine then woke and wrote diagnostics/state
// (reconcile -> updateStateView -> notifyErrorCode) after the marker — or
// against a closed DB.
func TestSupervisor_StopBeforeInitialReconcileIsBarrier(t *testing.T) {
	cfg := &config.Config{
		Listen: "127.0.0.1:8080",
		Servers: []*config.ServerConfig{
			{Name: "late-server", Enabled: true},
		},
	}

	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	defer configSvc.Close()

	upstream := &barrierUpstreamAdapter{MockUpstreamAdapter: NewMockUpstreamAdapter()}
	sup := New(configSvc, upstream, zap.NewNop())

	var stopReturned atomic.Bool
	var notifierAfterStop atomic.Int32
	sup.SetErrorCodeNotifier(func(string) {
		if stopReturned.Load() {
			notifierAfterStop.Add(1)
		}
	})

	sup.Start()
	sup.Stop() // well inside the 500ms initial-reconcile delay

	// Everything the supervisor owns (reconciliation loop, event forwarding,
	// exemption cleanup, the initial-reconcile goroutine, in-flight actions)
	// is joined by Stop() via s.wg / drainActions, so from this point on no
	// upstream call and no notifier call may ever happen again.
	upstream.barrier.Store(true)
	stopReturned.Store(true)

	// Regression net: the pre-fix bare goroutine would wake ~500ms after
	// Start() and run reconcile() against the stopped supervisor. Wait out the
	// full window plus slack, then assert the barrier held. With the fix this
	// sleep is pure idle time — the wg-joined goroutine already exited before
	// Stop() returned, via the s.ctx.Done() arm of its select.
	time.Sleep(700 * time.Millisecond)

	require.Zero(t, upstream.violations.Load(),
		"upstream adapter was called after Supervisor.Stop() returned")
	require.Zero(t, notifierAfterStop.Load(),
		"error-code notifier fired after Supervisor.Stop() returned")
}

// TestToolInfosFromMetadata_ParsesParamsJSON verifies that the cached upstream
// schema (ToolMetadata.ParamsJSON) is actually parsed into StateView's
// InputSchema. The REST/CLI tool listings serve StateView verbatim, so a
// fabricated placeholder here surfaces to users as an empty
// {"type":"object","properties":{}} schema for every tool.
func TestToolInfosFromMetadata_ParsesParamsJSON(t *testing.T) {
	tools := []*config.ToolMetadata{
		{
			Name:        "read_file",
			ServerName:  "fs",
			Description: "Read a file",
			ParamsJSON:  `{"type":"object","properties":{"path":{"type":"string","description":"file path"}},"required":["path"]}`,
		},
	}

	infos := toolInfosFromMetadata(tools)
	require.Len(t, infos, 1)

	schema := infos[0].InputSchema
	require.NotNil(t, schema, "InputSchema must be populated from ParamsJSON")
	require.Equal(t, "object", schema["type"])

	props, ok := schema["properties"].(map[string]interface{})
	require.True(t, ok, "properties must be an object, got %#v", schema["properties"])
	require.Contains(t, props, "path", "parsed schema must carry the upstream properties")

	pathProp, ok := props["path"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "string", pathProp["type"])
	require.Equal(t, "file path", pathProp["description"])

	required, ok := schema["required"].([]interface{})
	require.True(t, ok, "required must round-trip, got %#v", schema["required"])
	require.Equal(t, []interface{}{"path"}, required)
}

// TestToolInfosFromMetadata_EmptyAndMalformed asserts we never fabricate a
// placeholder schema: an absent or unparsable ParamsJSON leaves InputSchema
// nil so the field is omitted downstream rather than reported as an empty
// object schema.
func TestToolInfosFromMetadata_EmptyAndMalformed(t *testing.T) {
	tools := []*config.ToolMetadata{
		{Name: "no_schema", ServerName: "srv", ParamsJSON: ""},
		{Name: "broken_schema", ServerName: "srv", ParamsJSON: `{not json`},
		{Name: "non_object_schema", ServerName: "srv", ParamsJSON: `"just a string"`},
	}

	infos := toolInfosFromMetadata(tools)
	require.Len(t, infos, 3)

	for _, info := range infos {
		require.Nil(t, info.InputSchema, "tool %q should have no InputSchema", info.Name)
	}
}

// TestSupervisor_RefreshToolsFromDiscovery_StateViewCarriesSchema covers the
// end-to-end StateView population path that the REST tool listings read: after
// discovery, the per-server StateView entry must carry the real upstream
// schema, not a placeholder.
func TestSupervisor_RefreshToolsFromDiscovery_StateViewCarriesSchema(t *testing.T) {
	cfg := &config.Config{
		Listen: "127.0.0.1:8080",
		Servers: []*config.ServerConfig{
			{Name: "server1", Enabled: true},
		},
	}

	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	defer configSvc.Close()

	mockUpstream := NewMockUpstreamAdapter()
	defer mockUpstream.Close()

	supervisor := New(configSvc, mockUpstream, zap.NewNop())

	_ = supervisor.reconcile(configSvc.Current())
	time.Sleep(50 * time.Millisecond)

	tools := []*config.ToolMetadata{
		{
			Name:        "search",
			ServerName:  "server1",
			Description: "Search things",
			ParamsJSON:  `{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer"}},"required":["query"]}`,
		},
	}

	_, err := supervisor.RefreshToolsFromDiscovery(tools, supervisor.DiscoveryGenerations())
	require.NoError(t, err)

	snapshot := supervisor.StateView().Snapshot()
	server1, ok := snapshot.Servers["server1"]
	require.True(t, ok, "expected server1 in StateView snapshot")
	require.Len(t, server1.Tools, 1)

	schema := server1.Tools[0].InputSchema
	require.NotNil(t, schema)
	props, ok := schema["properties"].(map[string]interface{})
	require.True(t, ok, "properties must be an object, got %#v", schema["properties"])
	require.Contains(t, props, "query")
	require.Contains(t, props, "limit")
}

// TestSupervisor_Reconcile_RespectsRetryBackoff verifies that periodic
// reconciliation does not re-dial a failed upstream while the managed client's
// exponential backoff window is open, after it gave up, or while it is parked
// in PendingAuth — but does reconnect once the backoff has elapsed.
func TestSupervisor_Reconcile_RespectsRetryBackoff(t *testing.T) {
	cfg := &config.Config{
		Listen: "127.0.0.1:8080",
		Servers: []*config.ServerConfig{
			{Name: "flaky-server", Enabled: true},
		},
	}

	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	defer configSvc.Close()

	mockUpstream := NewMockUpstreamAdapter()
	defer mockUpstream.Close()

	supervisor := New(configSvc, mockUpstream, zap.NewNop())

	// First reconciliation - server is added and connected
	require.NoError(t, supervisor.reconcile(configSvc.Current()))
	supervisor.actionWg.Wait()

	setConnectionState := func(connected bool, info *types.ConnectionInfo) {
		mockUpstream.mu.Lock()
		defer mockUpstream.mu.Unlock()
		mockUpstream.connected["flaky-server"] = connected
		if state, ok := mockUpstream.states["flaky-server"]; ok {
			state.Connected = connected
			state.ConnectionInfo = info
		}
	}
	isConnected := func() bool {
		mockUpstream.mu.Lock()
		defer mockUpstream.mu.Unlock()
		return mockUpstream.connected["flaky-server"]
	}
	// reconcile dispatches its actions into goroutines tracked by actionWg, so
	// draining that group is an exact barrier: after it returns, either the
	// connect ran or none was planned. A fixed sleep would let the negative
	// assertions below pass before an erroneous redial had a chance to execute.
	reconcileAndDrain := func() {
		t.Helper()
		require.NoError(t, supervisor.reconcile(configSvc.Current()))
		supervisor.actionWg.Wait()
	}

	// Simulate a connection failure with the backoff window still open:
	// reconciliation must NOT re-dial.
	setConnectionState(false, &types.ConnectionInfo{
		State:         types.StateError,
		RetryCount:    5,
		LastRetryTime: time.Now(),
	})
	reconcileAndDrain()
	require.False(t, isConnected(), "supervisor re-dialed a failed server inside its backoff window")

	// A server that gave up after max retries must not be re-dialed either,
	// until the give-up probe interval has elapsed (see below).
	setConnectionState(false, &types.ConnectionInfo{
		State:         types.StateError,
		RetryCount:    types.MaxConnectionRetries,
		GaveUp:        true,
		LastRetryTime: time.Now().Add(-time.Minute),
	})
	reconcileAndDrain()
	require.False(t, isConnected(), "supervisor re-dialed a server that gave up after max retries")

	// An OAuth-classified failure is paced by the OAuth ladder, which bumps
	// OAuthRetryCount and never RetryCount — without that gate it reads as
	// "no failures yet" and is re-dialed on every tick forever (#1013).
	setConnectionState(false, &types.ConnectionInfo{
		State:            types.StateError,
		IsOAuthError:     true,
		OAuthRetryCount:  2,
		LastOAuthAttempt: time.Now().Add(-time.Minute),
	})
	reconcileAndDrain()
	require.False(t, isConnected(), "supervisor re-dialed a server inside its OAuth backoff window")

	// A server parked in PendingAuth (waiting for user OAuth login) must not be
	// re-dialed - each attempt fires real requests at the upstream and cannot
	// succeed until the user completes the login.
	setConnectionState(false, &types.ConnectionInfo{
		State: types.StatePendingAuth,
	})
	reconcileAndDrain()
	require.False(t, isConnected(), "supervisor re-dialed a server pending OAuth login")

	// Once the backoff window has elapsed, reconciliation reconnects as before.
	setConnectionState(false, &types.ConnectionInfo{
		State:         types.StateError,
		RetryCount:    3,
		LastRetryTime: time.Now().Add(-10 * time.Second), // backoff for 3 failures is 4s
	})
	reconcileAndDrain()
	require.True(t, isConnected(), "supervisor did not reconnect after the backoff window elapsed")

	// A given-up server is still probed once per GaveUpProbeInterval, so an
	// outage longer than the retry ladder (sleep, VPN, maintenance) self-heals
	// instead of leaving the upstream silently dead until a human notices.
	setConnectionState(false, &types.ConnectionInfo{
		State:         types.StateError,
		RetryCount:    types.MaxConnectionRetries,
		GaveUp:        true,
		LastRetryTime: time.Now().Add(-types.GaveUpProbeInterval - time.Minute),
	})
	reconcileAndDrain()
	require.True(t, isConnected(), "supervisor never probes a given-up server again")
}

// TestSupervisor_ToolsDiscoveredMarker pins the Spec 105 FR-009 (research D4)
// discovery-completed marker: a server reads ToolsDiscovered=false from
// reconcile until discovery publishes a result; the per-server publication
// stamps it even for an EMPTY result; a disconnect clears it with the tool
// set; a reconnect restores it together with the retained non-empty set (an
// empty retained set restores nothing, so the server stays undiscovered
// until discovery re-runs); and a later reconcile preserves it.
func TestSupervisor_ToolsDiscoveredMarker(t *testing.T) {
	cfg := &config.Config{
		Listen: "127.0.0.1:8080",
		Servers: []*config.ServerConfig{
			{Name: "server1", Enabled: true},
			{Name: "empty", Enabled: true},
		},
	}
	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	defer configSvc.Close()
	mockUpstream := NewMockUpstreamAdapter()
	defer mockUpstream.Close()
	_ = mockUpstream.AddServer("server1", cfg.Servers[0])
	_ = mockUpstream.AddServer("empty", cfg.Servers[1])

	sup := New(configSvc, mockUpstream, zap.NewNop())
	settledReconcile(sup, configSvc)

	status := func(name string) *stateview.ServerStatus {
		t.Helper()
		st, ok := sup.StateView().Snapshot().Servers[name]
		if !ok {
			t.Fatalf("server %q missing from StateView", name)
		}
		return st
	}
	if status("server1").ToolsDiscovered || status("empty").ToolsDiscovered {
		t.Fatal("before any discovery: ToolsDiscovered must be false")
	}

	// The sweep publishes server1's tools; "empty" contributed nothing to
	// the flat list and must stay undiscovered.
	tools := []*config.ToolMetadata{{Name: "tool1", ServerName: "server1", Description: "Test tool 1"}}
	if _, err := sup.RefreshToolsFromDiscovery(tools, sup.DiscoveryGenerations()); err != nil {
		t.Fatalf("RefreshToolsFromDiscovery: %v", err)
	}
	if !status("server1").ToolsDiscovered {
		t.Error("server1: discovery published a tool set, ToolsDiscovered must be true")
	}
	if status("empty").ToolsDiscovered {
		t.Error("empty: absent from the sweep result, must stay undiscovered")
	}

	// The per-server publication of an EMPTY result is authoritative.
	if _, err := sup.RefreshServerToolsFromDiscovery("empty", nil, sup.DiscoveryGeneration("empty")); err != nil {
		t.Fatalf("RefreshServerToolsFromDiscovery: %v", err)
	}
	if st := status("empty"); !st.ToolsDiscovered || len(st.Tools) != 0 || st.ToolCount != 0 {
		t.Errorf("empty: a completed zero-tool discovery must stamp ToolsDiscovered with no tools, got discovered=%v tools=%d count=%d",
			st.ToolsDiscovered, len(st.Tools), st.ToolCount)
	}
	// The per-server variant only publishes tools that belong to the server.
	if _, err := sup.RefreshServerToolsFromDiscovery("empty", tools, sup.DiscoveryGeneration("empty")); err != nil {
		t.Fatalf("RefreshServerToolsFromDiscovery: %v", err)
	}
	if st := status("empty"); len(st.Tools) != 0 {
		t.Errorf("empty: another server's tools must not be published under it, got %d", len(st.Tools))
	}

	// Disconnect clears the marker with the tool set.
	for _, name := range []string{"server1", "empty"} {
		sup.updateSnapshotFromEvent(Event{
			Type: EventServerDisconnected, ServerName: name, Timestamp: time.Now(),
			Payload: map[string]interface{}{"connected": false},
		})
		if st := status(name); st.ToolsDiscovered || len(st.Tools) != 0 {
			t.Errorf("%s after disconnect: marker and tools must be cleared, got discovered=%v tools=%d", name, st.ToolsDiscovered, len(st.Tools))
		}
	}

	// The disconnect clears the marker on the retained Supervisor state too:
	// a reconcile pass (which copies the retained state back into the
	// StateView) must not resurrect "discovery completed" for a connection
	// that has not run a pass — the retained tools survive, the marker does
	// not.
	if snap := sup.snapshot.Load().(*ServerStateSnapshot); snap.Servers["server1"].ToolsDiscovered || len(snap.Servers["server1"].Tools) != 1 {
		t.Errorf("server1 retained state after disconnect: marker must be cleared, tools kept, got discovered=%v tools=%d",
			snap.Servers["server1"].ToolsDiscovered, len(snap.Servers["server1"].Tools))
	}
	settledReconcile(sup, configSvc)
	if st := status("server1"); st.ToolsDiscovered || len(st.Tools) != 1 {
		t.Errorf("server1 after disconnect+reconcile: marker must stay cleared with the tools retained, got discovered=%v tools=%d", st.ToolsDiscovered, len(st.Tools))
	}

	// Reconnect restores the retained set for server1 but NOT the marker —
	// the next connection needs its own discovery pass; the empty server has
	// nothing to restore and stays undiscovered.
	for _, name := range []string{"server1", "empty"} {
		sup.updateSnapshotFromEvent(Event{
			Type: EventServerConnected, ServerName: name, Timestamp: time.Now(),
			Payload: map[string]interface{}{"connected": true},
		})
	}
	if st := status("server1"); st.ToolsDiscovered || len(st.Tools) != 1 {
		t.Errorf("server1 after reconnect: retained set restored without the marker, got discovered=%v tools=%d", st.ToolsDiscovered, len(st.Tools))
	}
	if st := status("empty"); st.ToolsDiscovered {
		t.Error("empty after reconnect: nothing retained, must stay undiscovered until discovery re-runs")
	}

	// astra r1 I1: a zero-tool list on the NEW connection must not certify
	// the retained set — that set is the previous connection's discovery
	// result, and this connection listed none of it. The server stays in the
	// discovery window (unstamped, tools kept for counts) until a non-empty
	// or authoritative pass replaces the set.
	sup.MarkServersToolsDiscovered([]string{"server1"}, sup.DiscoveryGenerations())
	if st := status("server1"); st.ToolsDiscovered || len(st.Tools) != 1 {
		t.Errorf("server1: a zero-tool stamp must not certify a retained set, got discovered=%v tools=%d", st.ToolsDiscovered, len(st.Tools))
	}
	if snap := sup.snapshot.Load().(*ServerStateSnapshot); snap.Servers["server1"].ToolsDiscovered {
		t.Error("server1: the retained Supervisor state must not be stamped over a retained set either")
	}

	// A reconcile pass carries the (cleared) marker over with the retained
	// tools; the connection's own discovery pass re-stamps it. Reconcile is
	// authoritative for Connected (astra r1 I4) and reads it from the
	// adapter, whose ConnectServer the earlier passes dispatched on a
	// goroutine — on a slow runner that goroutine may not have run yet and
	// reconcile would rightly clear the marker as a dropped disconnect. The
	// scenario under test is a server that IS connected, so settle the
	// adapter state deterministically before every reconcile below.
	_ = mockUpstream.ConnectServer(context.Background(), "server1")
	settledReconcile(sup, configSvc)
	if st := status("server1"); st.ToolsDiscovered || len(st.Tools) != 1 {
		t.Errorf("server1 after reconnect+reconcile: marker must stay cleared until discovery re-runs, got discovered=%v tools=%d", st.ToolsDiscovered, len(st.Tools))
	}
	if _, err := sup.RefreshServerToolsFromDiscovery("server1", tools, sup.DiscoveryGeneration("server1")); err != nil {
		t.Fatalf("RefreshServerToolsFromDiscovery: %v", err)
	}
	if st := status("server1"); !st.ToolsDiscovered || len(st.Tools) != 1 {
		t.Errorf("server1 after rediscovery: marker must be re-stamped with the tools, got discovered=%v tools=%d", st.ToolsDiscovered, len(st.Tools))
	}
	_ = mockUpstream.ConnectServer(context.Background(), "server1")
	settledReconcile(sup, configSvc)
	if st := status("server1"); !st.ToolsDiscovered || len(st.Tools) != 1 {
		t.Errorf("server1 after rediscovery+reconcile: marker must survive with the tools, got discovered=%v tools=%d", st.ToolsDiscovered, len(st.Tools))
	}

	// The lenient connect path and the sweep stamp a server whose tools/list
	// completed with ZERO tools without touching any tool set: a
	// never-discovered "fresh" server gets the marker with no tools, server1
	// keeps its retained set, and a name the snapshot does not hold is
	// ignored.
	// The earlier reconcile passes dispatched their connect actions
	// asynchronously and those goroutines still read the published config's
	// Servers slice (configsvc.Snapshot.GetServer), so the live *config.Config
	// is never mutated here: a fresh copy with its own Servers slice is
	// published through the config service instead, exactly as production
	// config updates arrive.
	fresh := &config.ServerConfig{Name: "fresh", Enabled: true}
	grown := *cfg
	grown.Servers = append(append([]*config.ServerConfig(nil), cfg.Servers...), fresh)
	if err := configSvc.Update(&grown, configsvc.UpdateTypeModify, "test"); err != nil {
		t.Fatalf("configSvc.Update: %v", err)
	}
	_ = mockUpstream.AddServer("fresh", fresh)
	_ = mockUpstream.ConnectServer(context.Background(), "server1")
	settledReconcile(sup, configSvc)
	if status("fresh").ToolsDiscovered {
		t.Fatal("fresh: before any discovery ToolsDiscovered must be false")
	}
	before := sup.snapshot.Load().(*ServerStateSnapshot).Version
	sup.MarkServersToolsDiscovered([]string{"fresh", "server1", "ghost"}, sup.DiscoveryGenerations())
	if st := status("fresh"); !st.ToolsDiscovered || len(st.Tools) != 0 {
		t.Errorf("fresh: a completed zero-tool list must stamp the marker with no tools, got discovered=%v tools=%d", st.ToolsDiscovered, len(st.Tools))
	}
	if st := status("server1"); !st.ToolsDiscovered || len(st.Tools) != 1 {
		t.Errorf("server1: stamping must not touch a retained tool set, got discovered=%v tools=%d", st.ToolsDiscovered, len(st.Tools))
	}
	if snap := sup.snapshot.Load().(*ServerStateSnapshot); snap.Version != before+1 || !snap.Servers["fresh"].ToolsDiscovered {
		t.Errorf("the Supervisor snapshot must carry the stamp in one new version, got version %d (before %d) discovered=%v",
			snap.Version, before, snap.Servers["fresh"].ToolsDiscovered)
	}
	// Nothing to stamp: no new snapshot version is published.
	sup.MarkServersToolsDiscovered([]string{"fresh", "server1", "ghost"}, sup.DiscoveryGenerations())
	if snap := sup.snapshot.Load().(*ServerStateSnapshot); snap.Version != before+1 {
		t.Errorf("an idempotent stamp must not publish, got version %d", snap.Version)
	}
	// A disconnect clears the marker on both sides; a zero-tool list on the
	// next connection re-stamps both.
	sup.updateSnapshotFromEvent(Event{
		Type: EventServerDisconnected, ServerName: "fresh", Timestamp: time.Now(),
		Payload: map[string]interface{}{"connected": false},
	})
	if status("fresh").ToolsDiscovered {
		t.Fatal("fresh after disconnect: StateView marker must be cleared")
	}
	if sup.snapshot.Load().(*ServerStateSnapshot).Servers["fresh"].ToolsDiscovered {
		t.Fatal("fresh after disconnect: the retained Supervisor state must drop its stamp too")
	}
	sup.MarkServersToolsDiscovered([]string{"fresh"}, sup.DiscoveryGenerations())
	if !status("fresh").ToolsDiscovered || !sup.snapshot.Load().(*ServerStateSnapshot).Servers["fresh"].ToolsDiscovered {
		t.Error("fresh: a zero-tool list after reconnect must stamp both the StateView and the retained state")
	}
}

// connectionEvent is a delivered connect / disconnect event for name.
func connectionEvent(name string, connected bool) Event {
	typ := EventServerConnected
	if !connected {
		typ = EventServerDisconnected
	}
	return Event{Type: typ, ServerName: name, Timestamp: time.Now(), Payload: map[string]interface{}{"connected": connected}}
}

// settledReconcile runs one reconcile pass and waits for the actions it
// dispatched (AddServer + ConnectServer on the mock, on a goroutine) to land
// before returning, so a later step that disconnects the mock and reconciles
// again cannot be raced by a connect action from an EARLIER pass re-connecting
// the mock underneath it.
func settledReconcile(sup *Supervisor, configSvc *configsvc.Service) {
	_ = sup.reconcile(configSvc.Current())
	sup.actionWg.Wait()
}

// markerFixture is a reconciled one-server supervisor plus a StateView reader.
func markerFixture(t *testing.T) (*Supervisor, *MockUpstreamAdapter, *configsvc.Service, func(string) *stateview.ServerStatus) {
	t.Helper()
	cfg := &config.Config{Listen: "127.0.0.1:8080", Servers: []*config.ServerConfig{{Name: "s", Enabled: true}}}
	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	t.Cleanup(func() { configSvc.Close() })
	mockUpstream := NewMockUpstreamAdapter()
	t.Cleanup(func() { mockUpstream.Close() })
	_ = mockUpstream.AddServer("s", cfg.Servers[0])
	sup := New(configSvc, mockUpstream, zap.NewNop())
	settledReconcile(sup, configSvc)
	return sup, mockUpstream, configSvc, func(name string) *stateview.ServerStatus {
		t.Helper()
		st, ok := sup.StateView().Snapshot().Servers[name]
		if !ok {
			t.Fatalf("server %q missing from StateView", name)
		}
		return st
	}
}

// TestSupervisor_StaleDiscoveryResultIsDropped pins the connection-generation
// binding of discovery publication (Spec 105 FR-009 "stale generation";
// astra r1 I2). The marker is per connection, but publication was keyed by
// server name alone and last-writer-wins, so a result captured on connection
// A landed on connection B whenever ListTools→index→publish spanned a
// disconnect+reconnect (the serial sweep with a synchronous RestartServer
// inside it): B read discovered=true with A's names before its own pass, and
// — if B's reactive discovery had published FIRST — the delayed sweep
// overwrote it, so B's genuine names were refused as stale. A publish now
// carries the generation it was captured under and is dropped when the
// server's connection has moved on.
func TestSupervisor_StaleDiscoveryResultIsDropped(t *testing.T) {
	aTools := []*config.ToolMetadata{{Name: "old_tool", ServerName: "s"}}
	bTools := []*config.ToolMetadata{{Name: "new_tool", ServerName: "s"}}

	t.Run("captured on A, published after B connected", func(t *testing.T) {
		sup, _, _, status := markerFixture(t)
		sup.updateSnapshotFromEvent(connectionEvent("s", true))
		gens := sup.DiscoveryGenerations() // captured under A, before ListTools
		sup.updateSnapshotFromEvent(connectionEvent("s", false))
		sup.updateSnapshotFromEvent(connectionEvent("s", true))
		require.False(t, status("s").ToolsDiscovered, "precondition: B is undiscovered")

		stale, err := sup.RefreshToolsFromDiscovery(aTools, gens)
		require.NoError(t, err)
		require.Equal(t, []string{"s"}, stale, "the caller is told which servers to re-list")
		st := status("s")
		require.False(t, st.ToolsDiscovered, "A's result must not certify B: discovered=%v tools=%v", st.ToolsDiscovered, st.Tools)
		require.Empty(t, st.Tools, "A's names must not land on B")

		// B's own result, captured under B's generation, publishes.
		published, err := sup.RefreshServerToolsFromDiscovery("s", bTools, sup.DiscoveryGeneration("s"))
		require.NoError(t, err)
		require.True(t, published)
		st = status("s")
		require.True(t, st.ToolsDiscovered)
		require.Len(t, st.Tools, 1)
		require.Equal(t, "new_tool", st.Tools[0].Name)

		// A's delayed result arriving AFTER B published is still dropped:
		// B's genuine names are not overwritten.
		stale, err = sup.RefreshToolsFromDiscovery(aTools, gens)
		require.NoError(t, err)
		require.Equal(t, []string{"s"}, stale)
		st = status("s")
		require.True(t, st.ToolsDiscovered)
		require.Equal(t, "new_tool", st.Tools[0].Name, "a stale publish must not overwrite the current connection's result")
	})

	t.Run("captured on A, published while disconnected, then B connects", func(t *testing.T) {
		sup, _, _, status := markerFixture(t)
		sup.updateSnapshotFromEvent(connectionEvent("s", true))
		gens := sup.DiscoveryGenerations()
		sup.updateSnapshotFromEvent(connectionEvent("s", false))
		stale, err := sup.RefreshToolsFromDiscovery(aTools, gens)
		require.NoError(t, err)
		require.Equal(t, []string{"s"}, stale)
		require.False(t, sup.snapshot.Load().(*ServerStateSnapshot).Servers["s"].ToolsDiscovered, "a stale result must not stamp the retained state while disconnected")
		sup.updateSnapshotFromEvent(connectionEvent("s", true))
		st := status("s")
		require.True(t, st.Connected)
		require.False(t, st.ToolsDiscovered, "B must not read discovered from a result captured on A")
		require.Empty(t, st.Tools)
	})

	t.Run("a zero-tool stamp captured under a superseded connection is dropped too", func(t *testing.T) {
		sup, _, _, status := markerFixture(t)
		sup.updateSnapshotFromEvent(connectionEvent("s", true))
		gens := sup.DiscoveryGenerations()
		sup.updateSnapshotFromEvent(connectionEvent("s", false))
		sup.updateSnapshotFromEvent(connectionEvent("s", true))
		sup.MarkServersToolsDiscovered([]string{"s"}, gens)
		require.False(t, status("s").ToolsDiscovered)
		sup.MarkServersToolsDiscovered([]string{"s"}, sup.DiscoveryGenerations())
		require.True(t, status("s").ToolsDiscovered, "the same stamp under the current generation lands")
	})

	t.Run("control: a result captured and published on the same connection lands", func(t *testing.T) {
		sup, _, _, status := markerFixture(t)
		sup.updateSnapshotFromEvent(connectionEvent("s", true))
		stale, err := sup.RefreshToolsFromDiscovery(aTools, sup.DiscoveryGenerations())
		require.NoError(t, err)
		require.Empty(t, stale)
		require.True(t, status("s").ToolsDiscovered)
	})

	t.Run("reconcile carries the generation and bumps it on a disconnect it observes", func(t *testing.T) {
		sup, mockUpstream, configSvc, _ := markerFixture(t)
		require.NoError(t, mockUpstream.ConnectServer(context.Background(), "s"))
		_ = sup.reconcile(configSvc.Current())
		sup.updateSnapshotFromEvent(connectionEvent("s", true))
		before := sup.DiscoveryGeneration("s").Generation
		_ = sup.reconcile(configSvc.Current())
		require.Equal(t, before, sup.DiscoveryGeneration("s").Generation, "a reconcile that observes no change keeps the generation")
		require.NoError(t, mockUpstream.DisconnectServer("s"))
		_ = sup.reconcile(configSvc.Current())
		require.Equal(t, before+1, sup.DiscoveryGeneration("s").Generation, "a disconnect reconcile observes (dropped event) moves the generation")
	})
}

// TestSupervisor_DroppedDisconnectEventClearsMarker pins astra r1 I4: the
// only code that cleared the per-connection marker was the disconnect event
// handler, and event delivery is best-effort (actor_pool.emitEvent drops on a
// full channel). After a dropped disconnect, reconcile copied the stale stamp
// forward with the retained tools and the connect branch left it in place
// (its restore ran only for an empty StateView set), so connection B read
// discovered=true with connection A's names before its own pass. Reconcile
// now invalidates the marker whenever it observes the server disconnected,
// and the connect event clears it on both sides regardless of what was
// retained.
func TestSupervisor_DroppedDisconnectEventClearsMarker(t *testing.T) {
	tools := []*config.ToolMetadata{{Name: "erase", ServerName: "s"}}
	retained := func(t *testing.T, sup *Supervisor) *ServerState {
		t.Helper()
		return sup.snapshot.Load().(*ServerStateSnapshot).Servers["s"]
	}

	t.Run("reconcile observes the disconnect", func(t *testing.T) {
		sup, mockUpstream, configSvc, status := markerFixture(t)
		require.NoError(t, mockUpstream.ConnectServer(context.Background(), "s"))
		settledReconcile(sup, configSvc)
		sup.updateSnapshotFromEvent(connectionEvent("s", true))
		published, err := sup.RefreshServerToolsFromDiscovery("s", tools, sup.DiscoveryGeneration("s"))
		require.NoError(t, err)
		require.True(t, published)
		require.True(t, status("s").ToolsDiscovered)

		// The manager drops the connection; the event is lost.
		require.NoError(t, mockUpstream.DisconnectServer("s"))
		_ = sup.reconcile(configSvc.Current())
		st := status("s")
		require.False(t, st.Connected)
		require.False(t, st.ToolsDiscovered, "reconcile is authoritative for Connected and must invalidate the per-connection marker with it")
		require.False(t, retained(t, sup).ToolsDiscovered, "the retained state must drop the stamp too")
		require.Len(t, retained(t, sup).Tools, 1, "the retained tools are kept (MCP-2094)")

		// Connection B: the connect event arrives, no rediscovery yet.
		require.NoError(t, mockUpstream.ConnectServer(context.Background(), "s"))
		sup.updateSnapshotFromEvent(connectionEvent("s", true))
		st = status("s")
		require.True(t, st.Connected)
		require.False(t, st.ToolsDiscovered, "B has not completed a pass: A's stamp must not certify A's names")
		require.Len(t, st.Tools, 1, "the retained set is restored for counts and listings")

		// B's own pass re-stamps.
		published, err = sup.RefreshServerToolsFromDiscovery("s", tools, sup.DiscoveryGeneration("s"))
		require.NoError(t, err)
		require.True(t, published)
		require.True(t, status("s").ToolsDiscovered)
	})

	t.Run("no reconcile between the dropped disconnect and the connect event", func(t *testing.T) {
		sup, mockUpstream, configSvc, status := markerFixture(t)
		require.NoError(t, mockUpstream.ConnectServer(context.Background(), "s"))
		_ = sup.reconcile(configSvc.Current())
		sup.updateSnapshotFromEvent(connectionEvent("s", true))
		_, err := sup.RefreshServerToolsFromDiscovery("s", tools, sup.DiscoveryGeneration("s"))
		require.NoError(t, err)
		require.True(t, status("s").ToolsDiscovered)
		gen := sup.DiscoveryGeneration("s").Generation

		// Dropped disconnect, then the connect event for B with the
		// StateView still holding A's tools and stamp.
		sup.updateSnapshotFromEvent(connectionEvent("s", true))
		st := status("s")
		require.True(t, st.Connected)
		require.False(t, st.ToolsDiscovered, "a connect event is a new connection: the stamp must be cleared on the StateView even when its tools were never cleared")
		require.False(t, retained(t, sup).ToolsDiscovered, "... and on the retained state")
		require.Len(t, st.Tools, 1, "the tools stay for counts")
		require.Equal(t, gen+1, sup.DiscoveryGeneration("s").Generation, "the connect edge moves the generation, so a result captured on A is dropped")
	})
}

// TestSupervisor_ReconcileDetectsUnobservedReconnect pins astra r2 C3 on the
// reconcile side. The Supervisor's ConnectionGeneration moves only on an edge
// it OBSERVES — a delivered connect/disconnect event, or a reconcile that
// reads the server disconnected. When both events of a disconnect+reconnect
// are dropped (actor_pool.emitEvent drops on a full channel) and the manager
// already reports the new connection by the time reconcile runs, nothing
// moved: the previous connection's stamp, tools and generation survived until
// the next sweep (default 5 min). The adapter now reports the live client's
// connection token (managed.Client.ConnectionEpoch); a discovery result is
// stamped with the token it was captured under, and a reconcile that finds
// the live token moved treats it as the missed edge: marker cleared,
// generation bumped, reactive discovery kicked.
func TestSupervisor_ReconcileDetectsUnobservedReconnect(t *testing.T) {
	tools := []*config.ToolMetadata{{Name: "erase", ServerName: "s"}}
	sup, mockUpstream, configSvc, status := markerFixture(t)

	// Connection A, observed, discovered.
	require.NoError(t, mockUpstream.ConnectServer(context.Background(), "s"))
	settledReconcile(sup, configSvc)
	sup.updateSnapshotFromEvent(connectionEvent("s", true))
	captureA := sup.DiscoveryGeneration("s")
	require.NotZero(t, captureA.Epoch, "the adapter reports the live connection token")
	published, err := sup.RefreshServerToolsFromDiscovery("s", tools, captureA)
	require.NoError(t, err)
	require.True(t, published)
	st := status("s")
	require.True(t, st.ToolsDiscovered)
	require.Equal(t, captureA.Epoch, st.DiscoveryEpoch, "the stamp carries the token it was captured under")
	require.Equal(t, captureA.Epoch, sup.snapshot.Load().(*ServerStateSnapshot).Servers["s"].DiscoveryEpoch)

	// The connect event above kicked A's own reactive discovery (the
	// pre-105 trigger); the kicks counted from here on are reconcile's.
	var kickedMu sync.Mutex
	var kicked []string
	sup.SetOnServerConnectedCallback(func(name string) {
		kickedMu.Lock()
		defer kickedMu.Unlock()
		kicked = append(kicked, name)
	})
	kickedCount := func() int {
		kickedMu.Lock()
		defer kickedMu.Unlock()
		return len(kicked)
	}

	// A reconcile that observes the SAME connection keeps everything.
	settledReconcile(sup, configSvc)
	st = status("s")
	require.True(t, st.Connected && st.ToolsDiscovered)
	require.Equal(t, captureA.Epoch, st.DiscoveryEpoch)
	require.Equal(t, captureA.Generation, sup.DiscoveryGeneration("s").Generation)
	require.Zero(t, kickedCount(), "nothing to re-list on an unchanged connection")

	// Both events of a disconnect+reconnect are dropped; by the time reconcile
	// runs the manager reports connection B as connected.
	require.NoError(t, mockUpstream.DisconnectServer("s"))
	require.NoError(t, mockUpstream.ConnectServer(context.Background(), "s"))
	settledReconcile(sup, configSvc)
	st = status("s")
	require.True(t, st.Connected, "reconcile reads B as connected")
	require.False(t, st.ToolsDiscovered, "A's stamp must not survive an unobserved reconnect")
	require.Zero(t, st.DiscoveryEpoch)
	require.Len(t, st.Tools, 1, "the retained tools stay for counts and listings (MCP-2094)")
	retained := sup.snapshot.Load().(*ServerStateSnapshot).Servers["s"]
	require.False(t, retained.ToolsDiscovered)
	require.Equal(t, captureA.Generation+1, retained.ConnectionGeneration, "the missed edge moves the generation")
	require.Eventually(t, func() bool { return kickedCount() == 1 }, 2*time.Second, 10*time.Millisecond,
		"the reactive discovery the dropped connect event would have kicked is kicked by reconcile")

	// A's delayed result (captured under A) is dropped at publish time.
	published, err = sup.RefreshServerToolsFromDiscovery("s", tools, captureA)
	require.NoError(t, err)
	require.False(t, published)
	require.False(t, status("s").ToolsDiscovered)

	// B's own pass, captured under B's token, lands and re-stamps.
	captureB := sup.DiscoveryGeneration("s")
	require.NotEqual(t, captureA.Epoch, captureB.Epoch)
	published, err = sup.RefreshServerToolsFromDiscovery("s", tools, captureB)
	require.NoError(t, err)
	require.True(t, published)
	st = status("s")
	require.True(t, st.ToolsDiscovered)
	require.Equal(t, captureB.Epoch, st.DiscoveryEpoch)

	// Steady state again: no further kick, stamp kept.
	settledReconcile(sup, configSvc)
	require.True(t, status("s").ToolsDiscovered)
	require.Equal(t, 1, kickedCount())
}

// TestSupervisor_ReconcileRepublishesRetainedToolsWhileNotConnected pins the
// StateView shape behind codex r4 E1: after a real adapter disconnect and its
// event (which clears the StateView tool list), the reconcile sweep copies
// the RETAINED Supervisor-snapshot tools back into a StateView entry that
// reads Connected=false, ToolsDiscovered=false, DiscoveryEpoch=0. A
// not-connected snapshot is therefore NOT necessarily empty: it can list the
// previous connection's names without certifying any of them. The
// dispatch-side identity check (internal/server liveIdentityRefusal) relies
// on this test's shape being real, and admits only a certified name once the
// live client is connected — never a merely listed one.
func TestSupervisor_ReconcileRepublishesRetainedToolsWhileNotConnected(t *testing.T) {
	sup, mockUpstream, configSvc, status := markerFixture(t)
	require.NoError(t, mockUpstream.ConnectServer(context.Background(), "s"))
	settledReconcile(sup, configSvc)
	sup.updateSnapshotFromEvent(connectionEvent("s", true))

	tools := []*config.ToolMetadata{{Name: "erase", ServerName: "s"}}
	_, err := sup.RefreshToolsFromDiscovery(tools, sup.DiscoveryGenerations())
	require.NoError(t, err)
	st := status("s")
	require.True(t, st.Connected && st.ToolsDiscovered, "precondition: certified on the first connection (got %+v)", st)
	require.NotZero(t, st.DiscoveryEpoch)
	require.Len(t, st.Tools, 1)

	// The adapter really disconnects and the disconnect event lands: the
	// StateView tool list is cleared.
	require.NoError(t, mockUpstream.DisconnectServer("s"))
	sup.updateSnapshotFromEvent(connectionEvent("s", false))
	st = status("s")
	require.False(t, st.Connected)
	require.False(t, st.ToolsDiscovered)
	require.Empty(t, st.Tools, "the disconnect event clears the StateView tools")

	// The sweep observes the adapter as not connected and republishes the
	// retained set into the not-connected entry.
	settledReconcile(sup, configSvc)
	st = status("s")
	assert.False(t, st.Connected, "reconcile is authoritative for Connected")
	assert.False(t, st.ToolsDiscovered, "no pass has run on the next connection")
	assert.Zero(t, st.DiscoveryEpoch, "an unstamped entry carries no generation")
	assert.Len(t, st.Tools, 1, "the retained tools are republished while not connected: a listed name is NOT a certified one")
}
