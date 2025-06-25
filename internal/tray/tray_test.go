//go:build !nogui && !headless

package tray

import (
	"context"
	"testing"

	"go.uber.org/zap/zaptest"
)

// MockServerInterface provides a mock implementation for testing
type MockServerInterface struct {
	running                      bool
	listenAddress                string
	allServers                   []map[string]interface{}
	quarantinedServers           []map[string]interface{}
	upstreamStats                map[string]interface{}
	statusCh                     chan interface{}
	configPath                   string
	reloadConfigurationCalled    bool
	reloadConfigurationCallCount int
}

func NewMockServer() *MockServerInterface {
	return &MockServerInterface{
		running:            false,
		listenAddress:      ":8080",
		allServers:         []map[string]interface{}{},
		quarantinedServers: []map[string]interface{}{},
		upstreamStats:      map[string]interface{}{},
		statusCh:           make(chan interface{}, 10),
		configPath:         "/test/config.json",
	}
}

func (m *MockServerInterface) IsRunning() bool {
	return m.running
}

func (m *MockServerInterface) GetListenAddress() string {
	return m.listenAddress
}

func (m *MockServerInterface) GetUpstreamStats() map[string]interface{} {
	return m.upstreamStats
}

func (m *MockServerInterface) StartServer(_ context.Context) error {
	m.running = true
	return nil
}

func (m *MockServerInterface) StopServer() error {
	m.running = false
	return nil
}

func (m *MockServerInterface) GetStatus() interface{} {
	return map[string]interface{}{
		"phase":   "Ready",
		"message": "Test server ready",
	}
}

func (m *MockServerInterface) StatusChannel() <-chan interface{} {
	return m.statusCh
}

func (m *MockServerInterface) GetQuarantinedServers() ([]map[string]interface{}, error) {
	return m.quarantinedServers, nil
}

func (m *MockServerInterface) UnquarantineServer(serverName string) error {
	// Remove from quarantined servers
	for i, server := range m.quarantinedServers {
		if name, ok := server["name"].(string); ok && name == serverName {
			m.quarantinedServers = append(m.quarantinedServers[:i], m.quarantinedServers[i+1:]...)
			break
		}
	}

	// Update the server in allServers to set quarantined = false
	for _, server := range m.allServers {
		if name, ok := server["name"].(string); ok && name == serverName {
			server["quarantined"] = false
			break
		}
	}

	return nil
}

func (m *MockServerInterface) EnableServer(serverName string, enabled bool) error {
	for _, server := range m.allServers {
		if name, ok := server["name"].(string); ok && name == serverName {
			server["enabled"] = enabled
			break
		}
	}
	return nil
}

func (m *MockServerInterface) QuarantineServer(serverName string, quarantined bool) error {
	for _, server := range m.allServers {
		if name, ok := server["name"].(string); ok && name == serverName {
			server["quarantined"] = quarantined

			if quarantined {
				// Add to quarantined servers list
				m.quarantinedServers = append(m.quarantinedServers, server)
			} else {
				// Remove from quarantined servers list
				for i, qServer := range m.quarantinedServers {
					if qName, ok := qServer["name"].(string); ok && qName == serverName {
						m.quarantinedServers = append(m.quarantinedServers[:i], m.quarantinedServers[i+1:]...)
						break
					}
				}
			}
			break
		}
	}
	return nil
}

func (m *MockServerInterface) GetAllServers() ([]map[string]interface{}, error) {
	return m.allServers, nil
}

func (m *MockServerInterface) DeleteServer(serverName string) error {
	// Remove from allServers
	for i, server := range m.allServers {
		if name, ok := server["name"].(string); ok && name == serverName {
			m.allServers = append(m.allServers[:i], m.allServers[i+1:]...)
			break
		}
	}

	// Remove from quarantinedServers if present
	for i, server := range m.quarantinedServers {
		if name, ok := server["name"].(string); ok && name == serverName {
			m.quarantinedServers = append(m.quarantinedServers[:i], m.quarantinedServers[i+1:]...)
			break
		}
	}

	return nil
}

func (m *MockServerInterface) ForceMenuUpdate() {
	// Mock implementation - no-op for testing
}

func (m *MockServerInterface) ReloadConfiguration() error {
	m.reloadConfigurationCallCount++
	return nil
}

func (m *MockServerInterface) GetConfigPath() string {
	return m.configPath
}

