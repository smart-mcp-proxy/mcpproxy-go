package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// The CLAUDE.md / AGENTS.md block documented in
// docs/features/agent-instructions.md — keep the two in sync. The workflow
// lines follow the configured routing_mode, since each mode exposes a
// different set of built-in tools.
const agentInstructionsHeader = `## MCP tools via mcpproxy

External services (GitHub, Jira, Slack, databases, cloud APIs, …) are available
as MCP tools through the ` + "`mcpproxy`" + ` MCP server.
`

const agentInstructionsRetrieve = `Its tools are NOT listed individually.

- Before using a shell CLI (` + "`gh`, `aws`, `kubectl`, `curl`" + `, …) or a raw HTTP API
  for an external service, call ` + "`mcpproxy`" + ` ` + "`retrieve_tools`" + ` with the task and
  the service name (e.g. "create github issue").
- Call the tool it returns via the ` + "`call_tool_*`" + ` variant named in ` + "`call_with`" + `.
- If nothing relevant comes back, retry with different wording or just the
  service name before concluding the tool does not exist.
- Use ` + "`upstream_servers`" + ` (operation ` + "`list`" + `) to see which servers are connected.
`

const agentInstructionsCodeExecution = `Its tools are NOT listed individually.

- Before using a shell CLI (` + "`gh`, `aws`, `kubectl`, `curl`" + `, …) or a raw HTTP API
  for an external service, call ` + "`mcpproxy`" + ` ` + "`retrieve_tools`" + ` with the task and
  the service name (e.g. "create github issue").
- Run the tools it returns with ` + "`code_execution`" + ` (JavaScript ` + "`call_tool(server, tool, args)`" + `).
- If nothing relevant comes back, retry with different wording or just the
  service name before concluding the tool does not exist.
`

// routingModeCodeExecutionDisabled is a CLI-local pseudo mode:
// routing_mode code_execution with enable_code_execution false.
const routingModeCodeExecutionDisabled = "code_execution_disabled"

const agentInstructionsCodeExecutionDisabled = `Its tools are NOT listed individually.

- Before using a shell CLI (` + "`gh`, `aws`, `kubectl`, `curl`" + `, …) or a raw HTTP API
  for an external service, call ` + "`mcpproxy`" + ` ` + "`retrieve_tools`" + ` with the task and
  the service name (e.g. "create github issue") to check whether a tool exists.
- Calling those tools needs ` + "`code_execution`" + `, which is disabled in this mcpproxy
  config; ask the user to enable it rather than working around it.
`

const agentInstructionsDirect = `Each upstream tool is listed as ` + "`<server>__<tool>`" + ` (e.g. ` + "`github__create_issue`" + `).

- Before using a shell CLI (` + "`gh`, `aws`, `kubectl`, `curl`" + `, …) or a raw HTTP API
  for an external service, search your MCP tool list for the service name and
  prefer the mcpproxy tool.
- Call ` + "`describe_tool`" + ` for a tool's full input schema when its listing is abbreviated.
`

// snippetServerName mirrors internal/server's advertisableServerName.
var snippetServerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

var (
	agentInstructionsWithServers bool
	agentInstructionsClient      string
)

var agentInstructionsCmd = &cobra.Command{
	Use:   "agent-instructions",
	Short: "Print a CLAUDE.md / AGENTS.md snippet that makes agents use mcpproxy tools",
	Long: `Print a snippet for an agent's memory file (CLAUDE.md, AGENTS.md, Cursor rules)
telling it that external services are reachable through mcpproxy, so it stops
falling back to shell CLIs such as gh or curl. The workflow follows the
configured routing_mode.

Append it to the file your client reads:

  mcpproxy agent-instructions >> AGENTS.md
  mcpproxy agent-instructions --with-servers >> ~/.claude/CLAUDE.md
  mcpproxy agent-instructions --client cursor > .cursor/rules/mcpproxy.mdc

See https://docs.mcpproxy.app/features/agent-instructions`,
	Args: cobra.NoArgs,
	RunE: runAgentInstructions,
}

func init() {
	agentInstructionsCmd.Flags().BoolVar(&agentInstructionsWithServers, "with-servers", false,
		"List the currently connected upstream servers in the snippet (asks the running daemon; falls back to enabled servers in the config)")
	agentInstructionsCmd.Flags().StringVar(&agentInstructionsClient, "client", "",
		"Format for a specific client: claude-code, codex or cursor (cursor adds .mdc front matter)")
}

func runAgentInstructions(cmd *cobra.Command, _ []string) error {
	switch agentInstructionsClient {
	case "", "claude-code", "codex", "cursor":
	default:
		return fmt.Errorf("unknown --client %q (want claude-code, codex or cursor)", agentInstructionsClient)
	}
	mode := ""
	if cfg, err := loadUpstreamConfig(); err == nil && cfg != nil {
		mode = cfg.RoutingMode
		if mode == config.RoutingModeCodeExecution && !cfg.EnableCodeExecution {
			// code_execution mode with the tool switched off exposes no
			// calling tool at all; don't prescribe one that is absent.
			mode = routingModeCodeExecutionDisabled
		}
	}
	var servers []string
	if agentInstructionsWithServers {
		servers = agentInstructionsServers()
	}
	_, err := fmt.Fprint(cmd.OutOrStdout(), buildAgentInstructions(agentInstructionsClient, mode, servers))
	return err
}

// buildAgentInstructions renders the snippet for client and routing mode
// (unset or unknown = retrieve_tools, the default) with an optional server
// list.
func buildAgentInstructions(client, routingMode string, servers []string) string {
	var b strings.Builder
	if client == "cursor" {
		b.WriteString("---\ndescription: Use mcpproxy MCP tools for external services\nalwaysApply: true\n---\n\n")
	}
	b.WriteString(agentInstructionsHeader)
	switch routingMode {
	case config.RoutingModeDirect:
		b.WriteString(agentInstructionsDirect)
	case config.RoutingModeCodeExecution:
		b.WriteString(agentInstructionsCodeExecution)
	case routingModeCodeExecutionDisabled:
		b.WriteString(agentInstructionsCodeExecutionDisabled)
	default:
		b.WriteString(agentInstructionsRetrieve)
	}
	// Same rule as the MCP surface (advertisableServerName): a name that
	// could carry prompt text never lands in an agent memory file.
	var safe []string
	for _, name := range servers {
		if snippetServerName.MatchString(name) {
			safe = append(safe, name)
		}
	}
	if len(safe) > 0 {
		sort.Strings(safe)
		b.WriteString("\nServers currently behind mcpproxy: " + strings.Join(safe, ", ") + ".\n")
	}
	return b.String()
}

// agentInstructionsServers asks the running daemon for connected servers,
// falling back to the config's enabled, non-quarantined servers.
func agentInstructionsServers() []string {
	globalConfig, err := loadUpstreamConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: cannot load config, omitting server list: %v\n", err)
		return nil
	}
	logger, err := createUpstreamLogger("error")
	if err == nil {
		if client, ok := newDaemonClient(globalConfig, logger.Sugar()); ok {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if list, err := client.GetServers(ctx); err == nil {
				var names []string
				for _, s := range list {
					if getBoolField(s, "connected") && !getBoolField(s, "quarantined") {
						names = append(names, getStringField(s, "name"))
					}
				}
				return names
			}
		}
	}
	fmt.Fprintln(os.Stderr, "note: daemon not reachable; listing enabled servers from the config file")
	var names []string
	for _, s := range globalConfig.Servers {
		if s != nil && s.Enabled && !s.Quarantined {
			names = append(names, s.Name)
		}
	}
	return names
}
