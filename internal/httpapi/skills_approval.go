package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 125 (skills over MCP), PR-2: the approval gate. Administrator only,
// like every skills route; the gate runs before any controller call.

const (
	skillsApprovalDenialMessage = "Agent tokens cannot approve, block or unblock skills"
	// skillsBodyMaxBytes bounds a request body of the approval endpoints.
	skillsBodyMaxBytes = 1 << 20
	// skillsMaxPerRequest bounds the number of skills one request names.
	skillsMaxPerRequest = 200
	// skillsApprovedBy is what the tool approval endpoint stores as the
	// approver of a REST call; skills use the same value.
	skillsApprovedBy = "api"
)

// decodeSkillsBody reads one JSON object from the bounded body into v,
// refusing unknown fields and trailing data. It writes the 400 itself.
func (s *Server) decodeSkillsBody(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	r.Body = http.MaxBytesReader(w, r.Body, skillsBodyMaxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			s.writeError(w, r, http.StatusBadRequest, fmt.Sprintf("Request body is larger than %d bytes", skillsBodyMaxBytes))
			return false
		}
		s.writeError(w, r, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		s.writeError(w, r, http.StatusBadRequest, "Invalid request body: unexpected data after the JSON object")
		return false
	}
	return true
}

// validSkillNames checks the skills array of a request and returns it
// de-duplicated in request order. It writes the 400 itself.
func (s *Server) validSkillNames(w http.ResponseWriter, r *http.Request, names []string) ([]string, bool) {
	if len(names) == 0 {
		s.writeError(w, r, http.StatusBadRequest, "skills must name at least one skill")
		return nil, false
	}
	if len(names) > skillsMaxPerRequest {
		s.writeError(w, r, http.StatusBadRequest, fmt.Sprintf("skills names %d skills; the limit is %d per request", len(names), skillsMaxPerRequest))
		return nil, false
	}
	for _, n := range names {
		if n == "" {
			s.writeError(w, r, http.StatusBadRequest, "skills must not contain an empty name")
			return nil, false
		}
	}
	return uniqueStrings(names), true
}

// skillsApprovalPrelude runs the checks every approval-gate route shares:
// admin gate, server lookup (404), enable_skills (409). It returns false
// after writing the answer.
func (s *Server) skillsApprovalPrelude(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !s.requireAdminRead(w, r, skillsApprovalDenialMessage) {
		return "", false
	}
	serverID := chi.URLParam(r, "id")
	cfg, sc := s.skillsServerConfig(serverID)
	if sc == nil {
		s.writeError(w, r, http.StatusNotFound, fmt.Sprintf("Server not found: %s", serverID))
		return "", false
	}
	if !cfg.EnableSkills {
		s.writeSkillsError(w, r, http.StatusConflict, "skills_disabled", "enable_skills is off; set enable_skills: true in the configuration")
		return "", false
	}
	return serverID, true
}

// writeSkillsWriteError maps the errors the approval writers share.
func (s *Server) writeSkillsWriteError(w http.ResponseWriter, r *http.Request, serverID string, err error, prefix string) {
	var replaced *commitview.ServerReplacedError
	switch {
	case errors.Is(err, internalRuntime.ErrSkillNotFound):
		s.writeError(w, r, http.StatusNotFound, err.Error())
	case errors.As(err, &replaced), errors.Is(err, commitview.ErrServerReplaced), errors.Is(err, internalRuntime.ErrServerNotFound):
		s.writeApprovalWriteError(w, r, err, prefix)
	default:
		s.logger.Errorw(prefix, "server", serverID, "error", err)
		s.writeApprovalWriteError(w, r, err, prefix)
	}
}

