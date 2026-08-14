package jsruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/google/uuid"
)

// ExecutionOptions contains optional parameters for JavaScript execution
type ExecutionOptions struct {
	Input          map[string]interface{} // Input data accessible as global `input` variable
	TimeoutMs      int                    // Execution timeout in milliseconds
	MaxToolCalls   int                    // Maximum number of call_tool() invocations (0 = unlimited)
	AllowedServers []string               // Whitelist of allowed server names (empty = all allowed, unless RestrictToAllowed)
	ExecutionID    string                 // Unique execution ID for logging (auto-generated if empty)
	Language       string                 // Source language: "javascript" (default) or "typescript"
	MaxParallel    int                    // Default concurrency for call_tools() batches (0 = built-in default; per-batch options override this)

	// RestrictToAllowed enforces AllowedServers even when it is empty. Set by the
	// Spec 057 profile path: an active profile with an empty effective server set
	// (deny-all profile, or a non-overlapping token∩profile) must deny ALL
	// call_tool() invocations rather than fall back to the "empty = allow all"
	// default. Leave false for unrestricted code_execution and agent-token scopes.
	RestrictToAllowed bool

	// Auth enforcement (Spec 031)
	AuthContext        *AuthInfo            // Auth context for permission enforcement (nil = no restrictions)
	ToolAnnotationFunc ToolAnnotationLookup // Function to look up tool annotations for permission checking
}

// AuthInfo carries authentication context for permission enforcement in JS execution.
// This is a simplified view of the auth.AuthContext to avoid circular imports.
type AuthInfo struct {
	Type           string   // "admin", "agent", "user", etc.
	AgentName      string   // Name of the agent token
	AllowedServers []string // Servers this token can access (nil = all)
	Permissions    []string // Permission tiers: "read", "write", "destructive"
}

// CanAccessServer checks whether this auth context can access the named server.
func (a *AuthInfo) CanAccessServer(name string) bool {
	if a == nil || a.Type == "admin" || a.Type == "admin_user" {
		return true
	}
	if name == "" {
		return false
	}
	for _, s := range a.AllowedServers {
		if s == "*" || s == name {
			return true
		}
	}
	return false
}

