package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills"
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
	// skillNotAvailableMessage is the one body a scoped caller gets for a skill
	// or file that is unknown or not available, whatever the reason.
	skillNotAvailableMessage = "Skill not found"
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
	if rec.Approved != nil {
		e.ApprovedFiles = make([]contracts.SkillFileInfo, 0, len(rec.Approved.Files))
		for _, f := range rec.Approved.Files {
			e.ApprovedFiles = append(e.ApprovedFiles, contracts.SkillFileInfo{Path: f.RelPath, Size: f.Size, Digest: f.Digest, Text: f.Text})
		}
	}
	e.ApprovedBy = rec.ApprovedBy
	if !rec.ApprovedAt.IsZero() {
		t := rec.ApprovedAt.UTC()
		e.ApprovedAt = &t
	}
	// A scan of older content says nothing about the current content, so it
	// is reported only when it was made of exactly the current hash.
	if rec.Scan != nil && rec.Scan.Hash == rec.CurrentHash {
		e.Scan.Verdict = rec.Scan.Verdict
		e.Scan.Complete = rec.Scan.Complete
		e.Scan.Incomplete = rec.Scan.Incomplete
		e.Scan.FindingsTruncated = rec.Scan.FindingsTruncated
		if !rec.Scan.ScannedAt.IsZero() {
			t := rec.Scan.ScannedAt.UTC()
			e.Scan.ScannedAt = &t
		}
		for _, f := range rec.Scan.Findings {
			e.Scan.Findings = append(e.Scan.Findings, contracts.SkillScanFinding{
				File: f.File, Offset: f.Offset, Rule: f.Rule, Severity: f.Severity, Evidence: f.Evidence,
			})
		}
	}
	return e
}

// recordAgentSkillAccess writes the skill_access row of a scoped caller's REST
// read (FR-024): an agent token that reads skill text over REST is recorded
// like one that uses the agent tools. The row never holds the text.
func (s *Server) recordAgentSkillAccess(r *http.Request, start time.Time, a internalRuntime.SkillAccessActivity) {
	a.Source = string(storage.ActivitySourceAPI)
	a.RequestID = reqcontext.GetRequestID(r.Context())
	a.DurationMs = time.Since(start).Milliseconds()
	if ac := auth.AuthContextFromContext(r.Context()); ac != nil {
		a.Attribution.TokenName = ac.AgentName
		a.Attribution.TokenPrefix = ac.TokenPrefix
		if ac.IsClientCredential() {
			a.Attribution.ClientID = ac.ClientID
		}
	}
	s.controller.EmitActivitySkillAccess(a)
}