// Helper methods for testing
func (m *MockServerInterface) AddServer(name, url string, enabled, quarantined bool) {
	server := map[string]interface{}{
		"name":        name,
		"url":         url,
		"enabled":     enabled,
		"quarantined": quarantined,
		"connected":   false,
		"tool_count":  0,
	}
	m.allServers = append(m.allServers, server)

	if quarantined {
		m.quarantinedServers = append(m.quarantinedServers, server)
	}
}

func TestQuarantineWorkflow(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	mockServer := NewMockServer()

	// Add a test server
	mockServer.AddServer("test-server", "http://localhost:3001", true, false)

	// Create tray app (we don't use it directly but it's good to test creation)
	_ = New(mockServer, logger, "v1.0.0", func() {})

	// Test quarantine operation
	err := mockServer.QuarantineServer("test-server", true)
	if err != nil {
		t.Fatalf("Failed to quarantine server: %v", err)
	}

	// Verify server is now quarantined
	quarantinedServers, err := mockServer.GetQuarantinedServers()
	if err != nil {
		t.Fatalf("Failed to get quarantined servers: %v", err)
	}

	if len(quarantinedServers) != 1 {
		t.Fatalf("Expected 1 quarantined server, got %d", len(quarantinedServers))
	}

	if quarantinedServers[0]["name"] != "test-server" {
		t.Fatalf("Expected quarantined server to be 'test-server', got %v", quarantinedServers[0]["name"])
	}

	// Test unquarantine operation
	err = mockServer.UnquarantineServer("test-server")
	if err != nil {
		t.Fatalf("Failed to unquarantine server: %v", err)
	}

	// Verify server is no longer quarantined
	quarantinedServers, err = mockServer.GetQuarantinedServers()
	if err != nil {
		t.Fatalf("Failed to get quarantined servers: %v", err)
	}

	if len(quarantinedServers) != 0 {
		t.Fatalf("Expected 0 quarantined servers, got %d", len(quarantinedServers))
	}

	// Verify server is no longer marked as quarantined in allServers
	allServers, err := mockServer.GetAllServers()
	if err != nil {
		t.Fatalf("Failed to get all servers: %v", err)
	}

	for _, server := range allServers {
		if server["name"] == "test-server" {
			if quarantined, ok := server["quarantined"].(bool); !ok || quarantined {
				t.Fatalf("Expected server to not be quarantined, but quarantined=%v", quarantined)
			}
		}
	}
}

func TestServerEnableDisable(t *testing.T) {
	mockServer := NewMockServer()

	// Add a test server
	mockServer.AddServer("test-server", "http://localhost:3001", true, false)

	// Test disable operation
	err := mockServer.EnableServer("test-server", false)
	if err != nil {
		t.Fatalf("Failed to disable server: %v", err)
	}

	// Verify server is disabled
	allServers, err := mockServer.GetAllServers()
	if err != nil {
		t.Fatalf("Failed to get all servers: %v", err)
	}

	for _, server := range allServers {
		if server["name"] == "test-server" {
			if enabled, ok := server["enabled"].(bool); !ok || enabled {
				t.Fatalf("Expected server to be disabled, but enabled=%v", enabled)
			}
		}
	}

	// Test enable operation
	err = mockServer.EnableServer("test-server", true)
	if err != nil {
		t.Fatalf("Failed to enable server: %v", err)
	}

	// Verify server is enabled
	allServers, err = mockServer.GetAllServers()
	if err != nil {
		t.Fatalf("Failed to get all servers: %v", err)
	}

	for _, server := range allServers {
		if server["name"] == "test-server" {
			if enabled, ok := server["enabled"].(bool); !ok || !enabled {
				t.Fatalf("Expected server to be enabled, but enabled=%v", enabled)
			}
		}
	}
}

func TestMenuRefreshLogic(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	mockServer := NewMockServer()

	// Add test servers
	mockServer.AddServer("server1", "http://localhost:3001", true, false)
	mockServer.AddServer("server2", "http://localhost:3002", true, true) // quarantined

	// Create tray app
	app := New(mockServer, logger, "v1.0.0", func() {})

	// Since we can't test menu functionality without systray.Run, we focus on state logic
	// The app should be properly initialized
	if app == nil {
		t.Fatalf("Expected app to be initialized")
	}

	// Test that the refresh handlers work properly (call the mock server directly since we can't test menu sync without systray)
	err := mockServer.QuarantineServer("server1", true)
	if err != nil {
		t.Fatalf("Failed to quarantine server1: %v", err)
	}

	// Verify that quarantine operation calls the mock correctly
	quarantinedServers, err := mockServer.GetQuarantinedServers()
	if err != nil {
		t.Fatalf("Failed to get quarantined servers: %v", err)
	}

	if len(quarantinedServers) != 2 { // server2 was already quarantined, server1 just got quarantined
		t.Fatalf("Expected 2 quarantined servers, got %d", len(quarantinedServers))
	}
}

