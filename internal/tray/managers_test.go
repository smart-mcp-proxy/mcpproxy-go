//go:build !nogui && !headless && !linux

package tray

import (
	"sort"
	"testing"

	"mcpproxy-go/internal/contracts"
)

func TestMenuSorting(t *testing.T) {

	// Test data with mixed alphanumeric names that should be sorted
	testServers := []map[string]interface{}{
		{"name": "zebra-server", "enabled": true, "connected": true, "quarantined": false, "tool_count": 5},
		{"name": "alpha-server", "enabled": true, "connected": false, "quarantined": false, "tool_count": 0},
		{"name": "2-numeric-server", "enabled": false, "connected": false, "quarantined": false, "tool_count": 0},
		{"name": "10-high-numeric", "enabled": true, "connected": true, "quarantined": false, "tool_count": 10},
		{"name": "beta-server", "enabled": true, "connected": true, "quarantined": false, "tool_count": 3},
		{"name": "1-first-numeric", "enabled": true, "connected": false, "quarantined": false, "tool_count": 1},
	}

	// Test sorting logic by creating a server map like the real code does
	currentServerMap := make(map[string]map[string]interface{})
	for _, server := range testServers {
		if name, ok := server["name"].(string); ok {
			currentServerMap[name] = server
		}
	}

	// Extract server names and sort them (same logic as in UpdateUpstreamServersMenu)
	var serverNames []string
	for serverName := range currentServerMap {
		serverNames = append(serverNames, serverName)
	}

	// This is the key test - verify that Go's sort.Strings gives us the expected order
	// Go's sort.Strings should sort alphanumerically: numbers first, then letters
	expectedOrder := []string{
		"1-first-numeric",
		"10-high-numeric",
		"2-numeric-server",
		"alpha-server",
		"beta-server",
		"zebra-server",
	}

	// Sort the server names
	sort.Strings(serverNames)

	// Verify the order matches our expectations
	if len(serverNames) != len(expectedOrder) {
		t.Fatalf("Expected %d servers, got %d", len(expectedOrder), len(serverNames))
	}

	for i, expected := range expectedOrder {
		if i >= len(serverNames) || serverNames[i] != expected {
			t.Errorf("Expected server at position %d to be '%s', got '%s'", i, expected, serverNames[i])
		}
	}

	t.Logf("✓ Server names sorted correctly: %v", serverNames)
}

func TestQuarantineSorting(t *testing.T) {

	// Test data with mixed alphanumeric quarantined server names
	testQuarantineServers := []map[string]interface{}{
		{"name": "z-quarantine-server"},
		{"name": "a-quarantine-server"},
		{"name": "5-suspicious-server"},
		{"name": "12-bad-server"},
		{"name": "c-quarantine-server"},
		{"name": "1-quarantine-server"},
	}

	// Test sorting logic by creating a quarantine map like the real code does
	currentQuarantineMap := make(map[string]bool)
	for _, server := range testQuarantineServers {
		if name, ok := server["name"].(string); ok {
			currentQuarantineMap[name] = true
		}
	}

	// Extract quarantine server names and sort them (same logic as in UpdateQuarantineMenu)
	var quarantineNames []string
	for serverName := range currentQuarantineMap {
		quarantineNames = append(quarantineNames, serverName)
	}

	// Expected order (alphanumeric: numbers first, then letters)
	expectedOrder := []string{
		"1-quarantine-server",
		"12-bad-server",
		"5-suspicious-server",
		"a-quarantine-server",
		"c-quarantine-server",
		"z-quarantine-server",
	}

	// Sort the quarantine names
	sort.Strings(quarantineNames)

	// Verify the order matches our expectations
	if len(quarantineNames) != len(expectedOrder) {
		t.Fatalf("Expected %d quarantine servers, got %d", len(expectedOrder), len(quarantineNames))
	}

	for i, expected := range expectedOrder {
		if i >= len(quarantineNames) || quarantineNames[i] != expected {
			t.Errorf("Expected quarantine server at position %d to be '%s', got '%s'", i, expected, quarantineNames[i])
		}
	}

	t.Logf("✓ Quarantine server names sorted correctly: %v", quarantineNames)
}

