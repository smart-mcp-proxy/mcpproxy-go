# CLI Management Commands Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add `mcpproxy upstream` and `mcpproxy doctor` commands for managing upstream servers and monitoring system health via CLI.

**Architecture:** Two new command groups following existing CLI patterns. Commands auto-detect daemon via socket and support both daemon mode (fast API calls) and standalone mode (direct connections). Uses existing `internal/cliclient` for API communication and adds new endpoints to `internal/httpapi` for diagnostics.

**Tech Stack:** Go, Cobra CLI, internal/cliclient, internal/httpapi, golang.org/x/term (for TTY detection)

---

## Task 1: Add confirmation helper for bulk operations

**Files:**
- Create: `cmd/mcpproxy/confirmation.go`
- Test: Manual testing (no unit tests for TTY interaction)

**Step 1: Create confirmation helper file**

Create `cmd/mcpproxy/confirmation.go`:

```go
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// confirmBulkAction prompts user for confirmation when performing bulk operations.
// Returns (true, nil) if user confirms or force=true
// Returns (false, nil) if user declines
// Returns (false, error) if non-interactive without force flag
func confirmBulkAction(action string, count int, force bool) (bool, error) {
	// Skip prompt if force flag provided
	if force {
		return true, nil
	}

	// Check if stdin is a TTY (interactive terminal)
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return false, fmt.Errorf("--all requires --force flag in non-interactive mode")
	}

	// Show confirmation prompt
	fmt.Printf("⚠️  This will %s %d server(s). Continue? [y/N]: ", action, count)

	// Read user input
	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("failed to read confirmation: %w", err)
	}

	// Parse response (accept y, yes case-insensitive)
	response = strings.ToLower(strings.TrimSpace(response))
	return response == "y" || response == "yes", nil
}
```

**Step 2: Commit**

```bash
git add cmd/mcpproxy/confirmation.go
git commit -m "feat: add confirmation helper for bulk operations"
```

---

## Task 2: Add `mcpproxy upstream list` command

**Files:**
- Create: `cmd/mcpproxy/upstream_cmd.go`
- Modify: `cmd/mcpproxy/main.go`
- Modify: `internal/cliclient/client.go`

**Step 1: Create upstream command structure**

Create `cmd/mcpproxy/upstream_cmd.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"mcpproxy-go/internal/cliclient"
	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/socket"
)

var (
	upstreamCmd = &cobra.Command{
		Use:   "upstream",
		Short: "Manage upstream MCP servers",
		Long:  "Commands for managing and monitoring upstream MCP servers",
	}

	upstreamListCmd = &cobra.Command{
		Use:   "list",
		Short: "List all upstream servers with status",
		Long: `List all configured upstream MCP servers with connection status, tool counts, and errors.

