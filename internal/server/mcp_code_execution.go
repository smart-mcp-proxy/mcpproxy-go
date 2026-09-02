package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/codescripts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/jsruntime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"
)

// The code_execution tool is registered on two surfaces — the default tool set
// (registerTools) and the routing-mode builder (buildCodeExecutionTool) — which
// must advertise exactly the same contract. They share these strings so the two
// descriptions cannot drift apart.
const (
	codeExecutionToolDescription = "Execute JavaScript or TypeScript code that orchestrates multiple upstream MCP tools in a single request. " +
		"Use this when you need to combine results from 2+ tools, implement conditional logic, loops, or data transformations " +
		"that would require multiple round-trips otherwise.\n\n" +
		"**When to use**: Multi-step workflows with data transformation, conditional logic, error handling, or iterating over results.\n" +
		"**When NOT to use**: Single tool calls (use call_tool directly), long-running operations (>2 minutes).\n\n" +
		"**Available in code**:\n" +
		"- `input` global: Your input data passed via the 'input' parameter\n" +
		"- `call_tool(serverName, toolName, args)`: Call upstream tools (returns {ok, result} or {ok, error})\n" +
		"- `call_tools(requests, options)`: Call INDEPENDENT tools in parallel. `requests` is an array (max 100) of " +
		"{server, tool, args} objects; `options` is optional and accepts `max_parallel` (1-32, defaults to the configured " +
		"code_execution_max_parallel). Returns one {ok, result} / {ok, error} slot per request, in input order, so one " +
		"failing call never fails the others. Malformed arguments return a single {ok:false, error} envelope and dispatch nothing.\n" +
		"- Modern JavaScript (ES2020+): arrow functions, const/let, template literals, destructuring, classes, for-of, " +
		"optional chaining (?.), nullish coalescing (??), spread/rest, Promises, Symbols, Map/Set, Proxy/Reflect " +
		"(no require(), filesystem, or network access)\n\n" +
		"**TypeScript support**: Set `language: \"typescript\"` to write TypeScript code with type annotations, interfaces, enums, and generics. " +
		"Types are automatically stripped before execution.\n\n" +
		"**Stored scripts**: Instead of `code`, pass `script: \"<name>\"` to run a script stored server-side in the `scripts/` directory next to mcpproxy's config file — " +
		"a long workflow then costs a name per run instead of its full source. Provide exactly one of `code` or `script`. Naming a script that does not exist returns the " +
		"available names, which is how you discover what is stored.\n\n" +
		"**Important runtime rules**:\n" +
		"- `call_tool` and `call_tools` are strictly SYNCHRONOUS. Do not use `await`.\n" +
		"- Upstream tools usually return an MCP content array. To parse JSON results: `const data = JSON.parse(res.result.content[0].text);`\n" +
		"- The last evaluated expression in your script is automatically returned as the final output.\n\n" +
		"**Security**: Sandboxed execution with timeout enforcement. Respects existing quarantine and server restrictions."

	codeExecutionCodeDescription = "JavaScript or TypeScript source code (ES2020+) to execute. Supports modern syntax: arrow functions, const/let, template literals, destructuring, " +
		"optional chaining, nullish coalescing. Use `input` to access input data, `call_tool(serverName, toolName, args)` to invoke one upstream tool and " +
		"`call_tools([{server, tool, args}, ...], {max_parallel})` to invoke independent tools in parallel. " +
		"Both are SYNCHRONOUS — do not use await. Return value is the last evaluated expression and must be JSON-serializable. " +
		"Example: `const res = call_tool('github', 'get_user', {username: input.username}); const data = JSON.parse(res.result.content[0].text); ({user: data, timestamp: Date.now()})`"

	codeExecutionLanguageDescription = "Source code language. When set to 'typescript', the code is automatically transpiled to JavaScript before execution. " +
		"Type annotations are stripped, enums and namespaces are converted to JavaScript equivalents. Default: 'javascript'."

	codeExecutionScriptDescription = "Name of a STORED script to execute instead of sending `code` inline (Spec 097). Scripts live as `<name>.js` / `<name>.ts` files in the `scripts/` " +
		"directory next to mcpproxy's active config file and are read fresh on every invocation, so an edited script takes effect immediately. " +
		"Provide EXACTLY ONE of `code` or `script`. The name is a bare identifier (letters, digits, '-' and '_'; 1-64 chars) — never a path. " +
		"The language comes from the file extension (.js → javascript, .ts → typescript); an explicit `language` that contradicts it is an error. " +
		"DISCOVERY: calling with a name that does not exist returns an error listing the available script names (first 20 alphabetically, plus the total), " +
		"so the current set can always be recovered from a single failed call. Everything else — `input`, options, sandbox limits, results — behaves exactly as for inline code."

	codeExecutionInputDescription = "Input data accessible as global `input` variable in code (default: {})"

	codeExecutionOptionsDescription = "Execution options: timeout_ms (1-600000, default: 120000), max_tool_calls (>= 0, 0=unlimited), " +
		"allowed_servers (array of server names, empty=all allowed). Batch concurrency is not an execution option: " +
		"call_tools() defaults to the configured code_execution_max_parallel and is overridden per batch with call_tools(requests, {max_parallel})."
)

