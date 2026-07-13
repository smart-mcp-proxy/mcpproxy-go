package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/experiments"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/socket"
)

// Registry command flags (spec 070).
var (
	registryConfigPath     string
	registrySearchTag      string
	registryLimit          int
	registryAddName        string
	registryAddEnv         []string
	registryAddEnabled     bool
	registryAddSourceProto string // MCP-866
	registryAddSourceID    string
	registryAddSourceName  string
	registryEditName       string // MCP-1072
	registryEditURL        string
	registryEditServersURL string
)

// GetRegistryCommand builds the `registry` command group (spec 070): a single
// discovery→add flow on the CLI.
//
//   - `registry list` / `registry search` are daemon-first with an in-process
//     fallback, so discovery works whether or not a daemon is running.
//   - `registry add` REQUIRES a running daemon: the keystone add op
//     (registry→config derivation + quarantine) lives server-side so identical
//     input yields an identical persisted config across every surface and a
//     client cannot smuggle in arbitrary command/args (CN-001 / decision D1).
//
// The legacy top-level `search-servers` command is retained unchanged as a
// back-compat alias.
func GetRegistryCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "registry",
		Short: "Discover and add MCP servers from registries",
		Long: `Discover MCP servers in known registries and add them as upstream servers.

Typical flow:
  mcpproxy registry list                       # see available registries
  mcpproxy registry search weather -r pulse    # find a server
  mcpproxy registry add pulse weather-mcp       # add it (quarantined)
  mcpproxy upstream approve weather-mcp          # approve once you trust it

Add your own registry source (any official modelcontextprotocol/registry v0.1 endpoint):
  mcpproxy registry add-source https://registry.example.com   # tagged "custom"
  mcpproxy registry edit my-reg --url https://new.example.com  # update a custom source

'registry add', 'add-source', 'edit' and 'remove' talk to the running mcpproxy
daemon. 'list' and 'search' use the daemon when available and otherwise read the
registries directly.`,
	}

	cmd.PersistentFlags().StringVarP(&registryConfigPath, "config", "c", "", "Path to MCP configuration file")
	cmd.AddCommand(newRegistryListCmd(), newRegistrySearchCmd(), newRegistryAddCmd(), newRegistryAddSourceCmd(), newRegistryEditCmd(), newRegistryRemoveCmd())
	return cmd
}

func newRegistryRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "remove <id>",
		Aliases: []string{"remove-source", "rm"},
		Short:   "Remove a user-added custom registry source",
		Long: `Remove a custom MCP registry source you previously added with
'registry add-source'. Use 'registry list' to see the ids.

Only custom/unverified registries can be removed — the shipped built-in
registries (official, reference, docker-mcp-catalog, pulse, smithery) cannot be
removed. Removing a source does not touch any upstream servers you already added
from it.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			registryID := args[0]

			cfg, err := loadRegistryConfig()
			if err != nil {
				return outputError(clioutput.NewStructuredError(clioutput.ErrCodeConfigNotFound, err.Error()).
					WithRecoveryCommand("mcpproxy doctor"), clioutput.ErrCodeConfigNotFound)
			}

			// remove MUST go through the daemon: the registry list lives on the
			// runtime config snapshot and is updated copy-on-write via UpdateConfig.
			if !shouldUseUpstreamDaemon(cfg.DataDir) {
				return outputError(clioutput.NewStructuredError(clioutput.ErrCodeConnectionFailed,
					"removing a registry source requires a running mcpproxy daemon").
					WithGuidance("Start the daemon, then retry").
					WithRecoveryCommand("mcpproxy serve"), clioutput.ErrCodeConnectionFailed)
			}

			ctx, cancel := registryContext()
			defer cancel()

			client := cliclient.NewClient(socket.DetectSocketPath(cfg.DataDir), nil)
			reg, err := client.RemoveRegistrySource(ctx, registryID)
			if err != nil {
				return registryRemoveErrorOutput(err)
			}

			outputFormat := ResolveOutputFormat()
			if outputFormat == "json" || outputFormat == "yaml" {
				formatter, _ := GetOutputFormatter()
				out, _ := formatter.Format(reg)
				fmt.Println(out)
				return nil
			}

			fmt.Printf("✅ Removed registry source '%s'\n", reg.ID)
			return nil
		},
	}
	return cmd
}

// registryRemoveErrorOutput maps a *cliclient.RegistryAddError from a remove op
// to a structured CLI error with remove-specific guidance (MCP-1057).
func registryRemoveErrorOutput(err error) error {
	var addErr *cliclient.RegistryAddError
	if !errors.As(err, &addErr) {
		return outputError(clioutput.NewStructuredError(clioutput.ErrCodeOperationFailed, err.Error()), clioutput.ErrCodeOperationFailed)
	}

	switch addErr.Code {
	case "registry_not_found":
		return outputError(clioutput.NewStructuredError(clioutput.ErrCodeServerNotFound, addErr.Message).
			WithGuidance("Check the ids with 'mcpproxy registry list' — only custom/unverified registries can be removed"), clioutput.ErrCodeServerNotFound)
	case "registry_shadows_builtin":
		return outputError(clioutput.NewStructuredError(clioutput.ErrCodeInvalidInput, addErr.Message).
			WithGuidance("Built-in registries cannot be removed"), clioutput.ErrCodeInvalidInput)
	case "registries_locked":
		return outputError(clioutput.NewStructuredError(clioutput.ErrCodeOperationFailed, addErr.Message).
			WithGuidance("Registry changes are disabled by policy (registries_locked)"), clioutput.ErrCodeOperationFailed)
	default:
		return outputError(clioutput.NewStructuredError(clioutput.ErrCodeOperationFailed, addErr.Message), clioutput.ErrCodeOperationFailed)
	}
}

func newRegistryAddSourceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add-source <https-url>",
		Short: "Add a custom MCP registry source",
		Long: `Add your own MCP server registry — any https endpoint implementing the