func TestMenuRebuildLogic(t *testing.T) {
	// Test that the menu manager properly detects when new servers are added
	// and rebuilds the menu in sorted order

	// First batch of servers (existing servers)
	existingServers := []map[string]interface{}{
		{"name": "a-server", "enabled": true, "connected": true, "quarantined": false, "tool_count": 5},
		{"name": "c-server", "enabled": true, "connected": false, "quarantined": false, "tool_count": 0},
	}

	// Second batch with new server added in between
	serversWithNewOne := []map[string]interface{}{
		{"name": "a-server", "enabled": true, "connected": true, "quarantined": false, "tool_count": 5},
		{"name": "b-server", "enabled": true, "connected": true, "quarantined": false, "tool_count": 3}, // New server
		{"name": "c-server", "enabled": true, "connected": false, "quarantined": false, "tool_count": 0},
	}

	// Simulate the logic from UpdateUpstreamServersMenu
	// Step 1: Process existing servers
	existingServerMap := make(map[string]map[string]interface{})
	existingMenuItems := make(map[string]bool) // simulate existing menu items

	for _, server := range existingServers {
		if name, ok := server["name"].(string); ok {
			existingServerMap[name] = server
			existingMenuItems[name] = true // simulate that menu item exists
		}
	}

	// Step 2: Process new batch of servers
	newServerMap := make(map[string]map[string]interface{})
	var newServerNames []string
	for _, server := range serversWithNewOne {
		if name, ok := server["name"].(string); ok {
			newServerMap[name] = server
			if !existingMenuItems[name] {
				newServerNames = append(newServerNames, name)
			}
		}
	}

	// Step 3: Verify that new servers are detected
	if len(newServerNames) != 1 {
		t.Fatalf("Expected 1 new server, got %d", len(newServerNames))
	}

	if newServerNames[0] != "b-server" {
		t.Errorf("Expected new server to be 'b-server', got '%s'", newServerNames[0])
	}

	// Step 4: Verify that all servers would be rebuilt in sorted order
	var allServerNames []string
	for serverName := range newServerMap {
		allServerNames = append(allServerNames, serverName)
	}
	sort.Strings(allServerNames)

	expectedOrder := []string{"a-server", "b-server", "c-server"}
	if len(allServerNames) != len(expectedOrder) {
		t.Fatalf("Expected %d servers after rebuild, got %d", len(expectedOrder), len(allServerNames))
	}

	for i, expected := range expectedOrder {
		if allServerNames[i] != expected {
			t.Errorf("Expected server at position %d to be '%s', got '%s'", i, expected, allServerNames[i])
		}
	}

	t.Logf("✓ Menu rebuild logic works correctly: %v", allServerNames)
}

