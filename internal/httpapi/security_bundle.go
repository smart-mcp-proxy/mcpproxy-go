package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// Spec 101 TPA signature bundle surface (contracts/bundle-status-surface.md).
// Status and history are admin-read; the three mutations are additionally
// gated by auth.ServerOpBundleManage at the route.

// bundleAdminDenialMessage is the fixed refusal a scoped caller gets on the
// bundle reads. The bundle status names the artifact digests an operator rolls
// back to, and the last rejection reason — deployment-wide, not per server.
const bundleAdminDenialMessage = "Agent tokens cannot read the TPA signature bundle status (administrators only)"

// BundleRollbackRefusedResponse is the HTTP 409 body of a well-formed rollback
// that the scanner refused. ErrorCode is the rejection reason (for example
// rollback_target_mismatch); last-known-good keeps serving.
type BundleRollbackRefusedResponse struct {
	Success   bool                    `json:"success"`
	Error     string                  `json:"error"`
	ErrorCode string                  `json:"error_code"`
	Rejection scanner.BundleRejection `json:"rejection"`
	RequestID string                  `json:"request_id,omitempty"`
}

// bundleRollbackRequest is the POST /security/bundle/rollback body.
type bundleRollbackRequest struct {
	// Embedded rolls back to the corpus compiled into this binary.
	Embedded bool `json:"embedded"`
	// Path is where to find the target bundle (a hint only).
	Path string `json:"path"`
	// Digest is the full 64-character lowercase hex SHA-256 of the target.
	Digest string `json:"digest"`
}

// isFullBundleDigest reports whether d is 64 lowercase hex characters.
func isFullBundleDigest(d string) bool {
	if len(d) != 64 {
		return false
	}
	for i := 0; i < len(d); i++ {
		c := d[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// handleGetSecurityBundle godoc
// @Summary Get the TPA signature bundle status
// @Description Reports the offline TPA signature corpus this install is serving: full SHA-256 digest, sequence, authority, trust epoch, signature verification, staleness, degraded coverage with its reason, rollback pin, and the last refused candidate. Admin only.
// @Tags security
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.SuccessResponse "Bundle status (data is the BundleInfo object)"
// @Failure 403 {object} contracts.ErrorResponse "Admin credentials required"
// @Failure 501 {object} contracts.ErrorResponse "Security scanner not configured"
// @Router /api/v1/security/bundle [get]
func (s *Server) handleGetSecurityBundle(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}
	if !s.requireAdminRead(w, r, bundleAdminDenialMessage) {
		return
	}
	s.writeSuccess(w, s.securityController.BundleStatus())
}

// handleGetSecurityBundleHistory godoc
// @Summary Get the TPA signature bundle activation history
// @Description Append-only list of bundle activations, oldest first, each with its full SHA-256 digest. It is the trustworthy source of the digest to name in a rollback. Admin only.
// @Tags security
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.SuccessResponse "Activation history (data is an array of TPABundleActivation)"
// @Failure 403 {object} contracts.ErrorResponse "Admin credentials required"
// @Failure 500 {object} contracts.ErrorResponse "History could not be read"
// @Failure 501 {object} contracts.ErrorResponse "Security scanner not configured"
// @Router /api/v1/security/bundle/history [get]
func (s *Server) handleGetSecurityBundleHistory(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}
	if !s.requireAdminRead(w, r, bundleAdminDenialMessage) {
		return
	}
	history, err := s.securityController.BundleHistory()
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	if history == nil {
		history = []scanner.TPABundleActivation{}
	}
	s.writeSuccess(w, history)
}

// handleRollbackSecurityBundle godoc
// @Summary Roll the TPA signature bundle back to a named artifact
// @Description The one operator override of the downgrade watermark. Exactly one of embedded or path must be given, and digest must be the FULL 64-character lowercase hex SHA-256 of the target (the 12-character fingerprint is refused). On success the target serves and is pinned. A refusal (for example rollback_target_mismatch) returns 409 with the rejection reason and last-known-good keeps serving. Admin API key only; agent tokens are refused.
// @Tags security
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param body body bundleRollbackRequest true "Rollback target"
// @Success 200 {object} contracts.SuccessResponse "Updated bundle status"
// @Failure 400 {object} contracts.ErrorResponse "Usage error: both or neither of embedded/path, or digest is not 64 lowercase hex"
// @Failure 403 {object} contracts.ErrorResponse "Admin credentials required"
// @Failure 409 {object} BundleRollbackRefusedResponse "Rollback refused; the response carries the rejection reason"
// @Failure 500 {object} contracts.ErrorResponse "Internal error"
// @Failure 501 {object} contracts.ErrorResponse "Security scanner not configured"
// @Router /api/v1/security/bundle/rollback [post]
func (s *Server) handleRollbackSecurityBundle(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}
	var req bundleRollbackRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			s.writeError(w, r, http.StatusBadRequest, "Invalid request body")
			return
		}
	}
	if !isFullBundleDigest(req.Digest) {
		s.writeError(w, r, http.StatusBadRequest,
			"the full digest is required: give the 64-character lowercase hex SHA-256 of the target (the 12-character fingerprint is a label, not an authorization); `mcpproxy security bundle history` lists the full digests")
		return
	}

	info, err := s.securityController.RollbackBundle(scanner.BundleRollbackRequest{
		Embedded: req.Embedded,
		Path:     req.Path,
		Digest:   req.Digest,
	})
	if err != nil {
		var refused *scanner.BundleRollbackRefused
		switch {
		case errors.As(err, &refused):
			s.writeJSON(w, http.StatusConflict, BundleRollbackRefusedResponse{
				Success:   false,
				Error:     refused.Error(),
				ErrorCode: refused.Rejection.Reason,
				Rejection: refused.Rejection,
				RequestID: reqcontext.GetRequestID(r.Context()),
			})
		case errors.Is(err, scanner.ErrBundleRollbackUsage):
			s.writeError(w, r, http.StatusBadRequest, err.Error())
		default:
			s.writeError(w, r, http.StatusInternalServerError, err.Error())
		}
		return
	}
	s.writeSuccess(w, info)
}

// handleUnpinSecurityBundle godoc
// @Summary Clear the TPA signature bundle rollback pin
// @Description Clears the rollback pin and re-validates the serving bundle under the normal downgrade watermark, so a rolled-back target below the watermark no longer serves. A quiet no-op when nothing is pinned. Admin API key only.
// @Tags security
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.SuccessResponse "Updated bundle status"
// @Failure 403 {object} contracts.ErrorResponse "Admin credentials required"
// @Failure 500 {object} contracts.ErrorResponse "The unpin could not be recorded"
// @Failure 501 {object} contracts.ErrorResponse "Security scanner not configured"
// @Router /api/v1/security/bundle/unpin [post]
func (s *Server) handleUnpinSecurityBundle(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}
	info, err := s.securityController.UnpinBundle()
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeSuccess(w, info)
}

// handleReloadSecurityBundle godoc
// @Summary Reload the TPA signature bundle
// @Description Re-runs the single-flight bundle pipeline against the configured source and current policy. A refused candidate leaves last-known-good serving and is reported as last_rejection in the returned status. Admin API key only.
// @Tags security
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.SuccessResponse "Updated bundle status"
// @Failure 403 {object} contracts.ErrorResponse "Admin credentials required"
// @Failure 501 {object} contracts.ErrorResponse "Security scanner not configured"
// @Router /api/v1/security/bundle/reload [post]
func (s *Server) handleReloadSecurityBundle(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecurity(w, r) {
		return
	}
	s.writeSuccess(w, s.securityController.ReloadBundle())
}
