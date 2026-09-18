package jsruntime

import (
	"context"
	"sync"
	"testing"
)

// Spec 107 PR-D (T102, US3): checkDispatchGates (runtime.go:384-418) is the
// single seam every nested scope/permission decision passes through, lone
// call_tool() and each call_tools() batch element alike. The audit line (T107
// installs the wrapper-side emitter, T104 wires this observer into
// checkDispatchGates itself) needs one authz report per gate decision,
// carrying the request context, the parent_id the wrapper installed, the
// canonical "server:tool" target, the tier the gate resolved, and the
// arguments with every _auth_*-prefixed key stripped (Spec 107 FR-015) —
// never the raw arguments a caller-injected identity key could still be
// hiding in.
//
// This file is [compile-red until T104]: AuthzObserver, AuthzGateReport and
// ExecutionOptions.{AuthzObserver,ParentID} do not exist yet. No production
// code is added by this task.

// recordingAuthzObserver captures every report it receives, in order, guarded
// by a mutex so the batch path's concurrent workers can report safely.
type recordingAuthzObserver struct {
	mu      sync.Mutex
	reports []AuthzGateReport
}

func (o *recordingAuthzObserver) ObserveAuthzGate(report AuthzGateReport) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.reports = append(o.reports, report)
}

func (o *recordingAuthzObserver) snapshot() []AuthzGateReport {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]AuthzGateReport, len(o.reports))
	copy(out, o.reports)
	return out
}

// TestCheckDispatchGates_ReportsServerScopeRefusal proves a lone call_tool()
// refused by the allow-list gate (RestrictToAllowed / AllowedServers) is
// reported exactly once, with the canonical target and no upstream dispatch.
func TestCheckDispatchGates_ReportsServerScopeRefusal(t *testing.T) {
	caller := newMockToolCaller()
	observer := &recordingAuthzObserver{}

	parentID := "parent-attempt-123"
	result := Execute(context.Background(), caller, `call_tool("s", "t", {"x": 1})`, ExecutionOptions{
		AllowedServers: []string{"other"},
		AuthzObserver:  observer,
		ParentID:       parentID,
	})
	if !result.Ok {
		t.Fatalf("execution failed: %+v", result.Error)
	}
	if len(caller.calls) != 0 {
		t.Fatalf("a refused call must never dispatch upstream, got %d calls", len(caller.calls))
	}

	reports := observer.snapshot()
	if len(reports) != 1 {
		t.Fatalf("exactly one authz report expected, got %d: %#v", len(reports), reports)
	}
	report := reports[0]
	if report.Ctx == nil {
		t.Fatalf("report must carry the request context, got nil")
	}
	if report.ParentID != parentID {
		t.Fatalf("report.ParentID = %q, want %q", report.ParentID, parentID)
	}
	if report.CanonicalTarget != "s:t" {
		t.Fatalf("report.CanonicalTarget = %q, want %q", report.CanonicalTarget, "s:t")
	}
	if !report.Denied {
		t.Fatalf("a server-scope refusal must report Denied=true, got %#v", report)
	}
	if report.Arguments == nil || report.Arguments["x"] != int64(1) {
		t.Fatalf("report.Arguments must carry the stripped arguments, got %#v", report.Arguments)
	}
}

// TestCheckDispatchGates_ReportsPermissionTierRefusal proves an AuthInfo
// permission-tier refusal is reported with the required tier the tool
// annotation lookup resolved, not merely "denied".
func TestCheckDispatchGates_ReportsPermissionTierRefusal(t *testing.T) {
	caller := newMockToolCaller()
	observer := &recordingAuthzObserver{}

	result := Execute(context.Background(), caller, `call_tool("s", "t", {})`, ExecutionOptions{
		AuthContext: &AuthInfo{Type: "agent", AgentName: "a", AllowedServers: []string{"s"}, Permissions: []string{"read"}},
		ToolAnnotationFunc: func(serverName, toolName string) string {
			return "destructive"
		},
		AuthzObserver: observer,
	})
	if !result.Ok {
		t.Fatalf("execution failed: %+v", result.Error)
	}

	reports := observer.snapshot()
	if len(reports) != 1 {
		t.Fatalf("exactly one authz report expected, got %d: %#v", len(reports), reports)
	}
	report := reports[0]
	if !report.Denied {
		t.Fatalf("a permission-tier refusal must report Denied=true, got %#v", report)
	}
	if report.RequiredPerm != "destructive" {
		t.Fatalf("report.RequiredPerm = %q, want %q", report.RequiredPerm, "destructive")
	}
	if report.CanonicalTarget != "s:t" {
		t.Fatalf("report.CanonicalTarget = %q, want %q", report.CanonicalTarget, "s:t")
	}
}