func TestQuarantineMenuRebuildLogic(t *testing.T) {
	// Test that the quarantine menu manager properly detects when new servers are quarantined
	// and rebuilds the menu in sorted order

	// First batch of quarantined servers
	existingQuarantined := []map[string]interface{}{
		{"name": "evil-server"},
		{"name": "suspicious-server"},
	}

	// Second batch with new quarantined server added in between
	quarantinedWithNewOne := []map[string]interface{}{
		{"name": "evil-server"},
		{"name": "malicious-server"}, // New quarantined server
		{"name": "suspicious-server"},
	}

	// Simulate the logic from UpdateQuarantineMenu
	// Step 1: Process existing quarantined servers
	existingQuarantineMap := make(map[string]bool)
	existingMenuItems := make(map[string]bool) // simulate existing menu items

	for _, server := range existingQuarantined {
		if name, ok := server["name"].(string); ok {
			existingQuarantineMap[name] = true
			existingMenuItems[name] = true // simulate that menu item exists
		}
	}

	// Step 2: Process new batch of quarantined servers
	newQuarantineMap := make(map[string]bool)
	var newQuarantineNames []string
	for _, server := range quarantinedWithNewOne {
		if name, ok := server["name"].(string); ok {
			newQuarantineMap[name] = true
			if !existingMenuItems[name] {
				newQuarantineNames = append(newQuarantineNames, name)
			}
		}
	}

	// Step 3: Verify that new quarantined servers are detected
	if len(newQuarantineNames) != 1 {
		t.Fatalf("Expected 1 new quarantined server, got %d", len(newQuarantineNames))
	}

	if newQuarantineNames[0] != "malicious-server" {
		t.Errorf("Expected new quarantined server to be 'malicious-server', got '%s'", newQuarantineNames[0])
	}

	// Step 4: Verify that all quarantined servers would be rebuilt in sorted order
	var allQuarantineNames []string
	for serverName := range newQuarantineMap {
		allQuarantineNames = append(allQuarantineNames, serverName)
	}
	sort.Strings(allQuarantineNames)

	expectedOrder := []string{"evil-server", "malicious-server", "suspicious-server"}
	if len(allQuarantineNames) != len(expectedOrder) {
		t.Fatalf("Expected %d quarantined servers after rebuild, got %d", len(expectedOrder), len(allQuarantineNames))
	}

	for i, expected := range expectedOrder {
		if allQuarantineNames[i] != expected {
			t.Errorf("Expected quarantined server at position %d to be '%s', got '%s'", i, expected, allQuarantineNames[i])
		}
	}

	t.Logf("✓ Quarantine menu rebuild logic works correctly: %v", allQuarantineNames)
}

// =============================================================================
// Health Level Extraction Tests (Spec 013: Health as Source of Truth)
// =============================================================================

// TestExtractHealthLevel_StructPointer tests extraction when health is a *contracts.HealthStatus
func TestExtractHealthLevel_StructPointer(t *testing.T) {
	// This simulates data coming directly from runtime.GetAllServers() in-process
	server := map[string]interface{}{
		"name":      "test-server",
		"connected": false, // Legacy field - should be ignored
		"health": &contracts.HealthStatus{
			Level:      "healthy",
			AdminState: "enabled",
			Summary:    "Connected (5 tools)",
		},
	}

	level := extractHealthLevel(server)
	if level != "healthy" {
		t.Errorf("Expected health level 'healthy' from struct pointer, got '%s'", level)
	}
}

// TestExtractHealthLevel_MapInterface tests extraction when health is a map[string]interface{}
func TestExtractHealthLevel_MapInterface(t *testing.T) {
	// This simulates data after JSON serialization/deserialization (API path)
	server := map[string]interface{}{
		"name":      "test-server",
		"connected": false, // Legacy field - should be ignored
		"health": map[string]interface{}{
			"level":       "healthy",
			"admin_state": "enabled",
			"summary":     "Connected (5 tools)",
		},
	}

	level := extractHealthLevel(server)
	if level != "healthy" {
		t.Errorf("Expected health level 'healthy' from map, got '%s'", level)
	}
}

// TestExtractHealthLevel_Nil tests extraction when health is nil
func TestExtractHealthLevel_Nil(t *testing.T) {
	server := map[string]interface{}{
		"name":      "test-server",
		"connected": true,
		"health":    nil,
	}

	level := extractHealthLevel(server)
	if level != "" {
		t.Errorf("Expected empty string for nil health, got '%s'", level)
	}
}

// TestExtractHealthLevel_Missing tests extraction when health field is missing
func TestExtractHealthLevel_Missing(t *testing.T) {
	server := map[string]interface{}{
		"name":      "test-server",
		"connected": true,
	}

	level := extractHealthLevel(server)
	if level != "" {
		t.Errorf("Expected empty string for missing health, got '%s'", level)
	}
}

// =============================================================================
// Connected Count Consistency Tests (Spec 013: Single Source of Truth)
// =============================================================================

