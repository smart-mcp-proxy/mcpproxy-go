package httpapi

import (
	"errors"
	"net/http"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolreview"
)

// StaleToolsDetail lists why a review-bound approval was refused (Spec 117
// FR-008). Every list is always present, possibly empty.
type StaleToolsDetail struct {
	Changed       []string `json:"changed"`
	Unreviewed    []string `json:"unreviewed"`
	MissingBlocks []string `json:"missing_blocks"`
}

// StaleReviewErrorResponse is the HTTP 409 body of an approval bound to an
// out-of-date review (Spec 117 PR-2, #1566). The error text contains "out of
// date" and never "dangerous", so clients reload the review instead of
// offering a force approval.
type StaleReviewErrorResponse struct {
	Success    bool             `json:"success"`
	Error      string           `json:"error"`
	ErrorCode  string           `json:"error_code"`
	StaleTools StaleToolsDetail `json:"stale_tools"`
	RequestID  string           `json:"request_id,omitempty"`
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// writeStaleReview writes the 409 out-of-date response when err is (or wraps)
// a *toolreview.StaleToolReviewError and reports whether it did.
func (s *Server) writeStaleReview(w http.ResponseWriter, r *http.Request, err error) bool {
	var stale *toolreview.StaleToolReviewError
	if !errors.As(err, &stale) {
		return false
	}
	s.writeJSON(w, http.StatusConflict, StaleReviewErrorResponse{
		Success:   false,
		Error:     stale.Error(),
		ErrorCode: toolreview.ErrorCodeReviewOutOfDate,
		StaleTools: StaleToolsDetail{
			Changed:       nonNilStrings(stale.Changed),
			Unreviewed:    nonNilStrings(stale.Unreviewed),
			MissingBlocks: nonNilStrings(stale.MissingBlocks),
		},
		RequestID: reqcontext.GetRequestID(r.Context()),
	})
	return true
}