// HasPermission checks whether this auth context includes the given permission.
func (a *AuthInfo) HasPermission(perm string) bool {
	if a == nil || a.Type == "admin" || a.Type == "admin_user" {
		return true
	}
	for _, p := range a.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

// ToolAnnotationLookup is a function that returns the permission tier required for a tool.
// Returns one of "read", "write", "destructive".
type ToolAnnotationLookup func(serverName, toolName string) string

// ToolCaller is an interface for calling upstream MCP tools
type ToolCaller interface {
	CallTool(ctx context.Context, serverName, toolName string, args map[string]interface{}) (interface{}, error)
}

// ExecutionContext tracks the state of a single JavaScript execution
type ExecutionContext struct {
	ExecutionID       string
	StartTime         time.Time
	EndTime           *time.Time
	Status            string // "running", "success", "error", "timeout"
	ToolCalls         []ToolCallRecord
	ResultValue       interface{}
	ErrorDetails      *JsError
	toolCaller        ToolCaller
	maxToolCalls      int
	maxParallel       int // configured default concurrency for call_tools() batches
	allowedServerMap  map[string]bool
	restrictToAllowed bool // enforce allowedServerMap even when empty (Spec 057 deny-all profile)

	// ctx is the execution's timeout context, wired by Execute. Batch workers
	// dispatch under it so they are cancelled with the execution instead of
	// outliving it; the lone call_tool() path keeps context.Background().
	ctx context.Context

	// Auth enforcement (Spec 031)
	authInfo           *AuthInfo
	toolAnnotationFunc ToolAnnotationLookup
	maxPermissionLevel string // Tracks highest permission used: read < write < destructive
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

// newExecutionContext builds the per-execution state Execute drives and the
// host functions enforce against.
func newExecutionContext(caller ToolCaller, opts ExecutionOptions) *ExecutionContext {
	execCtx := &ExecutionContext{
		ExecutionID:        opts.ExecutionID,
		StartTime:          time.Now(),
		Status:             "running",
		ToolCalls:          make([]ToolCallRecord, 0),
		toolCaller:         caller,
		maxToolCalls:       opts.MaxToolCalls,
		maxParallel:        opts.MaxParallel,
		allowedServerMap:   make(map[string]bool),
		restrictToAllowed:  opts.RestrictToAllowed,
		authInfo:           opts.AuthContext,
		toolAnnotationFunc: opts.ToolAnnotationFunc,
		maxPermissionLevel: "",
	}

	// Build allowed server map for fast lookup
	for _, serverName := range opts.AllowedServers {
		execCtx.allowedServerMap[serverName] = true
	}

	return execCtx
}

// Execute runs JavaScript or TypeScript code in a sandboxed environment with tool call capabilities.
// When opts.Language is "typescript", the code is transpiled to JavaScript before execution.
func Execute(ctx context.Context, caller ToolCaller, code string, opts ExecutionOptions) *Result {
	result, _ := execute(ctx, caller, code, opts)
	return result
}

// execute is Execute plus the execution context it ran, which tests inspect for
// state the Result does not carry (recorded tool calls, the worker context).
func execute(ctx context.Context, caller ToolCaller, code string, opts ExecutionOptions) (*Result, *ExecutionContext) {
	// Generate execution ID if not provided
	if opts.ExecutionID == "" {
		opts.ExecutionID = uuid.New().String()
	}

	// Create execution context
	execCtx := newExecutionContext(caller, opts)

	// Validate language parameter
	if langErr := ValidateLanguage(opts.Language); langErr != nil {
		return NewErrorResult(langErr), execCtx
	}

	// Transpile TypeScript to JavaScript if needed
	if opts.Language == "typescript" {
		transpiled, transpileErr := TranspileTypeScript(code)
		if transpileErr != nil {
			return NewErrorResult(transpileErr), execCtx
		}
		code = transpiled
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
		return NewErrorResult(NewJsError(ErrorCodeRuntimeError, fmt.Sprintf("failed to set input: %v", err))), execCtx
	}

	// Bind call_tool function
	callToolFunc := execCtx.makeCallToolFunction(vm)
	if err := vm.Set("call_tool", callToolFunc); err != nil {
		return NewErrorResult(NewJsError(ErrorCodeRuntimeError, fmt.Sprintf("failed to set call_tool: %v", err))), execCtx
	}

	// Bind call_tools function (batched fan-out, Spec 096)
	callToolsFunc := execCtx.makeCallToolsFunction(vm)
	if err := vm.Set("call_tools", callToolsFunc); err != nil {
		return NewErrorResult(NewJsError(ErrorCodeRuntimeError, fmt.Sprintf("failed to set call_tools: %v", err))), execCtx
	}

	// Set up timeout enforcement
	timeoutMs := opts.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 120000 // Default 2 minutes
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	// Batch workers dispatch under the execution's timeout context. Assigned
	// before the script goroutine starts, so nothing reads it concurrently.
	execCtx.ctx = timeoutCtx

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
		return result, execCtx
	case <-timeoutCtx.Done():
		// Timeout occurred
		endTime := time.Now()
		execCtx.EndTime = &endTime
		execCtx.Status = "timeout"
		return NewErrorResult(NewJsError(ErrorCodeTimeout, "JavaScript execution timed out")), execCtx
	}
}

// executionCtx returns the context batch workers dispatch under. An
// ExecutionContext built outside Execute has none, so fall back to a
// background context rather than dispatching with a nil one.
func (ec *ExecutionContext) executionCtx() context.Context {
	if ec.ctx != nil {
		return ec.ctx
	}
	return context.Background()
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

// errorEnvelope builds the {ok:false, error:{code, message}} value both
// call_tool() and call_tools() hand back to scripts.
func errorEnvelope(code ErrorCode, message string) map[string]interface{} {
	return map[string]interface{}{
		"ok": false,
		"error": map[string]interface{}{
			"code":    string(code),
			"message": message,
		},
	}
}

// successEnvelope builds the {ok:true, result} value both host functions hand
// back to scripts.
func successEnvelope(result interface{}) map[string]interface{} {
	return map[string]interface{}{
		"ok":     true,
		"result": result,
	}
}

// checkDispatchGates runs the scope gates a tool call must pass before it may
// be dispatched — allow-list/profile, agent-token server scope, permission
// tier — and returns the error envelope of the first failure (nil when the
// call may proceed) plus the permission tier the call requires.
//
// The gates are pure: the budget check and the updateMaxPermissionLevel side
// effect stay with the callers, because the batch path accounts for both
// across a whole batch before dispatching any of it.
func (ec *ExecutionContext) checkDispatchGates(serverName, toolName string) (gateErr map[string]interface{}, requiredPerm string) {
	// Check allowed servers. When restrictToAllowed is set (active Spec 057
	// profile), the map is enforced even when empty — an empty effective set
	// means "deny everything". Otherwise an empty map means "no restriction".
	if (ec.restrictToAllowed || len(ec.allowedServerMap) > 0) && !ec.allowedServerMap[serverName] {
		return errorEnvelope(ErrorCodeServerNotAllowed, fmt.Sprintf("server not allowed: %s", serverName)), ""
	}

	// Auth context enforcement (Spec 031)
	if ec.authInfo == nil {
		return nil, ""
	}

	if !ec.authInfo.CanAccessServer(serverName) {
		return errorEnvelope(ErrorCodeAccessDenied, fmt.Sprintf("token does not have access to server '%s'", serverName)), ""
	}

	// Determine required permission via annotation lookup
	requiredPerm = "read" // Default to read
	if ec.toolAnnotationFunc != nil {
		requiredPerm = ec.toolAnnotationFunc(serverName, toolName)
	}

	if !ec.authInfo.HasPermission(requiredPerm) {
		return errorEnvelope(ErrorCodePermissionDenied,
			fmt.Sprintf("token does not have '%s' permission for tool '%s:%s'", requiredPerm, serverName, toolName)), ""
	}

	return nil, requiredPerm
}

// makeCallToolFunction creates the call_tool() function bound to this execution context
func (ec *ExecutionContext) makeCallToolFunction(vm *goja.Runtime) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		// Extract arguments: call_tool(serverName, toolName, args)
		if len(call.Arguments) < 3 {
			return vm.ToValue(errorEnvelope(ErrorCodeInvalidArgs, "call_tool requires 3 arguments: serverName, toolName, args"))
		}

		serverName := call.Arguments[0].String()
		toolName := call.Arguments[1].String()

		// Parse args (must be an object)
		argsValue := call.Arguments[2].Export()
		args, ok := argsValue.(map[string]interface{})
		if !ok {
			return vm.ToValue(errorEnvelope(ErrorCodeInvalidArgs, "args must be an object"))
		}

		// Check max_tool_calls limit
		if ec.maxToolCalls > 0 && len(ec.ToolCalls) >= ec.maxToolCalls {
			return vm.ToValue(errorEnvelope(ErrorCodeMaxToolCallsExceeded,
				fmt.Sprintf("exceeded max tool calls limit: %d", ec.maxToolCalls)))
		}

		if gateErr, requiredPerm := ec.checkDispatchGates(serverName, toolName); gateErr != nil {
			return vm.ToValue(gateErr)
		} else if requiredPerm != "" {
			// Track highest permission level
			ec.updateMaxPermissionLevel(requiredPerm)
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

			return vm.ToValue(errorEnvelope(ErrorCodeUpstreamError, err.Error()))
		}

		// Tool call succeeded
		record.Success = true

		// Expose the result under its wire (JSON) shape rather than the live Go
		// value: goja would otherwise surface Go field names and exported methods.
		plain, nerr := normalizeToolResult(result)
		if nerr != nil {
			record.Success = false
			record.ErrorDetail = nerr.Error()
			ec.ToolCalls = append(ec.ToolCalls, record)

			return vm.ToValue(errorEnvelope(ErrorCodeSerializationError,
				"tool result is not JSON-serializable: "+nerr.Error()))
		}

		record.Result = plain
		ec.ToolCalls = append(ec.ToolCalls, record)

		return vm.ToValue(successEnvelope(plain))
	}
}

