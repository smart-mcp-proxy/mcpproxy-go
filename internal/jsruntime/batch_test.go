package jsruntime

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// batchStub is a ToolCaller that records how a batch was dispatched: how many
// calls were made, how many ran at once (high-water mark), the context each
// one ran under, and — optionally — a fixed latency or a block until the
// context is cancelled.
type batchStub struct {
	mu          sync.Mutex
	latency     time.Duration
	blockOnCtx  bool
	dispatched  int
	inFlight    int
	highWater   int
	cancelled   int
	seenTools   []string
	seenArgs    map[string]map[string]interface{}
	seenCtxs    []context.Context
	results     map[string]interface{}
	errors      map[string]error
	defaultFunc func(server, tool string) interface{}
}

func newBatchStub() *batchStub {
	return &batchStub{
		results:  make(map[string]interface{}),
		errors:   make(map[string]error),
		seenArgs: make(map[string]map[string]interface{}),
	}
}

func (s *batchStub) CallTool(ctx context.Context, serverName, toolName string, args map[string]interface{}) (interface{}, error) {
	key := serverName + ":" + toolName

	s.mu.Lock()
	s.dispatched++
	s.inFlight++
	if s.inFlight > s.highWater {
		s.highWater = s.inFlight
	}
	s.seenTools = append(s.seenTools, toolName)
	s.seenArgs[toolName] = args
	s.seenCtxs = append(s.seenCtxs, ctx)
	latency, blockOnCtx := s.latency, s.blockOnCtx
	err, hasErr := s.errors[key]
	result, hasResult := s.results[key]
	defaultFunc := s.defaultFunc
	s.mu.Unlock()

	switch {
	case blockOnCtx:
		<-ctx.Done()
		s.mu.Lock()
		s.cancelled++
		s.mu.Unlock()
	case latency > 0:
		time.Sleep(latency)
	}

	s.mu.Lock()
	s.inFlight--
	s.mu.Unlock()

	if blockOnCtx {
		return nil, ctx.Err()
	}
	if hasErr {
		return nil, err
	}
	if hasResult {
		return result, nil
	}
	if defaultFunc != nil {
		return defaultFunc(serverName, toolName), nil
	}
	return map[string]interface{}{"success": true, "tool": toolName}, nil
}

func (s *batchStub) stats() (dispatched, highWater, cancelled int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dispatched, s.highWater, s.cancelled
}

// batchSlots runs a script that returns call_tools() slots and exports them.
func batchSlots(t *testing.T, caller ToolCaller, code string, opts ExecutionOptions) ([]interface{}, *ExecutionContext) {
	t.Helper()

	result, ec := execute(context.Background(), caller, code, opts)
	if !result.Ok {
		t.Fatalf("script failed: %v", result.Error)
	}
	slots, ok := result.Value.([]interface{})
	if !ok {
		t.Fatalf("call_tools returned %T, want an array: %#v", result.Value, result.Value)
	}
	return slots, ec
}

func slotError(t *testing.T, slot interface{}) (code, message string) {
	t.Helper()
	envelope, ok := slot.(map[string]interface{})
	if !ok {
		t.Fatalf("slot is %T, want an object", slot)
	}
	if ok, _ := envelope["ok"].(bool); ok {
		t.Fatalf("slot succeeded, expected an error: %#v", envelope)
	}
	return envelopeError(t, envelope)
}

// TestCallToolsReturnsOrderedSlots (US1): every element gets a slot at its own
// index, whatever order the workers finished in.
func TestCallToolsReturnsOrderedSlots(t *testing.T) {
	stub := newBatchStub()
	stub.defaultFunc = func(_, tool string) interface{} {
		return map[string]interface{}{"echo": tool}
	}

	code := `
		var reqs = [];
		for (var i = 0; i < 10; i++) { reqs.push({server: "s", tool: "t" + i, args: {i: i}}); }
		call_tools(reqs)
	`
	slots, _ := batchSlots(t, stub, code, ExecutionOptions{TimeoutMs: 10000})

	if len(slots) != 10 {
		t.Fatalf("got %d slots, want 10", len(slots))
	}
	for i, slot := range slots {
		envelope, ok := slot.(map[string]interface{})
		if !ok {
			t.Fatalf("slot %d is %T, want an object", i, slot)
		}
		if ok, _ := envelope["ok"].(bool); !ok {
			t.Fatalf("slot %d failed: %#v", i, envelope)
		}
		payload, ok := envelope["result"].(map[string]interface{})
		if !ok {
			t.Fatalf("slot %d result is %T, want an object", i, envelope["result"])
		}
		if want := fmt.Sprintf("t%d", i); payload["echo"] != want {
			t.Errorf("slot %d carries %v, want %q — slots must follow input order", i, payload["echo"], want)
		}
	}

	dispatched, _, _ := stub.stats()
	if dispatched != 10 {
		t.Errorf("dispatched %d calls, want 10", dispatched)
	}
}

