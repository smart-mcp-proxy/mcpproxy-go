package jsruntime

import (
	"context"
	"testing"
	"time"
)

// mockToolCaller implements ToolCaller for testing
type mockToolCaller struct {
	calls []struct {
		server string
		tool   string
		args   map[string]interface{}
	}
	results map[string]interface{}
	errors  map[string]error
}

func newMockToolCaller() *mockToolCaller {
	return &mockToolCaller{
		calls:   make([]struct{ server, tool string; args map[string]interface{} }, 0),
		results: make(map[string]interface{}),
		errors:  make(map[string]error),
	}
}

func (m *mockToolCaller) CallTool(ctx context.Context, serverName, toolName string, args map[string]interface{}) (interface{}, error) {
	m.calls = append(m.calls, struct {
		server string
		tool   string
		args   map[string]interface{}
	}{serverName, toolName, args})

	key := serverName + ":" + toolName
	if err, ok := m.errors[key]; ok {
		return nil, err
	}
	if result, ok := m.results[key]; ok {
		return result, nil
	}
	return map[string]interface{}{"success": true}, nil
}

// TestExecuteSimpleReturn tests basic JavaScript execution returning a value
func TestExecuteSimpleReturn(t *testing.T) {
	caller := newMockToolCaller()
	code := `({ result: 42, message: "hello" })`
	opts := ExecutionOptions{}

	result := Execute(context.Background(), caller, code, opts)

	if !result.Ok {
		t.Fatalf("expected ok=true, got ok=false with error: %v", result.Error)
	}

	resultMap, ok := result.Value.(map[string]interface{})
	if !ok {
		t.Fatalf("expected result to be a map, got %T", result.Value)
	}

	// Goja exports numbers as int64 or float64 depending on the value
	resultVal := resultMap["result"]
	var resultNum int64
	switch v := resultVal.(type) {
	case int64:
		resultNum = v
	case float64:
		resultNum = int64(v)
	default:
		t.Fatalf("expected result to be numeric, got %T", resultVal)
	}
	if resultNum != 42 {
		t.Errorf("expected result=42, got %v", resultNum)
	}

	if resultMap["message"].(string) != "hello" {
		t.Errorf("expected message='hello', got %v", resultMap["message"])
	}
}

// TestExecuteWithInput tests accessing the input global variable
func TestExecuteWithInput(t *testing.T) {
	caller := newMockToolCaller()
	code := `({ username: input.username, count: input.count + 1 })`
	opts := ExecutionOptions{
		Input: map[string]interface{}{
			"username": "alice",
			"count":    5,
		},
	}

	result := Execute(context.Background(), caller, code, opts)

	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	resultMap := result.Value.(map[string]interface{})
	if resultMap["username"] != "alice" {
		t.Errorf("expected username='alice', got %v", resultMap["username"])
	}

	// Handle both int64 and float64
	countVal := resultMap["count"]
	var count int64
	switch v := countVal.(type) {
	case int64:
		count = v
	case float64:
		count = int64(v)
	}
	if count != 6 {
		t.Errorf("expected count=6, got %v", count)
	}
}

// TestExecuteCallTool tests calling an upstream tool via call_tool()
func TestExecuteCallTool(t *testing.T) {
	caller := newMockToolCaller()
	caller.results["github:get_user"] = map[string]interface{}{
		"login": "octocat",
		"id":    583231,
	}

	code := `
		var res = call_tool("github", "get_user", { username: "octocat" });
		if (!res.ok) throw new Error("Failed to get user");
		({ user: res.result })
	`
	opts := ExecutionOptions{}

	result := Execute(context.Background(), caller, code, opts)

	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	if len(caller.calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(caller.calls))
	}

	if caller.calls[0].server != "github" || caller.calls[0].tool != "get_user" {
		t.Errorf("expected call to github:get_user, got %s:%s", caller.calls[0].server, caller.calls[0].tool)
	}
}

// TestExecuteMultipleToolCalls tests calling multiple tools in a loop
func TestExecuteMultipleToolCalls(t *testing.T) {
	caller := newMockToolCaller()
	caller.results["math:add"] = 10

	code := `
		var results = [];
		for (var i = 0; i < 3; i++) {
			var res = call_tool("math", "add", { a: i, b: 5 });
			if (res.ok) results.push(res.result);
		}
		({ results: results })
	`
	opts := ExecutionOptions{}

	result := Execute(context.Background(), caller, code, opts)

	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	if len(caller.calls) != 3 {
		t.Fatalf("expected 3 tool calls, got %d", len(caller.calls))
	}
}

// TestExecuteSyntaxError tests handling of JavaScript syntax errors
func TestExecuteSyntaxError(t *testing.T) {
	caller := newMockToolCaller()
	code := `{ invalid javascript syntax`
	opts := ExecutionOptions{}

	result := Execute(context.Background(), caller, code, opts)

	if result.Ok {
		t.Fatalf("expected ok=false for syntax error, got ok=true")
	}

	if result.Error.Code != ErrorCodeSyntaxError {
		t.Errorf("expected error code SYNTAX_ERROR, got %s", result.Error.Code)
	}
}

