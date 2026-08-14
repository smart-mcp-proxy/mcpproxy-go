package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cache"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/codescripts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/index"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/server"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var (
	codeCmd = &cobra.Command{
		Use:   "code",
		Short: "JavaScript/TypeScript code execution for multi-tool orchestration",
		Long:  "Execute JavaScript or TypeScript code that orchestrates multiple upstream MCP tools in a single request",
	}

	codeExecCmd = &cobra.Command{
		Use:   "exec",
		Short: "Execute JavaScript or TypeScript code",
		Long: `Execute JavaScript or TypeScript code that can orchestrate multiple upstream MCP tools.

Use --language typescript to write TypeScript code with type annotations,
interfaces, enums, and generics. Types are automatically stripped before execution.

Use --script <name> to run a script stored server-side in the scripts/ directory
next to the configuration file, instead of sending source with --code/--file.
Exactly one of --code, --file or --script may be given. The name is a bare
identifier, never a path, and its extension decides the language, so --language
is only needed for inline code. List what is stored with 'mcpproxy code scripts
list'.

The code has access to:
- input: Global variable containing the input data (from --input or --input-file)
- call_tool(serverName, toolName, args): Function to invoke upstream MCP tools

The code must return a JSON-serializable value. The sandbox prevents access to:
- require() - No module loading
- setTimeout/setInterval - No timers
- Filesystem, network, or environment variables

Exit codes:
  0 - Successful execution
  1 - Execution failed (syntax error, runtime error, timeout, etc.)
  2 - Invalid arguments or configuration`,
		RunE: runCodeExec,
	}

	codeScriptsCmd = &cobra.Command{
		Use:   "scripts",
		Short: "Inspect the stored scripts available to code execution",
		Long: `Stored scripts are ` + "`<name>.js`" + ` / ` + "`<name>.ts`" + ` files in the scripts/ directory
next to mcpproxy's configuration file. Run one with:

  mcpproxy code exec --script <name>

Scripts are authored in the filesystem — there is no command that writes them.`,
	}

	codeScriptsListCmd = &cobra.Command{
		Use:   "list",
		Short: "List stored scripts",
		Long: `List the stored scripts the code_execution tool can run.

When a daemon is running the listing comes from the daemon, so it always
describes the process that actually resolves scripts; otherwise the local
scripts directory is read directly.

Each entry carries a status: 'ok' (invocable), 'ambiguous' (both a .js and a .ts
file share the name — remove one) or 'invalid' with the reason it cannot run.`,
		Args: cobra.NoArgs,
		RunE: runCodeScriptsList,
	}

	// Command flags for code exec
	codeSource       string
	codeFile         string
	codeInput        string
	codeInputFile    string
	codeTimeout      int
	codeMaxToolCalls int
	codeAllowedSrvs  []string
	codeLogLevel     string
	codeConfigPath   string
	codeLanguage     string
	codeScriptName   string

	// codeLanguageExplicit records whether --language was actually set by the
	// user. The flag has a default ("javascript"), and a default is not a
	// choice: forwarded as one it would contradict every stored .ts script.
	codeLanguageExplicit bool
)

// GetCodeCommand returns the code command for adding to the root command
func GetCodeCommand() *cobra.Command {
	return codeCmd
}