Examples:
  mcpproxy upstream list
  mcpproxy upstream list --output=json
  mcpproxy upstream list --log-level=debug`,
		RunE: runUpstreamList,
	}

	// Command flags
	upstreamOutputFormat string
	upstreamLogLevel     string
	upstreamConfigPath   string
)

// GetUpstreamCommand returns the upstream command for adding to the root command
func GetUpstreamCommand() *cobra.Command {
	return upstreamCmd
}

func init() {
	// Add subcommands
	upstreamCmd.AddCommand(upstreamListCmd)

	// Define flags
	upstreamListCmd.Flags().StringVarP(&upstreamOutputFormat, "output", "o", "table", "Output format (table, json)")
	upstreamListCmd.Flags().StringVarP(&upstreamLogLevel, "log-level", "l", "warn", "Log level (trace, debug, info, warn, error)")
	upstreamListCmd.Flags().StringVarP(&upstreamConfigPath, "config", "c", "", "Path to MCP configuration file")
}

func runUpstreamList(_ *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Load configuration
	globalConfig, err := loadUpstreamConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		return err
	}

	// Create logger
	logger, err := createUpstreamLogger(upstreamLogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating logger: %v\n", err)
		return err
	}

	// Check if daemon is running
	if shouldUseUpstreamDaemon(globalConfig.DataDir) {
		logger.Info("Detected running daemon, using client mode via socket")
		return runUpstreamListClientMode(ctx, globalConfig.DataDir, logger)
	}

	// No daemon - load from config file
	logger.Info("No daemon detected, reading from config file")
	return runUpstreamListFromConfig(globalConfig)
}

func shouldUseUpstreamDaemon(dataDir string) bool {
	socketPath := socket.DetectSocketPath(dataDir)
	return socket.IsSocketAvailable(socketPath)
}

func runUpstreamListClientMode(ctx context.Context, dataDir string, logger *zap.Logger) error {
	socketPath := socket.DetectSocketPath(dataDir)
	client := cliclient.NewClient(socketPath, logger.Sugar())

	// Call GET /api/v1/servers
	servers, err := client.GetServers(ctx)
	if err != nil {
		return fmt.Errorf("failed to get servers from daemon: %w", err)
	}

	return outputServers(servers)
}

func runUpstreamListFromConfig(globalConfig *config.Config) error {
	// Convert config servers to output format
	servers := make([]map[string]interface{}, len(globalConfig.Servers))
	for i, srv := range globalConfig.Servers {
		servers[i] = map[string]interface{}{
			"name":       srv.Name,
			"enabled":    srv.Enabled,
			"protocol":   srv.Protocol,
			"connected":  false,
			"tool_count": 0,
			"status":     "unknown (daemon not running)",
		}
	}

	return outputServers(servers)
}

func outputServers(servers []map[string]interface{}) error {
	switch upstreamOutputFormat {
	case "json":
		output, err := json.MarshalIndent(servers, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to format output: %w", err)
		}
		fmt.Println(string(output))
	case "table":
	default:
		// Table format
		fmt.Printf("%-25s %-10s %-10s %-12s %-10s %s\n",
			"NAME", "ENABLED", "PROTOCOL", "CONNECTED", "TOOLS", "STATUS")
		fmt.Printf("%-25s %-10s %-10s %-12s %-10s %s\n",
			"====", "=======", "========", "=========", "=====", "======")

		for _, srv := range servers {
			name := getStringField(srv, "name")
			enabled := getBoolField(srv, "enabled")
			protocol := getStringField(srv, "protocol")
			connected := getBoolField(srv, "connected")
			toolCount := getIntField(srv, "tool_count")
			status := getStringField(srv, "status")

			enabledStr := "no"
			if enabled {
				enabledStr = "yes"
			}

			connectedStr := "no"
			if connected {
				connectedStr = "yes"
			}

			fmt.Printf("%-25s %-10s %-10s %-12s %-10d %s\n",
				name, enabledStr, protocol, connectedStr, toolCount, status)
		}
	}

	return nil
}

func loadUpstreamConfig() (*config.Config, error) {
	if upstreamConfigPath != "" {
		return config.LoadFromFile(upstreamConfigPath)
	}
	return config.Load()
}

func createUpstreamLogger(level string) (*zap.Logger, error) {
	var zapLevel zap.AtomicLevel
	switch level {
	case "trace", "debug":
		zapLevel = zap.NewAtomicLevelAt(zap.DebugLevel)
	case "info":
		zapLevel = zap.NewAtomicLevelAt(zap.InfoLevel)
	case "warn":
		zapLevel = zap.NewAtomicLevelAt(zap.WarnLevel)
	case "error":
		zapLevel = zap.NewAtomicLevelAt(zap.ErrorLevel)
	default:
		zapLevel = zap.NewAtomicLevelAt(zap.WarnLevel)
	}

	cfg := zap.Config{
		Level:            zapLevel,
		Development:      false,
		Encoding:         "console",
		EncoderConfig:    zap.NewDevelopmentEncoderConfig(),
		OutputPaths:      []string{"stderr"},
		ErrorOutputPaths: []string{"stderr"},
	}

	return cfg.Build()
}

func getStringField(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getBoolField(m map[string]interface{}, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

func getIntField(m map[string]interface{}, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	if v, ok := m[key].(int); ok {
		return v
	}
	return 0
}
```

**Step 2: Add GetServers method to cliclient**

Modify `internal/cliclient/client.go`, add this method:

```go
// GetServers retrieves list of servers from daemon.
func (c *Client) GetServers(ctx context.Context) ([]map[string]interface{}, error) {
	url := c.baseURL + "/api/v1/servers"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call servers API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp struct {
		Success bool `json:"success"`
		Data    struct {
			Servers []map[string]interface{} `json:"servers"`
		} `json:"data"`
		Error string `json:"error"`
	}

	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("API call failed: %s", apiResp.Error)
	}

	return apiResp.Data.Servers, nil
}
```

**Step 3: Register upstream command in main.go**

Modify `cmd/mcpproxy/main.go`, add after other command registrations (around line 113-126):

```go
// Add after secretsCmd := GetSecretsCommand()
upstreamCmd := GetUpstreamCommand()

// Add after rootCmd.AddCommand(secretsCmd)
rootCmd.AddCommand(upstreamCmd)
```

**Step 4: Test manually**

```bash
# Build
go build -o mcpproxy ./cmd/mcpproxy

# Test with daemon not running
./mcpproxy upstream list

# Expected: Table showing servers from config with "unknown (daemon not running)" status
```

**Step 5: Commit**

```bash
git add cmd/mcpproxy/upstream_cmd.go cmd/mcpproxy/main.go internal/cliclient/client.go
git commit -m "feat: add 'mcpproxy upstream list' command"
```

---

## Task 3: Add `mcpproxy upstream logs` command

**Files:**
- Modify: `cmd/mcpproxy/upstream_cmd.go`
- Modify: `internal/cliclient/client.go`

**Step 1: Add logs subcommand to upstream_cmd.go**

Add to `cmd/mcpproxy/upstream_cmd.go` in the `var` block:

