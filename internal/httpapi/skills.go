package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 125 (skills over MCP), PR-1: the administrator-only skills endpoints.
// Every handler starts with the admin gate, before any runtime call, so a
// scoped agent token learns nothing and triggers nothing (FR-019a).

const (
	skillsDenialMessage = "Agent tokens cannot read or refresh skills"
	// skillContentMaxLimit is both the default and the maximum chunk size of
	// the content endpoint, in bytes.
	skillContentMaxLimit = 65536
)

// writeSkillsError writes an error body that carries a stable error_code.
func (s *Server) writeSkillsError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	s.writeJSON(w, status, contracts.SkillsErrorResponse{
		Success:   false,
		Error:     code + ": " + message,
		ErrorCode: code,
		RequestID: reqcontext.GetRequestID(r.Context()),
	})
}

// skillsServerConfig finds the configured server, nil when there is none.
func (s *Server) skillsServerConfig(name string) (*config.Config, *config.ServerConfig) {
	cfg := s.controller.GetCurrentConfig()
	if cfg == nil {
		return nil, nil
	}
	for _, sc := range cfg.Servers {
		if sc != nil && sc.Name == name {
			return cfg, sc
		}
	}
	return cfg, nil
}

// buildSkillsList assembles the listing payload for one server.
func (s *Server) buildSkillsList(serverName string, sc *config.ServerConfig) (contracts.SkillsListResponse, error) {
	recs, err := s.controller.ListSkillRecords(serverName)
	if err != nil {
		return contracts.SkillsListResponse{}, err
	}
	st := s.controller.SkillsStatus(serverName)
	out := contracts.SkillsListResponse{
		Enabled:      true,
		Server:       serverName,
		Supported:    st.Supported,
		ExposeSkills: sc.ExposeSkills,
		Error:        st.Error,
		Truncated:    st.Truncated,
		Skills:       make([]contracts.SkillEntry, 0, len(recs)),
	}
	if !st.LastRefresh.IsZero() {
		t := st.LastRefresh.UTC()
		out.LastRefresh = &t
	}
	for _, rec := range recs {
		out.Skills = append(out.Skills, skillEntryFor(serverName, rec, s.controller.SkillAvailable(serverName, rec)))
	}
	return out, nil
}

func skillEntryFor(serverName string, rec *storage.SkillApprovalRecord, available bool) contracts.SkillEntry {
	e := contracts.SkillEntry{
		ID:             serverName + ":" + rec.SkillPath,
		URI:            rec.SkillURI,
		Path:           rec.SkillPath,
		Name:           rec.Name,
		Description:    rec.Description,
		Status:         rec.Status,
		Blocked:        rec.Blocked,
		Listed:         rec.Listed,
		Available:      available,
		UnusableReason: rec.UnusableReason,
		CurrentHash:    rec.CurrentHash,
		ApprovedHash:   rec.ApprovedHash,
		Files:          make([]contracts.SkillFileInfo, 0, len(rec.Current.Files)),
		Scan:           contracts.SkillScanInfo{Findings: []contracts.SkillScanFinding{}},
	}
	for _, f := range rec.Current.Files {
		e.Files = append(e.Files, contracts.SkillFileInfo{Path: f.RelPath, Size: f.Size, Digest: f.Digest, Text: f.Text})
		e.TotalSize += f.Size
	}
	if rec.Scan != nil {
		e.Scan.Verdict = rec.Scan.Verdict
		e.Scan.Complete = rec.Scan.Complete
		for _, f := range rec.Scan.Findings {
			e.Scan.Findings = append(e.Scan.Findings, contracts.SkillScanFinding{
				File: f.File, Offset: f.Offset, Rule: f.Rule, Severity: f.Severity, Evidence: f.Evidence,
			})
		}
	}
	return e
}

