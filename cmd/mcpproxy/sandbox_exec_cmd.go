package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/sandbox"
)

// newSandboxExecCommand returns the hidden `__sandbox_exec` subcommand (MCP-34.3).
//
// mcpproxy re-executes itself as `mcpproxy __sandbox_exec -- <command> [args...]`
// to launch a stdio MCP server under native sandbox isolation: the child applies
// Landlock + rlimits confinement (encoded in the environment by the upstream
// launcher) and then execs the real command, replacing this process so stdin/
// stdout pass straight through. It is not meant to be invoked by users directly.
func newSandboxExecCommand() *cobra.Command {
	return &cobra.Command{
		Use:    sandbox.Subcommand + " -- command [args...]",
		Short:  "internal: re-exec wrapper applying native sandbox confinement (do not call directly)",
		Hidden: true,
		// Pass everything after the subcommand through untouched — the wrapped
		// command has its own flags we must not interpret.
		DisableFlagParsing: true,
		Run: func(_ *cobra.Command, args []string) {
			// cobra may retain the "--" separator; drop a single leading one.
			if len(args) > 0 && args[0] == "--" {
				args = args[1:]
			}
			os.Exit(sandbox.RunChild(args, os.Stderr))
		},
	}
}