func init() {
	// Add exec subcommand to code command
	codeCmd.AddCommand(codeExecCmd)

	// Stored-script discovery (Spec 097). Read-only: scripts are authored in
	// the filesystem, never through the CLI.
	codeScriptsCmd.AddCommand(codeScriptsListCmd)
	codeCmd.AddCommand(codeScriptsCmd)

	// Define flags for code exec command
	codeExecCmd.Flags().StringVar(&codeSource, "code", "", "JavaScript code to execute (required if --file is not provided)")
	codeExecCmd.Flags().StringVar(&codeFile, "file", "", "Path to JavaScript file to execute (required if --code is not provided)")
	codeExecCmd.Flags().StringVar(&codeScriptName, "script", "", "Name of a stored script in the scripts/ directory next to the config file (mutually exclusive with --code/--file)")
	codeExecCmd.Flags().StringVar(&codeInput, "input", "{}", "Input data as JSON string (default: {})")
	codeExecCmd.Flags().StringVar(&codeInputFile, "input-file", "", "Path to JSON file containing input data")
	codeExecCmd.Flags().IntVar(&codeTimeout, "timeout", 120000, "Execution timeout in milliseconds (1-600000)")
	codeExecCmd.Flags().IntVar(&codeMaxToolCalls, "max-tool-calls", 0, "Maximum number of tool calls (0 = unlimited)")
	codeExecCmd.Flags().StringSliceVar(&codeAllowedSrvs, "allowed-servers", []string{}, "Comma-separated list of allowed server names (empty = all allowed)")
	codeExecCmd.Flags().StringVarP(&codeLogLevel, "log-level", "l", "info", "Log level (trace, debug, info, warn, error)")
	codeExecCmd.Flags().StringVarP(&codeConfigPath, "config", "c", "", "Path to MCP configuration file (default: ~/.mcpproxy/mcp_config.json)")
	codeExecCmd.Flags().StringVar(&codeLanguage, "language", "javascript", "Source code language: javascript, typescript")

	// The scripts commands resolve the same config FILE as exec, so they take
	// the same --config override.
	codeScriptsListCmd.Flags().StringVarP(&codeConfigPath, "config", "c", "", "Path to MCP configuration file (default: ~/.mcpproxy/mcp_config.json)")

	// Add examples
	codeExecCmd.Example = `  # Execute inline code with input
  mcpproxy code exec --code="({ result: input.value * 2 })" --input='{"value": 21}'

  # Execute TypeScript code
  mcpproxy code exec --language typescript --code="const x: number = 42; ({ result: x })"

  # Execute code from file
  mcpproxy code exec --file=script.js --input-file=params.json

  # Execute TypeScript from file
  mcpproxy code exec --language typescript --file=script.ts --input-file=params.json

  # Execute a stored script by name (scripts/ next to the config file)
  mcpproxy code exec --script=daily-report --input='{"repo":"smart-mcp-proxy/mcpproxy-go"}'

  # See which stored scripts exist
  mcpproxy code scripts list

  # Call upstream tools
  mcpproxy code exec --code="call_tool('github', 'get_user', {username: input.user})" --input='{"user":"octocat"}'

  # With timeout and tool call limits
  mcpproxy code exec --code="..." --timeout=60000 --max-tool-calls=10

  # Restrict to specific servers
  mcpproxy code exec --code="..." --allowed-servers=github,gitlab

  # With trace logging for debugging
  mcpproxy code exec --code="..." --log-level=trace`
}

func runCodeExec(cmd *cobra.Command, _ []string) error {
	// Validate arguments
	if err := validateCodeSourceFlags(codeSource, codeFile, codeScriptName); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return exitError(2)
	}

	// Record whether --language is the user's choice or just its default,
	// before either execution mode reads it.
	setCodeLanguageExplicit(cmd)

	// Load code and input
	code, inputData, err := loadCodeAndInput()
	if err != nil {
		return exitError(2)
	}

	// Validate options
	if err := validateOptions(); err != nil {
		return exitError(2)
	}

	// Load config to get data directory
	globalConfig, err := loadCodeConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		return exitError(2)
	}

	// Check if code_execution is enabled
	if !globalConfig.EnableCodeExecution {
		fmt.Fprintf(os.Stderr, "Error: code_execution is disabled in configuration. Set 'enable_code_execution: true' in config file.\n")
		return exitError(2)
	}

	// Create logger
	logger, err := createCodeLogger(codeLogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating logger: %v\n", err)
		return exitError(2)
	}

	// Detect daemon (socket first, then TCP fallback) and choose mode
	if client, ok := newDaemonClient(globalConfig, logger.Sugar()); ok {
		logger.Info("Detected running daemon, using client mode")
		return runCodeExecClientMode(client, code, inputData, logger)
	}

	logger.Info("No daemon detected, using standalone mode")
	return runCodeExecStandalone(globalConfig, code, inputData, logger)
}