// TestCallToolsRunsInParallel (SC-001 shape): a fan-out of independent calls
// costs roughly the slowest element, not their sum.
func TestCallToolsRunsInParallel(t *testing.T) {
	const (
		elements = 10
		latency  = 50 * time.Millisecond
	)
	stub := newBatchStub()
	stub.latency = latency

	code := `
		var reqs = [];
		for (var i = 0; i < 10; i++) { reqs.push({server: "s", tool: "t" + i, args: {}}); }
		call_tools(reqs)
	`
	start := time.Now()
	slots, _ := batchSlots(t, stub, code, ExecutionOptions{TimeoutMs: 30000})
	elapsed := time.Since(start)

	if len(slots) != elements {
		t.Fatalf("got %d slots, want %d", len(slots), elements)
	}
	serial := time.Duration(elements) * latency
	if budget := serial * 35 / 100; elapsed >= budget {
		t.Errorf("batch took %v, want < %v (35%% of the %v serial equivalent)", elapsed, budget, serial)
	}
}

// TestCallToolsEmptyBatch: an empty batch is a no-op — an empty array back, no
// dispatch, no budget consumed.
func TestCallToolsEmptyBatch(t *testing.T) {
	stub := newBatchStub()

	slots, ec := batchSlots(t, stub, `call_tools([])`, ExecutionOptions{MaxToolCalls: 3, TimeoutMs: 10000})

	if len(slots) != 0 {
		t.Errorf("got %d slots, want 0", len(slots))
	}
	if dispatched, _, _ := stub.stats(); dispatched != 0 {
		t.Errorf("dispatched %d calls for an empty batch", dispatched)
	}
	if len(ec.ToolCalls) != 0 {
		t.Errorf("recorded %d tool calls for an empty batch", len(ec.ToolCalls))
	}
}

// TestCallToolsSingleElementMatchesLoneCall: one element through call_tools()
// must be indistinguishable from the same request through call_tool().
func TestCallToolsSingleElementMatchesLoneCall(t *testing.T) {
	result := map[string]interface{}{"content": []interface{}{map[string]interface{}{"type": "text", "text": "hi"}}}

	batchCaller := newBatchStub()
	batchCaller.results["s:t"] = result
	slots, batchCtx := batchSlots(t, batchCaller, `call_tools([{server: "s", tool: "t", args: {}}])`, ExecutionOptions{TimeoutMs: 10000})
	if len(slots) != 1 {
		t.Fatalf("got %d slots, want 1", len(slots))
	}

	loneCaller := newBatchStub()
	loneCaller.results["s:t"] = result
	lone, loneCtx := execute(context.Background(), loneCaller, `call_tool("s", "t", {})`, ExecutionOptions{TimeoutMs: 10000})
	if !lone.Ok {
		t.Fatalf("lone call script failed: %v", lone.Error)
	}

	if !reflect.DeepEqual(slots[0], lone.Value) {
		t.Errorf("batch slot = %#v, lone call_tool = %#v", slots[0], lone.Value)
	}
	if len(batchCtx.ToolCalls) != len(loneCtx.ToolCalls) {
		t.Errorf("batch recorded %d tool calls, lone call recorded %d", len(batchCtx.ToolCalls), len(loneCtx.ToolCalls))
	}
	if len(batchCtx.ToolCalls) == 1 {
		if batchCtx.ToolCalls[0].ServerName != "s" || batchCtx.ToolCalls[0].ToolName != "t" || !batchCtx.ToolCalls[0].Success {
			t.Errorf("batch record = %#v", batchCtx.ToolCalls[0])
		}
	}
}

// TestCallToolsResultsUseWireShape: slot results are the documented JSON wire
// shape, not the live Go value — the same guarantee tool_result_test.go pins
// for call_tool().
func TestCallToolsResultsUseWireShape(t *testing.T) {
	stub := newBatchStub()
	stub.results["s:t"] = &mcp.CallToolResult{
		Content: []mcp.Content{mcp.TextContent{Type: "text", Text: `{"hello":"world"}`}},
	}

	code := `
		var slots = call_tools([{server: "s", tool: "t"}]);
		var r = slots[0];
		if (!r.ok) throw new Error("call_tools failed: " + JSON.stringify(r.error));
		({
			hello: JSON.parse(r.result.content[0].text).hello,
			pascal: typeof r.result.Content,
			method: typeof r.result.MarshalJSON
		})
	`
	result := Execute(context.Background(), stub, code, ExecutionOptions{TimeoutMs: 10000})
	if !result.Ok {
		t.Fatalf("script failed: %v", result.Error)
	}
	values, ok := result.Value.(map[string]interface{})
	if !ok {
		t.Fatalf("expected a map, got %T", result.Value)
	}
	if values["hello"] != "world" {
		t.Errorf("hello = %v, want world", values["hello"])
	}
	if values["pascal"] != "undefined" {
		t.Errorf("Go field name Content leaked into the slot result: typeof = %v", values["pascal"])
	}
	if values["method"] != "undefined" {
		t.Errorf("Go method MarshalJSON leaked into the slot result: typeof = %v", values["method"])
	}
}

