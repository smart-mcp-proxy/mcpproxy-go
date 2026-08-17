package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/codescripts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
)

const (
	// minCodeExecTimeoutMS and maxCodeExecTimeoutMS mirror the bounds the
	// code_execution tool enforces on timeout_ms.
	minCodeExecTimeoutMS = 1
	maxCodeExecTimeoutMS = 600000
)

// CodeExecRequest represents the request body for code execution.
type CodeExecRequest struct {
	Code string `json:"code"`
	// Script names a server-side stored script to run instead of Code
	// (Spec 097). Exactly one of Code or Script may be set; the value is a
	// bare name, never a path, and only the code_execution tool resolves it.
	Script   string                 `json:"script,omitempty"`
	Language string                 `json:"language,omitempty"` // "javascript" (default) or "typescript"
	Input    map[string]interface{} `json:"input"`
	Options  CodeExecOptions        `json:"options"`
}

// CodeExecOptions represents execution options.
//
// Every field is a pointer so the handler can tell an option the caller sent
// from one they left out. Inferring that from the zero value conflated the
// two: an explicit "timeout_ms": 0 (out of range, and rejected as such over
// MCP) was read as "unset" and silently replaced by the configured budget,
// while an explicit "max_tool_calls": 0 or "allowed_servers": [] was dropped
// instead of reaching the tool as the caller wrote it.
type CodeExecOptions struct {
	TimeoutMS      *int      `json:"timeout_ms,omitempty"`
	MaxToolCalls   *int      `json:"max_tool_calls,omitempty"`
	AllowedServers *[]string `json:"allowed_servers,omitempty"`
}

// CodeExecResponse represents the response format.
type CodeExecResponse struct {
	OK        bool                   `json:"ok"`
	Result    interface{}            `json:"result,omitempty"`
	Error     *CodeExecError         `json:"error,omitempty"`
	Stats     map[string]interface{} `json:"stats,omitempty"`
	RequestID string                 `json:"request_id,omitempty"` // T016: Added for error correlation
}

// CodeExecError represents execution error details.
type CodeExecError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

// ToolCaller interface for calling tools (subset of ServerController).
type ToolCaller interface {
	CallTool(ctx context.Context, toolName string, arguments map[string]interface{}) (interface{}, error)
}

// CodeExecHandler handles POST /api/v1/code/exec requests.
type CodeExecHandler struct {
	toolCaller ToolCaller
	logger     *zap.SugaredLogger
}

// NewCodeExecHandler creates a new code execution handler.
func NewCodeExecHandler(toolCaller ToolCaller, logger *zap.SugaredLogger) *CodeExecHandler {
	return &CodeExecHandler{
		toolCaller: toolCaller,
		logger:     logger,
	}
}