// codeExecClientSlack is the head-room added to the server-side execution
// budget when deriving the client-side deadline: transport, queueing and
// (de)serialization all happen outside the daemon's own timeout accounting.
const codeExecClientSlack = 30 * time.Second

// codeExecClientTimeout returns how long the CLI waits for the daemon to
// answer a code execution request. The daemon enforces the --timeout budget
// itself, so the client deadline only has to outlive it.
func codeExecClientTimeout(timeoutMS int) time.Duration {
	return time.Duration(timeoutMS)*time.Millisecond + codeExecClientSlack
}

// runCodeExecClientMode executes code via the daemon HTTP API.
func runCodeExecClientMode(client *cliclient.Client, code string, input map[string]interface{}, logger *zap.Logger) error {
	// Ping daemon to verify connectivity
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pingCancel()
	if err := client.Ping(pingCtx); err != nil {
		logger.Warn("Failed to ping daemon, falling back to standalone mode",
			zap.Error(err),
			zap.Duration("ping_timeout", 2*time.Second),
			zap.String("fallback_mode", "standalone"))
		// Fall back to standalone mode, which needs a usable configuration of
		// its own (data dir, limits) - without one there is nothing to run.
		cfg, cfgErr := loadCodeConfig()
		if cfgErr == nil && cfg == nil {
			cfgErr = errors.New("no configuration available")
		}
		if cfgErr != nil {
			fmt.Fprintf(os.Stderr, "Error loading configuration for standalone fallback: %v\n", cfgErr)
			return exitError(2)
		}
		return runCodeExecStandalone(cfg, code, input, logger)
	}

	// ADD CLI mode indicator
	fmt.Fprintf(os.Stderr, "ℹ️  Using daemon mode - fast execution\n")

	// Execute code via daemon
	clientTimeout := codeExecClientTimeout(codeTimeout)
	execCtx, execCancel := context.WithTimeout(context.Background(), clientTimeout)
	defer execCancel()

	result, err := client.CodeExec(
		execCtx,
		code,
		input,
		codeTimeout,
		codeMaxToolCalls,
		codeAllowedSrvs,
		cliclient.CodeExecOptions{Language: codeExecLanguageArg(), Script: codeScriptName},
	)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintf(os.Stderr,
				"Error: client-side timeout after %s waiting for the daemon to return results (execution budget --timeout=%dms); the daemon may still be running the code\n",
				clientTimeout, codeTimeout)
			return exitError(1)
		}
		// T029: Use formatErrorWithRequestID to include request_id in error output
		fmt.Fprintf(os.Stderr, "Error calling daemon: %s\n", formatErrorWithRequestID(err))
		return exitError(1)
	}

	// Output result
	return outputResult(result)
}

// runCodeExecStandalone executes code locally (existing logic).
func runCodeExecStandalone(globalConfig *config.Config, code string, input map[string]interface{}, logger *zap.Logger) error {
	// ADD standalone mode indicator
	fmt.Fprintf(os.Stderr, "⚠️  Using standalone mode - daemon not detected (slower startup)\n")

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(codeTimeout+5000)*time.Millisecond)
	defer cancel()

	// Create storage manager
	storageManager, err := storage.NewManager(globalConfig.DataDir, logger.Sugar())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage manager: %v\n", err)
		return exitError(2)
	}
	defer storageManager.Close()

	// Create index manager
	indexManager, err := index.NewManager(globalConfig.DataDir, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating index manager: %v\n", err)
		return exitError(2)
	}
	defer indexManager.Close()

	// Create secret resolver
	secretResolver := secret.NewResolver()

	// Create upstream manager
	upstreamManager := upstream.NewManager(logger, globalConfig, storageManager.GetBoltDB(), secretResolver, storageManager)

	// Create cache manager
	cacheManager, err := cache.NewManager(storageManager.GetDB(), logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating cache manager: %v\n", err)
		return exitError(2)
	}
	defer cacheManager.Close()

	// Create truncator
	truncator := truncate.NewTruncator(globalConfig.ToolResponseLimit)

	// Spec 097: the in-process server needs the SAME config-file authority the
	// daemon has, or a stored script resolves differently depending on whether
	// a daemon happened to be running. A failure here is not fatal: only stored
	// scripts depend on it, and they report their own error.
	configFilePath, cfgPathErr := codeConfigFilePath()
	if cfgPathErr != nil {
		logger.Warn("could not resolve the config file path for stored scripts", zap.Error(cfgPathErr))
	}

	// Create MCP proxy server
	mcpProxy := server.NewMCPProxyServer(
		storageManager,
		indexManager,
		upstreamManager,
		cacheManager,
		func() *truncate.Truncator { return truncator },
		logger,
		nil,
		false,
		globalConfig,
		nil, // standalone one-shot: no runtime-owned signature cache
		server.WithConfigFilePath(configFilePath),
	)
	defer mcpProxy.Close()

	// Call the code_execution tool
	result, err := mcpProxy.CallBuiltInTool(ctx, "code_execution", codeExecToolArgs(code, codeScriptName, input))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error calling code_execution tool: %v\n", err)
		return exitError(1)
	}

	// Parse and output result
	return outputResultFromMCP(result)
}

