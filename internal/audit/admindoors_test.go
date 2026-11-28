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
			if d.Slice == "" || len(d.Actions) == 0 {
				t.Errorf("%s: a pending row names its slice and every action it owes (the frozen set is per effect)", key)
			}
			if len(d.PendingActions()) == 0 {
				t.Errorf("%s: every action has shipped; mark the row done", key)
			}
			for _, a := range d.Shipped {
				found := false
				for _, b := range d.Actions {
					found = found || a == b
				}
				if !found {
					t.Errorf("%s: shipped action %q is not one of its actions", key, a)
				}
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
		for _, a := range d.PendingActions() {
			k := d.Door + "#" + d.Branch + "#" + a
			pending[k] = true
			if !frozen[k] {
				t.Errorf("pending effect %s is not in AdminDoorsFrozenPending: a new pending effect is a reviewed change", k)
			}
		}
	}
	for k := range frozen {
		if !pending[k] {
			t.Errorf("AdminDoorsFrozenPending lists %s, which is no longer a pending effect: remove it", k)
		}
	}
}

// Round 25: a config apply that changes mcpServers starts a discovery sweep
// (runtime applyConfigInTx → DiscoverAndIndexTools), whose approval
// transitions belong to the door that committed the change.
func TestAdminDoorRegistry_ConfigApplyDiscoveryPass(t *testing.T) {
	for _, door := range []string{"REST POST /api/v1/config/apply", "REST PATCH /api/v1/config"} {
		found := false
		for _, d := range audit.AdminDoors {
			if d.Door != door || d.Branch != "discovery pass" {
				continue
			}
			found = true
			want := map[string]bool{"tool.approve": false, "tool.hold": false, "server.baseline_set": false}
			for _, a := range d.Actions {
				if _, ok := want[a]; ok {
					want[a] = true
				}
			}
			for a, ok := range want {
				if !ok {
					t.Errorf("%s discovery pass lacks %s", door, a)
				}
			}
		}
		if !found {
			t.Errorf("%s has no discovery pass branch", door)
		}
	}
}