official modelcontextprotocol/registry v0.1 protocol (the same protocol shipped
by Copilot/VS Code/Azure).

That protocol is the ONLY one MCPProxy speaks. The URL is fetched when you add
it and rejected right away if it turns out not to be a v0.1 registry — a static
JSON catalog or an HTML page cannot be browsed, and you hear about it now rather
than on your first search. Paste either the base URL or the full servers
endpoint; whatever you paste is used as-is.

The added source is tagged "custom" (informational). Servers you discover and
add through it follow the global quarantine default like any other server, so
with quarantine enabled (the default) they land quarantined for review:
  mcpproxy registry search <query> -r <id>
  mcpproxy registry add <id> <serverId>
  mcpproxy upstream approve <name>`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			sourceURL := args[0]

			cfg, err := loadRegistryConfig()
			if err != nil {
				return outputError(clioutput.NewStructuredError(clioutput.ErrCodeConfigNotFound, err.Error()).
					WithRecoveryCommand("mcpproxy doctor"), clioutput.ErrCodeConfigNotFound)
			}

			// add-source MUST go through the daemon: the registry list lives on the
			// runtime config snapshot and is updated copy-on-write via UpdateConfig.
			if !shouldUseUpstreamDaemon(cfg.DataDir) {
				return outputError(clioutput.NewStructuredError(clioutput.ErrCodeConnectionFailed,
					"adding a registry source requires a running mcpproxy daemon").
					WithGuidance("Start the daemon, then retry").
					WithRecoveryCommand("mcpproxy serve"), clioutput.ErrCodeConnectionFailed)
			}

			ctx, cancel := registryContext()
			defer cancel()

			client := cliclient.NewClient(socket.DetectSocketPath(cfg.DataDir), nil)
			reg, err := client.AddRegistrySource(ctx, sourceURL, registryAddSourceProto, registryAddSourceID, registryAddSourceName)
			if err != nil {
				return registryAddErrorOutput(err)
			}

			outputFormat := ResolveOutputFormat()
			if outputFormat == "json" || outputFormat == "yaml" {
				formatter, _ := GetOutputFormatter()
				out, _ := formatter.Format(reg)
				fmt.Println(out)
				return nil
			}

			fmt.Printf("✅ Added registry source '%s' (%s)\n", reg.ID, reg.Provenance)
			fmt.Printf("⚠️  This is a third-party registry — with quarantine enabled, servers you add from it are quarantined until you approve them.\n")
			fmt.Printf("   Search it with: mcpproxy registry search <query> -r %s\n", reg.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&registryAddSourceProto, "protocol", "", "Registry protocol (default: modelcontextprotocol/registry)")
	cmd.Flags().StringVar(&registryAddSourceID, "id", "", "Override the derived registry id")
	cmd.Flags().StringVar(&registryAddSourceName, "name", "", "Override the registry display name")
	return cmd
}

func newRegistryEditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit a user-added custom registry source",
		Long: `Update a custom MCP registry source you previously added with