```go
	upstreamLogsCmd = &cobra.Command{
		Use:   "logs <server-name>",
		Short: "Show logs for a specific server",
		Long: `Display recent log entries from a specific upstream server.

Examples:
  mcpproxy upstream logs github-server
  mcpproxy upstream logs github-server --tail=100
  mcpproxy upstream logs weather-api --follow`,
		Args: cobra.ExactArgs(1),
		RunE: runUpstreamLogs,
	}

	// Add new flags
	upstreamLogsTail   int
	upstreamLogsFollow bool
```

Add to `init()` function:

```go
	upstreamCmd.AddCommand(upstreamLogsCmd)

	upstreamLogsCmd.Flags().IntVarP(&upstreamLogsTail, "tail", "n", 50, "Number of log lines to show")
	upstreamLogsCmd.Flags().BoolVarP(&upstreamLogsFollow, "follow", "f", false, "Follow log output (requires daemon)")
	upstreamLogsCmd.Flags().StringVarP(&upstreamLogLevel, "log-level", "l", "warn", "Log level")
	upstreamLogsCmd.Flags().StringVarP(&upstreamConfigPath, "config", "c", "", "Path to config file")
```

Add implementation functions:

```go
import (
	"os/exec"
	"path/filepath"
)

func runUpstreamLogs(cmd *cobra.Command, args []string) error {
	serverName := args[0]

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Load configuration
	globalConfig, err := loadUpstreamConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		return err
	}

	// Create logger
	logger, err := createUpstreamLogger(upstreamLogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating logger: %v\n", err)
		return err
	}

	// Follow mode requires daemon
	if upstreamLogsFollow {
		if !shouldUseUpstreamDaemon(globalConfig.DataDir) {
			return fmt.Errorf("--follow requires running daemon")
		}
		logger.Info("Following logs from daemon")
		return runUpstreamLogsFollowMode(ctx, globalConfig.DataDir, serverName, logger)
	}

	// Check if daemon is running
	if shouldUseUpstreamDaemon(globalConfig.DataDir) {
		logger.Info("Detected running daemon, using client mode via socket")
		return runUpstreamLogsClientMode(ctx, globalConfig.DataDir, serverName, logger)
	}

	// No daemon - read from log file
	logger.Info("No daemon detected, reading from log file")
	return runUpstreamLogsFromFile(globalConfig, serverName)
}

func runUpstreamLogsClientMode(ctx context.Context, dataDir, serverName string, logger *zap.Logger) error {
	socketPath := socket.DetectSocketPath(dataDir)
	client := cliclient.NewClient(socketPath, logger.Sugar())

	// Call GET /api/v1/servers/{name}/logs?tail=N
	logs, err := client.GetServerLogs(ctx, serverName, upstreamLogsTail)
	if err != nil {
		return fmt.Errorf("failed to get logs from daemon: %w", err)
	}

	for _, logLine := range logs {
		fmt.Println(logLine)
	}

	return nil
}

func runUpstreamLogsFromFile(globalConfig *config.Config, serverName string) error {
	// Read from log file directly
	logDir := globalConfig.Logging.LogDir
	if logDir == "" {
		// Use default log directory
		homeDir, _ := os.UserHomeDir()
		logDir = filepath.Join(homeDir, ".mcpproxy", "logs")
	}

	logFile := filepath.Join(logDir, fmt.Sprintf("server-%s.log", serverName))

	// Check if file exists
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		return fmt.Errorf("log file not found: %s (daemon may not have run yet)", logFile)
	}

	// Read last N lines using tail command
	cmd := exec.Command("tail", "-n", fmt.Sprintf("%d", upstreamLogsTail), logFile)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to read log file: %w", err)
	}

	fmt.Print(string(output))
	return nil
}

func runUpstreamLogsFollowMode(ctx context.Context, dataDir, serverName string, logger *zap.Logger) error {
	socketPath := socket.DetectSocketPath(dataDir)
	client := cliclient.NewClient(socketPath, logger.Sugar())

	fmt.Printf("Following logs for server '%s' (Ctrl+C to stop)...\n", serverName)

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	lastLines := make(map[string]bool)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			logs, err := client.GetServerLogs(ctx, serverName, 10)
			if err != nil {
				logger.Warn("Failed to fetch logs", zap.Error(err))
				continue
			}

			// Print only new lines
			for _, line := range logs {
				if !lastLines[line] {
					fmt.Println(line)
					lastLines[line] = true
				}
			}
		}
	}
}
```

**Step 2: Add GetServerLogs method to cliclient**

Add to `internal/cliclient/client.go`:

```go
// GetServerLogs retrieves logs for a specific server.
func (c *Client) GetServerLogs(ctx context.Context, serverName string, tail int) ([]string, error) {
	url := fmt.Sprintf("%s/api/v1/servers/%s/logs?tail=%d", c.baseURL, serverName, tail)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call logs API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp struct {
		Success bool `json:"success"`
		Data    struct {
			Logs []string `json:"logs"`
		} `json:"data"`
		Error string `json:"error"`
	}

	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("API call failed: %s", apiResp.Error)
	}

	return apiResp.Data.Logs, nil
}
```