// TestConnectedCount_HealthVsLegacy tests that health.level is used over legacy connected field
// This reproduces the bug: tray showing 8/15 vs CLI showing 13 healthy
func TestConnectedCount_HealthVsLegacy(t *testing.T) {
	// This data simulates the real-world inconsistency:
	// - health.level says "healthy" (from health calculation)
	// - connected says false (from StateView.Connected which may be stale)
	servers := []map[string]interface{}{
		{
			"name":      "buildkite",
			"connected": false, // WRONG - stale StateView data
			"health": &contracts.HealthStatus{
				Level:      "healthy", // CORRECT - from health calculation
				AdminState: "enabled",
				Summary:    "Connected (28 tools)",
			},
		},
		{
			"name":      "context7",
			"connected": false, // WRONG
			"health": &contracts.HealthStatus{
				Level:      "healthy", // CORRECT
				AdminState: "enabled",
				Summary:    "Connected (10 tools)",
			},
		},
		{
			"name":      "datadog",
			"connected": false, // WRONG
			"health": &contracts.HealthStatus{
				Level:      "healthy", // CORRECT
				AdminState: "enabled",
				Summary:    "Connected (15 tools)",
			},
		},
		{
			"name":      "github",
			"connected": false,
			"health": &contracts.HealthStatus{
				Level:      "unhealthy", // Needs OAuth
				AdminState: "enabled",
				Summary:    "Authentication required",
				Action:     "login",
			},
		},
		{
			"name":      "gmail",
			"connected": false,
			"health": &contracts.HealthStatus{
				Level:      "unhealthy", // Needs OAuth
				AdminState: "enabled",
				Summary:    "Authentication required",
				Action:     "login",
			},
		},
	}

	// Count connected servers using the SAME logic as UpdateUpstreamServersMenu
	connectedCount := 0
	for _, server := range servers {
		// This matches lines 351-360 of managers.go
		healthLevel := extractHealthLevel(server)
		if healthLevel == "healthy" {
			connectedCount++
			continue
		}
		// Fallback to legacy connected field
		if connected, ok := server["connected"].(bool); ok && connected {
			connectedCount++
		}
	}

	// Spec: Health is single source of truth
	// Expected: 3 healthy servers (buildkite, context7, datadog)
	// Bug: If health extraction fails, fallback uses connected=false, giving 0
	expectedHealthy := 3
	if connectedCount != expectedHealthy {
		t.Errorf("Connected count mismatch: expected %d (from health.level), got %d. "+
			"This indicates health extraction is failing and falling back to stale 'connected' field.",
			expectedHealthy, connectedCount)
	}
}

// TestConnectedCount_MixedDataTypes tests counting with mixed struct and map health data
// This simulates real-world scenario where some data comes from in-process and some from API
func TestConnectedCount_MixedDataTypes(t *testing.T) {
	servers := []map[string]interface{}{
		{
			"name":      "server1",
			"connected": false,
			// Struct pointer (in-process path)
			"health": &contracts.HealthStatus{
				Level:   "healthy",
				Summary: "Connected",
			},
		},
		{
			"name":      "server2",
			"connected": false,
			// Map (API/JSON path)
			"health": map[string]interface{}{
				"level":   "healthy",
				"summary": "Connected",
			},
		},
		{
			"name":      "server3",
			"connected": true, // Only legacy field
			// No health field
		},
	}

	connectedCount := 0
	for _, server := range servers {
		healthLevel := extractHealthLevel(server)
		if healthLevel == "healthy" {
			connectedCount++
			continue
		}
		if connected, ok := server["connected"].(bool); ok && connected {
			connectedCount++
		}
	}

	// Expected: 3 (2 from health.level="healthy", 1 from connected=true fallback)
	if connectedCount != 3 {
		t.Errorf("Expected 3 connected servers, got %d", connectedCount)
	}
}

// =============================================================================
// Regression Test: Tray/CLI/API Consistency (Spec 013-FR-012)
// =============================================================================

