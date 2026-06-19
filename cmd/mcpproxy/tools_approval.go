package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	// tools approve/reject flags
	toolsApprovalServer string // --server scoping
	toolsApprovalAll    bool   // --all (requires --server)
)

func newToolsApproveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approve [<server>:<tool>...]",
		Short: "Approve tools pending tool-level quarantine (Spec 032)",
		Long: `Approve one or more tools that are pending or changed in tool-level
quarantine, clearing them for use without the Web UI or MCP.

Targets may be given as <server>:<tool> pairs, or as bare tool names when
scoped with --server. Use --server <name> --all to approve every pending or
changed tool for a server.

Examples:
  mcpproxy tools approve github:create_issue
  mcpproxy tools approve github:create_issue github:list_repos
  mcpproxy tools approve --server github create_issue list_repos
  mcpproxy tools approve --server github --all`,
		RunE: func(_ *cobra.Command, args []string) error {
			return runToolsApproval(args, false)
		},
	}
	cmd.Flags().StringVarP(&toolsApprovalServer, "server", "s", "", "Scope bare tool names to this server (required with --all)")
	cmd.Flags().BoolVar(&toolsApprovalAll, "all", false, "Approve all pending/changed tools for --server")
	return cmd
}

func newToolsRejectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reject [<server>:<tool>...]",
		Short: "Reject (block) tools pending tool-level quarantine (Spec 032)",
		Long: `Reject one or more tools pending tool-level quarantine. Reject maps to the
"block" action: the tool is atomically approved AND disabled (hidden), so it is
never left in the approved+enabled state. Mirrors the Web UI "Block" button.

Targets may be given as <server>:<tool> pairs, or as bare tool names when
scoped with --server. Use --server <name> --all to reject every pending or
changed tool for a server.

Examples:
  mcpproxy tools reject github:delete_repo
  mcpproxy tools reject --server github delete_repo force_push
  mcpproxy tools reject --server github --all`,
		RunE: func(_ *cobra.Command, args []string) error {
			return runToolsApproval(args, true)
		},
	}
	cmd.Flags().StringVarP(&toolsApprovalServer, "server", "s", "", "Scope bare tool names to this server (required with --all)")
	cmd.Flags().BoolVar(&toolsApprovalAll, "all", false, "Reject all pending/changed tools for --server")
	return cmd
}

// resolveToolApprovalTargets validates approve/reject invocation flags and
// returns the per-server tool grouping.
//
//   - With all=true: requires a non-empty server and no positional args, and
//     returns {server: nil} with allMode=true (caller issues a single
//     approve-all/block-all call for that server).
//   - Otherwise: each positional arg is either an explicit "<server>:<tool>"
//     pair (the colon form takes precedence) or a bare tool name scoped to the
//     --server flag. Bare names without --server are rejected.
func resolveToolApprovalTargets(args []string, server string, all bool) (groups map[string][]string, allMode bool, err error) {
	if all {
		if server == "" {
			return nil, false, fmt.Errorf("--all requires --server <name>")
		}
		if len(args) > 0 {
			return nil, false, fmt.Errorf("--all cannot be combined with explicit <server>:<tool> targets")
		}
		return map[string][]string{server: nil}, true, nil
	}

	if len(args) == 0 {
		return nil, false, fmt.Errorf("no targets specified: provide <server>:<tool> args, or use --server <name> --all")
	}

	groups = make(map[string][]string)
	for _, arg := range args {
		if strings.Contains(arg, ":") {
			srv, tool, parseErr := parseServerTool(arg)
			if parseErr != nil {
				return nil, false, parseErr
			}
			groups[srv] = append(groups[srv], tool)
			continue
		}
		// Bare tool name — must be scoped via --server.
		if server == "" {
			return nil, false, fmt.Errorf("invalid target %q: use <server>:<tool> format or scope bare names with --server", arg)
		}
		groups[server] = append(groups[server], arg)
	}
	return groups, false, nil
}

// runToolsApproval implements the `tools approve` and `tools reject` commands.
// When block is true it calls the block endpoint (approve+disable); otherwise
// it approves. Each server group is processed independently; the command exits
// non-zero if any group fails but still attempts the rest.
func runToolsApproval(args []string, block bool) error {
	groups, allMode, err := resolveToolApprovalTargets(args, toolsApprovalServer, toolsApprovalAll)
	if err != nil {
		return err
	}

	client, _, err := newSecurityCLIClient()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	verb := "approve"
	pastTense := "approved"
	if block {
		verb = "reject"
		pastTense = "blocked"
	}

	// Deterministic ordering for stable output and JSON results.
	servers := make([]string, 0, len(groups))
	for srv := range groups {
		servers = append(servers, srv)
	}
	sort.Strings(servers)

	type serverResult struct {
		Server string   `json:"server"`
		All    bool     `json:"all,omitempty"`
		Tools  []string `json:"tools,omitempty"`
		Count  int      `json:"count"`
		Error  string   `json:"error,omitempty"`
	}

	var results []serverResult
	anyFailed := false
	for _, srv := range servers {
		tools := groups[srv]
		var count int
		var callErr error
		if block {
			count, callErr = client.BlockTools(ctx, srv, tools, allMode)
		} else {
			count, callErr = client.ApproveTools(ctx, srv, tools, allMode)
		}
		res := serverResult{Server: srv, All: allMode, Tools: tools, Count: count}
		if callErr != nil {
			anyFailed = true
			res.Error = callErr.Error()
		}
		results = append(results, res)
	}

	format := ResolveOutputFormat()
	if format == "json" || format == "yaml" {
		if ferr := formatAndPrint(format, results); ferr != nil {
			return ferr
		}
		if anyFailed {
			return fmt.Errorf("one or more servers failed to %s", verb)
		}
		return nil
	}

	// Table / human-readable output: one line per server group.
	for _, res := range results {
		if res.Error != "" {
			fmt.Fprintf(os.Stderr, "FAILED  %s: %s\n", res.Server, res.Error)
			continue
		}
		scope := fmt.Sprintf("%d tool(s)", res.Count)
		if res.All {
			scope = fmt.Sprintf("all pending/changed (%d tool(s))", res.Count)
		}
		fmt.Printf("OK      %s: %s %s\n", res.Server, pastTense, scope)
	}

	if anyFailed {
		return fmt.Errorf("one or more servers failed to %s (see above)", verb)
	}
	return nil
}