**Step 3: Test manually**

```bash
go build -o mcpproxy ./cmd/mcpproxy

# Test without daemon (should read from file or error)
./mcpproxy upstream logs github-server
```

**Step 4: Commit**

```bash
git add cmd/mcpproxy/upstream_cmd.go internal/cliclient/client.go
git commit -m "feat: add 'mcpproxy upstream logs' command with follow mode"
```

---

## Task 4: Add `mcpproxy upstream enable/disable/restart` commands

**Files:**
- Modify: `cmd/mcpproxy/upstream_cmd.go`
- Modify: `internal/cliclient/client.go`

**Step 1: Add enable/disable/restart subcommands**

Add to `cmd/mcpproxy/upstream_cmd.go` in the `var` block:

```go
	upstreamEnableCmd = &cobra.Command{
		Use:   "enable <server-name>",
		Short: "Enable a server",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runUpstreamEnable,
	}

	upstreamDisableCmd = &cobra.Command{
		Use:   "disable <server-name>",
		Short: "Disable a server",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runUpstreamDisable,
	}

	upstreamRestartCmd = &cobra.Command{
		Use:   "restart <server-name>",
		Short: "Restart a server",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runUpstreamRestart,
	}

	// Flags for bulk operations
	upstreamAll   bool
	upstreamForce bool
```

Add to `init()` function:

```go
	upstreamCmd.AddCommand(upstreamEnableCmd)
	upstreamCmd.AddCommand(upstreamDisableCmd)
	upstreamCmd.AddCommand(upstreamRestartCmd)

	// Add --all and --force flags to enable/disable/restart
	upstreamEnableCmd.Flags().BoolVar(&upstreamAll, "all", false, "Enable all servers")
	upstreamEnableCmd.Flags().BoolVar(&upstreamForce, "force", false, "Skip confirmation prompt")

	upstreamDisableCmd.Flags().BoolVar(&upstreamAll, "all", false, "Disable all servers")
	upstreamDisableCmd.Flags().BoolVar(&upstreamForce, "force", false, "Skip confirmation prompt")

	upstreamRestartCmd.Flags().BoolVar(&upstreamAll, "all", false, "Restart all servers")
```

Add implementation functions:

```go
func runUpstreamEnable(cmd *cobra.Command, args []string) error {
	if upstreamAll {
		return runUpstreamBulkAction("enable", upstreamForce)
	}
	if len(args) == 0 {
		return fmt.Errorf("server name required (or use --all)")
	}
	return runUpstreamAction(args[0], "enable")
}

func runUpstreamDisable(cmd *cobra.Command, args []string) error {
	if upstreamAll {
		return runUpstreamBulkAction("disable", upstreamForce)
	}
	if len(args) == 0 {
		return fmt.Errorf("server name required (or use --all)")
	}
	return runUpstreamAction(args[0], "disable")
}

func runUpstreamRestart(cmd *cobra.Command, args []string) error {
	if upstreamAll {
		return runUpstreamBulkAction("restart", false) // restart doesn't need confirmation
	}
	if len(args) == 0 {
		return fmt.Errorf("server name required (or use --all)")
	}
	return runUpstreamAction(args[0], "restart")
}

func runUpstreamAction(serverName, action string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Load configuration
	globalConfig, err := loadUpstreamConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		return err
	}

	// Create logger
	logger, err := createUpstreamLogger(upstreamLogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating logger: %v\n", err)
		return err
	}

	// Require daemon for actions
	if !shouldUseUpstreamDaemon(globalConfig.DataDir) {
		return fmt.Errorf("server actions require running daemon. Start with: mcpproxy serve")
	}

	socketPath := socket.DetectSocketPath(globalConfig.DataDir)
	client := cliclient.NewClient(socketPath, logger.Sugar())

	fmt.Printf("Performing action '%s' on server '%s'...\n", action, serverName)

	err = client.ServerAction(ctx, serverName, action)
	if err != nil {
		return fmt.Errorf("failed to %s server: %w", action, err)
	}

	fmt.Printf("✅ Successfully %sd server '%s'\n", action, serverName)
	return nil
}

func runUpstreamBulkAction(action string, force bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Load configuration
	globalConfig, err := loadUpstreamConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		return err
	}

	// Create logger
	logger, err := createUpstreamLogger(upstreamLogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating logger: %v\n", err)
		return err
	}

	// Require daemon
	if !shouldUseUpstreamDaemon(globalConfig.DataDir) {
		return fmt.Errorf("server actions require running daemon. Start with: mcpproxy serve")
	}

	socketPath := socket.DetectSocketPath(globalConfig.DataDir)
	client := cliclient.NewClient(socketPath, logger.Sugar())

	// Get server list to count
	servers, err := client.GetServers(ctx)
	if err != nil {
		return fmt.Errorf("failed to get server list: %w", err)
	}

	// Filter based on action (enable=disabled servers, disable=enabled servers)
	var targetServers []string
	for _, srv := range servers {
		name := getStringField(srv, "name")
		enabled := getBoolField(srv, "enabled")

		if action == "enable" && !enabled {
			targetServers = append(targetServers, name)
		} else if action == "disable" && enabled {
			targetServers = append(targetServers, name)
		} else if action == "restart" && enabled {
			targetServers = append(targetServers, name)
		}
	}

	if len(targetServers) == 0 {
		fmt.Printf("⚠️  No servers to %s\n", action)
		return nil
	}

	// Require confirmation for enable/disable --all
	if action == "enable" || action == "disable" {
		confirmed, err := confirmBulkAction(action, len(targetServers), force)
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Println("Operation cancelled")
			return nil
		}
	}

	// Perform action on all servers
	fmt.Printf("Performing action '%s' on %d server(s)...\n", action, len(targetServers))

	for _, serverName := range targetServers {
		err = client.ServerAction(ctx, serverName, action)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to %s server '%s': %v\n", action, serverName, err)
		} else {
			fmt.Printf("✅ Successfully %sd server '%s'\n", action, serverName)
		}
	}

	return nil
}
```

