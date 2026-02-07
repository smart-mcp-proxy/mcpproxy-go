package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	hookCmd = &cobra.Command{
		Use:   "hook",
		Short: "Agent hook integration commands",
		Long:  "Commands for managing agent hook integration with data flow security",
	}

	hookEvaluateCmd = &cobra.Command{
		Use:   "evaluate",
		Short: "Evaluate a tool call for data flow security",
		Long: `Evaluate a tool call via the mcpproxy data flow security engine.
This command is designed to be called by agent hooks (e.g., Claude Code PreToolUse/PostToolUse).

It reads a JSON payload from stdin, sends it to the mcpproxy daemon via Unix socket,
and outputs a Claude Code hook protocol response (approve/block/ask).

If the daemon is unreachable, it fails open (returns approve) to avoid blocking the agent.

Example hook configuration for Claude Code:
  PreToolUse: mcpproxy hook evaluate --event PreToolUse
  PostToolUse: mcpproxy hook evaluate --event PostToolUse`,
		RunE:         runHookEvaluate,
		SilenceUsage: true,
	}

	hookEvaluateEvent string

	hookInstallCmd = &cobra.Command{
		Use:   "install",
		Short: "Install agent hook integration",
		Long: `Install data flow security hooks into the agent's configuration.

Currently supports Claude Code. The hooks enable full visibility into agent-internal
tool chains (e.g., Read→WebFetch) that proxy-only mode cannot see.

Example:
  mcpproxy hook install --agent claude-code --scope project`,
		RunE:         runHookInstall,
		SilenceUsage: true,
	}

	hookUninstallCmd = &cobra.Command{
		Use:   "uninstall",
		Short: "Remove agent hook integration",
		Long: `Remove mcpproxy data flow security hooks from the agent's configuration.
Other hooks and settings are preserved.

Example:
  mcpproxy hook uninstall --agent claude-code --scope project`,
		RunE:         runHookUninstall,
		SilenceUsage: true,
	}

	hookStatusCmd = &cobra.Command{
		Use:   "status",
		Short: "Show hook integration status",
		Long: `Show the current status of agent hook integration including:
- Whether hooks are installed
- Agent type and scope
- Whether the mcpproxy daemon is reachable

Example:
  mcpproxy hook status --agent claude-code --scope project`,
		RunE:         runHookStatus,
		SilenceUsage: true,
	}

	hookInstallAgent string
	hookInstallScope string
)

func init() {
	hookEvaluateCmd.Flags().StringVar(&hookEvaluateEvent, "event", "", "Hook event type (PreToolUse or PostToolUse)")

	hookInstallCmd.Flags().StringVar(&hookInstallAgent, "agent", "", "Agent type (required, e.g., claude-code)")
	hookInstallCmd.Flags().StringVar(&hookInstallScope, "scope", "project", "Scope for hook installation (project or user)")
	_ = hookInstallCmd.MarkFlagRequired("agent")

	hookUninstallCmd.Flags().StringVar(&hookInstallAgent, "agent", "", "Agent type (required, e.g., claude-code)")
	hookUninstallCmd.Flags().StringVar(&hookInstallScope, "scope", "project", "Scope for hook installation (project or user)")
	_ = hookUninstallCmd.MarkFlagRequired("agent")

	hookStatusCmd.Flags().StringVar(&hookInstallAgent, "agent", "", "Agent type (default: claude-code)")
	hookStatusCmd.Flags().StringVar(&hookInstallScope, "scope", "project", "Scope for hook installation (project or user)")

	hookCmd.AddCommand(hookEvaluateCmd)
	hookCmd.AddCommand(hookInstallCmd)
	hookCmd.AddCommand(hookUninstallCmd)
	hookCmd.AddCommand(hookStatusCmd)
}

// hookEvalResult represents the result of a hook evaluation.
type hookEvalResult struct {
	Decision string
	Reason   string
}