// codeScriptsListTimeout bounds the daemon listing request: it is a directory
// read on the far side, so it either answers immediately or something is wrong.
const codeScriptsListTimeout = 10 * time.Second

// codeScriptsPayload is the machine-readable shape of `code scripts list`,
// mirroring GET /api/v1/code/scripts so both surfaces read the same.
type codeScriptsPayload struct {
	Dir     string              `json:"dir"`
	Scripts []codescripts.Entry `json:"scripts"`
}

// runCodeScriptsList lists the stored scripts available to code execution.
// A running daemon answers for itself — it is the process that resolves scripts
// at execution time, so its view is the authoritative one; only without a
// daemon does the CLI read the scripts directory itself.
func runCodeScriptsList(_ *cobra.Command, _ []string) error {
	if cfg, err := loadCodeConfig(); err == nil {
		if client, ok := newDaemonClient(cfg, nil); ok {
			ctx, cancel := context.WithTimeout(context.Background(), codeScriptsListTimeout)
			defer cancel()

			dir, entries, err := client.GetCodeScripts(ctx)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error listing stored scripts from the daemon: %s\n", formatErrorWithRequestID(err))
				return exitError(1)
			}
			return outputCodeScripts(dir, entries)
		}
	}

	dir, entries, err := localCodeScripts()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing stored scripts: %v\n", err)
		return exitError(1)
	}
	return outputCodeScripts(dir, entries)
}

// localCodeScripts lists the scripts directory belonging to the config FILE the
// code command works against — the same authority the in-process handler uses,
// so a daemonless listing cannot disagree with a daemonless execution.
func localCodeScripts() (string, []codescripts.Entry, error) {
	configFilePath, err := codeConfigFilePath()
	if err != nil {
		return "", nil, err
	}
	dir := codescripts.DirFor(configFilePath)
	entries, err := codescripts.List(dir)
	if err != nil {
		return dir, nil, err
	}
	return dir, entries, nil
}

func outputCodeScripts(dir string, entries []codescripts.Entry) error {
	if format := ResolveOutputFormat(); format == "json" || format == "yaml" {
		formatter, err := GetOutputFormatter()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating output formatter: %v\n", err)
			return exitError(1)
		}
		out, err := formatter.Format(codeScriptsPayload{Dir: dir, Scripts: entries})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error formatting stored scripts: %v\n", err)
			return exitError(1)
		}
		fmt.Println(out)
		return nil
	}

	renderCodeScripts(os.Stdout, dir, entries)
	return nil
}

