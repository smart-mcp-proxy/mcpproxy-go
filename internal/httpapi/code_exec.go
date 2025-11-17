package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// CodeExecRequest represents the request body for code execution.
type CodeExecRequest struct {
	Code    string                 `json:"code"`
	Input   map[string]interface{} `json:"input"`
	Options CodeExecOptions        `json:"options"`
}

// CodeExecOptions represents execution options.
type CodeExecOptions struct {
	TimeoutMS      int      `json:"timeout_ms"`
	MaxToolCalls   int      `json:"max_tool_calls"`
	AllowedServers []string `json:"allowed_servers"`
}

// CodeExecResponse represents the response format.
type CodeExecResponse struct {
	OK     bool                   `json:"ok"`
	Result interface{}            `json:"result,omitempty"`
	Error  *CodeExecError         `json:"error,omitempty"`
	Stats  map[string]interface{} `json:"stats,omitempty"`
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
		h.writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Invalid JSON request body")
		return
	}

	// Validate required fields
	if req.Code == "" {
		h.writeError(w, http.StatusBadRequest, "MISSING_CODE", "Code field is required")
		return
	}

	// Set defaults
	if req.Input == nil {
		req.Input = make(map[string]interface{})
	}
	if req.Options.TimeoutMS == 0 {
		req.Options.TimeoutMS = 120000 // 2 minutes default
	}

	// Create context with timeout
	timeout := time.Duration(req.Options.TimeoutMS) * time.Millisecond
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	// Build arguments for code_execution tool
	args := map[string]interface{}{
		"code":  req.Code,
		"input": req.Input,
		"options": map[string]interface{}{
			"timeout_ms":      req.Options.TimeoutMS,
			"max_tool_calls":  req.Options.MaxToolCalls,
			"allowed_servers": req.Options.AllowedServers,
		},
	}

	// Call the code_execution built-in tool
	result, err := h.toolCaller.CallTool(ctx, "code_execution", args)
	if err != nil {
		h.logger.Errorw("Code execution failed", "error", err)
		h.writeError(w, http.StatusInternalServerError, "EXECUTION_FAILED", err.Error())
		return
	}

	// Parse result (code_execution tool returns map[string]interface{})
	response := h.parseResult(result)

	// Write JSON response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func (h *CodeExecHandler) parseResult(result interface{}) CodeExecResponse {
	// code_execution tool returns map[string]interface{} directly
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		return CodeExecResponse{
			OK: false,
			Error: &CodeExecError{
				Message: "Unexpected result format",
				Code:    "INTERNAL_ERROR",
			},
		}
	}

	// Check if execution succeeded
	okValue, exists := resultMap["ok"]
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
			Result: resultMap["result"],
			Stats:  extractStats(resultMap),
		}
	}

	// Execution failed
	return CodeExecResponse{
		OK: false,
		Error: &CodeExecError{
			Message: extractErrorMessage(resultMap),
			Code:    extractErrorCode(resultMap),
		},
	}
}

func (h *CodeExecHandler) writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	response := CodeExecResponse{
		OK: false,
		Error: &CodeExecError{
			Code:    code,
			Message: message,
		},
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