// writeAgentSkillsList answers a scoped caller: the approved copy of each
// available skill, and nothing about any other skill.
func (s *Server) writeAgentSkillsList(w http.ResponseWriter, r *http.Request, serverName string) {
	start := time.Now()
	avail, err := s.controller.ListAvailableSkills(serverName)
	outcome := internalRuntime.SkillAccessSuccess
	if err != nil {
		outcome = internalRuntime.SkillAccessError
	}
	s.recordAgentSkillAccess(r, start, internalRuntime.SkillAccessActivity{
		Action: internalRuntime.SkillActionList, Outcome: outcome,
		Server: serverName, Results: len(avail), Matches: len(avail),
	})
	if err != nil {
		s.logger.Errorw("Failed to list skills", "server", serverName, "error", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to list skills")
		return
	}
	out := contracts.SkillsAgentListResponse{Enabled: true, Server: serverName, Skills: make([]contracts.SkillAgentEntry, 0, len(avail))}
	for _, a := range avail {
		e := contracts.SkillAgentEntry{
			ID: a.ID, URI: a.URI, Path: a.Path, Name: a.Name, Description: a.Description,
			Status: storage.SkillStatusApproved, Available: true, ApprovedHash: a.ApprovedHash,
			Files: make([]contracts.SkillFileInfo, 0, len(a.Files)), TotalSize: a.TotalSize,
		}
		for _, f := range a.Files {
			e.Files = append(e.Files, contracts.SkillFileInfo{Path: f.RelPath, Size: f.Size, Digest: f.Digest, Text: f.Text})
		}
		out.Skills = append(out.Skills, e)
	}
	s.writeSuccess(w, out)
}

// handleGetServerSkills godoc
// @Summary List the skills of a server
// @Description Lists the skills of one upstream server. An administrator sees every record: status, hashes, files, scan result and the availability an agent would see. A scoped agent token with the server in its reach sees only the available skills, as the approved copy (id, uri, path, name, description, status "approved", approved_hash, files, total_size; contracts.SkillsAgentListResponse) and nothing about any other skill; a server outside its reach is 404. With enable_skills off the answer is {"enabled": false, "skills": []} and no upstream is contacted.
// @Tags servers
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param id path string true "Server ID or name"
// @Success 200 {object} contracts.SkillsListResponse "Skills of the server"
// @Failure 404 {object} contracts.ErrorResponse "Server not found, or outside the caller's reach"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/{id}/skills [get]
func (s *Server) handleGetServerSkills(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	cfg, sc := s.skillsServerConfig(serverID)
	// A scoped caller gets the same 404 for a server outside its reach as for
	// one that does not exist.
	scoped := auth.IsScopedCaller(r.Context())
	start := time.Now()
	// recordList writes the access row of a scoped listing that ends here.
	recordList := func(outcome string) {
		if scoped {
			s.recordAgentSkillAccess(r, start, internalRuntime.SkillAccessActivity{
				Action: internalRuntime.SkillActionList, Outcome: outcome, Server: cutSkillActivityText(serverID),
			})
		}
	}
	if sc == nil || (scoped && !canSeeServer(r.Context(), sc.Name)) {
		recordList(internalRuntime.SkillAccessNotFound)
		s.writeError(w, r, http.StatusNotFound, fmt.Sprintf("Server not found: %s", serverID))
		return
	}
	if !cfg.EnableSkills {
		if scoped {
			recordList(internalRuntime.SkillAccessSuccess)
			s.writeSuccess(w, contracts.SkillsAgentListResponse{Skills: []contracts.SkillAgentEntry{}})
			return
		}
		s.writeSuccess(w, contracts.SkillsListResponse{Skills: []contracts.SkillEntry{}})
		return
	}
	if scoped {
		s.writeAgentSkillsList(w, r, sc.Name)
		return
	}
	var out contracts.SkillsListResponse
	inc, err := s.readAtSettledIncarnation(serverID, func() error {
		var rerr error
		out, rerr = s.buildSkillsList(serverID, sc)
		return rerr
	})
	if errors.Is(err, errIncarnationUnsettled) {
		s.writeSkillsError(w, r, http.StatusConflict, "server_replaced", "the server was removed or replaced while its skills were read; retry")
		return
	}
	out.Incarnation = inc
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
// @Failure 409 {object} contracts.SkillsErrorResponse "skills_disabled, skills_not_supported or refresh_superseded"
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
		if errors.Is(err, internalRuntime.ErrSkillsRefreshSuperseded) {
			s.writeSkillsError(w, r, http.StatusConflict, "refresh_superseded", fmt.Sprintf("server %s reconnected during the refresh; its skills are read again after the reconnect", serverID))
			return
		}
		s.logger.Errorw("Skills refresh failed", "server", serverID, "error", err)
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Skills refresh failed: %v", err))
		return
	}
	// The refresh ends quietly when skills are switched off while it runs.
	if cfg, sc = s.skillsServerConfig(serverID); sc == nil || !cfg.EnableSkills {
		s.writeSkillsError(w, r, http.StatusConflict, "skills_disabled", "enable_skills was turned off during the refresh")
		return
	}
	if !s.controller.SkillsStatus(serverID).Supported {
		s.writeSkillsError(w, r, http.StatusConflict, "skills_not_supported", fmt.Sprintf("server %s does not announce the skills extension", serverID))
		return
	}
	// The records and the incarnation are read as one answer, as in the
	// listing: an incarnation read after the records could be the one of a
	// server added again under the same name.
	var out contracts.SkillsListResponse
	inc, err := s.readAtSettledIncarnation(serverID, func() error {
		var rerr error
		out, rerr = s.buildSkillsList(serverID, sc)
		return rerr
	})
	if errors.Is(err, errIncarnationUnsettled) {
		s.writeSkillsError(w, r, http.StatusConflict, "server_replaced", "the server was removed or replaced while its skills were read; retry")
		return
	}
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to list skills: %v", err))
		return
	}
	out.Incarnation = inc
	s.writeSuccess(w, out)
}

