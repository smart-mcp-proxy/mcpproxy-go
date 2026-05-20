package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/flow"
)

// handleHookEvaluate godoc
// @Summary Evaluate tool call for data flow security
// @Description Evaluates a tool call from an agent hook for data flow security analysis. Classifies the tool, tracks data origins, detects flow patterns, and returns a policy decision (allow/warn/ask/deny).
// @Tags hooks
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param request body flow.HookEvaluateRequest true "Hook evaluation request"
// @Success 200 {object} flow.HookEvaluateResponse "Hook evaluation result"
// @Failure 400 {object} contracts.ErrorResponse "Bad request - missing required fields"
// @Failure 401 {object} contracts.ErrorResponse "Unauthorized - missing or invalid API key"
// @Failure 500 {object} contracts.ErrorResponse "Hook evaluation failed"
// @Router /api/v1/hooks/evaluate [post]
func (s *Server) handleHookEvaluate(w http.ResponseWriter, r *http.Request) {
	var req flow.HookEvaluateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// Validate required fields
	if req.Event == "" {
		s.writeError(w, r, http.StatusBadRequest, "missing required field: event")
		return
	}
	if req.SessionID == "" {
		s.writeError(w, r, http.StatusBadRequest, "missing required field: session_id")
		return
	}
	if req.ToolName == "" {
		s.writeError(w, r, http.StatusBadRequest, "missing required field: tool_name")
		return
	}

	resp, err := s.controller.EvaluateHook(r.Context(), &req)
	if err != nil {
		logger := s.getRequestLogger(r)
		logger.Errorw("Hook evaluation failed",
			"event", req.Event,
			"tool_name", req.ToolName,
			"session_id", req.SessionID,
			"error", err,
		)
		s.writeError(w, r, http.StatusInternalServerError, "hook evaluation failed: "+err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, resp)
}