// ctxCapturingCaller records the context each dispatch ran under.
type ctxCapturingCaller struct {
	ctx context.Context
}

func (c *ctxCapturingCaller) CallTool(ctx context.Context, _, _ string, _ map[string]interface{}) (interface{}, error) {
	c.ctx = ctx
	return map[string]interface{}{"success": true}, nil
}

// TestExecuteWiresCancellableExecutionContext pins the context batch workers
// run under (T003): Execute hands the ExecutionContext its timeout context, so
// in-flight upstream calls are cancelled when the execution ends instead of
// being orphaned.
func TestExecuteWiresCancellableExecutionContext(t *testing.T) {
	result, ec := execute(context.Background(), newMockToolCaller(), `1 + 1`, ExecutionOptions{TimeoutMs: 5000})
	if !result.Ok {
		t.Fatalf("script failed: %v", result.Error)
	}

	if ec.ctx == nil {
		t.Fatal("Execute must wire an execution context for batch workers")
	}
	if _, hasDeadline := ec.ctx.Deadline(); !hasDeadline {
		t.Error("the execution context must carry the execution timeout")
	}
	if ec.ctx.Err() == nil {
		t.Error("the execution context must be cancelled once Execute returns")
	}
}

// TestExecutionContextFallsBackToBackground: an ExecutionContext built outside
// Execute has no wired context; the batch path must still have one to dispatch
// under rather than panicking on a nil context.
func TestExecutionContextFallsBackToBackground(t *testing.T) {
	ec := newExecutionContext(newMockToolCaller(), ExecutionOptions{})
	if ec.executionCtx() == nil {
		t.Fatal("executionCtx() must never return nil")
	}
}

// TestLoneCallToolKeepsBackgroundContext: threading the execution context into
// call_tool() would change when a lone call is cancelled, which is not part of
// this feature. The lone path stays on context.Background().
func TestLoneCallToolKeepsBackgroundContext(t *testing.T) {
	caller := &ctxCapturingCaller{}
	result := Execute(context.Background(), caller, `call_tool("s", "t", {})`, ExecutionOptions{TimeoutMs: 5000})
	if !result.Ok {
		t.Fatalf("script failed: %v", result.Error)
	}
	if caller.ctx == nil {
		t.Fatal("call_tool did not dispatch")
	}
	if caller.ctx.Done() != nil {
		t.Error("lone call_tool must keep dispatching on context.Background()")
	}
}

// loneCallEnvelope runs a single call_tool() through Execute and returns the
// envelope the script saw, so batch behaviour can be compared against the
// lone-call path byte for byte instead of against a restatement of it.
func loneCallEnvelope(t *testing.T, caller ToolCaller, server, tool string, opts ExecutionOptions) map[string]interface{} {
	t.Helper()

	result := Execute(context.Background(), caller, `call_tool(`+quoteJS(server)+`, `+quoteJS(tool)+`, {})`, opts)
	if !result.Ok {
		t.Fatalf("lone call_tool script failed: %v", result.Error)
	}
	envelope, ok := result.Value.(map[string]interface{})
	if !ok {
		t.Fatalf("lone call_tool returned %T, want map", result.Value)
	}
	return envelope
}

func quoteJS(s string) string {
	return `"` + s + `"`
}

func envelopeError(t *testing.T, envelope map[string]interface{}) (code, message string) {
	t.Helper()
	errObj, ok := envelope["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("envelope carries no error object: %#v", envelope)
	}
	code, _ = errObj["code"].(string)
	message, _ = errObj["message"].(string)
	return code, message
}