// Limits for call_tools() batches (Spec 096).
const (
	// batchMaxRequests caps a single batch so one script cannot fan out
	// without bound.
	batchMaxRequests = 100
	// batchMinParallel / batchMaxParallel bound the effective worker count,
	// whatever the config or the script asks for.
	batchMinParallel = 1
	batchMaxParallel = 32
	// batchDefaultParallel applies when neither the config nor the batch
	// specifies a concurrency.
	batchDefaultParallel = 8
)

// batchRequest is one validated element of a call_tools() batch.
type batchRequest struct {
	server string
	tool   string
	args   map[string]interface{}
}

// makeCallToolsFunction creates the call_tools() function bound to this
// execution context. The closure runs on the script goroutine: it parses and
// gates every element there, dispatches the survivors through a bounded worker
// pool, and converts the assembled slots back into VM values only after the
// workers have joined — the VM is owned by this goroutine alone.
func (ec *ExecutionContext) makeCallToolsFunction(vm *goja.Runtime) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		requests, maxParallel, err := parseBatchCall(call)
		if err != nil {
			// A malformed batch is a single envelope, never a throw: scripts
			// handle call_tools failures the same way they handle call_tool
			// failures.
			return vm.ToValue(errorEnvelope(ErrorCodeInvalidArgs, err.Error()))
		}

		return vm.ToValue(ec.runBatch(requests, maxParallel))
	}
}