// handleApproveServerSkills godoc
// @Summary Approve skills of a server
// @Description Administrator only. Approves the named skills for exactly the content the reviewer saw: expected_hashes maps each skill path to the current_hash from the listing, and expected_incarnation (the listing's incarnation) binds the approval to the server that was reviewed. When a hash differs, or a named skill has no expected hash, the response is 409 review_out_of_date with the changed and unreviewed names and nothing is approved; 409 server_replaced when the server was removed or replaced since the review; 422 skill_unusable for a skill that cannot be approved (a failed check or a dynamic file list). A skill already approved for its current content is left as it is and not counted. The blocked mark is never touched. A scoped agent token is refused with 403.
// @Tags servers
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param id path string true "Server ID or name"
// @Param body body contracts.SkillsApproveRequest true "Skills to approve, with the review binding"
// @Success 200 {object} contracts.SkillsApproveResponse "Number of skills newly approved (inside the data envelope)"
// @Failure 400 {object} contracts.ErrorResponse "Invalid request"
// @Failure 403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure 404 {object} contracts.ErrorResponse "Server or skill not found"
// @Failure 409 {object} contracts.SkillsStaleReviewResponse "review_out_of_date, server_replaced or skills_disabled"
// @Failure 422 {object} contracts.SkillsUnusableResponse "skill_unusable"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/{id}/skills/approve [post]
func (s *Server) handleApproveServerSkills(w http.ResponseWriter, r *http.Request) {
	serverID, ok := s.skillsApprovalPrelude(w, r)
	if !ok {
		return
	}
	var req contracts.SkillsApproveRequest
	if !s.decodeSkillsBody(w, r, &req) {
		return
	}
	skills, ok := s.validSkillNames(w, r, req.Skills)
	if !ok {
		return
	}
	if req.ExpectedIncarnation < 0 {
		s.writeError(w, r, http.StatusBadRequest, "expected_incarnation must not be negative")
		return
	}
	expected := req.ExpectedHashes
	if expected == nil {
		expected = map[string]string{}
	}
	n, err := s.controller.ApproveSkillsReviewedAt(r.Context(), serverID, skills, expected, skillsApprovedBy, req.ExpectedIncarnation)
	if err != nil {
		var stale *internalRuntime.StaleSkillReviewError
		var unusable *internalRuntime.SkillsUnusableError
		switch {
		case errors.As(err, &stale):
			s.writeJSON(w, http.StatusConflict, contracts.SkillsStaleReviewResponse{
				Success:    false,
				Error:      "review_out_of_date: " + stale.Error(),
				ErrorCode:  "review_out_of_date",
				Changed:    nonNilStrings(stale.Changed),
				Unreviewed: nonNilStrings(stale.Unreviewed),
				RequestID:  reqcontext.GetRequestID(r.Context()),
			})
		case errors.As(err, &unusable):
			s.writeJSON(w, http.StatusUnprocessableEntity, contracts.SkillsUnusableResponse{
				Success:   false,
				Error:     "skill_unusable: " + unusable.Error(),
				ErrorCode: "skill_unusable",
				Skills:    nonNilStrings(unusable.Skills),
				RequestID: reqcontext.GetRequestID(r.Context()),
			})
		default:
			s.writeSkillsWriteError(w, r, serverID, err, "Failed to approve skills")
		}
		return
	}
	s.writeSuccess(w, contracts.SkillsApproveResponse{Approved: n})
}

// handleBlockServerSkills godoc
// @Summary Block skills of a server
// @Description Administrator only. Sets the blocked mark on the named skills: a blocked skill is never offered to an agent, and no refresh, content change or automatic approval clears the mark. expected_incarnation is optional (0 binds to the current server). 409 server_replaced when the server was removed or replaced since it was reviewed. A scoped agent token is refused with 403.
// @Tags servers
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param id path string true "Server ID or name"
// @Param body body contracts.SkillsBlockRequest true "Skills to block"
// @Success 200 {object} contracts.SkillsBlockResponse "Number of skills whose mark changed (inside the data envelope)"
// @Failure 400 {object} contracts.ErrorResponse "Invalid request"
// @Failure 403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure 404 {object} contracts.ErrorResponse "Server or skill not found"
// @Failure 409 {object} contracts.SkillsErrorResponse "server_replaced or skills_disabled"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/{id}/skills/block [post]
func (s *Server) handleBlockServerSkills(w http.ResponseWriter, r *http.Request) {
	s.handleSetSkillsBlocked(w, r, true)
}

// handleUnblockServerSkills godoc
// @Summary Unblock skills of a server
// @Description Administrator only. Clears the blocked mark on the named skills. The skill's approval state is not changed: a skill that is not approved stays unavailable. expected_incarnation is optional (0 binds to the current server). 409 server_replaced when the server was removed or replaced since it was reviewed. A scoped agent token is refused with 403.
// @Tags servers
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param id path string true "Server ID or name"
// @Param body body contracts.SkillsBlockRequest true "Skills to unblock"
// @Success 200 {object} contracts.SkillsBlockResponse "Number of skills whose mark changed (inside the data envelope)"
// @Failure 400 {object} contracts.ErrorResponse "Invalid request"
// @Failure 403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure 404 {object} contracts.ErrorResponse "Server or skill not found"
// @Failure 409 {object} contracts.SkillsErrorResponse "server_replaced or skills_disabled"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/{id}/skills/unblock [post]
func (s *Server) handleUnblockServerSkills(w http.ResponseWriter, r *http.Request) {
	s.handleSetSkillsBlocked(w, r, false)
}

func (s *Server) handleSetSkillsBlocked(w http.ResponseWriter, r *http.Request, blocked bool) {
	serverID, ok := s.skillsApprovalPrelude(w, r)
	if !ok {
		return
	}
	var req contracts.SkillsBlockRequest
	if !s.decodeSkillsBody(w, r, &req) {
		return
	}
	skills, ok := s.validSkillNames(w, r, req.Skills)
	if !ok {
		return
	}
	if req.ExpectedIncarnation < 0 {
		s.writeError(w, r, http.StatusBadRequest, "expected_incarnation must not be negative")
		return
	}
	n, err := s.controller.SetSkillsBlockedAt(r.Context(), serverID, skills, blocked, skillsApprovedBy, req.ExpectedIncarnation)
	if err != nil {
		verb := "block"
		if !blocked {
			verb = "unblock"
		}
		s.writeSkillsWriteError(w, r, serverID, err, "Failed to "+verb+" skills")
		return
	}
	s.writeSuccess(w, contracts.SkillsBlockResponse{Changed: n})
}
