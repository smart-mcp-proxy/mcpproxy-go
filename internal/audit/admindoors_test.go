package audit_test

import (
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
)

func TestAdminDoorRegistry_WellFormed(t *testing.T) {
	actions := map[string]bool{}
	for _, a := range audit.AdminActions {
		actions[a] = true
	}
	seen := map[string]bool{}
	for _, d := range audit.AdminDoors {
		key := d.Door + "#" + d.Branch
		if seen[key] {
			t.Errorf("duplicate registry row %s", key)
		}
		seen[key] = true
		if !strings.HasPrefix(d.Door, "REST ") && !strings.HasPrefix(d.Door, "MCP ") && !strings.HasPrefix(d.Door, "CLI ") {
			t.Errorf("%s: door must start with REST, MCP or CLI", d.Door)
		}
		for _, a := range d.Actions {
			if !actions[a] {
				t.Errorf("%s: unknown action %q", key, a)
			}
		}
		switch d.Status {
		case audit.DoorDone:
			if len(d.Actions) == 0 || d.Slice == "" {
				t.Errorf("%s: a done row names its actions and slice", key)
			}
		case audit.DoorPending:
			if d.Slice == "" || (d.Reason == "" && len(d.Actions) == 0) {
				t.Errorf("%s: a pending row names its slice and what it will record", key)
			}
		case audit.DoorExempt:
			if d.Reason == "" || len(d.Actions) > 0 {
				t.Errorf("%s: an exempt row has a reason and no actions", key)
			}
		default:
			t.Errorf("%s: invalid status %q", key, d.Status)
		}
	}
}

// TestAdminDoorRegistry_PendingOnlyShrinks: the pending set is frozen by
// identity. A slice that ships lines flips its rows to done and removes them
// from AdminDoorsFrozenPending; a new pending row must be added there, in the
// same diff, where review sees it. Swapping one pending door for another
// therefore always shows up.
func TestAdminDoorRegistry_PendingOnlyShrinks(t *testing.T) {
	frozen := map[string]bool{}
	for _, k := range audit.AdminDoorsFrozenPending {
		frozen[k] = true
	}
	pending := map[string]bool{}
	for _, d := range audit.AdminDoors {
		if d.Status == audit.DoorPending {
			k := d.Door + "#" + d.Branch
			pending[k] = true
			if !frozen[k] {
				t.Errorf("pending row %s is not in AdminDoorsFrozenPending: a new pending door is a reviewed change", k)
			}
		}
	}
	for k := range frozen {
		if !pending[k] {
			t.Errorf("AdminDoorsFrozenPending lists %s, which is no longer pending: remove it", k)
		}
	}
}