func (h *CodeExecHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Parse request body
	var req CodeExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Invalid JSON request body")
		return
	}

	// Exactly one source: inline code, or the name of a stored script the tool
	// resolves (Spec 097). JSON Schema cannot express the XOR and neither can
	// this struct, so the rule is checked here — before dispatch — to answer a
	// malformed request as a 400 rather than as a tool error.
	if (req.Code == "") == (req.Script == "") {
		h.writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST",
			"Provide exactly one of 'code' (inline source) or 'script' (the name of a stored script)")
		return
	}

	// Validate language if provided
	if req.Language != "" && req.Language != "javascript" && req.Language != "typescript" {
		h.writeError(w, r, http.StatusBadRequest, "INVALID_LANGUAGE",
			fmt.Sprintf("Unsupported language %q. Supported languages: javascript, typescript", req.Language))
		return
	}

	// Validate the options the caller actually sent against the same bounds the
	// code_execution tool enforces. The tool reports a violation as a plain-text
	// tool error rather than the JSON envelope parseResult expects, so catching
	// it here keeps the caller's answer a proper 400 instead of a parse failure.
	if req.Options.TimeoutMS != nil &&
		(*req.Options.TimeoutMS < minCodeExecTimeoutMS || *req.Options.TimeoutMS > maxCodeExecTimeoutMS) {
		h.writeError(w, r, http.StatusBadRequest, "INVALID_OPTIONS",
			fmt.Sprintf("timeout_ms must be between %d and %d milliseconds", minCodeExecTimeoutMS, maxCodeExecTimeoutMS))
		return
	}
	if req.Options.MaxToolCalls != nil && *req.Options.MaxToolCalls < 0 {
		h.writeError(w, r, http.StatusBadRequest, "INVALID_OPTIONS", "max_tool_calls cannot be negative")
		return
	}

	// Set defaults
	if req.Input == nil {
		req.Input = make(map[string]interface{})
	}

	// Create context with timeout. This bounds the HTTP request; the
	// code_execution tool applies its own precise deadline inside it.
	//
	// A caller-named timeout_ms bounds the request exactly. Without one the
	// tool resolves the budget from the configured code_execution_timeout_ms,
	// which may legally be anything up to maxCodeExecTimeoutMS — and a context
	// only ever shrinks against its parent, so the parent has to cover that
	// ceiling. A narrower fallback here silently cancelled every configured
	// budget above it. The route's own deadline (codeExecRequestTimeout, which
	// adds IO slack on top of the ceiling) still bounds the request overall.
	timeoutMS := maxCodeExecTimeoutMS
	if req.Options.TimeoutMS != nil {
		timeoutMS = *req.Options.TimeoutMS
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()

	// Build arguments for code_execution tool. Exactly the options the caller
	// sent are forwarded, zero values included: to the tool an ABSENT option
	// means "resolve it" — timeout_ms and max_tool_calls come from config, and
	// a missing allowed_servers is read as "no restriction" — while a present
	// one is the caller's own choice. Sending this endpoint's request-level
	// fallback would override the configured code_execution_timeout_ms instead.
	execOptions := make(map[string]interface{}, 3)
	if req.Options.TimeoutMS != nil {
		execOptions["timeout_ms"] = *req.Options.TimeoutMS
	}
	if req.Options.MaxToolCalls != nil {
		execOptions["max_tool_calls"] = *req.Options.MaxToolCalls
	}
	if req.Options.AllowedServers != nil {
		execOptions["allowed_servers"] = *req.Options.AllowedServers
	}
	args := map[string]interface{}{
		"input":   req.Input,
		"options": execOptions,
	}
	// Forward whichever source the caller named — for a stored script that is
	// the NAME alone, so the tool stays the only execution-time resolver.
	if req.Script != "" {
		args["script"] = req.Script
	} else {
		args["code"] = req.Code
	}

	// Pass language if specified
	if req.Language != "" {
		args["language"] = req.Language
	}

	// Call the code_execution built-in tool
	result, err := h.toolCaller.CallTool(ctx, "code_execution", args)
	if err != nil {
		// A refusal the caller could have avoided is not a server fault. Naming
		// a script that does not exist is the documented discovery path, and a
		// mistyped or ambiguous name is a caller mistake; answered as 500 they
		// look retryable to an agent's retry policy and count as server errors
		// in monitoring. The tool's own explanation is what travels, since that
		// text is how the caller recovers.
		if status, code, message, ok := classifyCodeExecError(err); ok {
			h.logger.Debugw("Code execution refused", "status", status, "code", code, "error", err)
			h.writeError(w, r, status, code, message)
			return
		}
		h.logger.Errorw("Code execution failed", "error", err)
		h.writeError(w, r, http.StatusInternalServerError, "EXECUTION_FAILED", err.Error())
		return
	}

	// Debug: log the result type and value
	h.logger.Debugw("Received result from CallTool",
		"result_type", fmt.Sprintf("%T", result),
		"result_value", result)

	// Parse result (code_execution tool returns map[string]interface{})
	response := h.parseResult(result)

	// Write JSON response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// classifyCodeExecError maps a code_execution dispatch failure onto an HTTP
// status. ok=false means the failure is a genuine server-side fault and keeps
// the 500 EXECUTION_FAILED answer.
//
// The MCP contract makes the tool handler answer these with an isError RESULT,
// never a transport error, so the dispatch layer flattens them to a string on
// the way out — which would leave nothing here but prefix matching. It instead
// preserves the typed identity through errors.As/errors.Is (the same technique
// spec 093 uses for a concurrency shed's 429), so the returned message is the
// error's own wording, without the dispatch wrapper's "tool call failed:".
func classifyCodeExecError(err error) (status int, code, message string, ok bool) {
	if errors.Is(err, config.ErrCodeExecutionDisabled) {
		return http.StatusForbidden, "FEATURE_DISABLED", config.CodeExecutionDisabledMessage, true
	}

	var notFound *codescripts.NotFoundError
	if errors.As(err, &notFound) {
		// 404 rather than 400: the request is well formed, the script is not
		// there — and the message carries the available names (FR-004).
		return http.StatusNotFound, "SCRIPT_NOT_FOUND", notFound.Error(), true
	}

	var invalidName *codescripts.InvalidNameError
	if errors.As(err, &invalidName) {
		return http.StatusBadRequest, "INVALID_SCRIPT_NAME", invalidName.Error(), true
	}

	var mismatch *codescripts.LanguageMismatchError
	if errors.As(err, &mismatch) {
		// Same class as the handler's own pre-dispatch language check above.
		return http.StatusBadRequest, "INVALID_LANGUAGE", mismatch.Error(), true
	}

	var ambiguous *codescripts.AmbiguousError
	if errors.As(err, &ambiguous) {
		return http.StatusBadRequest, "SCRIPT_UNUSABLE", ambiguous.Error(), true
	}

	var invalid *codescripts.InvalidError
	if errors.As(err, &invalid) {
		// Empty, oversized, unreadable or non-regular: the named script exists
		// but cannot run. Nothing the daemon can do about it, and retrying the
		// same name will not help until the file is fixed.
		return http.StatusBadRequest, "SCRIPT_UNUSABLE", invalid.Error(), true
	}

	return 0, "", "", false
}

func (h *CodeExecHandler) parseResult(result interface{}) CodeExecResponse {
	// Result from CallTool is []mcp.Content (Content array directly)
	var textJSON string

	// Debug: log the exact type and value
	h.logger.Debugw("parseResult called",
		"result_type", fmt.Sprintf("%T", result),
		"result_value", result)

	// Use reflection to check if it's a slice
	rv := reflect.ValueOf(result)
	if rv.Kind() == reflect.Slice {
		h.logger.Debugw("Detected slice type",
			"kind", rv.Kind(),
			"length", rv.Len(),
			"elem_type", rv.Type().Elem())

		if rv.Len() == 0 {
			return CodeExecResponse{
				OK: false,
				Error: &CodeExecError{
					Message: "Empty content array",
					Code:    "INTERNAL_ERROR",
				},
			}
		}

		// Get first element
		firstElem := rv.Index(0).Interface()
		h.logger.Debugw("First element",
			"type", fmt.Sprintf("%T", firstElem),
			"value", firstElem)

		// Try to convert to map
		firstMap, ok := firstElem.(map[string]interface{})
		if !ok {
			// Try to marshal and unmarshal to convert struct to map
			jsonBytes, err := json.Marshal(firstElem)
			if err != nil {
				return CodeExecResponse{
					OK: false,
					Error: &CodeExecError{
						Message: fmt.Sprintf("Failed to marshal first element: %v", err),
						Code:    "INTERNAL_ERROR",
					},
				}
			}
			if err := json.Unmarshal(jsonBytes, &firstMap); err != nil {
				return CodeExecResponse{
					OK: false,
					Error: &CodeExecError{
						Message: fmt.Sprintf("Failed to unmarshal first element: %v", err),
						Code:    "INTERNAL_ERROR",
					},
				}
			}
		}

		// Extract text from content
		text, ok := firstMap["text"].(string)
		if !ok {
			return CodeExecResponse{
				OK: false,
				Error: &CodeExecError{
					Message: "Content text field missing or not string",
					Code:    "INTERNAL_ERROR",
				},
			}
		}
		textJSON = text
	} else if contentArray, ok := result.([]interface{}); ok {
		h.logger.Debugw("Successfully type asserted as []interface{}", "length", len(contentArray))
		if len(contentArray) == 0 {
			return CodeExecResponse{
				OK: false,
				Error: &CodeExecError{
					Message: "Empty content array",
					Code:    "INTERNAL_ERROR",
				},
			}
		}

		// Get first content item
		firstContent, ok := contentArray[0].(map[string]interface{})
		if !ok {
			return CodeExecResponse{
				OK: false,
				Error: &CodeExecError{
					Message: "Content item is not a map",
					Code:    "INTERNAL_ERROR",
				},
			}
		}

		// Extract text from content
		text, ok := firstContent["text"].(string)
		if !ok {
			return CodeExecResponse{
				OK: false,
				Error: &CodeExecError{
					Message: "Content text field missing or not string",
					Code:    "INTERNAL_ERROR",
				},
			}
		}
		textJSON = text
	} else {
		h.logger.Debugw("Type assertion as []interface{} failed, trying map format")
		// Fallback: try as map with content field
		resultMap, ok := result.(map[string]interface{})
		if !ok {
			return CodeExecResponse{
				OK: false,
				Error: &CodeExecError{
					Message: fmt.Sprintf("Unexpected result format: %T", result),
					Code:    "INTERNAL_ERROR",
				},
			}
		}

		// Extract content array from MCP response
		content, hasContent := resultMap["content"].([]interface{})
		if !hasContent || len(content) == 0 {
			return CodeExecResponse{
				OK: false,
				Error: &CodeExecError{
					Message: "Result missing 'content' array",
					Code:    "INTERNAL_ERROR",
				},
			}
		}

		// Get first content item (text)
		firstContent, ok := content[0].(map[string]interface{})
		if !ok {
			return CodeExecResponse{
				OK: false,
				Error: &CodeExecError{
					Message: "Content item is not a map",
					Code:    "INTERNAL_ERROR",
				},
			}
		}

		// Extract text from content
		text, ok := firstContent["text"].(string)
		if !ok {
			return CodeExecResponse{
				OK: false,
				Error: &CodeExecError{
					Message: "Content text field missing or not string",
					Code:    "INTERNAL_ERROR",
				},
			}
		}
		textJSON = text
	}

	// Parse the JSON text into execution result
	var execResult map[string]interface{}
	if err := json.Unmarshal([]byte(textJSON), &execResult); err != nil {
		return CodeExecResponse{
			OK: false,
			Error: &CodeExecError{
				Message: "Failed to parse execution result JSON: " + err.Error(),
				Code:    "INTERNAL_ERROR",
			},
		}
	}

	// Check if execution succeeded
	okValue, exists := execResult["ok"]
	if !exists {
		return CodeExecResponse{
			OK: false,
			Error: &CodeExecError{
				Message: "Result missing 'ok' field",
				Code:    "INTERNAL_ERROR",
			},
		}
	}

	okBool, isBool := okValue.(bool)
	if !isBool {
		return CodeExecResponse{
			OK: false,
			Error: &CodeExecError{
				Message: "Result 'ok' field is not boolean",
				Code:    "INTERNAL_ERROR",
			},
		}
	}

	if okBool {
		return CodeExecResponse{
			OK:     true,
			Result: execResult["value"],
			Stats:  extractStats(execResult),
		}
	}

	// Execution failed
	return CodeExecResponse{
		OK: false,
		Error: &CodeExecError{
			Message: extractErrorMessage(execResult),
			Code:    extractErrorCode(execResult),
		},
	}
}

// T016: Updated to include request_id in error responses
func (h *CodeExecHandler) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	requestID := reqcontext.GetRequestID(r.Context())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	response := CodeExecResponse{
		OK: false,
		Error: &CodeExecError{
			Code:    code,
			Message: message,
		},
		RequestID: requestID,
	}
	json.NewEncoder(w).Encode(response)
}

func extractStats(result map[string]interface{}) map[string]interface{} {
	if stats, ok := result["stats"].(map[string]interface{}); ok {
		return stats
	}
	return nil
}

func extractErrorMessage(result map[string]interface{}) string {
	if err, ok := result["error"].(map[string]interface{}); ok {
		if msg, ok := err["message"].(string); ok {
			return msg
		}
	}
	return "Unknown error"
}

func extractErrorCode(result map[string]interface{}) string {
	if err, ok := result["error"].(map[string]interface{}); ok {
		if code, ok := err["code"].(string); ok {
			return code
		}
	}
	return "UNKNOWN_ERROR"
}
