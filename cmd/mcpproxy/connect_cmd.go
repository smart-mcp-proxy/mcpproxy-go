package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
)

var (
	connectList       bool
	connectAll        bool
	connectForce      bool
	connectServerName string
)

// GetConnectCommand returns the connect parent command.
func GetConnectCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "connect [client]",
		Short: "Register MCPProxy in a client's MCP configuration",
		Long: `Register MCPProxy as an MCP server in the configuration file of supported
AI coding clients. This modifies the client's config file to add an HTTP/SSE
entry pointing to the running MCPProxy instance.

Supported clients: claude-code, cursor, windsurf, vscode, codex, gemini, opencode, zcode

A backup of the original config file is created before any modification.

Examples:
  mcpproxy connect --list                    # Show all clients and their status
  mcpproxy connect claude-code               # Register in Claude Code
  mcpproxy connect cursor --force            # Overwrite existing entry
  mcpproxy connect codex --name my-proxy     # Custom server name
  mcpproxy connect opencode                  # Register in OpenCode
  mcpproxy connect --all                     # Register in all supported clients`,
		Args: cobra.MaximumNArgs(1),
		RunE: runConnect,
	}

	cmd.Flags().BoolVar(&connectList, "list", false, "List all clients and their connection status")
	cmd.Flags().BoolVar(&connectAll, "all", false, "Connect to all supported clients")
	cmd.Flags().BoolVar(&connectForce, "force", false, "Overwrite existing entry")
	cmd.Flags().StringVar(&connectServerName, "name", "", "Server name in client config (default: mcpproxy)")

	return cmd
}

// GetDisconnectCommand returns the disconnect command.
func GetDisconnectCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disconnect <client>",
		Short: "Remove MCPProxy from a client's MCP configuration",
		Long: `Remove the MCPProxy entry from the specified client's configuration file.
A backup of the original config file is created before any modification.

Examples:
  mcpproxy disconnect claude-code
  mcpproxy disconnect cursor --name my-proxy`,
		Args: cobra.ExactArgs(1),
		RunE: runDisconnect,
	}

	cmd.Flags().StringVar(&connectServerName, "name", "", "Server name to remove (default: mcpproxy)")

	return cmd
}

func runConnect(cmd *cobra.Command, args []string) error {
	cfg, err := loadConnectConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	svc := connect.NewService(cfg.Listen, cfg.APIKey).WithRequireMCPAuth(cfg.RequireMCPAuth)

	format := clioutput.ResolveFormat(globalOutputFormat, globalJSONOutput)
	formatter, err := clioutput.NewFormatter(format)
	if err != nil {
		return err
	}

	// --list mode
	if connectList {
		return printConnectStatus(svc, formatter, format)
	}

	// --all mode
	if connectAll {
		return connectAllClients(svc, formatter, format)
	}

	// Single client mode
	if len(args) == 0 {
		return fmt.Errorf("client ID is required (or use --list / --all)")
	}

	clientID := args[0]
	result, err := svc.Connect(clientID, connectServerName, connectForce)
	if err != nil {
		return err
	}

	return printConnectResult(result, formatter, format)
}

func runDisconnect(cmd *cobra.Command, args []string) error {
	cfg, err := loadConnectConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	svc := connect.NewService(cfg.Listen, cfg.APIKey).WithRequireMCPAuth(cfg.RequireMCPAuth)

	format := clioutput.ResolveFormat(globalOutputFormat, globalJSONOutput)
	formatter, err := clioutput.NewFormatter(format)
	if err != nil {
		return err
	}

	clientID := args[0]
	result, err := svc.Disconnect(clientID, connectServerName)
	if err != nil {
		return err
	}

	return printConnectResult(result, formatter, format)
}

