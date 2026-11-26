//go:build !server && !qa_hooks

// Personal-edition registrations only: the -tags server router and command
// tree are enumerated from Spec 133 PR-1b on (plan.md).

package main

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
)

// Spec 133 FR-022: every runnable node of the constructed Cobra tree is
// classified in the door registry, and every CLI row still names a command.
func TestAdminDoorRegistry_CoversEveryCLICommand(t *testing.T) {
	var doors []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		// Cobra adds `help` and `completion` to a tree the first time a
		// command in it executes (other tests in this package execute
		// shared package-level commands); they are generated, not doors.
		if cobraGenerated(c) {
			return
		}
		if c.Runnable() {
			doors = append(doors, "CLI "+c.CommandPath())
		}
		for _, s := range c.Commands() {
			walk(s)
		}
	}
	walk(newRootCmd())
	if len(doors) < 100 {
		t.Fatalf("found only %d runnable commands: the tree changed shape", len(doors))
	}
	missing, stale := audit.DiffDoors("CLI ", doors)
	if len(missing) > 0 {
		t.Errorf("CLI commands with no registry row (internal/audit/admindoors.go): %v", missing)
	}
	if len(stale) > 0 {
		t.Errorf("registry rows whose CLI command no longer exists: %v", stale)
	}
}

// cobraGenerated reports Cobra's own default help and completion commands,
// recognised by Cobra's default Short text, so an explicitly registered
// command that happens to be called "help" or "completion" is still walked.
func cobraGenerated(c *cobra.Command) bool {
	switch c.Name() {
	case "help":
		return c.Short == "Help about any command"
	case "completion":
		return c.Short == "Generate the autocompletion script for the specified shell"
	}
	return false
}
