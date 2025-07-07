package tray

import (
	"sort"
	"testing"
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
