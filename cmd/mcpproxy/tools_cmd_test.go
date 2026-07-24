package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/socket"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestToolsDaemonDetection_NoDaemon(t *testing.T) {
	clearDaemonEnv(t)

	// Test with non-existent directory
	_, ok := newDaemonClient(&config.Config{DataDir: "/tmp/nonexistent-mcpproxy-test-dir-tools-99999"}, nil)
	assert.False(t, ok, "newDaemonClient should report no daemon for non-existent directory")

	// Test with existing directory but no socket
	tmpDir := t.TempDir()
	_, ok = newDaemonClient(&config.Config{DataDir: tmpDir}, nil)
	assert.False(t, ok, "newDaemonClient should report no daemon when socket doesn't exist")
}

func TestDetectSocketPath_Tools(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := socket.DetectSocketPath(tmpDir)

	assert.NotEmpty(t, socketPath, "DetectSocketPath should return non-empty path")

	// Platform-specific assertions
	if runtime.GOOS == "windows" {
		// Windows: Named pipes use global namespace with hash
		assert.True(t, strings.HasPrefix(socketPath, "npipe:////./pipe/mcpproxy-"),
			"Windows socket should be a named pipe")
	} else {
		// Unix: Socket file is within data directory
		assert.Contains(t, socketPath, tmpDir, "Socket path should be within data directory")
		assert.True(t, strings.HasPrefix(socketPath, "unix://"),
			"Unix socket should have unix:// prefix")
	}
}

// ---------------------------------------------------------------------------
// T018: Unit tests for new global-tools CLI logic
// ---------------------------------------------------------------------------

// TestParseServerTool verifies that parseServerTool correctly splits on the
// first ':' only, handles edge cases, and rejects malformed input.
func TestParseServerTool(t *testing.T) {
	tests := []struct {
		input      string
		wantServer string
		wantTool   string
		wantErr    bool
	}{
		{"github:create_issue", "github", "create_issue", false},
		{"my-server:some_tool", "my-server", "some_tool", false},
		// Tool name may contain colons — only the first colon is the separator
		{"server:tool:with:colons", "server", "tool:with:colons", false},
		// Missing colon → error
		{"notaservertool", "", "", true},
		// Empty server → error
		{":toolonly", "", "", true},
		// Empty tool → error
		{"serveronly:", "", "", true},
		// Completely empty → error
		{"", "", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			srv, tool, err := parseServerTool(tc.input)
			if tc.wantErr {
				assert.Error(t, err, "expected error for input %q", tc.input)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantServer, srv)
			assert.Equal(t, tc.wantTool, tool)
		})
	}
}

// TestGroupByServer verifies that groupByServer correctly groups
// (server, tool) pairs by their server name.
func TestGroupByServer(t *testing.T) {
	targets := []serverToolTarget{
		{server: "github", tool: "create_issue"},
		{server: "github", tool: "list_repos"},
		{server: "gitlab", tool: "merge_request"},
	}

	groups := groupByServer(targets)

	assert.Len(t, groups, 2)
	assert.ElementsMatch(t, []string{"create_issue", "list_repos"}, groups["github"])
	assert.ElementsMatch(t, []string{"merge_request"}, groups["gitlab"])
}

// TestApplyGlobalToolFilters_Status verifies that the status filter correctly
// identifies enabled vs disabled/config-denied tools.
func TestApplyGlobalToolFilters_Status(t *testing.T) {
	tools := []map[string]interface{}{
		{"name": "tool_a", "server_name": "srv", "disabled": false, "config_denied": false},
		{"name": "tool_b", "server_name": "srv", "disabled": true, "config_denied": false},
		{"name": "tool_c", "server_name": "srv", "disabled": false, "config_denied": true},
		{"name": "tool_d", "server_name": "srv", "disabled": false, "config_denied": false},
	}

	// Filter to disabled only
	got := applyGlobalToolFilters(tools, "disabled", "", "")
	assert.Len(t, got, 2, "expect tool_b (disabled) and tool_c (config_denied)")
	names := toolNames(got)
	assert.Contains(t, names, "tool_b")
	assert.Contains(t, names, "tool_c")

	// Filter to enabled only
	got = applyGlobalToolFilters(tools, "enabled", "", "")
	assert.Len(t, got, 2, "expect tool_a and tool_d")
	names = toolNames(got)
	assert.Contains(t, names, "tool_a")
	assert.Contains(t, names, "tool_d")

	// No filter → all returned
	got = applyGlobalToolFilters(tools, "", "", "")
	assert.Len(t, got, 4)
}