// renderCodeScripts writes the human-readable listing. It always names the
// directory it read: "no scripts" and "scripts, but not where you think" look
// identical otherwise.
func renderCodeScripts(w io.Writer, dir string, entries []codescripts.Entry) {
	if len(entries) == 0 {
		fmt.Fprintf(w, "No stored scripts in %s\n", dir)
		fmt.Fprintf(w, "Create <name>.js or <name>.ts there, then run: mcpproxy code exec --script <name>\n")
		return
	}

	fmt.Fprintf(w, "Stored scripts in %s (%d):\n", dir, len(entries))
	for _, entry := range entries {
		status := string(entry.Status)
		if entry.Reason != "" {
			status += " (" + entry.Reason + ")"
		}
		fmt.Fprintf(w, "  %-32s %-20s %s\n", entry.Name, status, strings.Join(entry.Paths, ", "))
	}
	fmt.Fprintf(w, "\nRun one with: mcpproxy code exec --script <name>\n")
}

// Helper functions

// validateCodeSourceFlags enforces the exactly-one-source rule across the three
// ways to name what runs: inline --code, a local --file, or a server-side
// stored script by --script (Spec 097).
func validateCodeSourceFlags(code, file, script string) error {
	named := 0
	for _, v := range []string{code, file, script} {
		if v != "" {
			named++
		}
	}
	switch {
	case named == 0:
		return fmt.Errorf("one of --code, --file or --script must be provided")
	case named > 1:
		return fmt.Errorf("--code, --file and --script are mutually exclusive")
	}
	return nil
}

// setCodeLanguageExplicit records whether --language carries the user's own
// choice or merely its default value.
func setCodeLanguageExplicit(cmd *cobra.Command) {
	codeLanguageExplicit = cmd != nil && cmd.Flags().Changed("language")
}

// codeExecLanguageArg returns the language to send with the request: the flag's
// value only when the user actually set it. A stored script derives its
// language from the file extension and rejects a contradicting explicit one, so
// forwarding the flag's "javascript" default would break every .ts script.
func codeExecLanguageArg() string {
	if !codeLanguageExplicit {
		return ""
	}
	return codeLanguage
}

// codeExecToolArgs builds the code_execution arguments for standalone
// (in-process) execution. A stored script contributes its NAME, never content
// the CLI resolved itself: the handler is the only execution-time resolver, so
// the same name means the same thing whether or not a daemon is running.
func codeExecToolArgs(code, script string, input map[string]interface{}) map[string]interface{} {
	args := map[string]interface{}{
		"input": input,
		"options": map[string]interface{}{
			"timeout_ms":      codeTimeout,
			"max_tool_calls":  codeMaxToolCalls,
			"allowed_servers": codeAllowedSrvs,
		},
	}
	if script != "" {
		args["script"] = script
	} else {
		args["code"] = code
	}
	if language := codeExecLanguageArg(); language != "" {
		args["language"] = language
	}
	return args
}

func loadCodeAndInput() (string, map[string]interface{}, error) {
	var code string
	if codeFile != "" {
		codeBytes, err := os.ReadFile(codeFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading code file: %v\n", err)
			return "", nil, err
		}
		code = string(codeBytes)
	} else {
		code = codeSource
	}

	var inputData map[string]interface{}
	if codeInputFile != "" {
		inputBytes, err := os.ReadFile(codeInputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading input file: %v\n", err)
			return "", nil, err
		}
		if err := json.Unmarshal(inputBytes, &inputData); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing input file JSON: %v\n", err)
			return "", nil, err
		}
	} else {
		if err := json.Unmarshal([]byte(codeInput), &inputData); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing input JSON: %v\n", err)
			return "", nil, err
		}
	}

	return code, inputData, nil
}

func validateOptions() error {
	if codeTimeout < 1 || codeTimeout > 600000 {
		fmt.Fprintf(os.Stderr, "Error: timeout must be between 1 and 600000 milliseconds\n")
		return fmt.Errorf("invalid timeout")
	}
	if codeMaxToolCalls < 0 {
		fmt.Fprintf(os.Stderr, "Error: max-tool-calls cannot be negative\n")
		return fmt.Errorf("invalid max-tool-calls")
	}
	if codeLanguage != "javascript" && codeLanguage != "typescript" {
		fmt.Fprintf(os.Stderr, "Error: unsupported language %q. Supported languages: javascript, typescript\n", codeLanguage)
		return fmt.Errorf("invalid language")
	}
	return nil
}

