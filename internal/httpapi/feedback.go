package httpapi

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// FeedbackSubmitter is the interface needed for feedback submission.
// This decouples the HTTP handler from the telemetry package.
type FeedbackSubmitter interface {
	SubmitFeedback(ctx context.Context, req *telemetry.FeedbackRequest) (*telemetry.FeedbackResponse, error)
}

// handleFeedback processes POST /api/v1/feedback requests.
//
// @Summary Submit feedback
// @Description Submit a bug report, feature request, or general feedback
// @Tags feedback
// @Accept json
// @Produce json
// @Param body body telemetry.FeedbackRequest true "Feedback request"
// @Success 200 {object} telemetry.FeedbackResponse
// @Failure 400 {object} map[string]string
// @Failure 429 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security ApiKeyAuth
// @Router /api/v1/feedback [post]
func (s *Server) handleFeedback(w http.ResponseWriter, r *http.Request) {
	if s.feedbackSubmitter == nil {
		s.writeError(w, r, http.StatusServiceUnavailable, "Feedback service not available")
		return
	}

	var req telemetry.FeedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "Invalid request body: "+err.Error())
		return
	}

	// Validate category
	if !telemetry.ValidateCategory(req.Category) {
		s.writeError(w, r, http.StatusBadRequest, "Invalid category: must be bug, feature, or other")
		return
	}

	// Validate message
	if err := telemetry.ValidateMessage(req.Message); err != nil {
		s.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := s.feedbackSubmitter.SubmitFeedback(r.Context(), &req)
	if err != nil {
		// Check if rate limited
		if err.Error() == "rate limit exceeded: maximum 5 feedback submissions per hour" {
			s.writeError(w, r, http.StatusTooManyRequests, err.Error())
			return
		}
		s.logger.Errorf("Failed to submit feedback: %v", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to submit feedback")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}