// handleGetServerSkillContent godoc
// @Summary Read one text file of a skill
// @Description Returns a chunk of one stored text file of a skill. An administrator reads the current copy by default; a scoped agent token with the server in its reach reads the approved copy of an available skill only (copy is forced to approved), and gets one 404 for every other skill or file. A server outside its reach is 404. Offsets are byte positions; a chunk never splits a UTF-8 character, so it can be a few bytes shorter than limit. next_offset is absent at the end of the file. 404 for an unknown skill or file (or with enable_skills off), 415 for a binary file, 409 stored_copy_damaged when the stored bytes no longer match their digest.
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
// @Failure 403 {object} contracts.SkillsErrorResponse "output_blocked: the output sanitisation policy withholds the text (scoped caller)"
// @Failure 404 {object} contracts.ErrorResponse "Server, skill or file not found"
// @Failure 409 {object} contracts.SkillsErrorResponse "stored_copy_damaged (administrator)"
// @Failure 415 {object} contracts.ErrorResponse "The file is binary"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/{id}/skills/content [get]
func (s *Server) handleGetServerSkillContent(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	cfg, sc := s.skillsServerConfig(serverID)
	scoped := auth.IsScopedCaller(r.Context())
	q := r.URL.Query()
	skillPath := q.Get("skill")
	file := q.Get("file")
	if file == "" {
		file = "SKILL.md"
	}
	offset := int64(0)
	serverName := serverID
	if sc != nil {
		serverName = sc.Name
	}

	// record writes the access row of a scoped read, whatever its outcome; an
	// administrator's read of its own store is not an agent access.
	var (
		data  []byte
		meta  storage.SkillFile
		sk    *internalRuntime.AvailableSkill
		err   error
		start = time.Now()
	)
	record := func(outcome string, n int64, delivered string) {
		if !scoped {
			return
		}
		a := internalRuntime.SkillAccessActivity{
			Action: internalRuntime.SkillActionRead, Outcome: outcome, Server: cutSkillActivityText(serverName),
			SkillID: cutSkillActivityText(serverName + ":" + skillPath), File: cutSkillActivityText(file),
			Offset: offset, Bytes: n, TotalSize: int64(len(data)), DetectionText: delivered,
		}
		if sk != nil {
			a.Hash, a.SkillName = sk.ApprovedHash, cutSkillActivityText(sk.Name)
		}
		s.recordAgentSkillAccess(r, start, a)
	}

	if sc == nil || (scoped && !canSeeServer(r.Context(), sc.Name)) {
		record(internalRuntime.SkillAccessNotFound, 0, "")
		s.writeError(w, r, http.StatusNotFound, fmt.Sprintf("Server not found: %s", serverID))
		return
	}
	if !cfg.EnableSkills {
		if scoped {
			record(internalRuntime.SkillAccessNotFound, 0, "")
			s.writeError(w, r, http.StatusNotFound, skillNotAvailableMessage)
			return
		}
		s.writeError(w, r, http.StatusNotFound, "Skills are disabled (enable_skills is off)")
		return
	}
	badRequest := func(msg string) {
		record(internalRuntime.SkillAccessInvalid, 0, "")
		s.writeError(w, r, http.StatusBadRequest, msg)
	}

	if skillPath == "" {
		badRequest("skill query parameter is required")
		return
	}
	approved := false
	switch q.Get("copy") {
	case "", "current":
	case "approved":
		approved = true
	default:
		badRequest("copy must be current or approved")
		return
	}
	if v := q.Get("offset"); v != "" {
		n, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil || n < 0 {
			badRequest("offset must be a non-negative integer")
			return
		}
		offset = n
	}
	limit := int64(skillContentMaxLimit)
	if v := q.Get("limit"); v != "" {
		n, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil || n < 1 {
			badRequest("limit must be a positive integer")
			return
		}
		if n < limit {
			limit = n
		}
	}

	if scoped {
		// copy is forced to approved, and only an available skill's approved
		// files exist. The text is what the output policy lets out. Every
		// reason the skill cannot be had is the one not-found answer: a
		// damaged stored copy too, which is reported on the skill's record and
		// in the access row, where an administrator reads it.
		data, meta, sk, err = s.controller.AvailableSkillFile(sc.Name, skillPath, file)
		switch {
		case errors.Is(err, internalRuntime.ErrSkillNotFound), errors.Is(err, internalRuntime.ErrSkillFileNotFound):
			record(internalRuntime.SkillAccessNotFound, 0, "")
			s.writeError(w, r, http.StatusNotFound, skillNotAvailableMessage)
			return
		case errors.Is(err, internalRuntime.ErrSkillStoredCopyDamaged):
			record(internalRuntime.SkillAccessDamaged, 0, "")
			s.writeError(w, r, http.StatusNotFound, skillNotAvailableMessage)
			return
		case errors.Is(err, internalRuntime.ErrSkillOutputBlocked):
			record(internalRuntime.SkillAccessBlocked, 0, "")
			s.writeSkillsError(w, r, http.StatusForbidden, "output_blocked", err.Error())
			return
		}
	} else {
		data, meta, err = s.controller.SkillFileText(serverID, skillPath, file, approved)
	}
	switch {
	case errors.Is(err, internalRuntime.ErrSkillNotFound), errors.Is(err, internalRuntime.ErrSkillFileNotFound):
		s.writeError(w, r, http.StatusNotFound, err.Error())
		return
	case errors.Is(err, internalRuntime.ErrSkillFileBinary):
		record(internalRuntime.SkillAccessBinary, 0, "")
		s.writeError(w, r, http.StatusUnsupportedMediaType, err.Error())
		return
	case errors.Is(err, internalRuntime.ErrSkillStoredCopyDamaged):
		s.writeSkillsError(w, r, http.StatusConflict, "stored_copy_damaged", err.Error())
		return
	case err != nil:
		record(internalRuntime.SkillAccessError, 0, "")
		s.logger.Errorw("Failed to read skill file", "server", serverID, "skill", skillPath, "error", err)
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to read skill file: %v", err))
		return
	}

	size := int64(len(data))
	text, next, more, cerr := skills.Chunk(data, offset, limit)
	switch {
	case errors.Is(cerr, skills.ErrChunkOffsetPastEnd):
		badRequest("offset is past the end of the file")
		return
	case errors.Is(cerr, skills.ErrChunkOffsetInsideRune):
		badRequest("offset is inside a UTF-8 character")
		return
	}
	resp := contracts.SkillContentResponse{
		Text:   string(text),
		Offset: offset,
		Size:   size,
		Digest: meta.Digest,
	}
	if more {
		resp.NextOffset = &next
	}
	record(internalRuntime.SkillAccessSuccess, int64(len(text)), string(text))
	s.writeSuccess(w, resp)
}

// cutSkillActivityText bounds a caller-supplied value before it goes into an
// activity row, and drops control characters.
func cutSkillActivityText(v string) string {
	v = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.ToValidUTF8(v, ""))
	const limit = 200
	if len(v) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(v[cut]) {
			cut--
		}
		v = v[:cut]
	}
	return v
}