// runHookEvaluate is the main entry point for the hook evaluate CLI command.
// Fast startup path — no config loading, no file logger.
func runHookEvaluate(cmd *cobra.Command, args []string) error {
	// Read JSON from stdin
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		// Fail-open: output approve on any error
		writeClaudeCodeResponse("approve", "")
		return nil
	}

	// Detect socket path
	socketPath := detectHookSocketPath()

	// Evaluate via daemon
	result := evaluateViaSocket(socketPath, input)

	// Translate and output Claude Code protocol response
	claudeDecision := translateToClaudeCode(result.Decision)
	writeClaudeCodeResponse(claudeDecision, result.Reason)

	return nil
}

// translateToClaudeCode maps internal policy decisions to Claude Code hook protocol.
func translateToClaudeCode(decision string) string {
	switch decision {
	case "deny":
		return "block"
	case "ask":
		return "ask"
	case "allow", "warn":
		return "approve"
	default:
		return "approve"
	}
}

// buildClaudeCodeResponse constructs the Claude Code hook protocol response map.
func buildClaudeCodeResponse(decision, reason string) map[string]interface{} {
	resp := map[string]interface{}{
		"decision": decision,
	}
	if reason != "" {
		resp["reason"] = reason
	}
	return resp
}

// writeClaudeCodeResponse writes the Claude Code hook protocol response to stdout.
func writeClaudeCodeResponse(decision, reason string) {
	resp := buildClaudeCodeResponse(decision, reason)
	json.NewEncoder(os.Stdout).Encode(resp)
}

// evaluateViaSocket sends the hook evaluation request to the mcpproxy daemon via Unix socket.
// Returns a fail-open default (approve) on any error.
func evaluateViaSocket(socketPath string, body []byte) hookEvalResult {
	failOpen := hookEvalResult{Decision: "approve", Reason: ""}

	// Create HTTP client that dials through the Unix socket
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
		Timeout: 5 * time.Second,
	}

	// POST to the hook evaluate endpoint
	req, err := http.NewRequest("POST", "http://localhost/api/v1/hooks/evaluate", bytes.NewReader(body))
	if err != nil {
		return failOpen
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return failOpen
	}
	defer resp.Body.Close()

	// Non-200 status: fail-open
	if resp.StatusCode != http.StatusOK {
		return failOpen
	}

	// Parse response
	var evalResp struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&evalResp); err != nil {
		return failOpen
	}

	return hookEvalResult{
		Decision: evalResp.Decision,
		Reason:   evalResp.Reason,
	}
}

// detectHookSocketPath detects the Unix socket path for the mcpproxy daemon.
// Uses the standard ~/.mcpproxy/mcpproxy.sock path.
func detectHookSocketPath() string {
	// Check environment variable
	if envPath := os.Getenv("MCPPROXY_SOCKET"); envPath != "" {
		return envPath
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".mcpproxy", "mcpproxy.sock")
}

// === Phase 8: Hook Install/Uninstall/Status (T071-T074) ===

// hookToolMatcher is the regex pattern matching all tools that should be monitored.
const hookToolMatcher = "Read|Glob|Grep|Bash|Write|Edit|WebFetch|WebSearch|Task|mcp__.*"

// claudeCodeHookEntry represents a single hook entry in Claude Code settings.
type claudeCodeHookEntry struct {
	Matcher string `json:"matcher"`
	Command string `json:"command"`
	Async   bool   `json:"async,omitempty"`
}

// claudeCodeHooks represents the hooks section of Claude Code settings.
type claudeCodeHooks struct {
	PreToolUse  []claudeCodeHookEntry `json:"PreToolUse"`
	PostToolUse []claudeCodeHookEntry `json:"PostToolUse"`
}

// hookStatusInfo reports the current hook integration status.
type hookStatusInfo struct {
	Installed      bool
	DaemonReachable bool
	AgentType      string
}