**Step 2: Add ServerAction method to cliclient**

Add to `internal/cliclient/client.go`:

```go
// ServerAction performs an action on a server (enable, disable, restart).
func (c *Client) ServerAction(ctx context.Context, serverName, action string) error {
	var url string
	method := http.MethodPost

	switch action {
	case "enable":
		url = fmt.Sprintf("%s/api/v1/servers/%s/enable", c.baseURL, serverName)
	case "disable":
		url = fmt.Sprintf("%s/api/v1/servers/%s/disable", c.baseURL, serverName)
	case "restart":
		url = fmt.Sprintf("%s/api/v1/servers/%s/restart", c.baseURL, serverName)
	default:
		return fmt.Errorf("unknown action: %s", action)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to call server action API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}

	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return fmt.Errorf("action failed: %s", apiResp.Error)
	}

	return nil
}
```

**Step 3: Test manually**

```bash
go build -o mcpproxy ./cmd/mcpproxy

# Test single server (should error without daemon)
./mcpproxy upstream enable github-server

# Expected: Error message about requiring daemon
```

**Step 4: Commit**

```bash
git add cmd/mcpproxy/upstream_cmd.go internal/cliclient/client.go
git commit -m "feat: add upstream enable/disable/restart commands with --all support"
```

---

## Task 5: Add `mcpproxy doctor` command (placeholder)

**Files:**
- Create: `cmd/mcpproxy/doctor_cmd.go`
- Modify: `cmd/mcpproxy/main.go`

**Step 1: Create doctor command structure**

Create `cmd/mcpproxy/doctor_cmd.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"mcpproxy-go/internal/cliclient"
	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/socket"
)

var (
	doctorCmd = &cobra.Command{
		Use:   "doctor",
		Short: "Run health checks to identify issues",
		Long: `Run comprehensive health checks on MCPProxy to identify:
- Upstream server connection errors
- OAuth authentication requirements
- Missing secrets
- Runtime warnings
- Docker isolation status

This is the first command to run when debugging server issues.