// TestCheckDispatchGatesMatchesLoneCall pins the extracted gate helper (T002)
// against the envelopes a lone call_tool() produces for the same execution
// context. The batch path calls the helper directly, so any drift here is
// drift between the two enforcement paths.
func TestCheckDispatchGatesMatchesLoneCall(t *testing.T) {
	tests := []struct {
		name     string
		opts     ExecutionOptions
		server   string
		tool     string
		wantCode string
		wantPerm string
	}{
		{
			name:     "allow-list violation",
			opts:     ExecutionOptions{AllowedServers: []string{"allowed"}},
			server:   "other",
			tool:     "t",
			wantCode: string(ErrorCodeServerNotAllowed),
		},
		{
			name:     "deny-all profile with an empty allow-list",
			opts:     ExecutionOptions{RestrictToAllowed: true},
			server:   "any",
			tool:     "t",
			wantCode: string(ErrorCodeServerNotAllowed),
		},
		{
			name: "agent token cannot reach the server",
			opts: ExecutionOptions{
				AuthContext: &AuthInfo{Type: "agent", AgentName: "a", AllowedServers: []string{"other"}, Permissions: []string{"read"}},
			},
			server:   "s",
			tool:     "t",
			wantCode: string(ErrorCodeAccessDenied),
		},
		{
			name: "agent token lacks the required permission tier",
			opts: ExecutionOptions{
				AuthContext:        &AuthInfo{Type: "agent", AgentName: "a", AllowedServers: []string{"s"}, Permissions: []string{"read"}},
				ToolAnnotationFunc: func(string, string) string { return "destructive" },
			},
			server:   "s",
			tool:     "t",
			wantCode: string(ErrorCodePermissionDenied),
		},
		{
			name: "allowed call reports the required permission tier",
			opts: ExecutionOptions{
				AuthContext:        &AuthInfo{Type: "agent", AgentName: "a", AllowedServers: []string{"s"}, Permissions: []string{"read", "write"}},
				ToolAnnotationFunc: func(string, string) string { return "write" },
			},
			server:   "s",
			tool:     "t",
			wantPerm: "write",
		},
		{
			name:   "no restrictions",
			opts:   ExecutionOptions{},
			server: "s",
			tool:   "t",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ec := newExecutionContext(newMockToolCaller(), tc.opts)
			gateErr, perm := ec.checkDispatchGates(tc.server, tc.tool)

			if tc.wantCode == "" {
				if gateErr != nil {
					t.Fatalf("expected the call to pass the gates, got %#v", gateErr)
				}
				if perm != tc.wantPerm {
					t.Errorf("required permission = %q, want %q", perm, tc.wantPerm)
				}
				return
			}

			if gateErr == nil {
				t.Fatalf("expected gate error %s, got none", tc.wantCode)
			}
			gotCode, gotMessage := envelopeError(t, gateErr)

			lone := loneCallEnvelope(t, newMockToolCaller(), tc.server, tc.tool, tc.opts)
			wantCode, wantMessage := envelopeError(t, lone)

			if gotCode != wantCode || gotCode != tc.wantCode {
				t.Errorf("code = %q, lone call_tool = %q, expected %q", gotCode, wantCode, tc.wantCode)
			}
			if gotMessage != wantMessage {
				t.Errorf("message = %q, lone call_tool = %q", gotMessage, wantMessage)
			}
			if ok, _ := gateErr["ok"].(bool); ok {
				t.Errorf("gate error envelope must carry ok=false: %#v", gateErr)
			}
		})
	}
}

// TestBudgetIsCheckedBeforeTheScopeGates pins the check ORDER the batch
// pre-dispatch pass must reproduce: an exhausted budget wins over a
// disallowed server, on both primitives.
func TestBudgetIsCheckedBeforeTheScopeGates(t *testing.T) {
	caller := newMockToolCaller()
	opts := ExecutionOptions{MaxToolCalls: 1, AllowedServers: []string{"allowed"}}

	code := `
		call_tool("allowed", "t", {});
		call_tool("denied", "t", {});
	`
	result := Execute(context.Background(), caller, code, opts)
	if !result.Ok {
		t.Fatalf("script failed: %v", result.Error)
	}
	envelope, ok := result.Value.(map[string]interface{})
	if !ok {
		t.Fatalf("expected an envelope, got %T", result.Value)
	}
	gotCode, _ := envelopeError(t, envelope)
	if gotCode != string(ErrorCodeMaxToolCallsExceeded) {
		t.Errorf("code = %q, want %q — the budget gate must run before the allow-list gate",
			gotCode, string(ErrorCodeMaxToolCallsExceeded))
	}
}

// TestCallToolsIsolatesUpstreamFailures (US2): a failing element fails its own
// slot only.
func TestCallToolsIsolatesUpstreamFailures(t *testing.T) {
	stub := newBatchStub()
	stub.errors["s:t3"] = fmt.Errorf("upstream exploded")

	code := `
		var reqs = [];
		for (var i = 0; i < 5; i++) { reqs.push({server: "s", tool: "t" + i, args: {}}); }
		call_tools(reqs)
	`
	slots, ec := batchSlots(t, stub, code, ExecutionOptions{TimeoutMs: 10000})

	if len(slots) != 5 {
		t.Fatalf("got %d slots, want 5", len(slots))
	}
	for i, slot := range slots {
		envelope := slot.(map[string]interface{})
		gotOK, _ := envelope["ok"].(bool)
		if i == 3 {
			if gotOK {
				t.Fatalf("slot 3 succeeded, expected the upstream failure")
			}
			code, message := slotError(t, slot)
			if code != string(ErrorCodeUpstreamError) {
				t.Errorf("slot 3 code = %q, want %q", code, string(ErrorCodeUpstreamError))
			}
			if message != "upstream exploded" {
				t.Errorf("slot 3 message = %q, want the upstream error text", message)
			}
			continue
		}
		if !gotOK {
			t.Errorf("slot %d failed because a sibling failed: %#v", i, envelope)
		}
	}

	if len(ec.ToolCalls) != 5 {
		t.Fatalf("recorded %d tool calls, want 5 (one per dispatched element)", len(ec.ToolCalls))
	}
	for i, record := range ec.ToolCalls {
		if want := fmt.Sprintf("t%d", i); record.ToolName != want {
			t.Errorf("record %d is for %q, want %q — records must land in input order", i, record.ToolName, want)
		}
		if record.Success == (i == 3) {
			t.Errorf("record %d success = %v", i, record.Success)
		}
	}
}