// handleCodeExecution executes JavaScript code that orchestrates multiple upstream tools
func (p *MCPProxyServer) handleCodeExecution(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	p.recordMCPSurface()
	p.recordBuiltinTool("code_execution")
	p.logger.Debug("code_execution tool called")

	// enable_code_execution is a FEATURE switch, so it is enforced where every
	// surface passes rather than at registration. The MCP surfaces gate by
	// omitting the tool or serving a disabled stub, but REST /api/v1/code/exec,
	// REST /api/v1/tools/call and the tray all reach this handler through
	// CallToolDirect — which routed straight here, letting an API-key holder run
	// inline code and, since Spec 097, read and execute a server-side stored
	// script while the operator believed the feature was off. The check reads
	// the LIVE snapshot so a hot-reloaded flag takes effect on the next call;
	// no config at all means nothing to disable.
	if cfg := p.currentConfig(); cfg != nil && !cfg.EnableCodeExecution {
		recordCodeExecRefusal(ctx, config.ErrCodeExecutionDisabled)
		return mcp.NewToolResultError(config.CodeExecutionDisabledMessage), nil
	}

	// Parse arguments. MaxToolCalls starts at the unset sentinel so an explicit
	// max_tool_calls: 0 — the documented unlimited override — survives default
	// resolution instead of being floored to the configured limit.
	options := jsruntime.ExecutionOptions{MaxToolCalls: codeExecMaxToolCallsUnset}

	// Get all arguments
	args := request.GetArguments()

	// Extract language (optional, default: "javascript")
	explicitLanguage, errMsg := codeExecStringArg(args, "language")
	if errMsg != "" {
		return mcp.NewToolResultError(errMsg), nil
	}
	if explicitLanguage != "" {
		options.Language = explicitLanguage
	}

	// Spec 097: the source is EITHER inline code or a stored script name, never
	// both and never neither. This is resolved before anything else runs — the
	// handler is the only execution-time resolver on every surface (MCP, REST
	// and both CLI modes send the NAME, never the content).
	code, scriptName, errMsg := p.resolveCodeExecutionSource(ctx, args, &options)
	if errMsg != "" {
		return mcp.NewToolResultError(errMsg), nil
	}

	// Extract input (optional) - this is an object
	input, ok := args["input"].(map[string]interface{})
	if !ok || input == nil {
		input = make(map[string]interface{})
	}
	options.Input = input

	// Extract options object (optional)
	if optionsObj, ok := args["options"].(map[string]interface{}); ok && optionsObj != nil {
		if errMsg := applyCodeExecutionOptions(optionsObj, &options); errMsg != "" {
			return mcp.NewToolResultError(errMsg), nil
		}
	}

	// Read the LIVE snapshot, not the construction-time one: a hot-reloaded
	// code_execution_* value must reach the executions that start after it.
	// Without a config at all, every knob resolves to its built-in default.
	var configTimeoutMs, configMaxToolCalls, configMaxParallel int
	if cfg := p.currentConfig(); cfg != nil {
		configTimeoutMs = cfg.CodeExecutionTimeoutMs
		configMaxToolCalls = cfg.CodeExecutionMaxToolCalls
		configMaxParallel = cfg.CodeExecutionMaxParallel
	}
	resolveCodeExecutionDefaults(&options, configTimeoutMs, configMaxToolCalls, configMaxParallel)

	// Extract session information from context
	var sessionID, clientName, clientVersion string
	if sess := mcpserver.ClientSessionFromContext(ctx); sess != nil {
		sessionID = sess.SessionID()
		if sessInfo := p.sessionStore.GetSession(sessionID); sessInfo != nil {
			clientName = sessInfo.ClientName
			clientVersion = sessInfo.ClientVersion
		}
	}

	// Generate parent call ID before execution
	executionStart := time.Now()
	parentCallID := mintCorrelationIDAt(executionStart, "code_execution")

	// The execution id IS the parent call id. jsruntime.Execute takes options
	// BY VALUE and only fills a missing ExecutionID on its own copy, so leaving
	// this unset left every downstream consumer of options.ExecutionID —
	// toolCaller.executionID, the parent history record's RequestID and the
	// nested history records' RequestID — with the empty string, which broke
	// exactly the correlation those fields exist for.
	options.ExecutionID = parentCallID

	// Config path for history records (empty when no authority was wired).
	configPath := p.activeConfigFilePath()

	// Create tool caller adapter that wraps the upstream manager
	toolCaller := &upstreamToolCaller{
		upstreamManager: p.upstreamManager,
		logger:          p.logger,
		executionID:     options.ExecutionID,
		storage:         p.storage,
		configPath:      configPath,
		parentCallID:    parentCallID,
		sessionID:       sessionID,
		clientName:      clientName,
		clientVersion:   clientVersion,
		mainServer:      p.mainServer,
		proxy:           p,
	}

	// Log pool metrics before acquisition
	if p.jsPool != nil {
		p.logger.Debug("pool metrics before acquisition",
			zap.String("execution_id", options.ExecutionID),
			zap.Int("pool_size", p.jsPool.Size()),
			zap.Int("available", p.jsPool.Available()),
			zap.Int("in_use", p.jsPool.Size()-p.jsPool.Available()),
		)
	}

	// Acquire a runtime instance from the pool (if pool is available)
	// This limits concurrent executions to the configured pool size
	acquireStart := time.Now()
	if p.jsPool != nil {
		vm, err := p.jsPool.Acquire(ctx)
		if err != nil {
			p.logger.Error("failed to acquire JavaScript runtime from pool",
				zap.String("execution_id", options.ExecutionID),
				zap.Error(err),
			)
			return mcp.NewToolResultError(fmt.Sprintf("Failed to acquire JavaScript runtime: %v", err)), nil
		}

		acquireDuration := time.Since(acquireStart)
		p.logger.Debug("acquired JavaScript runtime from pool",
			zap.String("execution_id", options.ExecutionID),
			zap.Duration("acquire_duration", acquireDuration),
			zap.Int("available_after", p.jsPool.Available()),
		)

		// Release the runtime back to the pool when done
		defer func() {
			releaseStart := time.Now()
			if releaseErr := p.jsPool.Release(vm); releaseErr != nil {
				p.logger.Warn("failed to release JavaScript runtime to pool",
					zap.String("execution_id", options.ExecutionID),
					zap.Error(releaseErr),
				)
			} else {
				p.logger.Debug("released JavaScript runtime to pool",
					zap.String("execution_id", options.ExecutionID),
					zap.Duration("release_duration", time.Since(releaseStart)),
					zap.Int("available_after", p.jsPool.Available()),
				)
			}
		}()
	}

	// Determine effective language for logging
	effectiveLanguage := options.Language
	if effectiveLanguage == "" {
		effectiveLanguage = "javascript"
	}

	// Inject auth context for permission enforcement (Spec 031)
	if authCtx := auth.AuthContextFromContext(ctx); authCtx != nil {
		options.AuthContext = &jsruntime.AuthInfo{
			Type:           authCtx.Type,
			AgentName:      authCtx.AgentName,
			AllowedServers: authCtx.AllowedServers,
			Permissions:    authCtx.Permissions,
		}
		// Provide tool annotation lookup function for permission tier resolution
		options.ToolAnnotationFunc = p.lookupToolPermission
	}

	// Spec 057 (Codex #621 finding 2): Intersect profile scope into code_execution.
	p.applyProfileScopeToExecution(ctx, &options)

	// Execute code
	p.logger.Info("executing code",
		zap.String("execution_id", options.ExecutionID),
		zap.String("language", effectiveLanguage),
		zap.String("script", scriptName), // empty for an inline call (Spec 097)
		zap.Int("code_length", len(code)),
		zap.Int("timeout_ms", options.TimeoutMs),
		zap.Int("max_tool_calls", options.MaxToolCalls),
		zap.Int("allowed_servers_count", len(options.AllowedServers)),
	)

	// Update execution start time to actual execution start
	executionStart = time.Now()
	result := jsruntime.Execute(ctx, toolCaller, code, options)
	executionDuration := time.Since(executionStart)

	// Log execution result with metrics
	if result.Ok {
		p.logger.Info("code execution succeeded",
			zap.String("execution_id", options.ExecutionID),
			zap.Duration("execution_duration", executionDuration),
			zap.Int("tool_calls_made", len(toolCaller.getToolCalls())),
		)
	} else {
		p.logger.Warn("code execution failed",
			zap.String("execution_id", options.ExecutionID),
			zap.Duration("execution_duration", executionDuration),
			zap.String("error_code", string(result.Error.Code)),
			zap.String("error_message", result.Error.Message),
			zap.Int("tool_calls_made", len(toolCaller.getToolCalls())),
		)
	}

	// Log detailed tool call metrics
	if len(toolCaller.getToolCalls()) > 0 {
		p.logger.Debug("tool call summary",
			zap.String("execution_id", options.ExecutionID),
			zap.Int("total_calls", len(toolCaller.getToolCalls())),
			zap.Any("tool_calls", toolCaller.getToolCalls()),
		)
	}

	// Calculate token metrics for the parent code_execution call
	var codeExecMetrics *storage.TokenMetrics
	if p.mainServer != nil && p.mainServer.runtime != nil {
		tokenizer := p.mainServer.runtime.Tokenizer()
		if tokenizer != nil {
			// Get model for token counting
			model := "gpt-4" // default
			if cfg := p.mainServer.runtime.Config(); cfg != nil && cfg.Tokenizer != nil && cfg.Tokenizer.DefaultModel != "" {
				model = cfg.Tokenizer.DefaultModel
			}

			// Count input tokens (code + input arguments)
			inputArgs := map[string]interface{}{
				"code":  code,
				"input": options.Input,
			}
			inputTokens, inputErr := tokenizer.CountTokensInJSONForModel(inputArgs, model)
			if inputErr != nil {
				p.logger.Debug("Failed to count input tokens for code_execution",
					zap.String("execution_id", options.ExecutionID),
					zap.Error(inputErr))
			}

			// Count output tokens (execution result)
			outputTokens := 0
			if result != nil {
				var outputErr error
				outputTokens, outputErr = tokenizer.CountTokensInJSONForModel(result, model)
				if outputErr != nil {
					p.logger.Debug("Failed to count output tokens for code_execution",
						zap.String("execution_id", options.ExecutionID),
						zap.Error(outputErr))
				}
			}

			// Get encoding from tokenizer
			encoding := "cl100k_base" // default
			if dt, ok := tokenizer.(interface{ GetDefaultEncoding() string }); ok {
				encoding = dt.GetDefaultEncoding()
			}

			// Create token metrics
			codeExecMetrics = &storage.TokenMetrics{
				InputTokens:  inputTokens,
				OutputTokens: outputTokens,
				TotalTokens:  inputTokens + outputTokens,
				Model:        model,
				Encoding:     encoding,
			}
		}
	}

	// Record the parent code_execution call in history
	codeExecRecord := &storage.ToolCallRecord{
		ID:               parentCallID,
		ServerID:         "code_execution", // Special server ID for built-in tool
		ServerName:       "mcpproxy",       // Built-in tool
		ToolName:         "code_execution",
		Arguments:        codeExecRecordArguments(code, scriptName, effectiveLanguage, options.Input),
		Response:         result,
		Duration:         int64(executionDuration),
		Timestamp:        executionStart,
		ConfigPath:       configPath,
		RequestID:        options.ExecutionID,
		ExecutionType:    "code_execution",
		MCPSessionID:     sessionID,
		MCPClientName:    clientName,
		MCPClientVersion: clientVersion,
		Metrics:          codeExecMetrics,
	}

	// Store parent call in history
	if err := p.storage.RecordToolCall(codeExecRecord); err != nil {
		p.logger.Warn("failed to record code_execution call in history",
			zap.String("execution_id", options.ExecutionID),
			zap.Error(err),
		)
	}

	// Update session stats for code_execution call
	if sessionID != "" && codeExecMetrics != nil {
		// Spec 082: code execution is real work — it earns the session a record,
		// and the record must exist before its stats are written.
		p.markSessionWorked(ctx, sessionID)
		p.sessionStore.UpdateSessionStats(sessionID, codeExecMetrics.TotalTokens)
	}

	// Convert result to MCP response format
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize result: %w", err)
	}

	// Spec 024: Emit internal tool call event for code_execution.
	//
	// This is the WRAPPER's own outcome and stays keyed on the JS runtime's
	// result: a script that called a tool, got an isError answer and handled it
	// has succeeded. The nested dispatches carry their own classification —
	// upstreamToolCaller.CallTool records an isError:true answer as a failed
	// tool call with the upstream's message (issue #935) — so a failure inside
	// the sandbox is visible in the tool-call history rather than being folded
	// into the wrapper.
	var status, errorMsg string
	if result.Ok {
		status = "success"
	} else {
		status = "error"
		if result.Error != nil {
			errorMsg = result.Error.Message
		}
	}
	codeExecArgs := codeExecRecordArguments(code, scriptName, effectiveLanguage, options.Input)

	// Spec 035: Determine content trust for code_execution based on tools called.
	// If any tool called within the JS sandbox has openWorldHint=true (or nil, default true),
	// the entire code_execution result is tagged as untrusted.
	codeExecContentTrust := ""
	toolCallRecords := toolCaller.getToolCalls()
	if len(toolCallRecords) > 0 {
		hasOpenWorldTool := false
		for _, tc := range toolCallRecords {
			toolAnnotations := p.lookupToolAnnotations(tc.ServerName, tc.ToolName)
			if contracts.IsOpenWorldTool(toolAnnotations) {
				hasOpenWorldTool = true
				break
			}
		}
		if hasOpenWorldTool {
			codeExecContentTrust = contracts.ContentTrustUntrusted
		} else {
			codeExecContentTrust = contracts.ContentTrustTrusted
		}
	}

	p.emitActivityInternalToolCall("code_execution", "", "", "", sessionID, parentCallID, status, errorMsg, executionDuration.Milliseconds(), codeExecArgs, result, nil, codeExecContentTrust)

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(string(resultJSON)),
		},
	}, nil
}

