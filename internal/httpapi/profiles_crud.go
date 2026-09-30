package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// The profile write routes (Spec 108-f, FR-034). Every mutating route is gated
// by requireServerOp(ServerOpConfigWrite) at registration and is NOT gated by
// read_only_mode on REST (like PATCH /config); the MCP surface gates its own.
// Every write runs through the profiles service, which holds the FR-008a guard
// and writes the profile_change record.

// decodeProfileBody decodes a request body into v, refusing unknown fields so a
// typo in a profile field is a 400, never a silently ignored setting.
func decodeProfileBody(r *http.Request, v interface{}) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("request body is required")
		}
		return err
	}
	return nil
}

func (s *Server) badProfileBody(w http.ResponseWriter, r *http.Request, err error) {
	s.writeClientBindingError(w, r, http.StatusBadRequest, "", "", "invalid request body: "+err.Error())
}

// handleCreateProfile godoc
// @Summary Create a profile
// @Description Adds a profile. The body is a ProfileConfig. A name that already exists is 409 profile_exists; `active` and `try` are reserved by the REST API; a fatal validation rule is 400 with the offending field and the unchanged FR-007 text. A write that would let a client bound to a named profile escape it while require_mcp_auth is off is refused 409 binding_bypassable_without_auth (FR-008a) and nothing is written. Writes one profile_change record.
// @Tags profiles
// @Accept json
// @Produce json
// @Param body body config.ProfileConfig true "The profile"
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 201 {object} contracts.APIResponse{data=ProfileWriteData} "Created, with the validator's warnings"
// @Failure 400 {object} ClientBindingErrorResponse "Invalid input; field names the offending input"
// @Failure 403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure 409 {object} ProfileConflictResponse "profile_exists, or binding_bypassable_without_auth (BindingGuardResponse)"
// @Failure 503 {object} contracts.ErrorResponse "Service unavailable"
// @Router /api/v1/profiles [post]
func (s *Server) handleCreateProfile(w http.ResponseWriter, r *http.Request) {
	var p config.ProfileConfig
	if err := decodeProfileBody(r, &p); err != nil {
		s.badProfileBody(w, r, err)
		return
	}
	res, err := s.profiles().Create(r.Context(), actorFromRequest(r), p)
	if err != nil {
		s.writeProfileServiceError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, contracts.NewSuccessResponse(res))
}

// handleUpdateProfile godoc
// @Summary Replace a profile
// @Description Replaces the named profile with the body (a ProfileConfig whose name must equal the path; 409 name_mismatch otherwise - a rename has its own route). A write whose only change is tools.classify is recorded as `classify`. Guarded by FR-008a like a create.
// @Tags profiles
// @Accept json
// @Produce json
// @Param name path string true "Profile name"
// @Param body body config.ProfileConfig true "The profile"
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.APIResponse{data=ProfileWriteData} "Updated, with the validator's warnings"
// @Failure 400 {object} ClientBindingErrorResponse "Invalid input; field names the offending input"
// @Failure 403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure 404 {object} contracts.ErrorResponse "profile not found"
// @Failure 409 {object} ProfileConflictResponse "name_mismatch, or binding_bypassable_without_auth (BindingGuardResponse)"
// @Failure 503 {object} contracts.ErrorResponse "Service unavailable"
// @Router /api/v1/profiles/{name} [put]
func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var p config.ProfileConfig
	if err := decodeProfileBody(r, &p); err != nil {
		s.badProfileBody(w, r, err)
		return
	}
	if p.Name == "" {
		p.Name = name
	}
	res, err := s.profiles().Update(r.Context(), actorFromRequest(r), name, p)
	if err != nil {
		s.writeProfileServiceError(w, r, err)
		return
	}
	s.writeSuccess(w, res)
}

// RenameProfileRequest is the body of POST /profiles/{name}/rename.
type RenameProfileRequest struct {
	NewName string `json:"new_name"`
}