'registry add-source'. Use 'registry list' to see the ids.

Only custom registries can be edited — the shipped built-in registries cannot.
Provide one or more of --name, --url, --servers-url; omitted fields are left
unchanged. Changing --url re-derives the servers URL unless --servers-url is
also given.

  mcpproxy registry edit my-reg --url https://new.example.com
  mcpproxy registry edit my-reg --name "My Registry"`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			registryID := args[0]

			cfg, err := loadRegistryConfig()
			if err != nil {
				return outputError(clioutput.NewStructuredError(clioutput.ErrCodeConfigNotFound, err.Error()).
					WithRecoveryCommand("mcpproxy doctor"), clioutput.ErrCodeConfigNotFound)
			}

			// edit MUST go through the daemon: the registry list lives on the
			// runtime config snapshot and is updated copy-on-write via UpdateConfig.
			if !shouldUseUpstreamDaemon(cfg.DataDir) {
				return outputError(clioutput.NewStructuredError(clioutput.ErrCodeConnectionFailed,
					"editing a registry source requires a running mcpproxy daemon").
					WithGuidance("Start the daemon, then retry").
					WithRecoveryCommand("mcpproxy serve"), clioutput.ErrCodeConnectionFailed)
			}

			ctx, cancel := registryContext()
			defer cancel()

			client := cliclient.NewClient(socket.DetectSocketPath(cfg.DataDir), nil)
			reg, err := client.EditRegistrySource(ctx, registryID, registryEditName, registryEditURL, registryEditServersURL)
			if err != nil {
				return registryEditErrorOutput(err)
			}

			outputFormat := ResolveOutputFormat()
			if outputFormat == "json" || outputFormat == "yaml" {
				formatter, _ := GetOutputFormatter()
				out, _ := formatter.Format(reg)
				fmt.Println(out)
				return nil
			}

			fmt.Printf("✅ Updated registry source '%s'\n", reg.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&registryEditName, "name", "", "New registry display name")
	cmd.Flags().StringVar(&registryEditURL, "url", "", "New base/servers https URL")
	cmd.Flags().StringVar(&registryEditServersURL, "servers-url", "", "New servers-collection URL (overrides the derived one)")
	return cmd
}

// registryEditErrorOutput maps a *cliclient.RegistryAddError from an edit op to
// a structured CLI error with edit-specific guidance (MCP-1072).
func registryEditErrorOutput(err error) error {
	var addErr *cliclient.RegistryAddError
	if !errors.As(err, &addErr) {
		return outputError(clioutput.NewStructuredError(clioutput.ErrCodeOperationFailed, err.Error()), clioutput.ErrCodeOperationFailed)
	}

	switch addErr.Code {
	case "registry_not_found":
		return outputError(clioutput.NewStructuredError(clioutput.ErrCodeServerNotFound, addErr.Message).
			WithGuidance("Check the ids with 'mcpproxy registry list' — only custom registries can be edited"), clioutput.ErrCodeServerNotFound)
	case "registry_shadows_builtin":
		return outputError(clioutput.NewStructuredError(clioutput.ErrCodeInvalidInput, addErr.Message).
			WithGuidance("Built-in registries cannot be edited"), clioutput.ErrCodeInvalidInput)
	case "invalid_registry_url":
		return outputError(clioutput.NewStructuredError(clioutput.ErrCodeInvalidInput, addErr.Message).
			WithGuidance("Provide a valid https URL"), clioutput.ErrCodeInvalidInput)
	case "registries_locked":
		return outputError(clioutput.NewStructuredError(clioutput.ErrCodeOperationFailed, addErr.Message).
			WithGuidance("Registry changes are disabled by policy (registries_locked)"), clioutput.ErrCodeOperationFailed)
	default:
		return outputError(clioutput.NewStructuredError(clioutput.ErrCodeOperationFailed, addErr.Message), clioutput.ErrCodeOperationFailed)
	}
}

func newRegistryListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available MCP server registries",
		RunE: func(_ *cobra.Command, _ []string) error {
			ctx, cancel := registryContext()
			defer cancel()

			cfg, err := loadRegistryConfig()
			if err != nil {
				return outputError(clioutput.NewStructuredError(clioutput.ErrCodeConfigNotFound, err.Error()).
					WithRecoveryCommand("mcpproxy doctor"), clioutput.ErrCodeConfigNotFound)
			}
			formatter, err := GetOutputFormatter()
			if err != nil {
				return err
			}

			// Daemon-first.
			if shouldUseUpstreamDaemon(cfg.DataDir) {
				client := cliclient.NewClient(socket.DetectSocketPath(cfg.DataDir), nil)
				if regs, derr := client.ListRegistries(ctx); derr == nil {
					return renderRegistries(formatter, regs)
				}
				// Fall through to in-process on daemon error.
			}

			// In-process fallback.
			registries.SetRegistriesFromConfig(cfg)
			local := registries.ListRegistries()
			regs := make([]map[string]interface{}, len(local))
			for i := range local {
				regs[i] = map[string]interface{}{
					"id":          local[i].ID,
					"name":        local[i].Name,
					"description": local[i].Description,
				}
			}
			return renderRegistries(formatter, regs)
		},
	}
}

func newRegistrySearchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search a registry for MCP servers",
		Long: `Search a registry for MCP servers matching a query.

