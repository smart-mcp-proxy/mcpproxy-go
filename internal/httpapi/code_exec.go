package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
)

const (
	// defaultCodeExecTimeoutMS bounds the HTTP request when the caller sent no
	// timeout_ms. It is only a request-level fallback — the execution's own
	// budget comes from the configured code_execution_timeout_ms.
	defaultCodeExecTimeoutMS = 120000
	// maxCodeExecTimeoutMS mirrors the ceiling the code_execution tool enforces
	// on timeout_ms.
	maxCodeExecTimeoutMS = 600000
)

// CodeExecRequest represents the request body for code execution.
type CodeExecRequest struct {
	Code     string                 `json:"code"`
	Language string                 `json:"language,omitempty"` // "javascript" (default) or "typescript"
	Input    map[string]interface{} `json:"input"`
	Options  CodeExecOptions        `json:"options"`
}

// CodeExecOptions represents execution options.
type CodeExecOptions struct {
	TimeoutMS      int      `json:"timeout_ms"`
	MaxToolCalls   int      `json:"max_tool_calls"`
	AllowedServers []string `json:"allowed_servers"`
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

	// Validate required fields
	if req.Code == "" {
		h.writeError(w, r, http.StatusBadRequest, "MISSING_CODE", "Code field is required")
		return
	}

	// Validate language if provided
	if req.Language != "" && req.Language != "javascript" && req.Language != "typescript" {
		h.writeError(w, r, http.StatusBadRequest, "INVALID_LANGUAGE",
			fmt.Sprintf("Unsupported language %q. Supported languages: javascript, typescript", req.Language))
		return
	}

	// Validate execution options against the same bounds the code_execution
	// tool enforces. The tool reports a violation as a plain-text tool error
	// rather than the JSON envelope parseResult expects, so catching it here
	// keeps the caller's answer a proper 400 instead of a parse failure.
	if req.Options.TimeoutMS < 0 || req.Options.TimeoutMS > maxCodeExecTimeoutMS {
		h.writeError(w, r, http.StatusBadRequest, "INVALID_OPTIONS",
			fmt.Sprintf("timeout_ms must be between 1 and %d milliseconds", maxCodeExecTimeoutMS))
		return
	}
	if req.Options.MaxToolCalls < 0 {
		h.writeError(w, r, http.StatusBadRequest, "INVALID_OPTIONS", "max_tool_calls cannot be negative")
		return
	}

	// Set defaults
	if req.Input == nil {
		req.Input = make(map[string]interface{})
	}

	// Create context with timeout. This bounds the HTTP request; the
	// code_execution tool applies its own deadline from timeout_ms inside it.
	timeoutMS := req.Options.TimeoutMS
	if timeoutMS == 0 {
		timeoutMS = defaultCodeExecTimeoutMS
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()

	// Build arguments for code_execution tool. Only options the caller actually
	// supplied are forwarded: a zero value means "unset" to the tool, which
	// resolves timeout_ms and max_tool_calls from config and reads an absent
	// allowed_servers as "no restriction". Sending this endpoint's own fallback
	// would override the configured code_execution_timeout_ms instead.
	execOptions := make(map[string]interface{}, 3)
	if req.Options.TimeoutMS > 0 {
		execOptions["timeout_ms"] = req.Options.TimeoutMS
	}
	if req.Options.MaxToolCalls > 0 {
		execOptions["max_tool_calls"] = req.Options.MaxToolCalls
	}
	if len(req.Options.AllowedServers) > 0 {
		execOptions["allowed_servers"] = req.Options.AllowedServers
	}
	args := map[string]interface{}{
		"code":    req.Code,
		"input":   req.Input,
		"options": execOptions,
	}

	// Pass language if specified
	if req.Language != "" {
		args["language"] = req.Language
	}

	// Call the code_execution built-in tool
	result, err := h.toolCaller.CallTool(ctx, "code_execution", args)
	if err != nil {
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
