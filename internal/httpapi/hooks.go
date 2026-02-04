package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/flow"
)

// handleHookEvaluate handles POST /api/v1/hooks/evaluate
// This endpoint evaluates tool calls from agent hooks for data flow security.
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
