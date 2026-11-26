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
		if c.Name() == "help" || c.Name() == "completion" {
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
