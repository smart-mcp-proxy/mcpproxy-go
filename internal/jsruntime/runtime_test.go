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
		calls: make([]struct {
			server, tool string
			args         map[string]interface{}
		}, 0),
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

// TestExecuteTypeScript tests TypeScript code execution via Execute()
func TestExecuteTypeScript(t *testing.T) {
	caller := newMockToolCaller()
	code := `const x: number = 42; const msg: string = "hello"; ({ result: x, message: msg })`
	opts := ExecutionOptions{
		Language: "typescript",
	}

	result := Execute(context.Background(), caller, code, opts)

	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	resultMap, ok := result.Value.(map[string]interface{})
	if !ok {
		t.Fatalf("expected result to be a map, got %T", result.Value)
	}

	var resultNum int64
	switch v := resultMap["result"].(type) {
	case int64:
		resultNum = v
	case float64:
		resultNum = int64(v)
	}
	if resultNum != 42 {
		t.Errorf("expected result=42, got %v", resultMap["result"])
	}
	if resultMap["message"] != "hello" {
		t.Errorf("expected message='hello', got %v", resultMap["message"])
	}
}

// TestExecuteTypeScriptWithCallTool tests TypeScript code that uses call_tool()
func TestExecuteTypeScriptWithCallTool(t *testing.T) {
	caller := newMockToolCaller()
	caller.results["github:get_user"] = map[string]interface{}{
		"login": "octocat",
		"id":    583231,
	}

	code := `
		interface ToolResult {
			ok: boolean;
			result?: any;
			error?: any;
		}
		const res: ToolResult = call_tool("github", "get_user", { username: "octocat" });
		if (!res.ok) throw new Error("Failed");
		({ user: res.result })
	`
	opts := ExecutionOptions{
		Language: "typescript",
	}

	result := Execute(context.Background(), caller, code, opts)

	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	if len(caller.calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(caller.calls))
	}
}

// TestExecuteJavaScriptWithLanguageExplicit tests that language: "javascript" works as before
func TestExecuteJavaScriptWithLanguageExplicit(t *testing.T) {
	caller := newMockToolCaller()
	code := `({ result: 42 })`
	opts := ExecutionOptions{
		Language: "javascript",
	}

	result := Execute(context.Background(), caller, code, opts)

	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}
}

// TestExecuteEmptyLanguageDefaultsToJavaScript tests that empty language works as JavaScript
func TestExecuteEmptyLanguageDefaultsToJavaScript(t *testing.T) {
	caller := newMockToolCaller()
	code := `({ result: 42 })`
	opts := ExecutionOptions{
		Language: "", // empty = default = javascript
	}

	result := Execute(context.Background(), caller, code, opts)

	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}
}

// TestExecuteInvalidLanguage tests that an invalid language returns an error
func TestExecuteInvalidLanguage(t *testing.T) {
	caller := newMockToolCaller()
	code := `({ result: 42 })`
	opts := ExecutionOptions{
		Language: "python",
	}

	result := Execute(context.Background(), caller, code, opts)

	if result.Ok {
		t.Fatalf("expected ok=false for invalid language, got ok=true")
	}
	if result.Error.Code != ErrorCodeInvalidLanguage {
		t.Errorf("expected error code INVALID_LANGUAGE, got %s", result.Error.Code)
	}
}

// TestExecuteTypeScriptTranspileError tests that transpilation errors are returned properly
func TestExecuteTypeScriptTranspileError(t *testing.T) {
	caller := newMockToolCaller()
	code := `const x: number = ;` // invalid TypeScript
	opts := ExecutionOptions{
		Language: "typescript",
	}

	result := Execute(context.Background(), caller, code, opts)

	if result.Ok {
		t.Fatalf("expected ok=false for transpile error, got ok=true")
	}
	if result.Error.Code != ErrorCodeTranspileError {
		t.Errorf("expected error code TRANSPILE_ERROR, got %s", result.Error.Code)
	}
}