// codeExecutionSourceXORMessage explains the Spec 097 exactly-one-of rule.
// JSON Schema cannot express XOR, so the schema marks both parameters optional
// and the handler is the one place that enforces the rule — on every surface.
const codeExecutionSourceXORMessage = "Provide exactly one of 'code' (inline source) or 'script' (the name of a script stored in the 'scripts' directory next to mcpproxy's config file) — not both, not neither."

// codeExecStringArg reads an optional string argument, returning a user-facing
// message when the value is present but is not a string.
func codeExecStringArg(args map[string]interface{}, key string) (value, errMsg string) {
	raw, present := args[key]
	if !present || raw == nil {
		return "", ""
	}
	str, ok := raw.(string)
	if !ok {
		return "", fmt.Sprintf("Parameter '%s' must be a string", key)
	}
	return str, ""
}

// resolveCodeExecutionSource applies the exactly-one-of rule and returns the
// source to execute together with the stored-script name it came from (empty
// for an inline call). For a stored script the language is derived from the
// file extension and written back into options, so everything downstream —
// transpilation, logging, records — sees what actually ran.
func (p *MCPProxyServer) resolveCodeExecutionSource(ctx context.Context, args map[string]interface{}, options *jsruntime.ExecutionOptions) (code, scriptName, errMsg string) {
	code, errMsg = codeExecStringArg(args, "code")
	if errMsg != "" {
		return "", "", errMsg
	}
	scriptName, errMsg = codeExecStringArg(args, "script")
	if errMsg != "" {
		return "", "", errMsg
	}

	if (code == "") == (scriptName == "") {
		return "", "", codeExecutionSourceXORMessage
	}
	if scriptName == "" {
		return code, "", ""
	}

	source, language, err := codescripts.Resolve(p.scriptsDir(), scriptName, options.Language)
	if err != nil {
		// Keep the typed identity reachable for the REST surface (404 for a
		// name that is not there, 400 for one that cannot run) — the text alone
		// would force it to classify these by prose.
		recordCodeExecRefusal(ctx, err)
		return "", "", fmt.Sprintf("Cannot execute stored script: %v", err)
	}
	options.Language = language
	return string(source), scriptName, ""
}