func printConnectStatus(svc *connect.Service, formatter clioutput.OutputFormatter, format string) error {
	// Spec 075: GetAllStatus is content-read-free and leaves Connected/AccessState
	// unresolved. Running `mcpproxy connect` is an explicit user action, so resolve
	// each supported+installed client's connected state on demand via GetStatus to
	// preserve the CONNECTED column. Unsupported/absent clients keep the cheap
	// metadata-only listing (no content read).
	statuses := svc.GetAllStatus()
	for i := range statuses {
		if statuses[i].Supported && statuses[i].Exists {
			if st, err := svc.GetStatus(statuses[i].ID); err == nil {
				statuses[i] = st
			}
		}
	}

	if format == "table" {
		headers := []string{"CLIENT", "STATUS", "CONFIG PATH", "CONNECTED"}
		var rows [][]string
		for _, s := range statuses {
			status := "supported"
			if !s.Supported {
				status = "unsupported"
			}

			connected := "-"
			if s.Supported {
				if s.Connected {
					connected = "yes"
				} else if s.Exists {
					connected = "no"
				} else {
					connected = "no (no config)"
				}
			}

			// FR-037: the table's CONFIG PATH column shows the home-shortened
			// display_path; the full path is still available via -o json.
			cfgPath := s.DisplayPath
			if cfgPath == "" {
				cfgPath = s.ConfigPath
			}
			if len(cfgPath) > 50 {
				cfgPath = "..." + cfgPath[len(cfgPath)-47:]
			}

			rows = append(rows, []string{s.Name, status, cfgPath, connected})
		}
		out, err := formatter.FormatTable(headers, rows)
		if err != nil {
			return err
		}
		fmt.Print(out)
		return nil
	}

	// JSON or YAML
	out, err := formatter.Format(statuses)
	if err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}

func connectAllClients(svc *connect.Service, formatter clioutput.OutputFormatter, format string) error {
	clients := connect.GetAllClients()
	var results []*connect.ConnectResult
	var errors []string

	for _, c := range clients {
		if !c.Supported {
			continue
		}
		result, err := svc.Connect(c.ID, connectServerName, connectForce)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", c.Name, err))
			continue
		}
		results = append(results, result)
	}

	if format == "table" {
		// FR-037/FR-042: --all lists many clients at once, so the same
		// "Config:"/"Next:" information the single-client path prints as
		// prose becomes two more columns here instead of N repeated blocks.
		headers := []string{"CLIENT", "ACTION", "MESSAGE", "CONFIG PATH", "NEXT"}
		var rows [][]string
		for _, r := range results {
			client := connect.FindClient(r.Client)
			name := r.Client
			if client != nil {
				name = client.Name
			}
			rows = append(rows, []string{name, r.Action, r.Message, connectResultDisplayPath(r), r.ReloadHint})
		}
		for _, e := range errors {
			parts := strings.SplitN(e, ": ", 2)
			msg := e
			clientName := "unknown"
			if len(parts) == 2 {
				clientName = parts[0]
				msg = parts[1]
			}
			rows = append(rows, []string{clientName, "error", msg, "", ""})
		}
		out, err := formatter.FormatTable(headers, rows)
		if err != nil {
			return err
		}
		fmt.Print(out)
		return nil
	}

	// JSON/YAML output
	out, err := formatter.Format(map[string]interface{}{
		"results": results,
		"errors":  errors,
	})
	if err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}

func printConnectResult(result *connect.ConnectResult, formatter clioutput.OutputFormatter, format string) error {
	if format == "table" {
		if result.Success {
			fmt.Printf("%s\n", result.Message)
			if result.BackupPath != "" {
				// Review round 4: this used to print the raw, un-shortened
				// BackupPath directly above the home-shortened "Config: ~/…"
				// line below, mixing a full path and a "~"-shortened path in
				// the same output block.
				fmt.Printf("Backup: %s\n", connect.DisplayPath(result.BackupPath, ""))
			}
			// FR-037: Config shows the home-shortened display_path; the full
			// path is still available via -o json's config_path.
			fmt.Printf("Config: %s\n", connectResultDisplayPath(result))
			// FR-042: name the client's reload step so a successful write
			// doesn't read as "done" when the client hasn't picked it up yet.
			if result.ReloadHint != "" {
				fmt.Printf("Next: %s\n", result.ReloadHint)
			}
		} else {
			fmt.Printf("Failed: %s\n", result.Message)
		}
		return nil
	}

	// JSON/YAML
	out, err := formatter.Format(result)
	if err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}

func loadConnectConfig() (*config.Config, error) {
	return loadCLIConfig(configFile)
}

// connectResultDisplayPath returns the home-shortened path for a table-format
// connect/disconnect result, falling back to the full ConfigPath for a result
// from a Service build that predates DisplayPath (defensive; the field is
// always populated by the current connect.Service).
func connectResultDisplayPath(result *connect.ConnectResult) string {
	if result.DisplayPath != "" {
		return result.DisplayPath
	}
	return result.ConfigPath
}