func outputResult(result *cliclient.CodeExecResult) error {
	output, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error formatting result: %v\n", err)
		return exitError(1)
	}
	fmt.Println(string(output))

	if !result.OK {
		return exitError(1)
	}
	return nil
}

func outputResultFromMCP(result *mcp.CallToolResult) error {
	// A tool ERROR is plain text, not the execution envelope — and for a stored
	// script that text is the recovery path: naming one that does not exist
	// answers with the available names (FR-004). Parsing it as JSON and giving
	// up ("unexpected result format") threw that away.
	if result.IsError {
		for _, content := range result.Content {
			if textContent, ok := mcp.AsTextContent(content); ok {
				fmt.Fprintf(os.Stderr, "Error: %s\n", textContent.Text)
				return exitError(1)
			}
		}
		fmt.Fprintf(os.Stderr, "Error: code execution failed\n")
		return exitError(1)
	}

	// Existing logic to parse MCP result
	for _, content := range result.Content {
		if textContent, ok := mcp.AsTextContent(content); ok {
			var execResult map[string]interface{}
			if err := json.Unmarshal([]byte(textContent.Text), &execResult); err == nil {
				output, err := json.MarshalIndent(execResult, "", "  ")
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error formatting result: %v\n", err)
					return exitError(1)
				}
				fmt.Println(string(output))

				if okValue, exists := execResult["ok"].(bool); exists && !okValue {
					return exitError(1)
				}
				return nil
			}
		}
	}

	fmt.Fprintf(os.Stderr, "Error: unexpected result format\n")
	return exitError(1)
}

// codeConfigFilePath resolves the config FILE the code command works against:
// --config when given, else the documented default. It is deliberately NOT
// derived from --data-dir — that flag overrides the data directory AFTER the
// config file has been chosen, so deriving from it would disagree with the
// file actually loaded (and with the daemon) about where stored scripts live.
func codeConfigFilePath() (string, error) {
	if codeConfigPath != "" {
		return codeConfigPath, nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}
	return filepath.Join(homeDir, ".mcpproxy", "mcp_config.json"), nil
}

// loadCodeConfig loads the MCP configuration file for code command
func loadCodeConfig() (*config.Config, error) {
	configFilePath, err := codeConfigFilePath()
	if err != nil {
		return nil, err
	}

	// Check if config file exists
	if _, err := os.Stat(configFilePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("configuration file not found at %s. Please run 'mcpproxy serve' first to create the config", configFilePath)
	}

	// Load configuration
	globalConfig, err := config.LoadFromFile(configFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config from %s: %w", configFilePath, err)
	}

	// Respect global --data-dir flag
	if dataDir != "" {
		globalConfig.DataDir = dataDir
	}

	return globalConfig, nil
}

// createCodeLogger creates a zap logger with the specified level
func createCodeLogger(level string) (*zap.Logger, error) {
	var zapLevel zap.AtomicLevel
	switch strings.ToLower(level) {
	case "trace", "debug":
		zapLevel = zap.NewAtomicLevelAt(zap.DebugLevel)
	case "info":
		zapLevel = zap.NewAtomicLevelAt(zap.InfoLevel)
	case "warn":
		zapLevel = zap.NewAtomicLevelAt(zap.WarnLevel)
	case "error":
		zapLevel = zap.NewAtomicLevelAt(zap.ErrorLevel)
	default:
		zapLevel = zap.NewAtomicLevelAt(zap.InfoLevel)
	}

	config := zap.Config{
		Level:            zapLevel,
		Development:      false,
		Encoding:         "console",
		EncoderConfig:    zap.NewDevelopmentEncoderConfig(),
		OutputPaths:      []string{"stderr"},
		ErrorOutputPaths: []string{"stderr"},
	}

	return config.Build()
}

// exitError wraps an error with the given exit code
type exitCodeError struct {
	code int
}

func (e exitCodeError) Error() string {
	return fmt.Sprintf("exit code %d", e.code)
}

func exitError(code int) error {
	os.Exit(code)
	return exitCodeError{code: code}
}