// activeConfigFilePath returns the configuration FILE this server belongs to:
// the path declared at construction (WithConfigFilePath — every production
// surface passes it), else the running server's own resolution.
func (p *MCPProxyServer) activeConfigFilePath() string {
	if p.configFilePath != "" {
		return p.configFilePath
	}
	if p.mainServer != nil {
		return p.mainServer.GetConfigPath()
	}
	return ""
}

// scriptsDir resolves the stored-scripts directory (Spec 097 FR-001): the
// `scripts` directory beside the active config file. When no authority was
// declared at all, the data dir's default config path is the documented
// last-resort fallback — never a directory derived from --data-dir alone.
func (p *MCPProxyServer) scriptsDir() string {
	configFilePath := p.activeConfigFilePath()
	if configFilePath == "" && p.config != nil {
		configFilePath = config.GetConfigPath(p.config.DataDir)
	}
	return codescripts.DirFor(configFilePath)
}

// codeExecRecordArguments builds the argument payload recorded for a
// code_execution call. History and the activity event share it so they cannot
// disagree: both keep the EXECUTED SOURCE under "code" (Spec 024 parity) and,
// for a stored script, additionally name it.
func codeExecRecordArguments(code, scriptName, language string, input map[string]interface{}) map[string]interface{} {
	args := map[string]interface{}{
		"code":     code,
		"input":    input,
		"language": language,
	}
	if scriptName != "" {
		args["script"] = scriptName
	}
	return args
}

// applyCodeExecutionOptions parses the `options` object of a code_execution
// call into opts, returning a user-facing message when a value is out of range
// or the wrong type (empty string means the options were applied).
//
// Values arrive in two shapes: JSON-decoded over the MCP transport (float64,
// []interface{}) and Go-typed from an in-process caller that builds the
// arguments map directly — the REST handler behind POST /api/v1/code/exec is
// one. Both are accepted; matching only the JSON shapes dropped every
// restriction a REST caller set, so a request limited to specific servers ran
// unrestricted.
// codeExecMaxToolCallsUnset marks max_tool_calls as not supplied by the
// caller. Zero cannot be the sentinel: an explicit 0 is the documented
// unlimited override, distinct from "use the configured limit". Negative
// caller values are rejected during parsing, so the sentinel can never arrive
// from outside.
const codeExecMaxToolCallsUnset = -1

// resolveCodeExecutionDefaults fills config defaults for the options the
// caller left unset. timeout_ms uses zero as its unset marker (0 is out of
// range and rejected during parsing); max_tool_calls uses the sentinel so an
// explicit zero survives. max_parallel has no request-level option — it is
// the configured default for call_tools() batches, which a script overrides
// per batch inside the sandbox.
func resolveCodeExecutionDefaults(opts *jsruntime.ExecutionOptions, configTimeoutMs, configMaxToolCalls, configMaxParallel int) {
	if opts.TimeoutMs == 0 {
		opts.TimeoutMs = configTimeoutMs
	}
	if opts.MaxToolCalls == codeExecMaxToolCallsUnset {
		opts.MaxToolCalls = configMaxToolCalls
	}
	if opts.MaxParallel == 0 {
		opts.MaxParallel = configMaxParallel
	}
}