The registry is selected with --registry (-r). Use 'registry list' to see ids.
The printed ID column is what you pass to 'registry add'.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := ""
			if len(args) > 0 {
				query = args[0]
			}
			registryID, _ := cmd.Flags().GetString("registry")
			if registryID == "" {
				return fmt.Errorf("--registry is required (use 'mcpproxy registry list' to see available ids)")
			}

			ctx, cancel := registryContext()
			defer cancel()

			cfg, err := loadRegistryConfig()
			if err != nil {
				return outputError(clioutput.NewStructuredError(clioutput.ErrCodeConfigNotFound, err.Error()).
					WithRecoveryCommand("mcpproxy doctor"), clioutput.ErrCodeConfigNotFound)
			}
			formatter, err := GetOutputFormatter()
			if err != nil {
				return err
			}

			// Daemon-first.
			if shouldUseUpstreamDaemon(cfg.DataDir) {
				client := cliclient.NewClient(socket.DetectSocketPath(cfg.DataDir), nil)
				if servers, derr := client.SearchRegistry(ctx, registryID, registrySearchTag, query, registryLimit); derr == nil {
					return renderRegistryServers(formatter, servers)
				}
				// Fall through to in-process on daemon error.
			}

			// In-process fallback (mirrors the legacy search-servers path).
			registries.SetRegistriesFromConfig(cfg)
			var guesser *experiments.Guesser
			if cfg.CheckServerRepo {
				guesser = experiments.NewGuesser(nil, zap.NewNop())
			}
			entries, serr := registries.SearchServers(ctx, registryID, registrySearchTag, query, registryLimit, guesser)
			if serr != nil {
				return outputError(clioutput.NewStructuredError(clioutput.ErrCodeOperationFailed, serr.Error()), clioutput.ErrCodeOperationFailed)
			}
			servers := make([]map[string]interface{}, len(entries))
			for i := range entries {
				installCmd := entries[i].InstallCmd
				if installCmd == "" && entries[i].RepositoryInfo != nil && entries[i].RepositoryInfo.NPM != nil {
					installCmd = entries[i].RepositoryInfo.NPM.InstallCmd
				}
				servers[i] = map[string]interface{}{
					"id":          entries[i].ID,
					"name":        entries[i].Name,
					"description": entries[i].Description,
					"installCmd":  installCmd,
					"url":         entries[i].URL,
				}
			}
			return renderRegistryServers(formatter, servers)
		},
	}
	cmd.Flags().StringP("registry", "r", "", "Registry id to search (use 'registry list' to see ids)")
	cmd.Flags().StringVarP(&registrySearchTag, "tag", "t", "", "Filter servers by tag/category")
	cmd.Flags().IntVarP(&registryLimit, "limit", "l", 10, "Maximum number of results to return")
	return cmd
}

func newRegistryAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <registryId> <serverId>",
		Short: "Add a server from a registry as a (quarantined) upstream",
		Long: `Add a server discovered in a registry as an upstream server.

The server is added quarantined by default; approve it once you trust it:
  mcpproxy upstream approve <name>

The daemon re-derives the runnable config (command/args/url) from the registry
entry — you only supply optional overrides. If the server declares required
inputs, supply them with --env KEY=VALUE.`,
		Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			registryID, serverID := args[0], args[1]

			env, err := parseRegistryEnv(registryAddEnv)
			if err != nil {
				return err
			}

			cfg, err := loadRegistryConfig()
			if err != nil {
				return outputError(clioutput.NewStructuredError(clioutput.ErrCodeConfigNotFound, err.Error()).
					WithRecoveryCommand("mcpproxy doctor"), clioutput.ErrCodeConfigNotFound)
			}

			// add MUST go through the daemon (keystone op is server-side).
			if !shouldUseUpstreamDaemon(cfg.DataDir) {
				return outputError(clioutput.NewStructuredError(clioutput.ErrCodeConnectionFailed,
					"adding from a registry requires a running mcpproxy daemon").
					WithGuidance("Start the daemon, then retry").
					WithRecoveryCommand("mcpproxy serve"), clioutput.ErrCodeConnectionFailed)
			}

			ctx, cancel := registryContext()
			defer cancel()

			client := cliclient.NewClient(socket.DetectSocketPath(cfg.DataDir), nil)
			enabled := registryAddEnabled
			result, err := client.AddFromRegistry(ctx, registryID, serverID, registryAddName, env, &enabled)
			if err != nil {
				return registryAddErrorOutput(err)
			}

			outputFormat := ResolveOutputFormat()
			if outputFormat == "json" || outputFormat == "yaml" {
				formatter, _ := GetOutputFormatter()
				out, _ := formatter.Format(result)
				fmt.Println(out)
				return nil
			}

			fmt.Printf("✅ Added '%s'", result.Name)
			if result.Quarantined {
				fmt.Printf(" (quarantined — approve with: mcpproxy upstream approve %s)", result.Name)
			}
			fmt.Println()
			return nil
		},
	}
	cmd.Flags().StringVar(&registryAddName, "name", "", "Override the server name")
	cmd.Flags().StringArrayVar(&registryAddEnv, "env", nil, "Set an environment variable (KEY=VALUE); repeatable")
	cmd.Flags().BoolVar(&registryAddEnabled, "enabled", true, "Whether the added server is enabled")
	return cmd
}

// registryAddErrorOutput maps a *cliclient.RegistryAddError to a structured CLI
// error. For missing_required_input it names the exact --env keys to supply.
func registryAddErrorOutput(err error) error {
	var addErr *cliclient.RegistryAddError
	if !errors.As(err, &addErr) {
		return outputError(clioutput.NewStructuredError(clioutput.ErrCodeOperationFailed, err.Error()), clioutput.ErrCodeOperationFailed)
	}

	switch addErr.Code {
	case "missing_required_input":
		guidance := "Supply the required input(s) with --env"
		if len(addErr.MissingInputs) > 0 {
			example := addErr.MissingInputs[0]
			guidance = fmt.Sprintf("Provide: %s — e.g. --env %s=<value>",
				strings.Join(addErr.MissingInputs, ", "), example)
		}
		return outputError(clioutput.NewStructuredError(clioutput.ErrCodeInvalidInput, addErr.Message).
			WithGuidance(guidance), clioutput.ErrCodeInvalidInput)
	case "duplicate_name":
		return outputError(clioutput.NewStructuredError(clioutput.ErrCodeOperationFailed, addErr.Message).
			WithGuidance("Choose a different name with --name, or remove the existing server"), clioutput.ErrCodeOperationFailed)
	case "registry_not_found", "server_not_found":
		return outputError(clioutput.NewStructuredError(clioutput.ErrCodeServerNotFound, addErr.Message).
			WithGuidance("Check the ids with 'mcpproxy registry list' and 'mcpproxy registry search'"), clioutput.ErrCodeServerNotFound)
	case "invalid_registry_url":
		return outputError(clioutput.NewStructuredError(clioutput.ErrCodeInvalidInput, addErr.Message).
			WithGuidance("Provide an https URL, e.g. https://registry.example.com"), clioutput.ErrCodeInvalidInput)
	case "unsupported_registry_protocol":
		return outputError(clioutput.NewStructuredError(clioutput.ErrCodeInvalidInput, addErr.Message).
			WithGuidance("MCPProxy speaks only modelcontextprotocol/registry v0.1 — omit --protocol"), clioutput.ErrCodeInvalidInput)
	case "registry_source_unusable":
		return outputError(clioutput.NewStructuredError(clioutput.ErrCodeInvalidInput, addErr.Message).
			WithGuidance("That URL does not serve a modelcontextprotocol/registry v0.1 server list"), clioutput.ErrCodeInvalidInput)
	case "registries_locked":
		return outputError(clioutput.NewStructuredError(clioutput.ErrCodeOperationFailed, addErr.Message).
			WithGuidance("Registry additions are disabled by policy (registries_locked)"), clioutput.ErrCodeOperationFailed)
	case "registry_shadows_builtin":
		return outputError(clioutput.NewStructuredError(clioutput.ErrCodeInvalidInput, addErr.Message).
			WithGuidance("Choose a different --id; built-in registry ids cannot be replaced"), clioutput.ErrCodeInvalidInput)
	case "duplicate_registry":
		return outputError(clioutput.NewStructuredError(clioutput.ErrCodeOperationFailed, addErr.Message).
			WithGuidance("A registry with that id already exists; pass a different --id"), clioutput.ErrCodeOperationFailed)
	default:
		return outputError(clioutput.NewStructuredError(clioutput.ErrCodeOperationFailed, addErr.Message), clioutput.ErrCodeOperationFailed)
	}
}

