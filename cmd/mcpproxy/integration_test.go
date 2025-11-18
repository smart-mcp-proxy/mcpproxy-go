//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// NOTE: Client mode E2E test (with daemon and socket) is tested comprehensively
// in internal/server/socket_e2e_test.go. This file focuses on CLI command integration.

// TestIntegration_CodeExecStandaloneMode tests code exec standalone mode without daemon
func TestIntegration_CodeExecStandaloneMode(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup temporary directory for test
	tmpDir := t.TempDir()
	configPath := setupTestConfig(t, tmpDir)

	// Get path to mcpproxy binary (should be in project root)
	binaryPath := filepath.Join("..", "..", "mcpproxy")

	// Run code exec CLI command (no daemon running)
	cliCmd := exec.Command(
		binaryPath, "code", "exec",
		"--config", configPath,
		"--code", "({ result: input.value * 2 })",
		"--input", `{"value": 21}`,
		"--log-level", "error", // Suppress info logs
	)
	// Explicitly disable socket detection
	cliCmd.Env = append(os.Environ(), "MCPPROXY_TRAY_ENDPOINT=")

	output, err := cliCmd.CombinedOutput()

	// Verify success
	require.NoError(t, err, "CLI command failed: %s", string(output))

	// Parse result
	var result map[string]interface{}
	err = json.Unmarshal(output, &result)
	require.NoError(t, err, "Failed to parse result: %s", string(output))

	// Verify response structure
	ok, exists := result["ok"]
	require.True(t, exists, "Result should have 'ok' field")
	assert.True(t, ok.(bool), "Result should be successful")

	// The result field might be "value" not "result"
	var actualResult float64
	if value, hasValue := result["value"]; hasValue {
		valueMap := value.(map[string]interface{})
		actualResult = valueMap["result"].(float64)
	} else if resultField, hasResult := result["result"]; hasResult {
		resultMap := resultField.(map[string]interface{})
		actualResult = resultMap["result"].(float64)
	} else {
		t.Fatalf("Result has unexpected structure: %+v", result)
	}

	assert.Equal(t, 42.0, actualResult, "Result should be 42")

	t.Logf("Standalone mode test passed: %s", string(output))
}

// setupTestConfig creates a minimal config file for testing
func setupTestConfig(t *testing.T, dataDir string) string {
	t.Helper()

	configContent := fmt.Sprintf(`{
		"listen": "127.0.0.1:18080",
		"data_dir": "%s",
		"enable_code_execution": true,
		"mcpServers": []
	}`, dataDir)

	configPath := filepath.Join(dataDir, "mcp_config.json")
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	return configPath
}