// parseBatchCall validates the call_tools(requests, options) arguments,
// returning the requests in input order and the per-batch max_parallel
// override (0 when absent). Any problem invalidates the WHOLE call — nothing
// is dispatched and no budget is consumed — and names the first offending
// element.
func parseBatchCall(call goja.FunctionCall) (requests []batchRequest, maxParallel int, err error) {
	if len(call.Arguments) < 1 {
		return nil, 0, fmt.Errorf("call_tools requires 1 argument: requests (an array of {server, tool, args})")
	}

	rawRequests, ok := call.Arguments[0].Export().([]interface{})
	if !ok {
		return nil, 0, fmt.Errorf("call_tools: requests must be an array of {server, tool, args}")
	}
	if len(rawRequests) > batchMaxRequests {
		return nil, 0, fmt.Errorf("call_tools: batch of %d exceeds the maximum of %d requests", len(rawRequests), batchMaxRequests)
	}

	maxParallel, err = parseBatchOptions(call)
	if err != nil {
		return nil, 0, err
	}

	requests = make([]batchRequest, 0, len(rawRequests))
	for i, raw := range rawRequests {
		// A sparse array hole exports as nil, so holes fail this check too.
		element, ok := raw.(map[string]interface{})
		if !ok {
			return nil, 0, fmt.Errorf("call_tools: element %d: must be an object with server and tool", i)
		}

		server, ok := element["server"].(string)
		if !ok || server == "" {
			return nil, 0, fmt.Errorf("call_tools: element %d: server must be a non-empty string", i)
		}
		tool, ok := element["tool"].(string)
		if !ok || tool == "" {
			return nil, 0, fmt.Errorf("call_tools: element %d: tool must be a non-empty string", i)
		}

		args := map[string]interface{}{}
		if rawArgs, present := element["args"]; present {
			args, ok = rawArgs.(map[string]interface{})
			if !ok {
				return nil, 0, fmt.Errorf("call_tools: element %d: args must be an object", i)
			}
		}

		requests = append(requests, batchRequest{server: server, tool: tool, args: args})
	}

	return requests, maxParallel, nil
}