// TestCallToolsEnforcesScopePerElement (US2): a scope violation fails one slot
// with the same code a lone call_tool() would return, and is never dispatched.
func TestCallToolsEnforcesScopePerElement(t *testing.T) {
	stub := newBatchStub()

	code := `call_tools([
		{server: "allowed", tool: "t", args: {}},
		{server: "denied", tool: "t", args: {}},
		{server: "allowed", tool: "u", args: {}}
	])`
	slots, ec := batchSlots(t, stub, code, ExecutionOptions{AllowedServers: []string{"allowed"}, TimeoutMs: 10000})

	if len(slots) != 3 {
		t.Fatalf("got %d slots, want 3", len(slots))
	}
	if ok, _ := slots[0].(map[string]interface{})["ok"].(bool); !ok {
		t.Errorf("slot 0 failed: %#v", slots[0])
	}
	if ok, _ := slots[2].(map[string]interface{})["ok"].(bool); !ok {
		t.Errorf("slot 2 failed: %#v", slots[2])
	}

	gotCode, gotMessage := slotError(t, slots[1])
	wantCode, wantMessage := envelopeError(t, loneCallEnvelope(t, newBatchStub(), "denied", "t",
		ExecutionOptions{AllowedServers: []string{"allowed"}}))
	if gotCode != wantCode || gotMessage != wantMessage {
		t.Errorf("slot 1 = %s/%q, lone call_tool = %s/%q", gotCode, gotMessage, wantCode, wantMessage)
	}

	if dispatched, _, _ := stub.stats(); dispatched != 2 {
		t.Errorf("dispatched %d calls, want 2 — a denied element must not reach the upstream", dispatched)
	}
	if len(ec.ToolCalls) != 2 {
		t.Errorf("recorded %d tool calls, want 2 — a denied element records nothing, as with call_tool()", len(ec.ToolCalls))
	}
}

// TestCallToolsRespectsToolCallBudget (US2): the budget is consumed in input
// order and the elements past it are refused before dispatch, so a batch can
// never overshoot max_tool_calls.
func TestCallToolsRespectsToolCallBudget(t *testing.T) {
	stub := newBatchStub()

	code := `
		var reqs = [];
		for (var i = 0; i < 5; i++) { reqs.push({server: "s", tool: "t" + i, args: {}}); }
		call_tools(reqs)
	`
	slots, ec := batchSlots(t, stub, code, ExecutionOptions{MaxToolCalls: 2, TimeoutMs: 10000})

	if len(slots) != 5 {
		t.Fatalf("got %d slots, want 5", len(slots))
	}
	for i := 0; i < 2; i++ {
		if ok, _ := slots[i].(map[string]interface{})["ok"].(bool); !ok {
			t.Errorf("slot %d failed, expected it to fit the budget: %#v", i, slots[i])
		}
	}
	for i := 2; i < 5; i++ {
		gotCode, _ := slotError(t, slots[i])
		if gotCode != string(ErrorCodeMaxToolCallsExceeded) {
			t.Errorf("slot %d code = %q, want %q", i, gotCode, string(ErrorCodeMaxToolCallsExceeded))
		}
	}

	if dispatched, _, _ := stub.stats(); dispatched != 2 {
		t.Errorf("dispatched %d calls, want 2 — over-budget elements must not be dispatched", dispatched)
	}
	if len(ec.ToolCalls) != 2 {
		t.Errorf("recorded %d tool calls, want 2", len(ec.ToolCalls))
	}
}

// TestCallToolsBudgetCountsEarlierCalls: the budget a batch sees includes the
// calls the script already made, so call_tool() and call_tools() share one
// allowance.
func TestCallToolsBudgetCountsEarlierCalls(t *testing.T) {
	stub := newBatchStub()

	code := `
		call_tool("s", "first", {});
		call_tools([{server: "s", tool: "a"}, {server: "s", tool: "b"}])
	`
	slots, _ := batchSlots(t, stub, code, ExecutionOptions{MaxToolCalls: 2, TimeoutMs: 10000})

	if len(slots) != 2 {
		t.Fatalf("got %d slots, want 2", len(slots))
	}
	if ok, _ := slots[0].(map[string]interface{})["ok"].(bool); !ok {
		t.Errorf("slot 0 failed, expected the last unit of budget to cover it: %#v", slots[0])
	}
	if gotCode, _ := slotError(t, slots[1]); gotCode != string(ErrorCodeMaxToolCallsExceeded) {
		t.Errorf("slot 1 code = %q, want %q", gotCode, string(ErrorCodeMaxToolCallsExceeded))
	}
}