func applyCodeExecutionOptions(optionsObj map[string]interface{}, opts *jsruntime.ExecutionOptions) string {
	// Parse timeout_ms
	if raw, present := optionsObj["timeout_ms"]; present && raw != nil {
		timeoutMs, ok := codeExecOptionInt(raw)
		if !ok {
			// A fractional 1.9 silently becoming a 1ms budget is worse than an
			// error; same for non-numeric values.
			return "timeout_ms must be an integer"
		}
		opts.TimeoutMs = timeoutMs
		// Validate timeout range
		if opts.TimeoutMs < 1 || opts.TimeoutMs > 600000 {
			return "timeout_ms must be between 1 and 600000 milliseconds"
		}
	}

	// Parse max_tool_calls
	if raw, present := optionsObj["max_tool_calls"]; present && raw != nil {
		maxToolCalls, ok := codeExecOptionInt(raw)
		if !ok {
			// 0.5 truncated to 0 would flip a caller's limit into the
			// unlimited override.
			return "max_tool_calls must be an integer"
		}
		opts.MaxToolCalls = maxToolCalls
		// Validate max_tool_calls
		if opts.MaxToolCalls < 0 {
			return "max_tool_calls cannot be negative"
		}
	}

	// Parse allowed_servers
	switch allowedServers := optionsObj["allowed_servers"].(type) {
	case []string:
		opts.AllowedServers = append(make([]string, 0, len(allowedServers)), allowedServers...)
	case []interface{}:
		serverNames := make([]string, 0, len(allowedServers))
		for _, serverVal := range allowedServers {
			serverName, ok := serverVal.(string)
			if !ok {
				return "allowed_servers must be an array of strings"
			}
			serverNames = append(serverNames, serverName)
		}
		opts.AllowedServers = serverNames
	}

	return ""
}

// codeExecOptionInt normalises the numeric shapes a code_execution option can
// arrive in: float64 from a JSON decode, json.Number from a decoder using
// UseNumber, and the integer types an in-process caller passes through.
func codeExecOptionInt(value interface{}) (int, bool) {
	switch v := value.(type) {
	case float64:
		if v != math.Trunc(v) {
			return 0, false
		}
		return int(v), true
	case float32:
		f := float64(v)
		if f != math.Trunc(f) {
			return 0, false
		}
		return int(f), true
	case int:
		return v, true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case json.Number:
		parsed, err := v.Int64()
		if err != nil {
			return 0, false
		}
		return int(parsed), true
	default:
		return 0, false
	}
}

// toolCallRecord tracks information about a single tool call for observability
type toolCallRecord struct {
	ServerName string        `json:"server_name"`
	ToolName   string        `json:"tool_name"`
	StartTime  time.Time     `json:"start_time"`
	Duration   time.Duration `json:"duration"`
	Success    bool          `json:"success"`
	Error      string        `json:"error,omitempty"`
}

// upstreamToolCaller adapts the upstream.Manager to implement jsruntime.ToolCaller
type upstreamToolCaller struct {
	upstreamManager *upstream.Manager
	logger          *zap.Logger
	executionID     string
	storage         *storage.Manager
	configPath      string
	toolCalls       []toolCallRecord
	mu              sync.Mutex
	parentCallID    string  // ID of the parent code_execution call
	sessionID       string  // MCP session ID
	clientName      string  // MCP client name
	clientVersion   string  // MCP client version
	mainServer      *Server // Reference to main server for tokenizer access
	// proxy is the policy authority for the shared dispatch gates (Spec 098
	// FR-002). nil in unit tests that drive the caller directly, in which case
	// the gate is skipped exactly as it was before the consolidation.
	proxy *MCPProxyServer
}

// CallTool implements jsruntime.ToolCaller interface
func (u *upstreamToolCaller) CallTool(ctx context.Context, serverName, toolName string, args map[string]interface{}) (interface{}, error) {
	startTime := time.Now()

	// Spec 093 FR-012: a call issued by a sandboxed script is an INTERNAL origin,
	// whatever surface asked for the code_execution around it. Without this the
	// context still carries the outer MCP (or REST) source, so a shed inside a
	// script was attributed to the client that started the script rather than to
	// the script itself.
	ctx = reqcontext.WithRequestSource(ctx, reqcontext.SourceInternal)

	u.logger.Debug("calling upstream tool from JavaScript",
		zap.String("execution_id", u.executionID),
		zap.String("server", serverName),
		zap.String("tool", toolName),
	)

	// Spec 098 FR-002: the sandbox is a dispatch path like any other, so it
	// consumes the same shared gate primitive as call_tool_* and direct mode —
	// a script must not reach a quarantined, disabled, config-denied or
	// approval-locked tool that every other surface refuses. This covers both
	// script surfaces: ad-hoc code_execution and stored scripts (spec 097).
	//
	// It is deliberately FAIL-OPEN for an unknown server (no stored record):
	// dispatch has always been permissive about existence (FR-002 makes that
	// guarantee one-way), and tightening it here would break in-process fixtures
	// that register an upstream without a config record.
	if refusal := u.policyRefusal(serverName, toolName); refusal != nil {
		duration := time.Since(startTime)
		u.recordToolCall(serverName, toolName, startTime, duration, false, refusal.Error())
		u.storeToolCallInHistory(serverName, toolName, args, nil, refusal, startTime, duration)
		u.emitSubCallRefused(serverName, toolName, args, refusal, startTime, duration)
		return nil, refusal
	}

	// Get the managed client for the server
	client, exists := u.upstreamManager.GetClient(serverName)
	if !exists {
		err := fmt.Errorf("server not found: %s", serverName)
		duration := time.Since(startTime)
		u.recordToolCall(serverName, toolName, startTime, duration, false, err.Error())
		u.storeToolCallInHistory(serverName, toolName, args, nil, err, startTime, duration)
		u.emitSubCallActivity(serverName, toolName, args, nil, err, startTime, duration)
		return nil, err
	}

	// Call the tool
	result, err := client.CallTool(ctx, toolName, args)
	duration := time.Since(startTime)

	// Record the tool call with timing and result. Issue #935: code_execution
	// is a fourth upstream dispatch path, so it classifies an isError:true
	// answer as a failure exactly like call_tool_* does — otherwise the same
	// upstream rejection is a clean success here and an error there.
	u.recordUpstreamCall(serverName, toolName, startTime, duration, result, err)
	u.storeToolCallInHistory(serverName, toolName, args, result, err, startTime, duration)
	u.emitSubCallActivity(serverName, toolName, args, result, err, startTime, duration)

	u.logger.Debug("upstream tool call completed",
		zap.String("execution_id", u.executionID),
		zap.String("server", serverName),
		zap.String("tool", toolName),
		zap.Duration("duration", duration),
		zap.Bool("success", err == nil && !upstreamAnsweredWithError(result)),
	)

	if err != nil {
		return nil, err
	}

	return result, nil
}