// parseBatchOptions reads the optional second argument of call_tools().
// Returns 0 when no override was supplied. Unknown keys are ignored.
func parseBatchOptions(call goja.FunctionCall) (int, error) {
	if len(call.Arguments) < 2 {
		return 0, nil
	}

	// Passing undefined/null for a trailing optional argument means "no
	// options", the way JavaScript callers expect.
	raw := call.Arguments[1].Export()
	if raw == nil {
		return 0, nil
	}

	options, ok := raw.(map[string]interface{})
	if !ok {
		return 0, fmt.Errorf("call_tools: options must be an object")
	}

	value, present := options["max_parallel"]
	if !present {
		return 0, nil
	}

	maxParallel, ok := batchOptionInt(value)
	if !ok {
		return 0, fmt.Errorf("call_tools: options.max_parallel must be an integer")
	}
	if maxParallel < batchMinParallel || maxParallel > batchMaxParallel {
		return 0, fmt.Errorf("call_tools: options.max_parallel must be between %d and %d, got %d",
			batchMinParallel, batchMaxParallel, maxParallel)
	}
	return maxParallel, nil
}

// batchOptionInt accepts the numeric shapes goja exports: int64 for integral
// numbers, float64 otherwise. A fractional value is rejected rather than
// truncated, so 2.5 never silently becomes 2.
func batchOptionInt(value interface{}) (int, bool) {
	switch v := value.(type) {
	case int64:
		return int(v), true
	case int:
		return v, true
	case float64:
		if v != math.Trunc(v) {
			return 0, false
		}
		return int(v), true
	default:
		return 0, false
	}
}

// effectiveMaxParallel resolves the worker bound for one batch:
// per-batch override > ExecutionOptions.MaxParallel (the configured default) >
// built-in default, always clamped to the supported range.
func (ec *ExecutionContext) effectiveMaxParallel(override int) int {
	value := batchDefaultParallel
	switch {
	case override > 0:
		value = override
	case ec.maxParallel > 0:
		value = ec.maxParallel
	}

	if value < batchMinParallel {
		return batchMinParallel
	}
	if value > batchMaxParallel {
		return batchMaxParallel
	}
	return value
}

// runBatch enforces, dispatches and assembles one call_tools() batch. It runs
// on the script goroutine; only the dispatch itself is concurrent.
func (ec *ExecutionContext) runBatch(requests []batchRequest, maxParallelOverride int) []interface{} {
	slots := make([]interface{}, len(requests))
	records := make([]ToolCallRecord, len(requests))
	perms := make([]string, len(requests))
	dispatch := make([]int, 0, len(requests))

	// Pre-dispatch pass, in input order, with the same check order a lone
	// call_tool() uses: budget first, then the scope gates. The budget of
	// element k counts the calls already recorded plus the elements this batch
	// has accepted so far — a script-local count, so it cannot race, and every
	// accepted element is guaranteed exactly one record at the join.
	for i, req := range requests {
		if ec.maxToolCalls > 0 && len(ec.ToolCalls)+len(dispatch) >= ec.maxToolCalls {
			slots[i] = errorEnvelope(ErrorCodeMaxToolCallsExceeded,
				fmt.Sprintf("exceeded max tool calls limit: %d", ec.maxToolCalls))
			continue
		}

		gateErr, requiredPerm := ec.checkDispatchGates(req.server, req.tool)
		if gateErr != nil {
			slots[i] = gateErr
			continue
		}

		perms[i] = requiredPerm
		dispatch = append(dispatch, i)
	}

	if len(dispatch) == 0 {
		return slots
	}

	ec.dispatchBatch(requests, dispatch, slots, records, ec.effectiveMaxParallel(maxParallelOverride))

	// Execution state is script-goroutine-only, so the records the workers
	// produced are folded in here, in input order, after the join.
	for _, i := range dispatch {
		ec.ToolCalls = append(ec.ToolCalls, records[i])
		if perms[i] != "" {
			ec.updateMaxPermissionLevel(perms[i])
		}
	}

	return slots
}

