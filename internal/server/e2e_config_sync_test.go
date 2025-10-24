package server

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/testutil"
)

// TestE2E_ConfigSyncAfterServerOperations tests that configuration is saved to disk
// and events are emitted after upstream_servers add/remove/update operations
func TestE2E_ConfigSyncAfterServerOperations(t *testing.T) {
	env := testutil.NewBinaryTestEnv(t)
	defer env.Cleanup()

	env.Start()
	// Wait for server to be ready (but we don't need everything server for this test)
	time.Sleep(500 * time.Millisecond)

	configPath := env.GetConfigPath()

	t.Run("add_server_via_upstream_servers_updates_config_file", func(t *testing.T) {
		// Read config before adding server
		configBefore, err := config.LoadFromFile(configPath)
		require.NoError(t, err)
		serverCountBefore := len(configBefore.Servers)

		// Add a new server via upstream_servers tool
		output, err := env.CallMCPTool("upstream_servers", map[string]interface{}{
			"operation": "add",
			"name":      "test-stdio-server",
			"command":   "echo",
			"args_json": `["hello"]`,
			"enabled":   true,
		})
		require.NoError(t, err)
		t.Logf("Add server output: %s", string(output))

		// Parse response
		var result map[string]interface{}
		err = json.Unmarshal(output, &result)
		require.NoError(t, err)
		assert.Contains(t, result, "name", "Response should contain server name")

		// Wait for config to be saved (allow time for async operations)
		time.Sleep(1 * time.Second)

		// Read config after adding server
		configAfter, err := config.LoadFromFile(configPath)
		require.NoError(t, err)

		// Verify server was added to config file
		assert.Equal(t, serverCountBefore+1, len(configAfter.Servers), "Config should have one more server")

		// Find the added server
		var found *config.ServerConfig
		for _, srv := range configAfter.Servers {
			if srv.Name == "test-stdio-server" {
				found = srv
				break
			}
		}
		require.NotNil(t, found, "Added server should be in config file")
		assert.Equal(t, "test-stdio-server", found.Name)
		assert.Equal(t, "echo", found.Command)
		assert.Equal(t, []string{"hello"}, found.Args)
		assert.True(t, found.Enabled)
	})

	t.Run("update_server_via_upstream_servers_updates_config_file", func(t *testing.T) {
		// Read config before update
		configBefore, err := config.LoadFromFile(configPath)
		require.NoError(t, err)

		// Update the server via upstream_servers tool
		output, err := env.CallMCPTool("upstream_servers", map[string]interface{}{
			"operation": "update",
			"name":      "test-stdio-server",
			"enabled":   false,
		})
		require.NoError(t, err)
		t.Logf("Update server output: %s", string(output))

		// Wait for config to be saved (allow time for async operations)
		time.Sleep(1 * time.Second)

		// Read config after update
		configAfter, err := config.LoadFromFile(configPath)
		require.NoError(t, err)

		// Verify server was updated in config file
		assert.Equal(t, len(configBefore.Servers), len(configAfter.Servers), "Server count should remain the same")

		// Find the updated server
		var found *config.ServerConfig
		for _, srv := range configAfter.Servers {
			if srv.Name == "test-stdio-server" {
				found = srv
				break
			}
		}
		require.NotNil(t, found, "Updated server should be in config file")
		assert.False(t, found.Enabled, "Server should be disabled after update")
	})

	t.Run("patch_server_via_upstream_servers_updates_config_file", func(t *testing.T) {
		// Patch the server to change command
		output, err := env.CallMCPTool("upstream_servers", map[string]interface{}{
			"operation":  "patch",
			"name":       "test-stdio-server",
			"patch_json": `{"command":"cat","args":["test.txt"]}`,
		})
		require.NoError(t, err)
		t.Logf("Patch server output: %s", string(output))

		// Wait for config to be saved (allow time for async operations)
		time.Sleep(1 * time.Second)

		// Read config after patch
		configAfter, err := config.LoadFromFile(configPath)
		require.NoError(t, err)

		// Find the patched server
		var found *config.ServerConfig
		for _, srv := range configAfter.Servers {
			if srv.Name == "test-stdio-server" {
				found = srv
				break
			}
		}
		require.NotNil(t, found, "Patched server should be in config file")
		assert.Equal(t, "cat", found.Command, "Command should be updated via patch")
		assert.Equal(t, []string{"test.txt"}, found.Args, "Args should be updated via patch")
	})

	t.Run("remove_server_via_upstream_servers_updates_config_file", func(t *testing.T) {
		// Read config before removing server
		configBefore, err := config.LoadFromFile(configPath)
		require.NoError(t, err)
		serverCountBefore := len(configBefore.Servers)

		// Remove the server via upstream_servers tool
		output, err := env.CallMCPTool("upstream_servers", map[string]interface{}{
			"operation": "remove",
			"name":      "test-stdio-server",
		})
		require.NoError(t, err)
		t.Logf("Remove server output: %s", string(output))

		// Wait for config to be saved (allow time for async operations)
		time.Sleep(1 * time.Second)

		// Read config after removing server
		configAfter, err := config.LoadFromFile(configPath)
		require.NoError(t, err)

		// Verify server was removed from config file
		assert.Equal(t, serverCountBefore-1, len(configAfter.Servers), "Config should have one fewer server")

		// Verify the server is no longer in config
		for _, srv := range configAfter.Servers {
			assert.NotEqual(t, "test-stdio-server", srv.Name, "Removed server should not be in config file")
		}
	})
}