// subCallActivityResponseLimit caps the response text recorded for ONE
// sandboxed sub-call. One execution can issue up to max_tool_calls of them, so
// the per-record budget is deliberately far smaller than the 64KB a single
// direct dispatch is allowed.
const subCallActivityResponseLimit = 8 * 1024

// emitSubCallActivity records one call issued from inside the JS sandbox as a
// first-class tool_call activity record.
//
// Before this, a sandboxed call reached the in-memory tool list and the legacy
// tool-call history and nothing else — so the activity log, which is what the
// tray glance, GET /api/v1/activity and the usage aggregate all read, showed
// the code_execution wrapper and none of the work it actually did. A script
// that made twenty upstream calls was one row.
//
// Every sub-call gets a FRESH request id and carries ParentID = the parent
// code_execution's correlation id, which makes navigation one query in each
// direction:
//
//	parent → children:  /api/v1/activity?parent_id=<parent request_id>
//	child  → parent:    /api/v1/activity?request_id=<child parent_id>
//
// The record is emitted on EVERY exit of CallTool — policy refusal, unknown
// server, and dispatch (success or failure) — because a call the sandbox was
// refused is exactly the kind of thing the transparency surfaces exist to show.
//
// Source is "internal" for the same reason the dispatch context is (Spec 093
// FR-012): the call was issued by the script, not by whoever started it. No
// `started` event is emitted: for a nested call it would arrive after the work
// already finished, and it would double the SSE traffic of a busy script for
// nothing — started events are never persisted anyway.
func (u *upstreamToolCaller) emitSubCallActivity(serverName, toolName string, args map[string]interface{}, result interface{}, callErr error, startTime time.Time, duration time.Duration) {
	// nil in the unit tests that drive the caller directly (see the field
	// comment on upstreamToolCaller.proxy) — there is no runtime to emit into.
	if u.proxy == nil {
		return
	}

	// A queue shed already produced its canonical "rejected" activity record
	// at the admission point inside the managed client (spec 093 FR-012,
	// installRejectionObserver) — that seam is origin-independent and fired
	// for this very call. Emitting a second record here would count one
	// refused attempt twice, once as rejected and once as an executed error.
	// Only queue_full/queue_timeout reach the observer (reportRejection);
	// server_unavailable is an ordinary error with no canonical record, so it
	// must fall through and be recorded here like any other failure.
	if shedHasCanonicalRecord(callErr) {
		return
	}

	status, errMsg, responseText, truncated := subCallActivityOutcome(result, callErr)

	requestID := mintCorrelationIDAt(startTime, serverName, toolName)
	requestBytes, responseBytes := subCallByteSizes(args, result)
	// CutShortenedRecordOnly, and note that this is a Type=tool_call record
	// whose cut runs the OPPOSITE way from every other tool_call's.
	//
	// A direct dispatch is cut on the way OUT at tool_response_limit, so its
	// record holds the agent's own copy. Nothing of the kind happened here: the
	// sandbox script received the WHOLE result — it is a Go value returned up
	// the stack, never forwarded through a truncator — and only the text this
	// record stores was cut, at subCallActivityResponseLimit, purely to bound
	// the log. The record is therefore a PREFIX of what was delivered, and
	// responseBytes (measured pre-cut, above) is the delivered size.
	//
	// Four review rounds inferred this record's direction from its type and got
	// it backwards. Nothing infers it now; this call site states it.
	responseCut := contracts.CutNone
	if truncated {
		responseCut = contracts.CutShortenedRecordOnly
	}
	u.proxy.emitActivityToolCallCompleted(
		serverName, toolName, u.sessionID, requestID, string(storage.ActivitySourceInternal),
		status, errMsg, duration.Milliseconds(), args, responseText, responseCut,
		"", nil, "", "", requestBytes, responseBytes, "", nil, u.parentCallID)
}

// subCallByteSizes returns the pre-truncation JSON byte lengths of a sandbox
// sub-call's arguments and result, the same way the top-level dispatch computes
// them (mcp.go, spec 069 A1).
//
// These were hardcoded to 0 until now, and that zero was not harmless: the
// convention throughout the activity log is that 0 bytes means UNKNOWN, not
// free. Every code-execution sub-call therefore had an unaccountable cost with
// bodies off, which is the gap bench records as ReasonSubCallZeroBytes — and it
// is exactly the population needed to measure what code execution actually
// saves, since the sub-call responses are the ones that never reach the model's
// context. Without these lengths that saving cannot be computed at all with
// bodies off.
//
// A nil result yields 0 response bytes. That is a TRUE zero rather than an
// unknown: the caller only reaches this with a nil result when the upstream
// never answered, and a call that produced no response contributed no response
// tokens.
// result is the untyped dispatch result, so a typed-nil pointer can arrive
// inside a non-nil interface; that marshals to "null" rather than nothing, and
// reflect is what tells the two apart.
func subCallByteSizes(args map[string]interface{}, result interface{}) (requestBytes, responseBytes int) {
	if result != nil {
		if rv := reflect.ValueOf(result); rv.Kind() == reflect.Ptr && rv.IsNil() {
			result = nil
		}
	}
	return rawByteSize(args), rawByteSize(result)
}

// emitSubCallRefused records a sandbox sub-call that the policy gate refused
// before dispatch (quarantined, disabled, approval-locked — spec 098 FR-002).
// Status is "blocked", not "error": the upstream never saw the call, and the
// aggregate routes blocked tool_calls off the executed-call statistics
// (Calls/latency) while still giving the attempt a failed bar in the timeline,
// the same treatment a direct-path policy_decision gets.
func (u *upstreamToolCaller) emitSubCallRefused(serverName, toolName string, args map[string]interface{}, refusal error, startTime time.Time, duration time.Duration) {
	if u.proxy == nil {
		return
	}
	requestID := mintCorrelationIDAt(startTime, serverName, toolName)
	u.proxy.emitActivityToolCallCompleted(
		serverName, toolName, u.sessionID, requestID, string(storage.ActivitySourceInternal),
		storage.ActivityStatusBlocked, refusal.Error(), duration.Milliseconds(), args, "", contracts.CutNone,
		// The policy gate refused this before dispatch, so there IS no response
		// and 0 response bytes is a true zero, not an unmeasured one. The
		// request was still formed and is measured like any other.
		"", nil, "", "", rawByteSize(args), 0, "", nil, u.parentCallID)
}