// dispatchBatch runs the accepted elements through a bounded worker pool and
// returns once every worker has finished. Each worker writes only into the
// slot and record cells its own index owns, so no locking is needed and the
// WaitGroup publishes the writes to the script goroutine.
func (ec *ExecutionContext) dispatchBatch(requests []batchRequest, dispatch []int, slots []interface{}, records []ToolCallRecord, workers int) {
	if workers > len(dispatch) {
		workers = len(dispatch)
	}

	// The queue is prefilled and closed before any worker starts: there is no
	// producer to outlive the pool and nothing to deadlock on.
	indices := make(chan int, len(dispatch))
	for _, i := range dispatch {
		indices <- i
	}
	close(indices)

	ctx := ec.executionCtx()
	caller := ec.toolCaller

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range indices {
				slots[i], records[i] = dispatchBatchElement(ctx, caller, requests[i])
			}
		}()
	}

	// Unconditional join: the script goroutine never touches the cells while a
	// worker is alive, cancellation included.
	wg.Wait()
}

// dispatchBatchElement performs one upstream call for a batch and returns the
// slot the script sees plus the record the execution keeps. It touches no
// execution state, so it is safe to run on a worker goroutine.
func dispatchBatchElement(ctx context.Context, caller ToolCaller, req batchRequest) (map[string]interface{}, ToolCallRecord) {
	record := ToolCallRecord{
		ServerName: req.server,
		ToolName:   req.tool,
		Arguments:  req.args,
		StartTime:  time.Now(),
	}

	// A cancelled execution still owes every accepted element a slot and a
	// record, so the remaining queue is drained into cancellation errors
	// instead of being dispatched.
	if err := ctx.Err(); err != nil {
		record.DurationMs = time.Since(record.StartTime).Milliseconds()
		record.ErrorDetail = err.Error()
		return errorEnvelope(ErrorCodeUpstreamError,
			fmt.Sprintf("execution ended before the call was dispatched: %v", err)), record
	}

	result, err := caller.CallTool(ctx, req.server, req.tool, req.args)
	record.DurationMs = time.Since(record.StartTime).Milliseconds()

	if err != nil {
		record.ErrorDetail = err.Error()
		return errorEnvelope(ErrorCodeUpstreamError, err.Error()), record
	}

	// Expose the result under its wire (JSON) shape rather than the live Go
	// value, exactly as the lone call_tool() path does.
	plain, nerr := normalizeToolResult(result)
	if nerr != nil {
		record.ErrorDetail = nerr.Error()
		return errorEnvelope(ErrorCodeSerializationError,
			"tool result is not JSON-serializable: "+nerr.Error()), record
	}

	record.Success = true
	record.Result = plain
	return successEnvelope(plain), record
}

// normalizeToolResult converts an upstream tool result into its JSON wire shape
// so scripts see the documented field names (content[0].text) instead of Go
// struct fields, and custom MarshalJSON semantics are preserved.
func normalizeToolResult(v interface{}) (interface{}, error) {
	if v == nil {
		return nil, nil
	}

	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	var plain interface{}
	if err := json.Unmarshal(data, &plain); err != nil {
		return nil, err
	}
	return plain, nil
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

// permissionRank maps permission tiers to numeric rank for comparison.
var permissionRank = map[string]int{
	"read":        1,
	"write":       2,
	"destructive": 3,
}

// updateMaxPermissionLevel tracks the highest permission level used during execution.
func (ec *ExecutionContext) updateMaxPermissionLevel(perm string) {
	if permissionRank[perm] > permissionRank[ec.maxPermissionLevel] {
		ec.maxPermissionLevel = perm
	}
}

// GetMaxPermissionLevel returns the highest permission level used during execution.
func (ec *ExecutionContext) GetMaxPermissionLevel() string {
	return ec.maxPermissionLevel
}