// TestCheckDispatchGates_AllowedCallReportsNoDenial proves an allowed call
// either produces no report at all, or a report with Denied=false — the
// contract this test pins is that a passing gate is never mistaken for a
// refusal by whatever consumes AuthzGateReport.Denied.
func TestCheckDispatchGates_AllowedCallReportsNoDenial(t *testing.T) {
	caller := newMockToolCaller()
	observer := &recordingAuthzObserver{}

	result := Execute(context.Background(), caller, `call_tool("s", "t", {})`, ExecutionOptions{
		AuthzObserver: observer,
	})
	if !result.Ok {
		t.Fatalf("execution failed: %+v", result.Error)
	}
	if len(caller.calls) != 1 {
		t.Fatalf("an allowed call must dispatch upstream exactly once, got %d", len(caller.calls))
	}

	for _, report := range observer.snapshot() {
		if report.Denied {
			t.Fatalf("an allowed call must never be reported as denied, got %#v", report)
		}
	}
}

// TestCheckDispatchGates_BatchReportsEachElementRefusalOnce proves the batch
// path (call_tools) reports one denial per refused element — not one for the
// whole batch, not more than one per element — while an accepted element in
// the same batch dispatches and is not reported as a denial.
func TestCheckDispatchGates_BatchReportsEachElementRefusalOnce(t *testing.T) {
	caller := newMockToolCaller()
	observer := &recordingAuthzObserver{}

	result := Execute(context.Background(), caller,
		`call_tools([
			{server: "allowed", tool: "t1", args: {}},
			{server: "blocked", tool: "t2", args: {}},
			{server: "blocked", tool: "t3", args: {}}
		], {max_parallel: 2})`,
		ExecutionOptions{
			AllowedServers: []string{"allowed"},
			AuthzObserver:  observer,
			ParentID:       "parent-batch-1",
		})
	if !result.Ok {
		t.Fatalf("execution failed: %+v", result.Error)
	}
	if len(caller.calls) != 1 {
		t.Fatalf("only the allowed element may dispatch upstream, got %d calls", len(caller.calls))
	}

	reports := observer.snapshot()
	deniedByTarget := map[string]int{}
	for _, report := range reports {
		if report.ParentID != "parent-batch-1" {
			t.Fatalf("every batch element report must carry the batch's parent_id, got %q", report.ParentID)
		}
		if report.Denied {
			deniedByTarget[report.CanonicalTarget]++
		}
	}
	if deniedByTarget["blocked:t2"] != 1 {
		t.Fatalf("blocked:t2 must be reported denied exactly once, got %d", deniedByTarget["blocked:t2"])
	}
	if deniedByTarget["blocked:t3"] != 1 {
		t.Fatalf("blocked:t3 must be reported denied exactly once, got %d", deniedByTarget["blocked:t3"])
	}
	if deniedByTarget["allowed:t1"] != 0 {
		t.Fatalf("allowed:t1 must never be reported denied, got %d", deniedByTarget["allowed:t1"])
	}
}

// TestCheckDispatchGates_StripsAuthInjectedArguments proves the report never
// carries a caller-injected _auth_*-prefixed argument key (FR-015): a script
// cannot smuggle one into the audit line by naming its own argument that way,
// and the host cannot either by way of an args map that already had one set
// before dispatch was attempted.
func TestCheckDispatchGates_StripsAuthInjectedArguments(t *testing.T) {
	caller := newMockToolCaller()
	observer := &recordingAuthzObserver{}

	result := Execute(context.Background(), caller,
		`call_tool("blocked", "t", {"_auth_user_id": "u1", "normal": "v"})`,
		ExecutionOptions{
			AllowedServers: []string{"other"},
			AuthzObserver:  observer,
		})
	if !result.Ok {
		t.Fatalf("execution failed: %+v", result.Error)
	}

	reports := observer.snapshot()
	if len(reports) != 1 {
		t.Fatalf("exactly one authz report expected, got %d", len(reports))
	}
	for key := range reports[0].Arguments {
		if len(key) >= 6 && key[:6] == "_auth_" {
			t.Fatalf("report.Arguments must never carry an _auth_*-prefixed key, got %#v", reports[0].Arguments)
		}
	}
	if reports[0].Arguments["normal"] != "v" {
		t.Fatalf("report.Arguments must still carry the caller's own arguments, got %#v", reports[0].Arguments)
	}
}

// TestCheckDispatchGates_NilObserverIsANoOp proves a nil AuthzObserver (the
// default for every caller that does not opt in) never panics and never
// changes dispatch behaviour — the observer is best-effort reporting, not a
// gate itself.
func TestCheckDispatchGates_NilObserverIsANoOp(t *testing.T) {
	caller := newMockToolCaller()
	result := Execute(context.Background(), caller, `call_tool("s", "t", {})`, ExecutionOptions{})
	if !result.Ok {
		t.Fatalf("execution failed: %+v", result.Error)
	}
	if len(caller.calls) != 1 {
		t.Fatalf("expected one dispatch with a nil observer, got %d", len(caller.calls))
	}
}