// shedHasCanonicalRecord reports whether callErr is a limiter shed the
// admission observer already recorded (managed.reportRejection forwards only
// queue_full/queue_timeout; server_unavailable travels the ordinary error path
// and has no canonical record).
func shedHasCanonicalRecord(callErr error) bool {
	var limitErr *limiter.LimitError
	return errors.As(callErr, &limitErr) &&
		(limitErr.Reason == limiter.ReasonQueueFull || limitErr.Reason == limiter.ReasonQueueTimeout)
}

// subCallActivityOutcome classifies one sandboxed sub-call for the activity log
// and produces the response text to record with it.
//
// Split out of emitSubCallActivity so the classification is testable without a
// runtime to emit into — and so the rule stays in ONE place: never hardcode a
// status. An upstream that ANSWERED isError:true has failed even though the
// transport hop succeeded (issue #935), and a Go error wins over the upstream's
// own text because a call that never completed has no upstream text worth
// keeping. This is the same precedence nestedCallFailure applies to the legacy
// history record, so the two cannot disagree about one call.
func subCallActivityOutcome(result interface{}, callErr error) (status, errMsg, response string, truncated bool) {
	status, errMsg = activityStatusForResult(result)
	if callErr != nil {
		return storage.ActivityStatusError, callErr.Error(), "", false
	}
	if result == nil {
		return status, errMsg, "", false
	}
	encoded, mErr := json.Marshal(result)
	if mErr != nil {
		return status, errMsg, "", false
	}
	response = string(encoded)
	if len(response) > subCallActivityResponseLimit {
		// safeTruncateBytes backs the cut up to a rune boundary, so the stored
		// text is always valid UTF-8.
		response = response[:safeTruncateBytes(response, subCallActivityResponseLimit)]
		truncated = true
	}
	return status, errMsg, response, truncated
}

// policyRefusal evaluates the shared per-tool policy gates for a sandboxed call
// and returns the refusal a script sees, or nil when the tool is callable.
func (u *upstreamToolCaller) policyRefusal(serverName, toolName string) error {
	if u.proxy == nil || u.proxy.storage == nil {
		return nil
	}
	gate := u.proxy.evaluateToolGate(serverName, toolName)
	if gate.serverConfig == nil {
		if gate.storageErr != nil {
			// The record exists as far as anyone knows — it just could not be
			// read. Fail-open is licensed for a server that is genuinely
			// UNKNOWN, not for one whose policy the proxy failed to load;
			// treating a BBolt failure as "unknown server" would let a script
			// through a quarantine gate by breaking the database.
			return fmt.Errorf("cannot verify policy for server %q: %w", serverName, gate.storageErr)
		}
		// Unknown server: leave the existing "server not found" path to answer.
		return nil
	}
	if gate.callable() {
		return nil
	}
	if gate.serverQuarantined() {
		return fmt.Errorf("server %q is quarantined for security review; its tools cannot be called until it is approved", serverName)
	}
	switch gate.lockStatus {
	case storage.ToolApprovalStatusPending:
		return fmt.Errorf("tool %s:%s is pending security approval and cannot be called", serverName, toolName)
	case storage.ToolApprovalStatusChanged:
		return fmt.Errorf("tool %s:%s changed since approval and is locked pending review", serverName, toolName)
	}
	return fmt.Errorf("%s", gate.blockedMessage())
}

// upstreamAnsweredWithError reports whether a dispatched result is an MCP
// answer flagged isError:true — a failure the upstream reported over a
// successful transport hop (issue #935).
func upstreamAnsweredWithError(result interface{}) bool {
	status, _ := activityStatusForResult(result)
	return status != "success"
}

// nestedCallFailure classifies one nested dispatch from the JS sandbox,
// returning (success, message). A Go error wins over the upstream's own text:
// when the call never completed, that is the useful explanation.
func nestedCallFailure(result interface{}, err error) (bool, string) {
	if err != nil {
		return false, err.Error()
	}
	status, msg := activityStatusForResult(result)
	if status == "success" {
		return true, ""
	}
	return false, msg
}

// recordUpstreamCall records a nested tool call with timing and its classified
// outcome (thread-safe).
func (u *upstreamToolCaller) recordUpstreamCall(serverName, toolName string, startTime time.Time, duration time.Duration, result interface{}, err error) {
	success, errMsg := nestedCallFailure(result, err)
	u.recordToolCall(serverName, toolName, startTime, duration, success, errMsg)
}

// recordToolCall records a tool call with timing and result information (thread-safe)
func (u *upstreamToolCaller) recordToolCall(serverName, toolName string, startTime time.Time, duration time.Duration, success bool, errMsg string) {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.toolCalls = append(u.toolCalls, toolCallRecord{
		ServerName: serverName,
		ToolName:   toolName,
		StartTime:  startTime,
		Duration:   duration,
		Success:    success,
		Error:      errMsg,
	})
}

// getToolCalls returns all recorded tool calls (thread-safe)
func (u *upstreamToolCaller) getToolCalls() []toolCallRecord {
	u.mu.Lock()
	defer u.mu.Unlock()

	// Return a copy to prevent external modification
	calls := make([]toolCallRecord, len(u.toolCalls))
	copy(calls, u.toolCalls)
	return calls
}