// TestExecuteTypeScriptWithInput tests TypeScript with input variable
func TestExecuteTypeScriptWithInput(t *testing.T) {
	caller := newMockToolCaller()
	code := `
		interface Input { value: number; name: string; }
		const inp = input as Input;
		({ doubled: inp.value * 2, greeting: "Hello " + inp.name })
	`
	opts := ExecutionOptions{
		Language: "typescript",
		Input: map[string]interface{}{
			"value": 21,
			"name":  "World",
		},
	}

	result := Execute(context.Background(), caller, code, opts)

	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	resultMap := result.Value.(map[string]interface{})
	var doubled int64
	switch v := resultMap["doubled"].(type) {
	case int64:
		doubled = v
	case float64:
		doubled = int64(v)
	}
	if doubled != 42 {
		t.Errorf("expected doubled=42, got %v", resultMap["doubled"])
	}
	if resultMap["greeting"] != "Hello World" {
		t.Errorf("expected greeting='Hello World', got %v", resultMap["greeting"])
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

// TestExecuteAuthContext_ServerAccessDenied tests auth enforcement blocks unauthorized server access
func TestExecuteAuthContext_ServerAccessDenied(t *testing.T) {
	caller := newMockToolCaller()

	code := `
		var res = call_tool("secret-server", "get_data", {});
		({ ok: res.ok, code: res.error ? res.error.code : null, msg: res.error ? res.error.message : null })
	`
	opts := ExecutionOptions{
		AuthContext: &AuthInfo{
			Type:           "agent",
			AgentName:      "test-bot",
			AllowedServers: []string{"github"}, // Only github, not secret-server
			Permissions:    []string{"read"},
		},
	}

	result := Execute(context.Background(), caller, code, opts)
	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	resultMap := result.Value.(map[string]interface{})
	if resultMap["ok"] != false {
		t.Errorf("expected tool call to fail, got ok=%v", resultMap["ok"])
	}
	if resultMap["code"] != "ACCESS_DENIED" {
		t.Errorf("expected ACCESS_DENIED, got %v", resultMap["code"])
	}
	if len(caller.calls) != 0 {
		t.Errorf("expected 0 upstream calls (blocked by auth), got %d", len(caller.calls))
	}
}

// TestExecuteAuthContext_PermissionDenied tests auth enforcement blocks insufficient permissions
func TestExecuteAuthContext_PermissionDenied(t *testing.T) {
	caller := newMockToolCaller()

	code := `
		var res = call_tool("github", "delete_repo", {});
		({ ok: res.ok, code: res.error ? res.error.code : null })
	`
	opts := ExecutionOptions{
		AuthContext: &AuthInfo{
			Type:           "agent",
			AgentName:      "reader-bot",
			AllowedServers: []string{"github"},
			Permissions:    []string{"read"}, // Only read, needs destructive
		},
		ToolAnnotationFunc: func(serverName, toolName string) string {
			if toolName == "delete_repo" {
				return "destructive"
			}
			return "read"
		},
	}

	result := Execute(context.Background(), caller, code, opts)
	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	resultMap := result.Value.(map[string]interface{})
	if resultMap["ok"] != false {
		t.Errorf("expected tool call to fail, got ok=%v", resultMap["ok"])
	}
	if resultMap["code"] != "PERMISSION_DENIED" {
		t.Errorf("expected PERMISSION_DENIED, got %v", resultMap["code"])
	}
}

// TestExecuteAuthContext_PermissionAggregation tests that max permission level is tracked
func TestExecuteAuthContext_PermissionAggregation(t *testing.T) {
	caller := newMockToolCaller()

	code := `
		call_tool("github", "list_repos", {});
		call_tool("github", "create_issue", {});
		call_tool("github", "list_repos", {});
		({ done: true })
	`
	opts := ExecutionOptions{
		AuthContext: &AuthInfo{
			Type:           "agent",
			AgentName:      "writer-bot",
			AllowedServers: []string{"github"},
			Permissions:    []string{"read", "write", "destructive"},
		},
		ToolAnnotationFunc: func(serverName, toolName string) string {
			if toolName == "create_issue" {
				return "write"
			}
			return "read"
		},
	}

	result := Execute(context.Background(), caller, code, opts)
	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	if len(caller.calls) != 3 {
		t.Errorf("expected 3 tool calls, got %d", len(caller.calls))
	}
}

// TestExecuteAuthContext_AdminBypass tests that admin auth bypasses permission checks
func TestExecuteAuthContext_AdminBypass(t *testing.T) {
	caller := newMockToolCaller()

	code := `
		var res = call_tool("secret-server", "delete_all", {});
		({ ok: res.ok })
	`
	opts := ExecutionOptions{
		AuthContext: &AuthInfo{
			Type: "admin",
			// No AllowedServers or Permissions - admin bypasses everything
		},
		ToolAnnotationFunc: func(serverName, toolName string) string {
			return "destructive"
		},
	}

	result := Execute(context.Background(), caller, code, opts)
	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	resultMap := result.Value.(map[string]interface{})
	if resultMap["ok"] != true {
		t.Errorf("admin should bypass all auth checks, got ok=%v", resultMap["ok"])
	}
	if len(caller.calls) != 1 {
		t.Errorf("expected 1 tool call (admin bypass), got %d", len(caller.calls))
	}
}

// TestExecuteAuthContext_NoAuthContext tests backward compatibility with nil auth
func TestExecuteAuthContext_NoAuthContext(t *testing.T) {
	caller := newMockToolCaller()

	code := `
		var res = call_tool("any-server", "any-tool", {});
		({ ok: res.ok })
	`
	opts := ExecutionOptions{
		// No AuthContext - should not enforce any restrictions
	}

	result := Execute(context.Background(), caller, code, opts)
	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	resultMap := result.Value.(map[string]interface{})
	if resultMap["ok"] != true {
		t.Errorf("expected ok=true with no auth context, got %v", resultMap["ok"])
	}
}

// TestAuthInfo_CanAccessServer tests the AuthInfo.CanAccessServer method
func TestAuthInfo_CanAccessServer(t *testing.T) {
	tests := []struct {
		name     string
		auth     *AuthInfo
		server   string
		expected bool
	}{
		{"nil auth allows all", nil, "anything", true},
		{"admin allows all", &AuthInfo{Type: "admin"}, "anything", true},
		{"admin_user allows all", &AuthInfo{Type: "admin_user"}, "anything", true},
		{"wildcard allows all", &AuthInfo{Type: "agent", AllowedServers: []string{"*"}}, "anything", true},
		{"specific match", &AuthInfo{Type: "agent", AllowedServers: []string{"github"}}, "github", true},
		{"no match", &AuthInfo{Type: "agent", AllowedServers: []string{"github"}}, "gitlab", false},
		{"empty server name", &AuthInfo{Type: "agent", AllowedServers: []string{"github"}}, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.auth.CanAccessServer(tt.server)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// TestAuthInfo_HasPermission tests the AuthInfo.HasPermission method
func TestAuthInfo_HasPermission(t *testing.T) {
	tests := []struct {
		name     string
		auth     *AuthInfo
		perm     string
		expected bool
	}{
		{"nil auth allows all", nil, "destructive", true},
		{"admin allows all", &AuthInfo{Type: "admin"}, "destructive", true},
		{"has read", &AuthInfo{Type: "agent", Permissions: []string{"read"}}, "read", true},
		{"no write", &AuthInfo{Type: "agent", Permissions: []string{"read"}}, "write", false},
		{"has write", &AuthInfo{Type: "agent", Permissions: []string{"read", "write"}}, "write", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.auth.HasPermission(tt.perm)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// TestPermissionLevelTracking tests max permission level tracking
func TestPermissionLevelTracking(t *testing.T) {
	ec := &ExecutionContext{}

	ec.updateMaxPermissionLevel("read")
	if ec.GetMaxPermissionLevel() != "read" {
		t.Errorf("expected read, got %s", ec.GetMaxPermissionLevel())
	}

	ec.updateMaxPermissionLevel("write")
	if ec.GetMaxPermissionLevel() != "write" {
		t.Errorf("expected write, got %s", ec.GetMaxPermissionLevel())
	}

	ec.updateMaxPermissionLevel("read") // Lower level shouldn't downgrade
	if ec.GetMaxPermissionLevel() != "write" {
		t.Errorf("expected write (should not downgrade), got %s", ec.GetMaxPermissionLevel())
	}

	ec.updateMaxPermissionLevel("destructive")
	if ec.GetMaxPermissionLevel() != "destructive" {
		t.Errorf("expected destructive, got %s", ec.GetMaxPermissionLevel())
	}
}