// handleRenameProfile godoc
// @Summary Rename a profile
// @Description Renames a profile and moves every reference to it - token pins, client bindings, other profiles' switchable_to and the anonymous_profile - in one write (D19). Tokens move first, so a failure part-way never widens a scope. Guarded by FR-008a. Writes one `rename` record.
// @Tags profiles
// @Accept json
// @Produce json
// @Param name path string true "Profile name"
// @Param body body RenameProfileRequest true "new_name"
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.APIResponse{data=ProfileRenameData} "Renamed; moved lists the clients and tokens that followed"
// @Failure 400 {object} ClientBindingErrorResponse "Invalid input; field names the offending input"
// @Failure 403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure 404 {object} contracts.ErrorResponse "profile not found"
// @Failure 409 {object} ProfileConflictResponse "profile_exists, or binding_bypassable_without_auth (BindingGuardResponse)"
// @Router /api/v1/profiles/{name}/rename [post]
func (s *Server) handleRenameProfile(w http.ResponseWriter, r *http.Request) {
	var req RenameProfileRequest
	if err := decodeProfileBody(r, &req); err != nil {
		s.badProfileBody(w, r, err)
		return
	}
	if req.NewName == "" {
		s.writeClientBindingError(w, r, http.StatusBadRequest, "", "new_name", `"new_name" is required`)
		return
	}
	res, err := s.profiles().Rename(r.Context(), actorFromRequest(r), chi.URLParam(r, "name"), req.NewName)
	if err != nil {
		s.writeProfileServiceError(w, r, err)
		return
	}
	s.writeSuccess(w, res)
}

// handleDeleteProfile godoc
// @Summary Delete a profile
// @Description Deletes a profile. 409 profile_in_use (with used_by) while clients or tokens point at it, unless reassign_to names another existing profile (every pin moves there) or force is set (pins are left dangling, which the resolver treats as deny-all). 409 profile_is_anonymous_profile whenever it is the anonymous_profile and reassign_to is absent, even with force. Both paths remove the name from every switchable_to. Guarded by FR-008a. Writes one `delete` record.
// @Tags profiles
// @Produce json
// @Param name path string true "Profile name"
// @Param reassign_to query string false "Another existing profile that takes over the pins"
// @Param force query boolean false "Delete although in use, leaving pins dangling"
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.APIResponse{data=ProfileDeleteData} "Deleted"
// @Failure 400 {object} ClientBindingErrorResponse "reassign_to does not name another existing profile"
// @Failure 403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure 404 {object} contracts.ErrorResponse "profile not found"
// @Failure 409 {object} ProfileConflictResponse "profile_in_use, profile_is_anonymous_profile, or binding_bypassable_without_auth (BindingGuardResponse)"
// @Router /api/v1/profiles/{name} [delete]
func (s *Server) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	force := false
	if raw := r.URL.Query().Get("force"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			s.writeClientBindingError(w, r, http.StatusBadRequest, "", "force", `"force" must be true or false`)
			return
		}
		force = v
	}
	res, err := s.profiles().Delete(r.Context(), actorFromRequest(r), chi.URLParam(r, "name"), r.URL.Query().Get("reassign_to"), force)
	if err != nil {
		s.writeProfileServiceError(w, r, err)
		return
	}
	s.writeSuccess(w, res)
}

// TryProfileRequest is the body of POST /profiles/try.
type TryProfileRequest struct {
	Profile config.ProfileConfig `json:"profile"`
	Query   string               `json:"query"`
	Limit   int                  `json:"limit,omitempty"`
}