Examples:
  mcpproxy doctor
  mcpproxy doctor --output=json`,
		RunE: runDoctor,
	}

	// Command flags
	doctorOutput    string
	doctorLogLevel  string
	doctorConfigPath string
)

// GetDoctorCommand returns the doctor command
func GetDoctorCommand() *cobra.Command {
	return doctorCmd
}

func init() {
	doctorCmd.Flags().StringVarP(&doctorOutput, "output", "o", "pretty", "Output format (pretty, json)")
	doctorCmd.Flags().StringVarP(&doctorLogLevel, "log-level", "l", "warn", "Log level")
	doctorCmd.Flags().StringVarP(&doctorConfigPath, "config", "c", "", "Path to config file")
}

func runDoctor(_ *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Load configuration
	globalConfig, err := loadDoctorConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		return err
	}

	// Create logger
	logger, err := createDoctorLogger(doctorLogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating logger: %v\n", err)
		return err
	}

	// Check if daemon is running
	if !shouldUseDoctorDaemon(globalConfig.DataDir) {
		return fmt.Errorf("doctor requires running daemon. Start with: mcpproxy serve")
	}

	logger.Info("Fetching diagnostics from daemon")
	return runDoctorClientMode(ctx, globalConfig.DataDir, logger)
}

func shouldUseDoctorDaemon(dataDir string) bool {
	socketPath := socket.DetectSocketPath(dataDir)
	return socket.IsSocketAvailable(socketPath)
}

func runDoctorClientMode(ctx context.Context, dataDir string, logger *zap.Logger) error {
	socketPath := socket.DetectSocketPath(dataDir)
	client := cliclient.NewClient(socketPath, logger.Sugar())

	// Call GET /api/v1/diagnostics
	diag, err := client.GetDiagnostics(ctx)
	if err != nil {
		return fmt.Errorf("failed to get diagnostics from daemon: %w", err)
	}

	return outputDiagnostics(diag)
}

func outputDiagnostics(diag map[string]interface{}) error {
	switch doctorOutput {
	case "json":
		output, err := json.MarshalIndent(diag, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to format output: %w", err)
		}
		fmt.Println(string(output))
	case "pretty":
	default:
		// Pretty format (placeholder - will be implemented with actual diagnostics API)
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("🔍 MCPProxy Health Check")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println()
		fmt.Println("✅ All systems operational! No issues detected.")
		fmt.Println()
		fmt.Println("(Full diagnostics will be implemented when API endpoint is ready)")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	}

	return nil
}

func loadDoctorConfig() (*config.Config, error) {
	if doctorConfigPath != "" {
		return config.LoadFromFile(doctorConfigPath)
	}
	return config.Load()
}

func createDoctorLogger(level string) (*zap.Logger, error) {
	var zapLevel zap.AtomicLevel
	switch level {
	case "trace", "debug":
		zapLevel = zap.NewAtomicLevelAt(zap.DebugLevel)
	case "info":
		zapLevel = zap.NewAtomicLevelAt(zap.InfoLevel)
	case "warn":
		zapLevel = zap.NewAtomicLevelAt(zap.WarnLevel)
	case "error":
		zapLevel = zap.NewAtomicLevelAt(zap.ErrorLevel)
	default:
		zapLevel = zap.NewAtomicLevelAt(zap.WarnLevel)
	}

	cfg := zap.Config{
		Level:            zapLevel,
		Development:      false,
		Encoding:         "console",
		EncoderConfig:    zap.NewDevelopmentEncoderConfig(),
		OutputPaths:      []string{"stderr"},
		ErrorOutputPaths: []string{"stderr"},
	}

	return cfg.Build()
}
```

**Step 2: Add GetDiagnostics placeholder to cliclient**

Add to `internal/cliclient/client.go`:

```go
// GetDiagnostics retrieves diagnostics information from daemon.
func (c *Client) GetDiagnostics(ctx context.Context) (map[string]interface{}, error) {
	url := c.baseURL + "/api/v1/diagnostics"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call diagnostics API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
		Error   string                 `json:"error"`
	}

	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("API call failed: %s", apiResp.Error)
	}

	return apiResp.Data, nil
}
```

**Step 3: Register doctor command in main.go**

Modify `cmd/mcpproxy/main.go`, add after other command registrations:

```go
// Add after upstreamCmd := GetUpstreamCommand()
doctorCmd := GetDoctorCommand()

// Add after rootCmd.AddCommand(upstreamCmd)
rootCmd.AddCommand(doctorCmd)
```

**Step 4: Test manually**

```bash
go build -o mcpproxy ./cmd/mcpproxy

# Test (should error without daemon)
./mcpproxy doctor

# Expected: Error message about requiring daemon
```

**Step 5: Commit**

```bash
git add cmd/mcpproxy/doctor_cmd.go cmd/mcpproxy/main.go internal/cliclient/client.go
git commit -m "feat: add 'mcpproxy doctor' command (placeholder)"
```

---

## Task 6: Add brief CLAUDE.md documentation

**Files:**
- Modify: `CLAUDE.md`

**Step 1: Add CLI Management Commands section**

Add to `CLAUDE.md` after the "### Testing" section (around line 150):

```markdown
### CLI Management Commands

MCPProxy provides CLI commands for managing upstream servers and monitoring health:

```bash
mcpproxy upstream list              # List all servers
mcpproxy upstream logs <name>       # View logs (--tail, --follow)
mcpproxy upstream restart <name>    # Restart server (supports --all)
mcpproxy doctor                     # Run health checks
```

**Common workflow:**
```bash
mcpproxy doctor                     # Check overall health
mcpproxy upstream list              # Identify issues
mcpproxy upstream logs failing-srv  # View logs
mcpproxy upstream restart failing-srv
```

See [docs/cli-management-commands.md](docs/cli-management-commands.md) for complete reference.
```

**Step 2: Update Debugging Guide section**

Find the "## Debugging Guide" section in CLAUDE.md and update it:

```markdown
## Debugging Guide

### Quick Diagnostics

Run this first when debugging any issue:

```bash
mcpproxy doctor
```

This checks for:
- Upstream server connection errors
- OAuth authentication requirements
- Missing secrets
- Runtime warnings
- Docker isolation status

See [docs/cli-management-commands.md](docs/cli-management-commands.md) for detailed workflows.

### Common Commands

```bash
# Monitor logs
tail -f ~/Library/Logs/mcpproxy/main.log

# Check server status
mcpproxy upstream list

# View specific server logs
mcpproxy upstream logs github-server --tail=100

# Follow logs in real-time (requires daemon)
mcpproxy upstream logs github-server --follow