// TestCallToolsSerializationFailureIsPerSlot (US2): a result that cannot be
// turned into JSON fails its slot, not the batch.
func TestCallToolsSerializationFailureIsPerSlot(t *testing.T) {
	stub := newBatchStub()
	stub.results["s:bad"] = map[string]interface{}{"ch": make(chan int)}

	slots, ec := batchSlots(t, stub, `call_tools([{server: "s", tool: "good"}, {server: "s", tool: "bad"}])`,
		ExecutionOptions{TimeoutMs: 10000})

	if len(slots) != 2 {
		t.Fatalf("got %d slots, want 2", len(slots))
	}
	if ok, _ := slots[0].(map[string]interface{})["ok"].(bool); !ok {
		t.Errorf("slot 0 failed: %#v", slots[0])
	}
	if gotCode, _ := slotError(t, slots[1]); gotCode != string(ErrorCodeSerializationError) {
		t.Errorf("slot 1 code = %q, want %q", gotCode, string(ErrorCodeSerializationError))
	}
	if len(ec.ToolCalls) != 2 {
		t.Errorf("recorded %d tool calls, want 2 — a dispatched element always records one", len(ec.ToolCalls))
	}
	if ec.ToolCalls[1].Success {
		t.Error("the unserializable call must be recorded as a failure")
	}
}

// TestCallToolsMalformedCallsAreWholeCallErrors (US2 / FR-012): a malformed
// batch returns ONE envelope naming the first offending element, dispatches
// nothing, and never throws.
func TestCallToolsMalformedCallsAreWholeCallErrors(t *testing.T) {
	tests := []struct {
		name        string
		code        string
		wantMessage string
	}{
		{name: "no arguments", code: `call_tools()`, wantMessage: "requires 1 argument"},
		{name: "requests is not an array", code: `call_tools({server: "s", tool: "t"})`, wantMessage: "must be an array"},
		{name: "requests is a string", code: `call_tools("s")`, wantMessage: "must be an array"},
		{name: "element is not an object", code: `call_tools([{server: "s", tool: "t"}, 42])`, wantMessage: "element 1"},
		{name: "sparse hole", code: `call_tools([{server: "s", tool: "t"}, , {server: "s", tool: "u"}])`, wantMessage: "element 1"},
		{name: "missing server", code: `call_tools([{tool: "t"}])`, wantMessage: "element 0: server"},
		{name: "empty server", code: `call_tools([{server: "", tool: "t"}])`, wantMessage: "element 0: server"},
		{name: "missing tool", code: `call_tools([{server: "s"}])`, wantMessage: "element 0: tool"},
		{name: "args is not an object", code: `call_tools([{server: "s", tool: "t", args: "x"}])`, wantMessage: "element 0: args"},
		{name: "args is null", code: `call_tools([{server: "s", tool: "t", args: null}])`, wantMessage: "element 0: args"},
		{name: "first offending element is named", code: `call_tools([{server: "s", tool: "t"}, {server: "s"}, 7])`, wantMessage: "element 1"},
		{name: "options is not an object", code: `call_tools([{server: "s", tool: "t"}], 4)`, wantMessage: "options must be an object"},
		{name: "fractional max_parallel", code: `call_tools([{server: "s", tool: "t"}], {max_parallel: 2.5})`, wantMessage: "max_parallel must be an integer"},
		{name: "non-numeric max_parallel", code: `call_tools([{server: "s", tool: "t"}], {max_parallel: "4"})`, wantMessage: "max_parallel must be an integer"},
		{name: "max_parallel below range", code: `call_tools([{server: "s", tool: "t"}], {max_parallel: 0})`, wantMessage: "between 1 and 32"},
		{name: "max_parallel above range", code: `call_tools([{server: "s", tool: "t"}], {max_parallel: 33})`, wantMessage: "between 1 and 32"},
		{
			name:        "batch above the cap",
			code:        `var reqs = []; for (var i = 0; i < 101; i++) { reqs.push({server: "s", tool: "t"}); } call_tools(reqs)`,
			wantMessage: "maximum of 100",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := newBatchStub()
			result, ec := execute(context.Background(), stub, tc.code, ExecutionOptions{TimeoutMs: 10000})
			if !result.Ok {
				t.Fatalf("a malformed call_tools must return an envelope, not throw: %v", result.Error)
			}
			envelope, ok := result.Value.(map[string]interface{})
			if !ok {
				t.Fatalf("expected a single envelope, got %T: %#v", result.Value, result.Value)
			}
			gotCode, gotMessage := envelopeError(t, envelope)
			if gotCode != string(ErrorCodeInvalidArgs) {
				t.Errorf("code = %q, want %q", gotCode, string(ErrorCodeInvalidArgs))
			}
			if !strings.Contains(gotMessage, tc.wantMessage) {
				t.Errorf("message = %q, want it to mention %q", gotMessage, tc.wantMessage)
			}
			if dispatched, _, _ := stub.stats(); dispatched != 0 {
				t.Errorf("dispatched %d calls for a malformed batch", dispatched)
			}
			if len(ec.ToolCalls) != 0 {
				t.Errorf("recorded %d tool calls for a malformed batch", len(ec.ToolCalls))
			}
		})
	}
}