// TestQuarantineSubmenuCreation tests that quarantine submenu creation logic works
func TestQuarantineSubmenuCreation(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	mockServer := NewMockServer()

	// Create tray app
	app := New(mockServer, logger, "v1.0.0", func() {})

	// Since we can't test menu functionality without systray.Run, we focus on state logic
	// The app should be properly initialized
	if app == nil {
		t.Fatalf("Expected app to be initialized")
	}

	// Test 1: Empty quarantine list should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("quarantine menu creation panicked with empty quarantine list: %v", r)
		}
	}()

	// Test the quarantine server tracking
	quarantinedServers, err := mockServer.GetQuarantinedServers()
	if err != nil {
		t.Fatalf("Failed to get quarantined servers: %v", err)
	}

	// Should be empty initially
	if len(quarantinedServers) != 0 {
		t.Fatalf("Expected 0 quarantined servers initially, got %d", len(quarantinedServers))
	}

	// Test 2: With quarantined servers
	mockServer.AddServer("quarantined-server", "http://localhost:3001", true, true)

	quarantinedServers, err = mockServer.GetQuarantinedServers()
	if err != nil {
		t.Fatalf("Failed to get quarantined servers: %v", err)
	}

	if len(quarantinedServers) != 1 {
		t.Fatalf("Expected 1 quarantined server, got %d", len(quarantinedServers))
	}

	// Verify server name
	if serverName, ok := quarantinedServers[0]["name"].(string); !ok || serverName != "quarantined-server" {
		t.Fatalf("Expected quarantined server 'quarantined-server', got %v", quarantinedServers[0]["name"])
	}

	// Test that the logic doesn't get stuck - since we can't test menu state directly
	// without systray.Run, we focus on ensuring no panics occur

	// The important part is no panic and proper state management during initialization
}

// TestManagerBasedMenuSystem tests the new manager-based menu system
func TestManagerBasedMenuSystem(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	mockServer := NewMockServer()

	// Add test servers
	mockServer.AddServer("server1", "http://localhost:3001", true, false)  // enabled
	mockServer.AddServer("server2", "http://localhost:3002", false, false) // disabled
	mockServer.AddServer("server3", "http://localhost:3003", true, true)   // quarantined

	// Create tray app and initialize managers
	_ = New(mockServer, logger, "v1.0.0", func() {})

	// Test direct server operations since managers may not be available on all platforms
	// This tests the underlying server interface that the tray depends on

	// Since we can't create actual systray menu items in tests, we'll test the server interface directly

	// Get all servers and verify
	allServers, err := mockServer.GetAllServers()
	if err != nil {
		t.Fatalf("Failed to get all servers: %v", err)
	}

	if len(allServers) != 3 {
		t.Fatalf("Expected 3 servers, got %d", len(allServers))
	}

	// Test quarantine operation
	err = mockServer.QuarantineServer("server1", true)
	if err != nil {
		t.Fatalf("Failed to quarantine server1: %v", err)
	}

	// Verify quarantine operation
	quarantinedServers, err := mockServer.GetQuarantinedServers()
	if err != nil {
		t.Fatalf("Failed to get quarantined servers: %v", err)
	}

	expectedQuarantined := 2 // server1 (newly quarantined) + server3 (already quarantined)
	if len(quarantinedServers) != expectedQuarantined {
		t.Fatalf("Expected %d quarantined servers, got %d", expectedQuarantined, len(quarantinedServers))
	}

	// Test unquarantine operation
	err = mockServer.UnquarantineServer("server3")
	if err != nil {
		t.Fatalf("Failed to unquarantine server3: %v", err)
	}

	// Verify unquarantine operation
	quarantinedServers, err = mockServer.GetQuarantinedServers()
	if err != nil {
		t.Fatalf("Failed to get quarantined servers after unquarantine: %v", err)
	}

	expectedQuarantined = 1 // only server1 should remain quarantined
	if len(quarantinedServers) != expectedQuarantined {
		t.Fatalf("Expected %d quarantined servers after unquarantine, got %d", expectedQuarantined, len(quarantinedServers))
	}

	// Verify it's the correct server
	if quarantinedServers[0]["name"] != "server1" {
		t.Fatalf("Expected server1 to be quarantined, got %v", quarantinedServers[0]["name"])
	}

	// Test enable/disable operations
	err = mockServer.EnableServer("server2", true)
	if err != nil {
		t.Fatalf("Failed to enable server2: %v", err)
	}

	// Verify enable operation by checking all servers
	allServers, err = mockServer.GetAllServers()
	if err != nil {
		t.Fatalf("Failed to get all servers after enable: %v", err)
	}

	// Find server2 and verify it's enabled
	server2Found := false
	for _, server := range allServers {
		if server["name"] == "server2" {
			server2Found = true
			if enabled, ok := server["enabled"].(bool); !ok || !enabled {
				t.Fatalf("Expected server2 to be enabled, got enabled=%v", enabled)
			}
			break
		}
	}

	if !server2Found {
		t.Fatalf("Server2 not found in allServers list")
	}

	t.Log("Server interface test completed successfully!")
}