// generateClaudeCodeHooks returns the hook configuration for Claude Code.
func generateClaudeCodeHooks() claudeCodeHooks {
	return claudeCodeHooks{
		PreToolUse: []claudeCodeHookEntry{
			{
				Matcher: hookToolMatcher,
				Command: "mcpproxy hook evaluate --event PreToolUse",
			},
		},
		PostToolUse: []claudeCodeHookEntry{
			{
				Matcher: hookToolMatcher,
				Command: "mcpproxy hook evaluate --event PostToolUse",
				Async:   true,
			},
		},
	}
}

// installHooksToFile installs mcpproxy hooks into a Claude Code settings file.
// Creates the file and parent directories if they don't exist.
// Merges with existing settings, preserving other hooks and configuration.
func installHooksToFile(settingsPath string) error {
	// Ensure parent directory exists
	dir := filepath.Dir(settingsPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}

	// Read existing settings or start fresh
	settings := make(map[string]interface{})
	if data, err := os.ReadFile(settingsPath); err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("parse existing settings: %w", err)
		}
	}

	// Generate our hooks
	hooks := generateClaudeCodeHooks()

	// Get or create the hooks section
	hooksSection, _ := settings["hooks"].(map[string]interface{})
	if hooksSection == nil {
		hooksSection = make(map[string]interface{})
	}

	// Merge PreToolUse: keep existing non-mcpproxy hooks, add ours
	preList := filterNonMCPProxyHooks(hooksSection, "PreToolUse")
	for _, h := range hooks.PreToolUse {
		preList = append(preList, map[string]interface{}{
			"matcher": h.Matcher,
			"command": h.Command,
		})
	}
	hooksSection["PreToolUse"] = preList

	// Merge PostToolUse: keep existing non-mcpproxy hooks, add ours
	postList := filterNonMCPProxyHooks(hooksSection, "PostToolUse")
	for _, h := range hooks.PostToolUse {
		entry := map[string]interface{}{
			"matcher": h.Matcher,
			"command": h.Command,
		}
		if h.Async {
			entry["async"] = true
		}
		postList = append(postList, entry)
	}
	hooksSection["PostToolUse"] = postList

	settings["hooks"] = hooksSection

	// Write back
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	return os.WriteFile(settingsPath, data, 0644)
}

// uninstallHooksFromFile removes mcpproxy hooks from a Claude Code settings file.
// Preserves other hooks and settings.
func uninstallHooksFromFile(settingsPath string) error {
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Nothing to uninstall
		}
		return fmt.Errorf("read settings: %w", err)
	}

	settings := make(map[string]interface{})
	if err := json.Unmarshal(data, &settings); err != nil {
		return fmt.Errorf("parse settings: %w", err)
	}

	hooksSection, ok := settings["hooks"].(map[string]interface{})
	if !ok {
		return nil // No hooks section
	}

	// Filter out mcpproxy hooks from each event type
	for _, eventType := range []string{"PreToolUse", "PostToolUse"} {
		filtered := filterNonMCPProxyHooks(hooksSection, eventType)
		if len(filtered) > 0 {
			hooksSection[eventType] = filtered
		} else {
			delete(hooksSection, eventType)
		}
	}

	settings["hooks"] = hooksSection

	// Write back
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	return os.WriteFile(settingsPath, out, 0644)
}

// filterNonMCPProxyHooks returns hook entries from a hooks section that don't belong to mcpproxy.
func filterNonMCPProxyHooks(hooksSection map[string]interface{}, eventType string) []interface{} {
	var result []interface{}
	list, ok := hooksSection[eventType].([]interface{})
	if !ok {
		return result
	}
	for _, entry := range list {
		entryMap, ok := entry.(map[string]interface{})
		if !ok {
			result = append(result, entry)
			continue
		}
		cmd, _ := entryMap["command"].(string)
		if !strings.Contains(cmd, "mcpproxy") {
			result = append(result, entry)
		}
	}
	return result
}