// TestApplyGlobalToolFilters_Risk verifies the risk filter matches annotations.
func TestApplyGlobalToolFilters_Risk(t *testing.T) {
	tools := []map[string]interface{}{
		{
			"name":        "read_file",
			"server_name": "srv",
			"annotations": map[string]interface{}{"operation_type": "read"},
		},
		{
			"name":        "write_file",
			"server_name": "srv",
			"annotations": map[string]interface{}{"operation_type": "write"},
		},
		{
			"name":        "delete_repo",
			"server_name": "srv",
			"annotations": map[string]interface{}{"operation_type": "destructive"},
		},
	}

	got := applyGlobalToolFilters(tools, "", "read", "")
	require.Len(t, got, 1)
	assert.Equal(t, "read_file", got[0]["name"])

	got = applyGlobalToolFilters(tools, "", "write", "")
	require.Len(t, got, 1)
	assert.Equal(t, "write_file", got[0]["name"])
}

// TestApplyGlobalToolFilters_Approval verifies the approval filter.
func TestApplyGlobalToolFilters_Approval(t *testing.T) {
	tools := []map[string]interface{}{
		{"name": "approved_tool", "server_name": "srv", "approval_status": "approved"},
		{"name": "pending_tool", "server_name": "srv", "approval_status": "pending"},
		{"name": "changed_tool", "server_name": "srv", "approval_status": "changed"},
	}

	got := applyGlobalToolFilters(tools, "", "", "pending")
	require.Len(t, got, 1)
	assert.Equal(t, "pending_tool", got[0]["name"])

	got = applyGlobalToolFilters(tools, "", "", "approved")
	require.Len(t, got, 1)
	assert.Equal(t, "approved_tool", got[0]["name"])
}

// TestOutputGlobalTools_JSONShape verifies -o json emits a valid JSON array
// with the expected fields.
func TestOutputGlobalTools_JSONShape(t *testing.T) {
	tools := []map[string]interface{}{
		{
			"name":            "create_issue",
			"server_name":     "github",
			"description":     "Create a new GitHub issue",
			"approval_status": "approved",
			"disabled":        false,
			"config_denied":   false,
			"usage":           float64(42),
		},
	}

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	globalOutputFormat = "json"
	globalJSONOutput = false
	err := outputGlobalTools(tools)

	w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	outStr := buf.String()

	require.NoError(t, err)

	var parsed []map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(outStr), &parsed), "output must be valid JSON array")
	require.Len(t, parsed, 1)
	assert.Equal(t, "create_issue", parsed[0]["name"])
	assert.Equal(t, "github", parsed[0]["server_name"])
}

// TestOutputGlobalTools_TableColumns verifies that table output contains the
// expected column headers for the global view.
func TestOutputGlobalTools_TableColumns(t *testing.T) {
	tools := []map[string]interface{}{
		{
			"name":            "create_issue",
			"server_name":     "github",
			"description":     "Create a new issue",
			"approval_status": "approved",
			"disabled":        false,
			"config_denied":   false,
			"usage":           float64(7),
		},
	}

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	globalOutputFormat = "table"
	globalJSONOutput = false
	err := outputGlobalTools(tools)

	w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	outStr := buf.String()

	require.NoError(t, err)
	assert.Contains(t, outStr, "NAME", "table must contain NAME column")
	assert.Contains(t, outStr, "SERVER", "table must contain SERVER column")
	assert.Contains(t, outStr, "STATE", "table must contain STATE column")
	assert.Contains(t, outStr, "APPROVAL", "table must contain APPROVAL column")
	assert.Contains(t, outStr, "USAGE", "table must contain USAGE column")
	assert.Contains(t, outStr, "create_issue", "table must contain tool name")
	assert.Contains(t, outStr, "github", "table must contain server name")
}

// TestGetToolsCommand_Subcommands verifies the tools command group has the
// expected subcommands including the new enable/disable.
func TestGetToolsCommand_Subcommands(t *testing.T) {
	cmd := GetToolsCommand()
	assert.Equal(t, "tools", cmd.Use)

	subCmds := cmd.Commands()
	names := make(map[string]bool)
	for _, sub := range subCmds {
		names[sub.Name()] = true
	}

	assert.True(t, names["list"], "tools command should have 'list' subcommand")
	assert.True(t, names["enable"], "tools command should have 'enable' subcommand")
	assert.True(t, names["disable"], "tools command should have 'disable' subcommand")
}

// TestToolsListCmd_ServerOptional verifies that --server flag is no longer
// required on 'tools list'.
func TestToolsListCmd_ServerOptional(t *testing.T) {
	// The --server flag should not be marked as required.
	// If it were required, calling without it would return an error from cobra.
	toolsListCmd.ResetFlags()
	initToolsFlags() // re-register flags to a clean state is tricky; check via annotation

	flag := toolsListCmd.Flags().Lookup("server")
	require.NotNil(t, flag, "server flag must exist")

	// Cobra marks required flags via the "cobra_annotation_bash_completion_one_required_flag"
	// annotation. Verify it is NOT set (flag is optional).
	annotations := flag.Annotations
	_, hasRequired := annotations["cobra_annotation_bash_completion_one_required_flag"]
	assert.False(t, hasRequired, "--server flag must NOT be marked as required for global listing")
}