// TestCallToolsOmittedArgsDefaultToEmptyObject: args is optional; an element
// without it dispatches with {} rather than failing.
func TestCallToolsOmittedArgsDefaultToEmptyObject(t *testing.T) {
	stub := newBatchStub()

	slots, _ := batchSlots(t, stub, `call_tools([{server: "s", tool: "t"}])`, ExecutionOptions{TimeoutMs: 10000})

	if len(slots) != 1 {
		t.Fatalf("got %d slots, want 1", len(slots))
	}
	if ok, _ := slots[0].(map[string]interface{})["ok"].(bool); !ok {
		t.Fatalf("slot 0 failed: %#v", slots[0])
	}

	stub.mu.Lock()
	defer stub.mu.Unlock()
	args, seen := stub.seenArgs["t"]
	if !seen {
		t.Fatal("the element was never dispatched")
	}
	if args == nil || len(args) != 0 {
		t.Errorf("dispatched args = %#v, want an empty object", args)
	}
}

// TestCallToolsHonorsMaxParallel (US3): concurrency never exceeds the
// effective bound, and the whole batch still completes.
func TestCallToolsHonorsMaxParallel(t *testing.T) {
	const latency = 50 * time.Millisecond

	tests := []struct {
		name      string
		elements  int
		optsMax   int
		code      string
		wantBound int
	}{
		{
			name:      "per-batch override bounds the pool",
			elements:  10,
			code:      `call_tools(reqs, {max_parallel: 3})`,
			wantBound: 3,
		},
		{
			name:      "per-batch override beats the configured default",
			elements:  10,
			optsMax:   8,
			code:      `call_tools(reqs, {max_parallel: 2})`,
			wantBound: 2,
		},
		{
			name:      "the configured default governs without an override",
			elements:  10,
			optsMax:   3,
			code:      `call_tools(reqs)`,
			wantBound: 3,
		},
		{
			name:      "the built-in default governs when nothing is configured",
			elements:  16,
			code:      `call_tools(reqs)`,
			wantBound: 8,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := newBatchStub()
			stub.latency = latency

			code := fmt.Sprintf(`
				var reqs = [];
				for (var i = 0; i < %d; i++) { reqs.push({server: "s", tool: "t" + i, args: {}}); }
				%s
			`, tc.elements, tc.code)
			slots, _ := batchSlots(t, stub, code, ExecutionOptions{MaxParallel: tc.optsMax, TimeoutMs: 30000})

			if len(slots) != tc.elements {
				t.Fatalf("got %d slots, want %d", len(slots), tc.elements)
			}
			for i, slot := range slots {
				if ok, _ := slot.(map[string]interface{})["ok"].(bool); !ok {
					t.Fatalf("slot %d failed: %#v", i, slot)
				}
			}

			dispatched, highWater, _ := stub.stats()
			if dispatched != tc.elements {
				t.Errorf("dispatched %d calls, want %d", dispatched, tc.elements)
			}
			if highWater > tc.wantBound {
				t.Errorf("high-water concurrency = %d, must not exceed %d", highWater, tc.wantBound)
			}
			if highWater != tc.wantBound {
				t.Errorf("high-water concurrency = %d, want %d — the bound must be used, not undershot", highWater, tc.wantBound)
			}
		})
	}
}

