package storage

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"mcpproxy-go/internal/config"
)

func TestQuarantineFunctionality(t *testing.T) {
	// Create temporary directory for test data
	tempDir, err := os.MkdirTemp("", "mcpproxy-quarantine-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create storage manager
	logger := zaptest.NewLogger(t).Sugar()
	manager, err := NewManager(tempDir, logger)
	require.NoError(t, err)
	defer manager.Close()

	// Test 1: Save a server with quarantine status
	server := &config.ServerConfig{
		Name:        "test-server",
		URL:         "http://localhost:3001",
		Protocol:    "http",
		Enabled:     true,
		Quarantined: true, // This server should be quarantined
		Created:     time.Now(),
	}

	err = manager.SaveUpstreamServer(server)
	require.NoError(t, err)

	// Test 2: Retrieve the server and verify quarantine status
	retrievedServer, err := manager.GetUpstreamServer("test-server")
	require.NoError(t, err)
	assert.Equal(t, "test-server", retrievedServer.Name)
	assert.True(t, retrievedServer.Quarantined, "Server should be quarantined")

	// Test 3: List quarantined servers
	quarantinedServers, err := manager.ListQuarantinedUpstreamServers()
	require.NoError(t, err)
	assert.Len(t, quarantinedServers, 1, "Should have one quarantined server")
	assert.Equal(t, "test-server", quarantinedServers[0].Name)

	// Test 4: Change quarantine status
	err = manager.QuarantineUpstreamServer("test-server", false)
	require.NoError(t, err)

	// Verify the server is no longer quarantined
	updatedServer, err := manager.GetUpstreamServer("test-server")
	require.NoError(t, err)
	assert.False(t, updatedServer.Quarantined, "Server should no longer be quarantined")

	// Test 5: List quarantined servers again (should be empty)
	quarantinedServers2, err := manager.ListQuarantinedUpstreamServers()
	require.NoError(t, err)
	assert.Len(t, quarantinedServers2, 0, "Should have no quarantined servers")

	// Test 6: Test ListQuarantinedTools for a quarantined server
	// First, quarantine the server again
	err = manager.QuarantineUpstreamServer("test-server", true)
	require.NoError(t, err)

	tools, err := manager.ListQuarantinedTools("test-server")
	require.NoError(t, err)
	assert.Len(t, tools, 1, "Should return placeholder tool analysis")
	assert.Contains(t, tools[0]["message"], "quarantined", "Message should mention quarantine")

	// Test 7: Try to list tools for non-quarantined server (should fail)
	err = manager.QuarantineUpstreamServer("test-server", false)
	require.NoError(t, err)

	_, err = manager.ListQuarantinedTools("test-server")
	assert.Error(t, err, "Should fail to list tools for non-quarantined server")
	assert.Contains(t, err.Error(), "not quarantined", "Error should mention server is not quarantined")
}