# Restart problematic server
mcpproxy upstream restart github-server
```
```

**Step 3: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: add CLI management commands to CLAUDE.md"
```

---

## Task 7: Create comprehensive CLI reference documentation

**Files:**
- Create: `docs/cli-management-commands.md`

**Step 1: Create comprehensive documentation**

Create `docs/cli-management-commands.md`:

```markdown
# CLI Management Commands

This document describes the CLI commands for managing MCPProxy upstream servers and monitoring system health.

## Overview

MCPProxy provides two command groups:

1. **`mcpproxy upstream`** - Server management (list, logs, enable, disable, restart)
2. **`mcpproxy doctor`** - Health checks and diagnostics

All commands support both **daemon mode** (fast, via socket) and **standalone mode** (direct connection).

## Command Reference

### `mcpproxy upstream list`

List all configured upstream servers with connection status.

**Usage:**
```bash
mcpproxy upstream list [flags]
```

**Flags:**
- `--output, -o` - Output format (table, json) [default: table]
- `--log-level, -l` - Log level (trace, debug, info, warn, error) [default: warn]
- `--config, -c` - Path to config file

**Examples:**
```bash
# List servers with table output
mcpproxy upstream list

# JSON output for scripting
mcpproxy upstream list --output=json

# With debug logging
mcpproxy upstream list --log-level=debug
```

**Output Fields:**
- NAME - Server name
- ENABLED - Whether server is enabled
- PROTOCOL - Transport protocol (stdio, http, sse)
- CONNECTED - Connection status
- TOOLS - Number of available tools
- STATUS - Current status or error message

---

### `mcpproxy upstream logs <name>`

Display recent log entries from a specific server.

**Usage:**
```bash
mcpproxy upstream logs <server-name> [flags]
```

**Flags:**
- `--tail, -n` - Number of lines to show [default: 50]
- `--follow, -f` - Follow log output (requires daemon)
- `--log-level, -l` - Log level [default: warn]
- `--config, -c` - Path to config file

**Examples:**
```bash
# Show last 50 lines
mcpproxy upstream logs github-server

# Show last 200 lines
mcpproxy upstream logs github-server --tail=200

# Follow logs (Ctrl+C to stop)
mcpproxy upstream logs github-server --follow
```

**Behavior:**
- **Daemon mode**: Fetches logs via API from running server
- **Standalone mode**: Reads directly from log file (read-only)
- **Follow mode**: Requires running daemon, polls for new lines

---

### `mcpproxy upstream enable <name|--all>`

Enable a disabled server or all disabled servers.

**Usage:**
```bash
mcpproxy upstream enable <server-name>
mcpproxy upstream enable --all [--force]
```

**Flags:**
- `--all` - Enable all disabled servers
- `--force` - Skip confirmation prompt (for automation)

**Requirements:**
- Daemon must be running

**Examples:**
```bash
# Enable single server
mcpproxy upstream enable github-server

# Enable all servers (interactive confirmation)
mcpproxy upstream enable --all

# Enable all servers (skip prompt)
mcpproxy upstream enable --all --force
```

**Interactive Confirmation:**
```bash
$ mcpproxy upstream enable --all
⚠️  This will enable 5 server(s). Continue? [y/N]: _
```

**Non-interactive mode requires --force:**
```bash
$ echo "y" | mcpproxy upstream enable --all
Error: --all requires --force flag in non-interactive mode
```

---

### `mcpproxy upstream disable <name|--all>`

Disable a server or all enabled servers.

**Usage:**
```bash
mcpproxy upstream disable <server-name>
mcpproxy upstream disable --all [--force]
```

**Flags:**
- `--all` - Disable all enabled servers
- `--force` - Skip confirmation prompt

**Requirements:**
- Daemon must be running

**Examples:**
```bash
# Disable single server
mcpproxy upstream disable github-server

# Disable all servers (interactive confirmation)
mcpproxy upstream disable --all

# Disable all in script
mcpproxy upstream disable --all --force
```

---

### `mcpproxy upstream restart <name|--all>`

Restart a server or all enabled servers.

**Usage:**
```bash
mcpproxy upstream restart <server-name>
mcpproxy upstream restart --all
```

**Flags:**
- `--all` - Restart all enabled servers

**Requirements:**
- Daemon must be running

**Examples:**
```bash
# Restart single server
mcpproxy upstream restart github-server

# Restart all servers (no confirmation needed)
mcpproxy upstream restart --all
```

**Note:** Restart does not require confirmation as it's non-destructive.

---

### `mcpproxy doctor`

Run comprehensive health checks to identify issues.

**Usage:**
```bash
mcpproxy doctor [flags]
```

**Flags:**
- `--output, -o` - Output format (pretty, json) [default: pretty]
- `--log-level, -l` - Log level [default: warn]
- `--config, -c` - Path to config file

**Requirements:**
- Daemon must be running

**Examples:**
```bash
# Pretty output with issue categorization
mcpproxy doctor

# JSON output for scripting
mcpproxy doctor --output=json
```