// TestExecuteRuntimeError tests handling of JavaScript runtime exceptions
func TestExecuteRuntimeError(t *testing.T) {
	caller := newMockToolCaller()
	code := `throw new Error("something went wrong")`
	opts := ExecutionOptions{}

	result := Execute(context.Background(), caller, code, opts)

	if result.Ok {
		t.Fatalf("expected ok=false for runtime error, got ok=true")
	}

	if result.Error.Code != ErrorCodeRuntimeError {
		t.Errorf("expected error code RUNTIME_ERROR, got %s", result.Error.Code)
	}

	// Error message should contain "something went wrong" (but may include location info)
	if result.Error.Message == "" {
		t.Errorf("expected non-empty error message")
	}
}

// TestExecuteTimeout tests timeout enforcement
func TestExecuteTimeout(t *testing.T) {
	caller := newMockToolCaller()
	code := `while(true) {}` // Infinite loop
	opts := ExecutionOptions{
		TimeoutMs: 100, // 100ms timeout
	}

	start := time.Now()
	result := Execute(context.Background(), caller, code, opts)
	duration := time.Since(start)

	if result.Ok {
		t.Fatalf("expected ok=false for timeout, got ok=true")
	}

	if result.Error.Code != ErrorCodeTimeout {
		t.Errorf("expected error code TIMEOUT, got %s", result.Error.Code)
	}

	// Verify timeout occurred around 100ms (allow some margin)
	if duration < 90*time.Millisecond || duration > 200*time.Millisecond {
		t.Errorf("expected timeout around 100ms, got %v", duration)
	}
}

// TestExecuteMaxToolCallsLimit tests max_tool_calls enforcement
func TestExecuteMaxToolCallsLimit(t *testing.T) {
	caller := newMockToolCaller()
	caller.results["test:tool"] = "ok"

	code := `
		var results = [];
		for (var i = 0; i < 10; i++) {
			var res = call_tool("test", "tool", {});
			if (!res.ok) {
				results.push({ error: res.error.code });
				break;
			}
			results.push({ success: true });
		}
		({ results: results })
	`
	opts := ExecutionOptions{
		MaxToolCalls: 5, // Limit to 5 calls
	}

	result := Execute(context.Background(), caller, code, opts)

	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	if len(caller.calls) != 5 {
		t.Fatalf("expected exactly 5 tool calls (at limit), got %d", len(caller.calls))
	}

	resultMap := result.Value.(map[string]interface{})
	results := resultMap["results"].([]interface{})
	if len(results) != 6 { // 5 successful + 1 error
		t.Errorf("expected 6 results (5 success + 1 error), got %d", len(results))
	}
}

// TestExecuteAllowedServers tests server whitelist enforcement
func TestExecuteAllowedServers(t *testing.T) {
	caller := newMockToolCaller()

	code := `
		var res1 = call_tool("allowed", "tool", {});
		var res2 = call_tool("blocked", "tool", {});
		({ res1_ok: res1.ok, res2_ok: res2.ok, res2_code: res2.error ? res2.error.code : null })
	`
	opts := ExecutionOptions{
		AllowedServers: []string{"allowed"},
	}

	result := Execute(context.Background(), caller, code, opts)

	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	resultMap := result.Value.(map[string]interface{})
	if resultMap["res1_ok"] != true {
		t.Errorf("expected res1_ok=true (allowed server), got %v", resultMap["res1_ok"])
	}
	if resultMap["res2_ok"] != false {
		t.Errorf("expected res2_ok=false (blocked server), got %v", resultMap["res2_ok"])
	}
	if resultMap["res2_code"] != string(ErrorCodeServerNotAllowed) {
		t.Errorf("expected res2_code=SERVER_NOT_ALLOWED, got %v", resultMap["res2_code"])
	}
}

// TestExecuteNonSerializableResult tests rejection of non-JSON-serializable results
func TestExecuteNonSerializableResult(t *testing.T) {
	caller := newMockToolCaller()
	code := `(function() { return 42; })` // Functions cannot be serialized
	opts := ExecutionOptions{}

	result := Execute(context.Background(), caller, code, opts)

	if result.Ok {
		t.Fatalf("expected ok=false for non-serializable result, got ok=true")
	}

	if result.Error.Code != ErrorCodeSerializationError {
		t.Errorf("expected error code SERIALIZATION_ERROR, got %s", result.Error.Code)
	}
}

// TestExecuteSandboxRestrictions tests that require() and other APIs are blocked
func TestExecuteSandboxRestrictions(t *testing.T) {
	caller := newMockToolCaller()

	tests := []struct {
		name string
		code string
	}{
		{"require", `require("fs")`},
		{"setTimeout", `setTimeout(function() {}, 100)`},
		{"setInterval", `setInterval(function() {}, 100)`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := ExecutionOptions{}
			result := Execute(context.Background(), caller, tt.code, opts)

			// Should either throw an error or return undefined
			if result.Ok {
				if result.Value != nil {
					t.Errorf("expected %s to be blocked or undefined, got: %v", tt.name, result.Value)
				}
			}
		})
	}
}
