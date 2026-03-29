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

Supported clients: claude-code, cursor, windsurf, vscode, codex, gemini

A backup of the original config file is created before any modification.

Examples:
  mcpproxy connect --list                    # Show all clients and their status
  mcpproxy connect claude-code               # Register in Claude Code
  mcpproxy connect cursor --force            # Overwrite existing entry
  mcpproxy connect codex --name my-proxy     # Custom server name
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

	svc := connect.NewService(cfg.Listen, cfg.APIKey)

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

	svc := connect.NewService(cfg.Listen, cfg.APIKey)

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
	statuses := svc.GetAllStatus()

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

			cfgPath := s.ConfigPath
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
		headers := []string{"CLIENT", "ACTION", "MESSAGE"}
		var rows [][]string
		for _, r := range results {
			client := connect.FindClient(r.Client)
			name := r.Client
			if client != nil {
				name = client.Name
			}
			rows = append(rows, []string{name, r.Action, r.Message})
		}
		for _, e := range errors {
			parts := strings.SplitN(e, ": ", 2)
			msg := e
			clientName := "unknown"
			if len(parts) == 2 {
				clientName = parts[0]
				msg = parts[1]
			}
			rows = append(rows, []string{clientName, "error", msg})
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
				fmt.Printf("Backup: %s\n", result.BackupPath)
			}
			fmt.Printf("Config: %s\n", result.ConfigPath)
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
	if configFile != "" {
		cfg, err := config.LoadFromFile(configFile)
		if err != nil {
			return nil, err
		}
		if dataDir != "" {
			cfg.DataDir = dataDir
		}
		return cfg, nil
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if dataDir != "" {
		cfg.DataDir = dataDir
	}
	return cfg, nil
}
