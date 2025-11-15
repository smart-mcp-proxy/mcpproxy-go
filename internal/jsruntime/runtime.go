package jsruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dop251/goja"
	"github.com/google/uuid"
)

// ExecutionOptions contains optional parameters for JavaScript execution
type ExecutionOptions struct {
	Input              map[string]interface{} // Input data accessible as global `input` variable
	TimeoutMs          int                     // Execution timeout in milliseconds
	MaxToolCalls       int                     // Maximum number of call_tool() invocations (0 = unlimited)
	AllowedServers     []string                // Whitelist of allowed server names (empty = all allowed)
	ExecutionID        string                  // Unique execution ID for logging (auto-generated if empty)
}

// ToolCaller is an interface for calling upstream MCP tools
type ToolCaller interface {
	CallTool(ctx context.Context, serverName, toolName string, args map[string]interface{}) (interface{}, error)
}

// ExecutionContext tracks the state of a single JavaScript execution
type ExecutionContext struct {
	ExecutionID  string
	StartTime    time.Time
	EndTime      *time.Time
	Status       string // "running", "success", "error", "timeout"
	ToolCalls    []ToolCallRecord
	ResultValue  interface{}
	ErrorDetails *JsError
	toolCaller   ToolCaller
	maxToolCalls int
	allowedServerMap map[string]bool
}

// ToolCallRecord represents a single call_tool() invocation
type ToolCallRecord struct {
	ServerName  string                 `json:"server_name"`
	ToolName    string                 `json:"tool_name"`
	Arguments   map[string]interface{} `json:"arguments"`
	StartTime   time.Time              `json:"start_time"`
	DurationMs  int64                  `json:"duration_ms"`
	Success     bool                   `json:"success"`
	Result      interface{}            `json:"result,omitempty"`
	ErrorDetail interface{}            `json:"error_details,omitempty"`
}

// Execute runs JavaScript code in a sandboxed environment with tool call capabilities
func Execute(ctx context.Context, caller ToolCaller, code string, opts ExecutionOptions) *Result {
	// Generate execution ID if not provided
	if opts.ExecutionID == "" {
		opts.ExecutionID = uuid.New().String()
	}

	// Create execution context
	execCtx := &ExecutionContext{
		ExecutionID:  opts.ExecutionID,
		StartTime:    time.Now(),
		Status:       "running",
		ToolCalls:    make([]ToolCallRecord, 0),
		toolCaller:   caller,
		maxToolCalls: opts.MaxToolCalls,
		allowedServerMap: make(map[string]bool),
	}

	// Build allowed server map for fast lookup
	for _, serverName := range opts.AllowedServers {
		execCtx.allowedServerMap[serverName] = true
	}

	// Initialize Goja VM
	vm := goja.New()

	// Set up sandbox restrictions
	setupSandbox(vm)

	// Bind input global variable
	if opts.Input == nil {
		opts.Input = make(map[string]interface{})
	}
	if err := vm.Set("input", opts.Input); err != nil {
		return NewErrorResult(NewJsError(ErrorCodeRuntimeError, fmt.Sprintf("failed to set input: %v", err)))
	}

	// Bind call_tool function
	callToolFunc := execCtx.makeCallToolFunction(vm)
	if err := vm.Set("call_tool", callToolFunc); err != nil {
		return NewErrorResult(NewJsError(ErrorCodeRuntimeError, fmt.Sprintf("failed to set call_tool: %v", err)))
	}

	// Set up timeout enforcement
	timeoutMs := opts.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 120000 // Default 2 minutes
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	// Run JavaScript with timeout enforcement
	resultChan := make(chan *Result, 1)
	go func() {
		resultChan <- executeWithVM(vm, code, execCtx)
	}()

	// Wait for execution or timeout
	select {
	case result := <-resultChan:
		endTime := time.Now()
		execCtx.EndTime = &endTime
		if result.Ok {
			execCtx.Status = "success"
			execCtx.ResultValue = result.Value
		} else {
			execCtx.Status = "error"
			execCtx.ErrorDetails = result.Error
		}
		return result
	case <-timeoutCtx.Done():
		// Timeout occurred
		endTime := time.Now()
		execCtx.EndTime = &endTime
		execCtx.Status = "timeout"
		return NewErrorResult(NewJsError(ErrorCodeTimeout, "JavaScript execution timed out"))
	}
}