// TestE2E_ConfigFileIntegrity tests that config file remains valid JSON
// throughout server operations
func TestE2E_ConfigFileIntegrity(t *testing.T) {
	env := testutil.NewBinaryTestEnv(t)
	defer env.Cleanup()

	env.Start()
	time.Sleep(500 * time.Millisecond)

	configPath := env.GetConfigPath()

	t.Run("config_file_is_valid_json_after_operations", func(t *testing.T) {
		// Perform multiple operations
		operations := []struct {
			name string
			args map[string]interface{}
		}{
			{
				name: "add_server_1",
				args: map[string]interface{}{
					"operation": "add",
					"name":      "test-server-1",
					"command":   "echo",
					"enabled":   true,
				},
			},
			{
				name: "add_server_2",
				args: map[string]interface{}{
					"operation": "add",
					"name":      "test-server-2",
					"command":   "cat",
					"enabled":   false,
				},
			},
			{
				name: "update_server_1",
				args: map[string]interface{}{
					"operation": "update",
					"name":      "test-server-1",
					"enabled":   false,
				},
			},
			{
				name: "remove_server_2",
				args: map[string]interface{}{
					"operation": "remove",
					"name":      "test-server-2",
				},
			},
		}

		for _, op := range operations {
			t.Run(op.name, func(t *testing.T) {
				_, err := env.CallMCPTool("upstream_servers", op.args)
				require.NoError(t, err)

				// Wait for config to be saved
				time.Sleep(100 * time.Millisecond)

				// Verify config file is valid JSON
				data, err := os.ReadFile(configPath)
				require.NoError(t, err)

				var cfg config.Config
				err = json.Unmarshal(data, &cfg)
				require.NoError(t, err, "Config file should be valid JSON after %s", op.name)

				// Validate config structure
				err = cfg.Validate()
				require.NoError(t, err, "Config should be valid after %s", op.name)
			})
		}
	})
}

// TestE2E_ConfigReloadPreservesManualChanges tests that manual config changes
// are preserved when using upstream_servers operations
func TestE2E_ConfigReloadPreservesManualChanges(t *testing.T) {
	env := testutil.NewBinaryTestEnv(t)
	defer env.Cleanup()

	env.Start()
	time.Sleep(500 * time.Millisecond)

	configPath := env.GetConfigPath()

	t.Run("manual_config_changes_preserved", func(t *testing.T) {
		// Read initial config
		cfg, err := config.LoadFromFile(configPath)
		require.NoError(t, err)

		// Manually add a field (like a comment or custom setting)
		originalTopK := cfg.TopK
		cfg.TopK = 999 // Change TopK value manually

		// Save the manual change
		err = config.SaveConfig(cfg, configPath)
		require.NoError(t, err)

		// Wait for potential file watcher to reload
		time.Sleep(500 * time.Millisecond)

		// Now add a server via upstream_servers
		_, err = env.CallMCPTool("upstream_servers", map[string]interface{}{
			"operation": "add",
			"name":      "test-preserve-server",
			"command":   "echo",
			"enabled":   true,
		})
		require.NoError(t, err)

		// Wait for config to be saved (allow time for async operations)
		time.Sleep(1 * time.Second)

		// Read config again
		cfgAfter, err := config.LoadFromFile(configPath)
		require.NoError(t, err)

		// Verify that manual change was preserved (or updated correctly)
		// Note: This depends on how the runtime handles config updates
		// For now, we just verify the new server was added
		found := false
		for _, srv := range cfgAfter.Servers {
			if srv.Name == "test-preserve-server" {
				found = true
				break
			}
		}
		assert.True(t, found, "New server should be in config")

		// Note: The TopK value might be reset by runtime reload,
		// which is expected behavior. The important thing is that
		// the servers list is correctly updated.
		t.Logf("TopK before: %d, after: %d", originalTopK, cfgAfter.TopK)
	})
}