// TestToolsEnableCmd_ArgValidation verifies enable/disable require at least one arg.
func TestToolsEnableCmd_ArgValidation(t *testing.T) {
	enableCmd := toolsEnableCmd
	require.NotNil(t, enableCmd)
	assert.Error(t, enableCmd.Args(enableCmd, []string{}), "enable must reject zero args")
	assert.NoError(t, enableCmd.Args(enableCmd, []string{"server:tool"}), "enable must accept one arg")
	assert.NoError(t, enableCmd.Args(enableCmd, []string{"server:tool", "other:tool2"}), "enable must accept multiple args")

	disableCmd := toolsDisableCmd
	require.NotNil(t, disableCmd)
	assert.Error(t, disableCmd.Args(disableCmd, []string{}), "disable must reject zero args")
	assert.NoError(t, disableCmd.Args(disableCmd, []string{"server:tool"}), "disable must accept one arg")
}

// ---------------------------------------------------------------------------
// CLI-JSON (v0.51.0-rc.1 QA): standalone mode must keep stdout clean for
// machine formats — banners/progress go to stderr.
// ---------------------------------------------------------------------------

// runStandaloneCapture runs runToolsListStandalone against a server whose
// command does not exist (Connect fails fast, no live MCP server needed) and
// returns everything written to stdout and stderr. The banner prints before
// Connect, so it is exercised regardless of the connection failure.
func runStandaloneCapture(t *testing.T, format string) (stdoutStr, stderrStr string) {
	t.Helper()

	oldStdout := os.Stdout
	oldStderr := os.Stderr
	rOut, wOut, err := os.Pipe()
	require.NoError(t, err)
	rErr, wErr, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = wOut
	os.Stderr = wErr

	oldFormat := globalOutputFormat
	oldJSON := globalJSONOutput
	globalOutputFormat = format
	globalJSONOutput = false
	t.Cleanup(func() {
		globalOutputFormat = oldFormat
		globalJSONOutput = oldJSON
	})

	cfg := &config.Config{Servers: []*config.ServerConfig{{
		Name:     "everything",
		Command:  "/nonexistent-mcpproxy-test-cmd",
		Protocol: "stdio",
		Enabled:  true,
	}}} // DataDir empty → bbolt storage is skipped

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Connect fails (command does not exist); the returned error is irrelevant
	// here — we only assert on where the banner/progress text landed.
	_ = runToolsListStandalone(ctx, "everything", cfg, zap.NewNop())

	wOut.Close()
	wErr.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr

	var bufOut, bufErr bytes.Buffer
	_, _ = bufOut.ReadFrom(rOut)
	_, _ = bufErr.ReadFrom(rErr)
	return bufOut.String(), bufErr.String()
}

// TestRunToolsListStandalone_MachineModeStdoutClean is the QA finding: with
// -o json, stdout must contain only the JSON payload — no human banner or
// progress text.
func TestRunToolsListStandalone_MachineModeStdoutClean(t *testing.T) {
	stdoutStr, stderrStr := runStandaloneCapture(t, "json")

	assert.NotContains(t, stdoutStr, "MCP Tools List", "banner must not pollute stdout in json mode")
	assert.NotContains(t, stdoutStr, "Connecting to server", "progress must not pollute stdout in json mode")
	assert.NotContains(t, stdoutStr, "Disconnecting from server", "progress must not pollute stdout in json mode")
	assert.Contains(t, stderrStr, "MCP Tools List", "banner should move to stderr")
}

// TestRunToolsListStandalone_TableModeBannerOnStderr locks the
// unconditional-stderr convention: banners go to stderr in table mode too, so
// `-o table | tee file` stays clean.
func TestRunToolsListStandalone_TableModeBannerOnStderr(t *testing.T) {
	stdoutStr, stderrStr := runStandaloneCapture(t, "table")

	assert.NotContains(t, stdoutStr, "MCP Tools List", "banner must not pollute stdout in table mode")
	assert.NotContains(t, stdoutStr, "Connecting to server", "progress must not pollute stdout in table mode")
	assert.Contains(t, stderrStr, "MCP Tools List", "banner should be on stderr")
	assert.Contains(t, stderrStr, "Connecting to server", "progress should be on stderr")
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func toolNames(tools []map[string]interface{}) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if n, ok := t["name"].(string); ok {
			names = append(names, n)
		}
	}
	return names
}