func parseRegistryEnv(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	env := make(map[string]string, len(pairs))
	for _, e := range pairs {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) != 2 || parts[0] == "" {
			return nil, fmt.Errorf("invalid --env format: %q (expected KEY=VALUE)", e)
		}
		env[parts[0]] = parts[1]
	}
	return env, nil
}

func renderRegistries(formatter clioutput.OutputFormatter, regs []map[string]interface{}) error {
	if _, isTable := formatter.(*clioutput.TableFormatter); isTable {
		headers := []string{"ID", "NAME", "DESCRIPTION"}
		rows := make([][]string, 0, len(regs))
		for _, r := range regs {
			rows = append(rows, []string{mapString(r, "id"), mapString(r, "name"), truncateStr(mapString(r, "description"), 60)})
		}
		out, err := formatter.FormatTable(headers, rows)
		if err != nil {
			return err
		}
		fmt.Print(out)
		fmt.Printf("\nFound %d registries. Search one with: mcpproxy registry search <query> -r <id>\n", len(regs))
		return nil
	}
	out, err := formatter.Format(regs)
	if err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}

func renderRegistryServers(formatter clioutput.OutputFormatter, servers []map[string]interface{}) error {
	if _, isTable := formatter.(*clioutput.TableFormatter); isTable {
		headers := []string{"ID", "NAME", "DESCRIPTION", "INSTALL CMD"}
		rows := make([][]string, 0, len(servers))
		for _, s := range servers {
			installCmd := mapString(s, "installCmd")
			if installCmd == "" {
				installCmd = "-"
			}
			rows = append(rows, []string{mapString(s, "id"), mapString(s, "name"), truncateStr(mapString(s, "description"), 45), installCmd})
		}
		out, err := formatter.FormatTable(headers, rows)
		if err != nil {
			return err
		}
		fmt.Print(out)
		fmt.Printf("\nFound %d servers. Add one with: mcpproxy registry add <registryId> <id>\n", len(servers))
		return nil
	}
	out, err := formatter.Format(servers)
	if err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}

func mapString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func truncateStr(s string, max int) string {
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}

// loadRegistryConfig loads config for the registry commands, honoring the
// command's --config flag and the global --data-dir, falling back to defaults
// so 'list'/'search' still work without a config file.
func loadRegistryConfig() (*config.Config, error) {
	var cfg *config.Config
	var err error
	if registryConfigPath != "" {
		cfg, err = config.LoadFromFile(registryConfigPath)
	} else {
		cfg, err = config.Load()
	}
	if err != nil {
		// Discovery should still work with defaults if no config is present.
		cfg = config.DefaultConfig()
	}
	if dataDir != "" {
		cfg.DataDir = dataDir
	}
	return cfg, nil
}

func registryContext() (context.Context, context.CancelFunc) {
	ctx := reqcontext.WithMetadata(context.Background(), reqcontext.SourceCLI)
	// Registry ops talk to live registries: add-source probes the URL, and a
	// search may page through a registry that takes ~20s per page (the official
	// one currently does). This must stay comfortably above the server-side
	// budgets, or the CLI reports a timeout for work the daemon actually finished.
	return context.WithTimeout(ctx, 120*time.Second)
}