// handleGetServerSkills godoc
// @Summary List the skills of a server
// @Description Administrator only. Lists every skill record mcpproxy holds for one upstream server: status, hashes, files and the availability an agent would see. With enable_skills off the answer is {"enabled": false, "skills": []} and no upstream is contacted. A scoped agent token is refused with 403.
// @Tags servers
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param id path string true "Server ID or name"
// @Success 200 {object} contracts.SkillsListResponse "Skills of the server"
// @Failure 403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure 404 {object} contracts.ErrorResponse "Server not found"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/{id}/skills [get]
func (s *Server) handleGetServerSkills(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminRead(w, r, skillsDenialMessage) {
		return
	}
	serverID := chi.URLParam(r, "id")
	cfg, sc := s.skillsServerConfig(serverID)
	if sc == nil {
		s.writeError(w, r, http.StatusNotFound, fmt.Sprintf("Server not found: %s", serverID))
		return
	}
	if !cfg.EnableSkills {
		s.writeSuccess(w, contracts.SkillsListResponse{Skills: []contracts.SkillEntry{}})
		return
	}
	out, err := s.buildSkillsList(serverID, sc)
	if err != nil {
		s.logger.Errorw("Failed to list skills", "server", serverID, "error", err)
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to list skills: %v", err))
		return
	}
	s.writeSuccess(w, out)
}

// handleRefreshServerSkills godoc
// @Summary Refresh the skills of a server
// @Description Administrator only. Reads the server's skills from the upstream again, verifies every file and returns the listing once applied. A refresh that is already running is joined. 409 skills_disabled when enable_skills is off; 409 skills_not_supported when the server does not announce the skills extension.
// @Tags servers
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param id path string true "Server ID or name"
// @Success 200 {object} contracts.SkillsListResponse "Skills of the server after the refresh"
// @Failure 403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure 404 {object} contracts.ErrorResponse "Server not found"
// @Failure 409 {object} contracts.SkillsErrorResponse "skills_disabled or skills_not_supported"
// @Failure 500 {object} contracts.ErrorResponse "Refresh failed"
// @Router /api/v1/servers/{id}/skills/refresh [post]
func (s *Server) handleRefreshServerSkills(w http.ResponseWriter, r *http.Request) {
	// requireAdminAuth carries a token-management message; the skills message
	// is the same gate (requireAdminRead) with a text that fits this route.
	if !s.requireAdminRead(w, r, skillsDenialMessage) {
		return
	}
	serverID := chi.URLParam(r, "id")
	cfg, sc := s.skillsServerConfig(serverID)
	if sc == nil {
		s.writeError(w, r, http.StatusNotFound, fmt.Sprintf("Server not found: %s", serverID))
		return
	}
	if !cfg.EnableSkills {
		s.writeSkillsError(w, r, http.StatusConflict, "skills_disabled", "enable_skills is off; set enable_skills: true in the configuration")
		return
	}
	if err := s.controller.RefreshServerSkills(r.Context(), serverID); err != nil {
		s.logger.Errorw("Skills refresh failed", "server", serverID, "error", err)
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Skills refresh failed: %v", err))
		return
	}
	if !s.controller.SkillsStatus(serverID).Supported {
		s.writeSkillsError(w, r, http.StatusConflict, "skills_not_supported", fmt.Sprintf("server %s does not announce the skills extension", serverID))
		return
	}
	out, err := s.buildSkillsList(serverID, sc)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to list skills: %v", err))
		return
	}
	s.writeSuccess(w, out)
}