// TestBatchCancellationStillRecordsEveryElement (US3 / FR-007): when the
// execution context is cancelled mid-batch the workers stop dispatching, the
// join still completes, and every element the batch accepted still gets one
// slot and exactly one record — reservations and records can never diverge.
func TestBatchCancellationStillRecordsEveryElement(t *testing.T) {
	t.Run("cancelled while in flight", func(t *testing.T) {
		stub := newBatchStub()
		stub.blockOnCtx = true

		ctx, cancel := context.WithCancel(context.Background())
		ec := newExecutionContext(stub, ExecutionOptions{})
		ec.ctx = ctx

		requests := make([]batchRequest, 6)
		for i := range requests {
			requests[i] = batchRequest{server: "s", tool: fmt.Sprintf("t%d", i), args: map[string]interface{}{}}
		}

		go func() {
			time.Sleep(20 * time.Millisecond)
			cancel()
		}()

		slots := ec.runBatch(requests, 3)

		if len(slots) != len(requests) {
			t.Fatalf("got %d slots, want %d", len(slots), len(requests))
		}
		for i, slot := range slots {
			if slot == nil {
				t.Fatalf("slot %d is empty — runBatch returned before its worker finished", i)
			}
			if ok, _ := slot.(map[string]interface{})["ok"].(bool); ok {
				t.Errorf("slot %d succeeded although the execution was cancelled: %#v", i, slot)
			}
		}
		if len(ec.ToolCalls) != len(requests) {
			t.Errorf("recorded %d tool calls, want %d — one per accepted element, cancellation included",
				len(ec.ToolCalls), len(requests))
		}
	})

	t.Run("cancelled before dispatch", func(t *testing.T) {
		stub := newBatchStub()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		ec := newExecutionContext(stub, ExecutionOptions{})
		ec.ctx = ctx

		requests := []batchRequest{
			{server: "s", tool: "a", args: map[string]interface{}{}},
			{server: "s", tool: "b", args: map[string]interface{}{}},
		}
		slots := ec.runBatch(requests, 2)

		if len(slots) != 2 {
			t.Fatalf("got %d slots, want 2", len(slots))
		}
		for i := range slots {
			if gotCode, _ := slotError(t, slots[i]); gotCode != string(ErrorCodeUpstreamError) {
				t.Errorf("slot %d code = %q, want %q", i, gotCode, string(ErrorCodeUpstreamError))
			}
		}
		if dispatched, _, _ := stub.stats(); dispatched != 0 {
			t.Errorf("dispatched %d calls under a cancelled context", dispatched)
		}
		if len(ec.ToolCalls) != 2 {
			t.Errorf("recorded %d tool calls, want 2", len(ec.ToolCalls))
		}
	})
}

// TestExecutionTimeoutCancelsBatchWorkers (US3 / FR-007): a batch that outlives
// the execution timeout ends the execution with TIMEOUT, and every in-flight
// upstream call is cancelled rather than orphaned.
func TestExecutionTimeoutCancelsBatchWorkers(t *testing.T) {
	stub := newBatchStub()
	stub.blockOnCtx = true

	code := `call_tools([
		{server: "s", tool: "a"},
		{server: "s", tool: "b"},
		{server: "s", tool: "c"}
	])`

	result := Execute(context.Background(), stub, code, ExecutionOptions{TimeoutMs: 150})
	if result.Ok {
		t.Fatalf("expected the execution to time out, got %#v", result.Value)
	}
	if result.Error.Code != ErrorCodeTimeout {
		t.Fatalf("error code = %s, want %s", result.Error.Code, ErrorCodeTimeout)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		dispatched, _, cancelled := stub.stats()
		if cancelled == 3 && dispatched == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d in-flight calls were cancelled — workers must honor the execution context",
				cancelled, dispatched)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestBatchWorkersDispatchUnderTheExecutionContext: the context a worker hands
// to the ToolCaller is the execution's, not a fresh background one — that is
// what makes upstream cancellation reach the server seam.
func TestBatchWorkersDispatchUnderTheExecutionContext(t *testing.T) {
	stub := newBatchStub()

	_, _ = batchSlots(t, stub, `call_tools([{server: "s", tool: "t"}])`, ExecutionOptions{TimeoutMs: 5000})

	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.seenCtxs) != 1 {
		t.Fatalf("captured %d contexts, want 1", len(stub.seenCtxs))
	}
	if _, hasDeadline := stub.seenCtxs[0].Deadline(); !hasDeadline {
		t.Error("batch workers must dispatch under the execution's timeout context")
	}
}

// TestQuickstartExample runs the documented quickstart script end to end, so
// the published example cannot drift from the implementation.
func TestQuickstartExample(t *testing.T) {
	stub := newBatchStub()
	stub.defaultFunc = func(_, tool string) interface{} {
		return map[string]interface{}{
			"content": []interface{}{map[string]interface{}{"type": "text", "text": `{"title":"` + tool + `"}`}},
		}
	}

	code := `
		var prs = call_tools(
		  [1, 2, 3, 4, 5].map(function (n) {
		    return {server: "github", tool: "get_pull_request",
		            args: {owner: "acme", repo: "api", pullNumber: n}};
		  }),
		  {max_parallel: 5}
		);

		var titles = prs.map(function (r) {
		  if (!r.ok) { return "ERR: " + r.error.code; }
		  return JSON.parse(r.result.content[0].text).title;
		});
		({titles: titles})
	`
	result := Execute(context.Background(), stub, code, ExecutionOptions{TimeoutMs: 10000})
	if !result.Ok {
		t.Fatalf("quickstart script failed: %v", result.Error)
	}
	values, ok := result.Value.(map[string]interface{})
	if !ok {
		t.Fatalf("expected an object, got %T", result.Value)
	}
	titles, ok := values["titles"].([]interface{})
	if !ok {
		t.Fatalf("titles is %T, want an array", values["titles"])
	}
	if len(titles) != 5 {
		t.Fatalf("got %d titles, want 5", len(titles))
	}
	for i, title := range titles {
		if title != "get_pull_request" {
			t.Errorf("title %d = %v", i, title)
		}
	}
}
