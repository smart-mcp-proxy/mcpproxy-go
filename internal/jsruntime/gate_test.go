package jsruntime

import (
	"context"
	"sync"
	"testing"
)

// Spec 105 FR-009 (codex r9 I1): the gate ToolGateFunc captures when it
// authorizes a call is the gate the dispatch of THAT call runs on — lone
// call_tool() and call_tools() elements alike — and a host that wires only
// the tier-only ToolAnnotationFunc, or a ToolCaller that cannot consume a
// gate, keeps dispatching through CallTool unchanged.

// gateToken is what the test's lookup hands out: one distinct value per
// lookup, so the test can prove the dispatch received the very capture its
// own authorization produced and not a fresh one.
type gateToken struct {
	server, tool string
	seq          int
}

// gatedMockCaller records, per dispatch, which gate (if any) it was given.
type gatedMockCaller struct {
	mockToolCaller
	mu    sync.Mutex
	gates []ToolGate
}

func (g *gatedMockCaller) CallToolWithGate(ctx context.Context, serverName, toolName string, args map[string]interface{}, gate ToolGate) (interface{}, error) {
	g.mu.Lock()
	g.gates = append(g.gates, gate)
	g.mu.Unlock()
	return g.CallTool(ctx, serverName, toolName, args)
}

// gateLookup counts its calls and returns a fresh token for each, tagged
// with the pair it was asked about.
func gateLookup(tier string) (ToolGateLookup, *int) {
	var seq int
	var mu sync.Mutex
	return func(serverName, toolName string) (string, ToolGate) {
		mu.Lock()
		defer mu.Unlock()
		seq++
		return tier, &gateToken{server: serverName, tool: toolName, seq: seq}
	}, &seq
}

func TestToolGateFunc_LoneCallDispatchesOnTheCapturedGate(t *testing.T) {
	caller := &gatedMockCaller{mockToolCaller: *newMockToolCaller()}
	lookup, lookups := gateLookup("read")
	result := Execute(context.Background(), caller, `call_tool("s", "t", {})`, ExecutionOptions{
		ToolGateFunc: lookup,
		AuthContext:  &AuthInfo{Type: "agent", AgentName: "a", AllowedServers: []string{"s"}, Permissions: []string{"read"}},
	})
	if !result.Ok {
		t.Fatalf("execution failed: %+v", result.Error)
	}
	if *lookups != 1 {
		t.Fatalf("the lookup must run exactly once per call, ran %d times", *lookups)
	}
	if len(caller.gates) != 1 {
		t.Fatalf("the dispatch must go through CallToolWithGate exactly once, got %d", len(caller.gates))
	}
	token, ok := caller.gates[0].(*gateToken)
	if !ok || token.server != "s" || token.tool != "t" || token.seq != 1 {
		t.Fatalf("the dispatch must receive the gate its own authorization captured, got %#v", caller.gates[0])
	}
}

func TestToolGateFunc_BatchElementsDispatchOnTheirOwnGates(t *testing.T) {
	caller := &gatedMockCaller{mockToolCaller: *newMockToolCaller()}
	lookup, lookups := gateLookup("read")
	result := Execute(context.Background(), caller,
		`call_tools([{server: "s", tool: "t1", args: {}}, {server: "s", tool: "t2", args: {}}], {max_parallel: 1})`,
		ExecutionOptions{ToolGateFunc: lookup})
	if !result.Ok {
		t.Fatalf("execution failed: %+v", result.Error)
	}
	if *lookups != 2 {
		t.Fatalf("one lookup per element, ran %d times", *lookups)
	}
	if len(caller.gates) != 2 {
		t.Fatalf("both elements must dispatch through CallToolWithGate, got %d", len(caller.gates))
	}
	seen := map[string]bool{}
	for _, gate := range caller.gates {
		token, ok := gate.(*gateToken)
		if !ok {
			t.Fatalf("unexpected gate %#v", gate)
		}
		seen[token.tool] = true
	}
	if !seen["t1"] || !seen["t2"] {
		t.Fatalf("each element must carry the gate captured for its own pair, saw %v", seen)
	}
}

func TestToolGateFunc_RefusedCallCapturesNoDispatch(t *testing.T) {
	caller := &gatedMockCaller{mockToolCaller: *newMockToolCaller()}
	lookup, _ := gateLookup(PermissionTierUnresolved)
	result := Execute(context.Background(), caller, `call_tool("s", "t", {})`, ExecutionOptions{ToolGateFunc: lookup})
	if !result.Ok {
		t.Fatalf("execution failed: %+v", result.Error)
	}
	envelope, ok := result.Value.(map[string]interface{})
	if !ok || envelope["ok"] != false {
		t.Fatalf("an unresolved identity must be refused before dispatch, got %#v", result.Value)
	}
	if len(caller.gates) != 0 || len(caller.calls) != 0 {
		t.Fatalf("a refused call must not dispatch (gated %d, plain %d)", len(caller.gates), len(caller.calls))
	}
}

func TestToolGateFunc_PlainCallerAndTierOnlyLookupUnchanged(t *testing.T) {
	// A ToolCaller without CallToolWithGate: the captured gate is dropped
	// and the call goes through CallTool, as before the gate contract.
	plain := newMockToolCaller()
	lookup, _ := gateLookup("read")
	result := Execute(context.Background(), plain, `call_tool("s", "t", {})`, ExecutionOptions{ToolGateFunc: lookup})
	if !result.Ok || len(plain.calls) != 1 {
		t.Fatalf("a plain ToolCaller must still be dispatched through CallTool (ok=%v, calls=%d)", result.Ok, len(plain.calls))
	}

	// A gated caller wired with the tier-only lookup: nothing is captured,
	// so the dispatch goes through CallTool and never CallToolWithGate.
	gated := &gatedMockCaller{mockToolCaller: *newMockToolCaller()}
	result = Execute(context.Background(), gated, `call_tool("s", "t", {})`, ExecutionOptions{
		ToolAnnotationFunc: func(string, string) string { return "read" },
	})
	if !result.Ok || len(gated.calls) != 1 || len(gated.gates) != 0 {
		t.Fatalf("the tier-only lookup captures no gate (ok=%v, calls=%d, gated=%d)", result.Ok, len(gated.calls), len(gated.gates))
	}

	// Both wired: the gate-capturing lookup takes precedence.
	gated = &gatedMockCaller{mockToolCaller: *newMockToolCaller()}
	annotationCalls := 0
	lookup, lookups := gateLookup("read")
	result = Execute(context.Background(), gated, `call_tool("s", "t", {})`, ExecutionOptions{
		ToolAnnotationFunc: func(string, string) string { annotationCalls++; return "read" },
		ToolGateFunc:       lookup,
	})
	if !result.Ok || *lookups != 1 || annotationCalls != 0 || len(gated.gates) != 1 {
		t.Fatalf("ToolGateFunc must take precedence (ok=%v, gate lookups=%d, annotation lookups=%d, gated dispatches=%d)",
			result.Ok, *lookups, annotationCalls, len(gated.gates))
	}
}