// handleGetServerSkillContent godoc
// @Summary Read one text file of a skill
// @Description Administrator only. Returns a chunk of one stored text file of a skill. Offsets are byte positions; a chunk never splits a UTF-8 character, so it can be a few bytes shorter than limit. next_offset is absent at the end of the file. 404 for an unknown skill or file (or with enable_skills off), 415 for a binary file, 409 stored_copy_damaged when the stored bytes no longer match their digest.
// @Tags servers
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param id path string true "Server ID or name"
// @Param skill query string true "Skill path, e.g. hf-cli"
// @Param file query string false "File path inside the skill (default SKILL.md)"
// @Param copy query string false "current (default) or approved"
// @Param offset query int false "Byte offset to start at (default 0)"
// @Param limit query int false "Maximum bytes (default and maximum 65536)"
// @Success 200 {object} contracts.SkillContentResponse "A chunk of the file"
// @Failure 400 {object} contracts.ErrorResponse "Bad parameters"
// @Failure 403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure 404 {object} contracts.ErrorResponse "Server, skill or file not found"
// @Failure 409 {object} contracts.SkillsErrorResponse "stored_copy_damaged"
// @Failure 415 {object} contracts.ErrorResponse "The file is binary"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/{id}/skills/content [get]
func (s *Server) handleGetServerSkillContent(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminRead(w, r, skillsDenialMessage) {
		return
	}
	serverID := chi.URLParam(r, "id")
	cfg, sc := s.skillsServerConfig(serverID)
	if sc == nil {
		s.writeError(w, r, http.StatusNotFound, fmt.Sprintf("Server not found: %s", serverID))
		return
	}
	if !cfg.EnableSkills {
		s.writeError(w, r, http.StatusNotFound, "Skills are disabled (enable_skills is off)")
		return
	}
	q := r.URL.Query()
	skillPath := q.Get("skill")
	if skillPath == "" {
		s.writeError(w, r, http.StatusBadRequest, "skill query parameter is required")
		return
	}
	file := q.Get("file")
	if file == "" {
		file = "SKILL.md"
	}
	approved := false
	switch q.Get("copy") {
	case "", "current":
	case "approved":
		approved = true
	default:
		s.writeError(w, r, http.StatusBadRequest, "copy must be current or approved")
		return
	}
	offset := int64(0)
	if v := q.Get("offset"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			s.writeError(w, r, http.StatusBadRequest, "offset must be a non-negative integer")
			return
		}
		offset = n
	}
	limit := int64(skillContentMaxLimit)
	if v := q.Get("limit"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			s.writeError(w, r, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		if n < limit {
			limit = n
		}
	}

	data, meta, err := s.controller.SkillFileText(serverID, skillPath, file, approved)
	switch {
	case errors.Is(err, internalRuntime.ErrSkillNotFound), errors.Is(err, internalRuntime.ErrSkillFileNotFound):
		s.writeError(w, r, http.StatusNotFound, err.Error())
		return
	case errors.Is(err, internalRuntime.ErrSkillFileBinary):
		s.writeError(w, r, http.StatusUnsupportedMediaType, err.Error())
		return
	case errors.Is(err, internalRuntime.ErrSkillStoredCopyDamaged):
		s.writeSkillsError(w, r, http.StatusConflict, "stored_copy_damaged", err.Error())
		return
	case err != nil:
		s.logger.Errorw("Failed to read skill file", "server", serverID, "skill", skillPath, "error", err)
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to read skill file: %v", err))
		return
	}

	size := int64(len(data))
	if offset > size {
		s.writeError(w, r, http.StatusBadRequest, "offset is past the end of the file")
		return
	}
	if offset < size && !utf8.RuneStart(data[offset]) {
		s.writeError(w, r, http.StatusBadRequest, "offset is inside a UTF-8 character")
		return
	}
	end := offset + limit
	if end >= size {
		end = size
	} else {
		// Shorten the chunk so it ends on a character boundary.
		for end > offset && !utf8.RuneStart(data[end]) {
			end--
		}
		if end == offset {
			// The limit is smaller than one character: take that character
			// whole so the reader always makes progress.
			end = offset + 1
			for end < size && !utf8.RuneStart(data[end]) {
				end++
			}
		}
	}
	resp := contracts.SkillContentResponse{
		Text:   string(data[offset:end]),
		Offset: offset,
		Size:   size,
		Digest: meta.Digest,
	}
	if end < size {
		next := end
		resp.NextOffset = &next
	}
	s.writeSuccess(w, resp)
}