// storeToolCallInHistory stores a nested tool call in the database for history tracking
func (u *upstreamToolCaller) storeToolCallInHistory(serverName, toolName string, args map[string]interface{}, result interface{}, callErr error, startTime time.Time, duration time.Duration) {
	// Skip if storage is not available
	if u.storage == nil {
		return
	}

	// Get server config to generate server ID
	serverConfig, err := u.storage.GetUpstreamServer(serverName)
	if err != nil {
		u.logger.Warn("failed to get server config for history recording",
			zap.String("server", serverName),
			zap.String("execution_id", u.executionID),
			zap.Error(err),
		)
		return
	}

	// Calculate token metrics for the nested call
	var tokenMetrics *storage.TokenMetrics
	if u.mainServer != nil && u.mainServer.runtime != nil {
		tokenizer := u.mainServer.runtime.Tokenizer()
		if tokenizer != nil {
			// Get model for token counting
			model := "gpt-4" // default
			if cfg := u.mainServer.runtime.Config(); cfg != nil && cfg.Tokenizer != nil && cfg.Tokenizer.DefaultModel != "" {
				model = cfg.Tokenizer.DefaultModel
			}

			// Count input tokens (arguments)
			inputTokens, inputErr := tokenizer.CountTokensInJSONForModel(args, model)
			if inputErr != nil {
				u.logger.Debug("failed to count input tokens for nested call",
					zap.String("server", serverName),
					zap.String("tool", toolName),
					zap.Error(inputErr),
				)
			}

			// Count output tokens (if result is available and no error)
			outputTokens := 0
			if result != nil && callErr == nil {
				var outputErr error
				outputTokens, outputErr = tokenizer.CountTokensInJSONForModel(result, model)
				if outputErr != nil {
					u.logger.Debug("failed to count output tokens for nested call",
						zap.String("server", serverName),
						zap.String("tool", toolName),
						zap.Error(outputErr),
					)
				}
			}

			// Get encoding from tokenizer
			encoding := "cl100k_base" // default
			if dt, ok := tokenizer.(interface{ GetDefaultEncoding() string }); ok {
				encoding = dt.GetDefaultEncoding()
			}

			// Create token metrics
			tokenMetrics = &storage.TokenMetrics{
				InputTokens:  inputTokens,
				OutputTokens: outputTokens,
				TotalTokens:  inputTokens + outputTokens,
				Model:        model,
				Encoding:     encoding,
			}
		}
	}

	// Create tool call record for history
	record := &storage.ToolCallRecord{
		ID:               mintCorrelationIDAt(startTime, toolName),
		ServerID:         storage.GenerateServerID(serverConfig),
		ServerName:       serverName,
		ToolName:         toolName,
		Arguments:        args,
		Response:         result,
		Duration:         int64(duration),
		Timestamp:        startTime,
		ConfigPath:       u.configPath,
		RequestID:        u.executionID, // Use execution ID as request ID to link nested calls
		ParentCallID:     u.parentCallID,
		ExecutionType:    "code_execution",
		MCPSessionID:     u.sessionID,
		MCPClientName:    u.clientName,
		MCPClientVersion: u.clientVersion,
		Metrics:          tokenMetrics,
	}

	// Issue #935: an upstream that answered isError:true failed, even though the
	// transport hop did not. Without this the nested history row was clean while
	// the identical call through call_tool_read recorded the upstream's message.
	if _, errMsg := nestedCallFailure(result, callErr); errMsg != "" {
		record.Error = errMsg
	}

	// Store in database
	if err := u.storage.RecordToolCall(record); err != nil {
		u.logger.Warn("failed to store nested tool call in history",
			zap.String("server", serverName),
			zap.String("tool", toolName),
			zap.String("execution_id", u.executionID),
			zap.Error(err),
		)
	} else {
		u.logger.Debug("stored nested tool call in history",
			zap.String("server", serverName),
			zap.String("tool", toolName),
			zap.String("execution_id", u.executionID),
			zap.String("record_id", record.ID),
		)
	}
}

// applyProfileScopeToExecution intersects the request's ACTIVE profile into the
// sandbox's allow-list (Spec 057, Codex #621 finding 2).
//
// It resolves through resolveActiveProfile — token pin > /mcp/p/<slug> URL >
// session set_profile — rather than reading the URL-injected scope alone. The
// URL-only read was a scope hole: a profile-pinned agent token connected to the
// base /mcp endpoint carried no URL scope, so the sandbox ran under the token's
// full server scope and could call straight past its pin, including a stale pin
// that every other session path now answers deny-all.
//
// The jsruntime treats an empty AllowedServers as "allow all", so an active
// profile ALWAYS sets RestrictToAllowed: a deny-all profile, a stale pin, or a
// non-overlapping token∩profile must yield an empty allow-list that denies
// everything rather than leaking every server.
func (p *MCPProxyServer) applyProfileScopeToExecution(ctx context.Context, options *jsruntime.ExecutionOptions) {
	if options == nil {
		return
	}
	_, profileScope := p.resolveActiveProfile(ctx)
	if profileScope == nil {
		return
	}

	options.RestrictToAllowed = true
	profileServers := profileScope.AllowedServerNames()
	if len(options.AllowedServers) == 0 {
		// No caller-supplied restriction: the profile is the restriction.
		// AllowedServerNames returns a non-nil empty slice for a deny-all
		// scope, which RestrictToAllowed then enforces as "nothing".
		options.AllowedServers = profileServers
		return
	}

	// Intersect the caller-supplied list with the profile's servers.
	profileSet := make(map[string]struct{}, len(profileServers))
	for _, s := range profileServers {
		profileSet[s] = struct{}{}
	}
	intersected := make([]string, 0, len(options.AllowedServers))
	for _, s := range options.AllowedServers {
		if _, ok := profileSet[s]; ok {
			intersected = append(intersected, s)
		}
	}
	options.AllowedServers = intersected
}

// lookupToolPermission returns the required permission tier for a tool based on its annotations.
// This is used by the JS runtime to enforce auth context permissions during code_execution.
func (p *MCPProxyServer) lookupToolPermission(serverName, toolName string) string {
	// Primary: exact match via StateView (no BM25 fuzzy matching)
	annotations := p.lookupToolAnnotations(serverName, toolName)
	if annotations != nil {
		callWith := contracts.DeriveCallWith(annotations)
		perm := contracts.ToolVariantToOperationType[callWith]
		if perm != "" {
			return perm
		}
	}

	// Fallback: search the index with enough candidates to find an exact match
	if p.index != nil {
		qualifiedName := serverName + ":" + toolName
		results, err := p.index.Search(qualifiedName, 20)
		if err == nil {
			for _, r := range results {
				if r.Tool != nil && r.Tool.ServerName == serverName && r.Tool.Name == toolName {
					callWith := contracts.DeriveCallWith(r.Tool.Annotations)
					perm := contracts.ToolVariantToOperationType[callWith]
					if perm != "" {
						return perm
					}
				}
			}
		}
	}

	// Default to read (safest)
	return contracts.OperationTypeRead
}