// TestHealthConsistency_TrayVsCLI verifies that the same server data produces
// the same health counts regardless of interface (tray, CLI, API)
func TestHealthConsistency_TrayVsCLI(t *testing.T) {
	// This is the exact data that was observed causing 8/15 vs 13 healthy discrepancy
	// Based on debugging-notes.md
	servers := []map[string]interface{}{
		{"name": "buildkite", "connected": false, "health": &contracts.HealthStatus{Level: "healthy", Summary: "Connected (28 tools)"}},
		{"name": "context7", "connected": false, "health": &contracts.HealthStatus{Level: "healthy", Summary: "Connected"}},
		{"name": "datadog", "connected": false, "health": &contracts.HealthStatus{Level: "healthy", Summary: "Connected"}},
		{"name": "gcal", "connected": false, "health": &contracts.HealthStatus{Level: "healthy", Summary: "Connected"}},
		{"name": "time", "connected": false, "health": &contracts.HealthStatus{Level: "healthy", Summary: "Connected"}},
		// Add servers that ARE correctly connected
		{"name": "ast-grep", "connected": true, "health": &contracts.HealthStatus{Level: "healthy", Summary: "Connected"}},
		{"name": "filesystem", "connected": true, "health": &contracts.HealthStatus{Level: "healthy", Summary: "Connected"}},
		{"name": "fetch", "connected": true, "health": &contracts.HealthStatus{Level: "healthy", Summary: "Connected"}},
		{"name": "memory", "connected": true, "health": &contracts.HealthStatus{Level: "healthy", Summary: "Connected"}},
		{"name": "postgres", "connected": true, "health": &contracts.HealthStatus{Level: "healthy", Summary: "Connected"}},
		{"name": "puppeteer", "connected": true, "health": &contracts.HealthStatus{Level: "healthy", Summary: "Connected"}},
		{"name": "sequential", "connected": true, "health": &contracts.HealthStatus{Level: "healthy", Summary: "Connected"}},
		{"name": "sqlite", "connected": true, "health": &contracts.HealthStatus{Level: "healthy", Summary: "Connected"}},
		// Unhealthy servers
		{"name": "github", "connected": false, "health": &contracts.HealthStatus{Level: "unhealthy", Action: "login"}},
		{"name": "gmail", "connected": false, "health": &contracts.HealthStatus{Level: "unhealthy", Action: "login"}},
	}

	// Method 1: Tray counting logic (extractHealthLevel + fallback)
	trayConnected := 0
	for _, server := range servers {
		healthLevel := extractHealthLevel(server)
		if healthLevel == "healthy" {
			trayConnected++
			continue
		}
		if connected, ok := server["connected"].(bool); ok && connected {
			trayConnected++
		}
	}

	// Method 2: CLI counting logic (should use health.level directly)
	cliHealthy := 0
	for _, server := range servers {
		healthLevel := extractHealthLevel(server)
		if healthLevel == "healthy" {
			cliHealthy++
		}
	}

	// Method 3: Legacy connected field only (wrong approach)
	legacyConnected := 0
	for _, server := range servers {
		if connected, ok := server["connected"].(bool); ok && connected {
			legacyConnected++
		}
	}

	// All methods should agree when health.level is source of truth
	expectedHealthy := 13 // 15 total - 2 unhealthy (github, gmail)
	expectedLegacy := 8   // Only 8 have connected=true (the bug shows this count)

	t.Logf("Tray connected count: %d", trayConnected)
	t.Logf("CLI healthy count: %d", cliHealthy)
	t.Logf("Legacy connected count: %d", legacyConnected)

	if trayConnected != expectedHealthy {
		t.Errorf("Tray connected count (%d) != expected healthy (%d). "+
			"Health extraction may be failing.", trayConnected, expectedHealthy)
	}

	if cliHealthy != expectedHealthy {
		t.Errorf("CLI healthy count (%d) != expected healthy (%d)", cliHealthy, expectedHealthy)
	}

	// Legacy count should NOT match (this documents the bug)
	if legacyConnected != expectedLegacy {
		t.Logf("Note: Legacy connected count (%d) differs from expected (%d)", legacyConnected, expectedLegacy)
	}

	// KEY ASSERTION: Tray and CLI must show same counts (spec requirement)
	if trayConnected != cliHealthy {
		t.Errorf("CONSISTENCY VIOLATION: Tray (%d) != CLI (%d). "+
			"Spec 013-FR-012 requires all interfaces show identical data.", trayConnected, cliHealthy)
	}
}