// executeWithVM runs the JavaScript code in the given VM and returns the result
func executeWithVM(vm *goja.Runtime, code string, execCtx *ExecutionContext) *Result {
	// Compile the code first to catch syntax errors
	_, err := goja.Compile("", code, false)
	if err != nil {
		// Extract syntax error details
		if exception, ok := err.(*goja.Exception); ok {
			return NewErrorResult(NewJsErrorWithStack(
				ErrorCodeSyntaxError,
				exception.String(),
				exception.String(),
			))
		}
		return NewErrorResult(NewJsError(ErrorCodeSyntaxError, err.Error()))
	}

	// Execute the code
	value, err := vm.RunString(code)
	if err != nil {
		// Extract runtime error details
		if exception, ok := err.(*goja.Exception); ok {
			stack := exception.String() // Stack trace is included in the exception string
			return NewErrorResult(NewJsErrorWithStack(
				ErrorCodeRuntimeError,
				exception.Error(),
				stack,
			))
		}
		return NewErrorResult(NewJsError(ErrorCodeRuntimeError, err.Error()))
	}

	// Export the result to Go value
	exported := value.Export()

	// Validate JSON serializability
	if err := validateSerializable(exported); err != nil {
		return NewErrorResult(NewJsError(ErrorCodeSerializationError, err.Error()))
	}

	return NewSuccessResult(exported)
}

// setupSandbox configures the VM to prevent access to restricted APIs
func setupSandbox(vm *goja.Runtime) {
	// Disable require() - prevent module loading
	vm.Set("require", goja.Undefined())

	// Disable setTimeout/setInterval - prevent async operations
	vm.Set("setTimeout", goja.Undefined())
	vm.Set("setInterval", goja.Undefined())

	// Disable clearTimeout/clearInterval
	vm.Set("clearTimeout", goja.Undefined())
	vm.Set("clearInterval", goja.Undefined())

	// Note: Goja does not provide filesystem, network, or process access by default
	// so we don't need to explicitly block those
}

// makeCallToolFunction creates the call_tool() function bound to this execution context
func (ec *ExecutionContext) makeCallToolFunction(vm *goja.Runtime) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		// Extract arguments: call_tool(serverName, toolName, args)
		if len(call.Arguments) < 3 {
			return vm.ToValue(map[string]interface{}{
				"ok": false,
				"error": map[string]interface{}{
					"code":    "INVALID_ARGS",
					"message": "call_tool requires 3 arguments: serverName, toolName, args",
				},
			})
		}

		serverName := call.Arguments[0].String()
		toolName := call.Arguments[1].String()

		// Parse args (must be an object)
		argsValue := call.Arguments[2].Export()
		args, ok := argsValue.(map[string]interface{})
		if !ok {
			return vm.ToValue(map[string]interface{}{
				"ok": false,
				"error": map[string]interface{}{
					"code":    "INVALID_ARGS",
					"message": "args must be an object",
				},
			})
		}

		// Check max_tool_calls limit
		if ec.maxToolCalls > 0 && len(ec.ToolCalls) >= ec.maxToolCalls {
			return vm.ToValue(map[string]interface{}{
				"ok": false,
				"error": map[string]interface{}{
					"code":    string(ErrorCodeMaxToolCallsExceeded),
					"message": fmt.Sprintf("exceeded max tool calls limit: %d", ec.maxToolCalls),
				},
			})
		}

		// Check allowed servers
		if len(ec.allowedServerMap) > 0 && !ec.allowedServerMap[serverName] {
			return vm.ToValue(map[string]interface{}{
				"ok": false,
				"error": map[string]interface{}{
					"code":    string(ErrorCodeServerNotAllowed),
					"message": fmt.Sprintf("server not allowed: %s", serverName),
				},
			})
		}

		// Record tool call start
		record := ToolCallRecord{
			ServerName: serverName,
			ToolName:   toolName,
			Arguments:  args,
			StartTime:  time.Now(),
		}

		// Call the upstream tool
		ctx := context.Background() // Note: Using background context for tool calls
		result, err := ec.toolCaller.CallTool(ctx, serverName, toolName, args)

		// Record duration
		record.DurationMs = time.Since(record.StartTime).Milliseconds()

		if err != nil {
			// Tool call failed
			record.Success = false
			record.ErrorDetail = err.Error()
			ec.ToolCalls = append(ec.ToolCalls, record)

			return vm.ToValue(map[string]interface{}{
				"ok": false,
				"error": map[string]interface{}{
					"code":    "UPSTREAM_ERROR",
					"message": err.Error(),
				},
			})
		}

		// Tool call succeeded
		record.Success = true
		record.Result = result
		ec.ToolCalls = append(ec.ToolCalls, record)

		return vm.ToValue(map[string]interface{}{
			"ok":     true,
			"result": result,
		})
	}
}

// validateSerializable checks if a value can be JSON-serialized
func validateSerializable(value interface{}) error {
	// Attempt JSON marshaling
	_, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("result must be JSON-serializable: %w", err)
	}
	return nil
}