// checkHookInstalled checks whether mcpproxy hooks are installed in the settings file.
func checkHookInstalled(settingsPath string) bool {
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return false
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return false
	}

	hooksSection, ok := settings["hooks"].(map[string]interface{})
	if !ok {
		return false
	}

	// Check if any hook entry contains "mcpproxy"
	for _, eventType := range []string{"PreToolUse", "PostToolUse"} {
		list, ok := hooksSection[eventType].([]interface{})
		if !ok {
			continue
		}
		for _, entry := range list {
			entryMap, ok := entry.(map[string]interface{})
			if !ok {
				continue
			}
			cmd, _ := entryMap["command"].(string)
			if strings.Contains(cmd, "mcpproxy") {
				return true
			}
		}
	}
	return false
}

// resolveSettingsPath resolves the settings file path based on the scope.
func resolveSettingsPath(scope string) (string, error) {
	switch scope {
	case "project":
		// Use current working directory
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get working directory: %w", err)
		}
		return filepath.Join(cwd, ".claude", "settings.json"), nil
	case "user":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("get home directory: %w", err)
		}
		return filepath.Join(home, ".claude", "settings.json"), nil
	default:
		return "", fmt.Errorf("invalid scope %q: must be \"project\" or \"user\"", scope)
	}
}

// getHookStatusInfo returns the current hook integration status.
func getHookStatusInfo(settingsPath, socketPath string) hookStatusInfo {
	status := hookStatusInfo{
		Installed:      checkHookInstalled(settingsPath),
		AgentType:      "claude-code",
		DaemonReachable: false,
	}

	// Check daemon reachability by attempting a connection
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err == nil {
		conn.Close()
		status.DaemonReachable = true
	}

	return status
}

// runHookInstall handles the `mcpproxy hook install` command.
func runHookInstall(cmd *cobra.Command, args []string) error {
	if hookInstallAgent != "claude-code" {
		return fmt.Errorf("unsupported agent %q: only \"claude-code\" is currently supported", hookInstallAgent)
	}

	settingsPath, err := resolveSettingsPath(hookInstallScope)
	if err != nil {
		return err
	}

	if err := installHooksToFile(settingsPath); err != nil {
		return fmt.Errorf("install hooks: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Hooks installed to %s\n", settingsPath)
	fmt.Fprintf(cmd.OutOrStdout(), "Run 'mcpproxy hook status' to verify the installation.\n")
	return nil
}

// runHookUninstall handles the `mcpproxy hook uninstall` command.
func runHookUninstall(cmd *cobra.Command, args []string) error {
	if hookInstallAgent != "claude-code" {
		return fmt.Errorf("unsupported agent %q: only \"claude-code\" is currently supported", hookInstallAgent)
	}

	settingsPath, err := resolveSettingsPath(hookInstallScope)
	if err != nil {
		return err
	}

	if err := uninstallHooksFromFile(settingsPath); err != nil {
		return fmt.Errorf("uninstall hooks: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Hooks removed from %s\n", settingsPath)
	return nil
}

// runHookStatus handles the `mcpproxy hook status` command.
func runHookStatus(cmd *cobra.Command, args []string) error {
	agent := hookInstallAgent
	if agent == "" {
		agent = "claude-code"
	}
	if agent != "claude-code" {
		return fmt.Errorf("unsupported agent %q: only \"claude-code\" is currently supported", agent)
	}

	settingsPath, err := resolveSettingsPath(hookInstallScope)
	if err != nil {
		return err
	}

	socketPath := detectHookSocketPath()
	status := getHookStatusInfo(settingsPath, socketPath)

	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "Agent:           %s\n", status.AgentType)
	fmt.Fprintf(w, "Scope:           %s\n", hookInstallScope)
	fmt.Fprintf(w, "Settings:        %s\n", settingsPath)
	if status.Installed {
		fmt.Fprintf(w, "Hooks installed: yes\n")
	} else {
		fmt.Fprintf(w, "Hooks installed: no\n")
	}
	if status.DaemonReachable {
		fmt.Fprintf(w, "Daemon:          reachable\n")
	} else {
		fmt.Fprintf(w, "Daemon:          not reachable\n")
	}

	return nil
}