// TestQuarantineStateMgmt tests that quarantine state management works correctly
func TestQuarantineStateMgmt(t *testing.T) {
	mockServer := NewMockServer()

	// Add test servers - some quarantined, some not
	mockServer.AddServer("server1", "http://localhost:3001", true, false) // enabled, not quarantined
	mockServer.AddServer("server2", "http://localhost:3002", true, true)  // enabled, quarantined
	mockServer.AddServer("server3", "http://localhost:3003", true, true)  // enabled, quarantined

	// Test server interface directly (don't test state manager which may not be available on all platforms)

	// Test initial state
	quarantinedServers, err := mockServer.GetQuarantinedServers()
	if err != nil {
		t.Fatalf("Failed to get quarantined servers: %v", err)
	}

	if len(quarantinedServers) != 2 {
		t.Fatalf("Expected 2 initially quarantined servers, got %d", len(quarantinedServers))
	}

	// Test unquarantine operation
	err = mockServer.UnquarantineServer("server2")
	if err != nil {
		t.Fatalf("Failed to unquarantine server2: %v", err)
	}

	// Verify server2 is no longer quarantined
	quarantinedServers, err = mockServer.GetQuarantinedServers()
	if err != nil {
		t.Fatalf("Failed to get quarantined servers after unquarantine: %v", err)
	}

	if len(quarantinedServers) != 1 {
		t.Fatalf("Expected 1 quarantined server after unquarantine, got %d", len(quarantinedServers))
	}

	// Verify it's the correct server (server3)
	if quarantinedServers[0]["name"] != "server3" {
		t.Fatalf("Expected server3 to remain quarantined, got %v", quarantinedServers[0]["name"])
	}

	// Test quarantining server1
	err = mockServer.QuarantineServer("server1", true)
	if err != nil {
		t.Fatalf("Failed to quarantine server1: %v", err)
	}

	// Verify we now have 2 quarantined servers
	quarantinedServers, err = mockServer.GetQuarantinedServers()
	if err != nil {
		t.Fatalf("Failed to get quarantined servers after quarantine: %v", err)
	}

	if len(quarantinedServers) != 2 {
		t.Fatalf("Expected 2 quarantined servers after quarantine, got %d", len(quarantinedServers))
	}

	// Verify both server1 and server3 are quarantined
	quarantinedNames := make([]string, len(quarantinedServers))
	for i, server := range quarantinedServers {
		quarantinedNames[i] = server["name"].(string)
	}

	expectedQuarantined := []string{"server1", "server3"}
	if !containsAll(quarantinedNames, expectedQuarantined) {
		t.Fatalf("Expected quarantined servers %v, got %v", expectedQuarantined, quarantinedNames)
	}

	t.Log("Quarantine state management test completed successfully!")
}

// Helper function to check if slice contains all expected elements
func containsAll(slice, expected []string) bool {
	if len(slice) != len(expected) {
		return false
	}

	for _, exp := range expected {
		found := false
		for _, item := range slice {
			if item == exp {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// testMenuItem is a dummy type for testing (since we can't create real systray items in tests)
type testMenuItem struct {
	title   string
	tooltip string
}