// handleTryProfile godoc
// @Summary Try a draft profile
// @Description Evaluates a DRAFT profile against a query exactly as retrieve_tools would (policy is applied before the limit) and returns the hits plus what the draft hides (hidden_by_profile and up to 100 {server, tool, reason}). The draft is validated as if it replaced the same-named profile, or was appended under a placeholder name when it has none. Nothing is persisted, no record is written, no event is published and the FR-008a guard does not run. Administrators only.
// @Tags profiles
// @Accept json
// @Produce json
// @Param body body TryProfileRequest true "draft profile, query and optional limit (default 10, max 50)"
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.APIResponse{data=ProfileTryData} "Search result under the draft"
// @Failure 400 {object} ClientBindingErrorResponse "Invalid draft; field names the offending input"
// @Failure 403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure 503 {object} contracts.ErrorResponse "Service unavailable"
// @Router /api/v1/profiles/try [post]
func (s *Server) handleTryProfile(w http.ResponseWriter, r *http.Request) {
	var req TryProfileRequest
	if err := decodeProfileBody(r, &req); err != nil {
		s.badProfileBody(w, r, err)
		return
	}
	if req.Query == "" {
		s.writeClientBindingError(w, r, http.StatusBadRequest, "", "query", `"query" is required`)
		return
	}
	res, err := s.profiles().Try(r.Context(), req.Profile, req.Query, req.Limit)
	if err != nil {
		s.writeProfileServiceError(w, r, err)
		return
	}
	s.writeSuccess(w, res)
}

// handleProfileEffectiveTools godoc
// @Summary Effective tools of a profile
// @Description One row per catalog tool with the verdict of the access chain (the same rendering GET /tools?profile= uses): server, tool, intrinsic_tier, profile_tier, access{visible, callable, reason} and classification_stale. With client=<id> the client's credential is evaluated UNDER this profile ("what would Cursor get here"). An administrator gets every row plus counts.callable, counts.by_reason and stale_classifications; every other caller gets only visible rows and counts{visible, hidden}, and client= and reason= are refused 403. An unreachable profile answers exactly like an unknown one.
// @Tags profiles
// @Produce json
// @Param name path string true "Profile name"
// @Param client query string false "Evaluate this client's credential under the profile (administrators only)"
// @Param server query string false "Keep only this server's rows"
// @Param reason query string false "Keep only rows with this access reason (administrators only)"
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.APIResponse{data=EffectiveToolsData} "Effective tools"
// @Failure 403 {object} contracts.ErrorResponse "operation requires admin access"
// @Failure 404 {object} contracts.ErrorResponse "profile not found / client not found"
// @Failure 503 {object} contracts.ErrorResponse "Service unavailable"
// @Router /api/v1/profiles/{name}/effective-tools [get]
func (s *Server) handleProfileEffectiveTools(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := chi.URLParam(r, "name")
	q := r.URL.Query()
	admin := auth.AuthorizeServerOp(auth.AuthContextFromContext(ctx), auth.ServerOpConfigWrite)

	cfg, err := s.controller.GetConfig()
	if err != nil || cfg == nil {
		// The server edition has no client credentials at all: a client= that
		// could never resolve is a 404 whatever the configuration says.
		if admin && q.Get("client") != "" && !clientRoutesSupported {
			s.writeError(w, r, http.StatusNotFound, errClientNotFound)
			return
		}
		s.writeError(w, r, http.StatusInternalServerError, "Configuration unavailable")
		return
	}
	if !admin {
		// One lookup, then the reachability check (uniform 404 for unknown and
		// unreachable), before anything about the request is revealed.
		if !profileReachable(ctx, cfg, name) {
			s.writeError(w, r, http.StatusNotFound, errProfileNotFound)
			return
		}
		if q.Get("client") != "" || q.Get("reason") != "" {
			s.writeError(w, r, http.StatusForbidden, "operation requires admin access")
			return
		}
	}
	if client := q.Get("client"); client != "" && !clientRoutesSupported {
		s.writeError(w, r, http.StatusNotFound, errClientNotFound)
		return
	}
	opt := internalRuntime.EffectiveToolsOptions{
		Client: q.Get("client"), Server: q.Get("server"), Reason: q.Get("reason"),
	}
	if !admin {
		opt.Viewer = viewerScope(ctx)
		opt.Viewer.Restricted = true
		if opt.Viewer.Visible == nil {
			opt.Viewer.Visible = func(server string) bool { return canSeeServer(ctx, server) }
		}
	}
	res, err := s.profiles().EffectiveTools(ctx, name, opt)
	if err != nil {
		s.writeProfileServiceError(w, r, err)
		return
	}
	s.writeSuccess(w, res)
}