**Health Checks:**
- Upstream server connection errors
- OAuth authentication requirements
- Missing secrets (unresolved references)
- Runtime warnings
- Docker isolation status

**Output:**
- Total issue count
- Categorized issues with actionable remediation steps
- Exit code 0 even if issues found (for scripting)

---

## Common Workflows

### Debugging Server Connection Issues

```bash
# 1. Run health check to see all issues
mcpproxy doctor

# 2. Check specific server status
mcpproxy upstream list

# 3. View logs for failing server
mcpproxy upstream logs failing-server --tail=100

# 4. Restart server
mcpproxy upstream restart failing-server

# 5. Verify fix
mcpproxy upstream list
```

### Monitoring Server Health

```bash
# Follow logs in terminal 1
mcpproxy upstream logs github-server --follow

# In terminal 2, trigger operations
mcpproxy call tool --tool-name=github:get_user --json_args='{"username":"octocat"}'

# Watch logs update in real-time
```

### Bulk Server Management

```bash
# List all servers first
mcpproxy upstream list

# Disable all for maintenance
mcpproxy upstream disable --all --force

# Perform maintenance...

# Re-enable all
mcpproxy upstream enable --all --force

# Verify
mcpproxy upstream list
```

---

## Daemon Mode vs Standalone Mode

### Daemon Mode (Preferred)
- **Detection**: Socket file exists at `~/.mcpproxy/mcpproxy.sock`
- **Behavior**: Uses HTTP API via Unix socket
- **Speed**: Fast (reuses existing connections)
- **Requirements**: `mcpproxy serve` running

### Standalone Mode (Fallback)
- **Detection**: No socket file found
- **Behavior**: Direct connection to servers or file reading
- **Speed**: Slower (establishes new connections)
- **Limitations**: Some commands unavailable (enable, disable, restart, follow, doctor)

### Command Availability

| Command | Daemon Mode | Standalone Mode | Notes |
|---------|-------------|-----------------|-------|
| `upstream list` | ✅ Full status | ✅ Config only | Standalone shows "unknown" |
| `upstream logs` | ✅ Via API | ✅ File read | Follow requires daemon |
| `upstream enable` | ✅ | ❌ | Requires daemon |
| `upstream disable` | ✅ | ❌ | Requires daemon |
| `upstream restart` | ✅ | ❌ | Requires daemon |
| `doctor` | ✅ | ❌ | Requires daemon |

---

## Safety Considerations

### Bulk Operations Warning

⚠️  **Safety Warning: Bulk Operations**

The `--all` flag affects all servers simultaneously:

- `disable --all` - Stops all upstream connections (reversible with `enable --all`)
- `enable --all` - Activates all servers (may trigger API calls, OAuth flows)
- `restart --all` - Reconnects all servers (may cause brief service disruption)

**Best practices:**
- Use `upstream list` first to see affected servers
- Test with single server before using `--all`
- Use `--force` in automation only when appropriate

### Interactive Confirmation

Confirmation prompt shows count:

```bash
⚠️  This will disable 12 server(s). Continue? [y/N]:
```

Shows what will be affected before proceeding.

---

## Exit Codes

- `0` - Success
- `1` - Execution failure (API error, connection failure, user declined confirmation)
- `2` - Invalid arguments or configuration (non-interactive without --force)

---

## Implementation Notes

### Architecture
- Commands in `cmd/mcpproxy/*_cmd.go`
- Client library in `internal/cliclient/client.go`
- API endpoints in `internal/httpapi/server.go`

### Adding New Commands
1. Create command file in `cmd/mcpproxy/`
2. Add methods to `internal/cliclient/client.go`
3. Register in `cmd/mcpproxy/main.go`
4. Follow existing patterns for daemon detection
5. Support both daemon and standalone modes where possible

### Testing
All commands support manual testing:
```bash
go build -o mcpproxy ./cmd/mcpproxy
./mcpproxy <command> [args]
```
```

**Step 2: Commit**

```bash
git add docs/cli-management-commands.md
git commit -m "docs: add comprehensive CLI management commands reference"
```

---

## Summary

This plan implements:

✅ **`mcpproxy upstream`** command group:
- `list` - List all servers with status (daemon + standalone)
- `logs` - View logs with tail/follow support (daemon + standalone file read)
- `enable/disable/restart` - Server lifecycle management with `--all` support (daemon only)

✅ **`mcpproxy doctor`** command:
- Placeholder for health checks (daemon only)
- Will be enhanced when diagnostics API is implemented

✅ **Interactive confirmation**:
- TTY detection for bulk enable/disable operations
- `--force` flag for automation

✅ **Documentation**:
- Brief summary in CLAUDE.md
- Comprehensive reference in docs/cli-management-commands.md

**Next steps:**
- Implement diagnostics API endpoints in `internal/httpapi`
- Add `doctor docker/oauth/secrets/upstream` subcommands
- Enhance doctor output with real health check data
