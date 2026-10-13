//go:build server

package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Spec 108-h H12: the server edition has no per-client credential model. The tool
// is registered in both editions and its profile operations work; every client
// operation answers the uniform "client not found" that REST's client= parameters
// answer there.
func TestProfilesTool_ServerEditionClientOps(t *testing.T) {
	f := newProfilesToolFixture(t, nil)

	listed := f.ok(map[string]any{"operation": "list"})
	assert.NotEmpty(t, listed["profiles"], "profile operations work in the server edition")
	f.ok(map[string]any{"operation": "get", "name": "work-full"})

	for name, args := range map[string]map[string]any{
		"list_clients":          {"operation": "list_clients"},
		"assign":                {"operation": "assign", "client": "cursor", "profile": "work-full"},
		"bulk assign":           {"operation": "assign", "from_profile": "work-readonly", "to_profile": "work-full"},
		"effective_tools":       {"operation": "effective_tools", "name": "work-full", "client": "cursor"},
		"explain client":        {"operation": "explain", "client": "cursor", "tool": "github:list_issues"},
		"list_clients by filer": {"operation": "list_clients", "profile": "work-full"},
	} {
		t.Run(name, func(t *testing.T) {
			body := f.refused(apiKeyCtx(), args)
			assert.Equal(t, map[string]any{"error": "client not found"}, body)
		})
	}

	// Non-client explain subjects still work.
	f.ok(map[string]any{"operation": "explain", "profile": "work-full", "tool": "github:list_issues"})
}
