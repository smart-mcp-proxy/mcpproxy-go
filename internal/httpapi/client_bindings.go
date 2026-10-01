//go:build !server

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// PutClientBindingRequest is the body of PUT /api/v1/clients/{client}/binding.
// Profile is required ("" is the built-in All servers scope). Mode is
// optional: omitted keeps the credential's current mode, except that profile
// "" means switchable.
type PutClientBindingRequest struct {
	Profile *string `json:"profile"`
	Mode    *string `json:"mode,omitempty"`
}

// ClientBindingResponse is the data of a successful binding write: the full
// decorated client row and the warnings that concern it (Spec 108-f F17).
type ClientBindingResponse struct {
	Client   clientPresence  `json:"client"`
	Warnings []ClientWarning `json:"warnings"`
}

// clientViewAndWarnings returns the decorated row of one client (list
// semantics: no config is read) and the warnings about it: the instance
// warnings naming the client plus the global binding-guard warning.
func (s *Server) clientViewAndWarnings(ctx context.Context, clientID string) (*clientPresence, []internalRuntime.Warning) {
	rows, warnings, err := s.clientRows(ctx, false, clientID)
	if err != nil {
		return nil, []internalRuntime.Warning{}
	}
	var view *clientPresence
	for i := range rows {
		if rows[i].ID == clientID {
			view = &rows[i]
			break
		}
	}
	mine := []internalRuntime.Warning{}
	for _, w := range warnings {
		if w.ClientID == clientID || w.Code == profile.WarningAnonymousDeniedByBindingGuard {
			mine = append(mine, w)
		}
	}
	return view, mine
}

// handlePutClientBinding godoc
// @Summary     Reassign a client's profile and/or mode
// @Description Changes the profile binding (and optionally the locked/switchable mode) of a
// @Description client that holds an active client credential. One service operation shared by
// @Description every surface: it updates the credential in the token store, clears the stored
// @Description set_profile selection of every live session of that credential, sends
// @Description notifications/tools/list_changed to each, emits client.binding_changed and writes
// @Description one profile_change activity record. It never touches the client's config file.
// @Description A client without an active client credential (none, admin key, revoked or expired)
// @Description is refused with 409 no_client_credential and nothing is minted or written. A
// @Description reassignment that would leave the binding bypassable while require_mcp_auth is off
// @Description is refused with 409 binding_bypassable_without_auth (FR-008a). Personal edition only.
// @Tags        clients
// @Accept      json
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       client path string                  true "Client id (lower-case letters, digits, '-' or '_', at most 56 characters)"
// @Param       body   body PutClientBindingRequest true "profile (required; empty = All servers) and optional mode (locked|switchable)"
// @Success     200 {object} contracts.APIResponse{data=ClientBindingResponse} "The client row after the change, with its warnings"
// @Failure     400 {object} ClientBindingErrorResponse "Invalid input; field names the offending input"
// @Failure     403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure     409 {object} BindingGuardResponse "no_client_credential, or binding_bypassable_without_auth with bindings and fixes"
// @Failure     503 {object} contracts.ErrorResponse "Service unavailable"
// @Router      /api/v1/clients/{client}/binding [put]
func (s *Server) handlePutClientBinding(w http.ResponseWriter, r *http.Request) {
	svc := s.clientsService
	if svc == nil {
		s.writeError(w, r, http.StatusServiceUnavailable, "client service not available")
		return
	}
	clientID := chi.URLParam(r, "client")
	if !auth.ValidClientID(clientID) {
		s.writeClientBindingError(w, r, http.StatusBadRequest, "", "id",
			fmt.Sprintf("invalid client id %q", clientID))
		return
	}

	var req PutClientBindingRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		msg := "invalid request body: " + err.Error()
		if errors.Is(err, io.EOF) {
			msg = `invalid request body: "profile" is required (use "" for All servers)`
		}
		s.writeClientBindingError(w, r, http.StatusBadRequest, "", "profile", msg)
		return
	}
	if req.Profile == nil {
		s.writeClientBindingError(w, r, http.StatusBadRequest, "", "profile", `"profile" is required (use "" for All servers)`)
		return
	}

	if _, err := svc.SetBinding(r.Context(), actorFromRequest(r), clientID, *req.Profile, req.Mode); err != nil {
		s.writeClientBindingFailure(w, r, err)
		return
	}
	view, warnings := s.clientViewAndWarnings(r.Context(), clientID)
	if view == nil {
		s.writeError(w, r, http.StatusInternalServerError, "failed to read the client after the change")
		return
	}
	s.writeSuccess(w, ClientBindingResponse{Client: *view, Warnings: warnings})
}

// writeClientBindingFailure maps a clients-service error to its wire shape.
func (s *Server) writeClientBindingFailure(w http.ResponseWriter, r *http.Request, err error) {
	if s.writeProfilesError(w, r, err) {
		return
	}
	s.logger.Errorw("client operation failed", "error", err)
	s.writeError(w, r, http.StatusInternalServerError, "the client operation failed")
}

// --- custom client add -----------------------------------------------------------

// CreateClientRequest is the body of POST /api/v1/clients.
type CreateClientRequest struct {
	ID          string  `json:"id"`
	DisplayName string  `json:"display_name,omitempty"`
	Profile     string  `json:"profile,omitempty"`
	Mode        *string `json:"mode,omitempty"`
	// ExpiresIn is a duration like 30d or 720h; default and cap are 365 days.
	ExpiresIn string `json:"expires_in,omitempty"`
}

// ClientSnippet is the paste-ready config of a custom client. The credential is
// always a header, never a query parameter.
type ClientSnippet struct {
	GenericHTTP string `json:"generic_http"`
	HeaderName  string `json:"header_name"`
}

// CreateClientResponse is the data of a successful POST /clients. credential is
// the secret, shown ONCE.
type CreateClientResponse struct {
	Client     clientPresence `json:"client"`
	Credential string         `json:"credential"`
	Snippet    ClientSnippet  `json:"snippet"`
}

// clientCredentialHeader is the header a client credential travels in.
const clientCredentialHeader = "X-API-Key"

// parseClientExpiry is auth.ParseTokenExpiry with the client-credential
// default: 365 days when omitted (FR-021), capped at 365 days.
func parseClientExpiry(now time.Time, expiresIn string) (time.Time, error) {
	if expiresIn == "" {
		return now.Add(auth.MaxTokenExpiry), nil
	}
	return auth.ParseTokenExpiry(expiresIn, now)
}

// mcpEndpointURL is this instance's MCP endpoint for a snippet.
func (s *Server) mcpEndpointURL() string {
	addr := "127.0.0.1:8080"
	if cfg, err := s.controller.GetConfig(); err == nil && cfg != nil && cfg.Listen != "" {
		addr = cfg.Listen
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	return "http://" + addr + "/mcp"
}

func (s *Server) clientSnippet(credential string) ClientSnippet {
	type entry struct {
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	}
	doc := struct {
		MCPServers map[string]entry `json:"mcpServers"`
	}{MCPServers: map[string]entry{"mcpproxy": {URL: s.mcpEndpointURL(), Headers: map[string]string{clientCredentialHeader: credential}}}}
	b, _ := json.Marshal(doc)
	return ClientSnippet{GenericHTTP: string(b), HeaderName: clientCredentialHeader}
}

// handleCreateClient godoc
// @Summary     Add a custom client
// @Description Creates a per-client credential for a client that is NOT in the connect registry (a script, a CI job, an editor without a connect adapter). The secret (mcp_cli_...) is returned ONCE, with a paste-ready snippet that carries it in the X-API-Key header. The id follows the client-id rule (lower-case letters, digits, '-' or '_', at most 56 characters) and must not be a supported client's id. A named profile binds the credential locked unless mode says otherwise. Refused with 409 binding_bypassable_without_auth when the binding would be bypassable while require_mcp_auth is off (FR-008a), and 409 conflicting_token when a regular token holds client-<id>. Writes one assign record. Personal edition only.
// @Tags        clients
// @Accept      json
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       body body CreateClientRequest true "id (required), optional display_name, profile, mode and expires_in"
// @Success     201 {object} contracts.APIResponse{data=CreateClientResponse} "The new client row, its credential (once) and a snippet"
// @Failure     400 {object} ClientBindingErrorResponse "Invalid input; field names the offending input"
// @Failure     403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure     409 {object} BindingGuardResponse "binding_bypassable_without_auth, or conflicting_token (ClientCredentialConflictResponse)"
// @Router      /api/v1/clients [post]
func (s *Server) handleCreateClient(w http.ResponseWriter, r *http.Request) {
	svc := s.clientsService
	if svc == nil {
		s.writeError(w, r, http.StatusServiceUnavailable, "client service not available")
		return
	}
	var req CreateClientRequest
	if err := decodeProfileBody(r, &req); err != nil {
		s.badProfileBody(w, r, err)
		return
	}
	now := time.Now().UTC()
	expiresAt, err := parseClientExpiry(now, req.ExpiresIn)
	if err != nil {
		s.writeClientBindingError(w, r, http.StatusBadRequest, "", "expires_in", err.Error())
		return
	}
	_, secret, err := svc.Add(r.Context(), actorFromRequest(r), internalRuntime.AddRequest{
		ID: req.ID, DisplayName: req.DisplayName, Profile: req.Profile, Mode: req.Mode, ExpiresAt: expiresAt,
	})
	if err != nil {
		if s.writeCredentialFailure(w, r, req.ID, err) {
			return
		}
		s.writeClientBindingFailure(w, r, err)
		return
	}
	view, _ := s.clientViewAndWarnings(r.Context(), req.ID)
	if view == nil {
		s.writeError(w, r, http.StatusInternalServerError, "failed to read the client after adding it")
		return
	}
	s.writeJSON(w, http.StatusCreated, contracts.NewSuccessResponse(CreateClientResponse{
		Client: *view, Credential: secret, Snippet: s.clientSnippet(secret),
	}))
}

// --- rotate / finalize -----------------------------------------------------------

// RotateClientRequest is the optional body of POST /clients/{client}/rotate.
type RotateClientRequest struct {
	// PreconditionToken is the token of GET /connect/{client}/preview; it
	// binds a supported client's rotation to what the preview showed.
	PreconditionToken string `json:"precondition_token,omitempty"`
}

// RotationState is the `rotation` object of a rotate response.
type RotationState struct {
	State string `json:"state"`
}

// RotateClientResponse is the data of POST /clients/{client}/rotate. For a
// supported client Connect holds the write result and the rotation is already
// finalized; for a custom client Credential carries the new secret ONCE, with a
// snippet, and the rotation stays pending until finalized (or 24 h pass).
type RotateClientResponse struct {
	Client     clientPresence         `json:"client"`
	Connect    *connect.ConnectResult `json:"connect,omitempty"`
	Credential string                 `json:"credential,omitempty"`
	Snippet    *ClientSnippet         `json:"snippet,omitempty"`
	Rotation   RotationState          `json:"rotation"`
}

// handleRotateClient godoc
// @Summary     Rotate a client's credential
// @Description Replaces a client's secret without ever invalidating the old one mid-flight (FR-021a). A SUPPORTED client is rewritten through connect: staged rotation, binding kept, finalized when the config write succeeds, rolled back (the old secret keeps working) when it fails; a preview's precondition_token binds the call, a mismatch is the connect 409 precondition_failed. A CUSTOM client gets a new secret returned ONCE with a snippet; both secrets authenticate until POST /clients/{client}/rotate/finalize or 24 hours. A client with no active credential is 409 no_client_credential. The FR-008a guard is not involved: a rotation never changes a binding's reach. Writes one rotate record when it finalizes.
// @Tags        clients
// @Accept      json
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       client path string              true  "Client id"
// @Param       body   body RotateClientRequest false "Optional precondition_token of a connect preview"
// @Success     200 {object} contracts.APIResponse{data=RotateClientResponse} "The client row and the rotation state"
// @Failure     400 {object} ClientBindingErrorResponse "Invalid input"
// @Failure     403 {object} contracts.ErrorResponse "Administrator credentials required (or macOS App-Data block)"
// @Failure     409 {object} ConnectConflictResponse "no_client_credential, or precondition_failed"
// @Failure     503 {object} contracts.ErrorResponse "Service unavailable"
// @Router      /api/v1/clients/{client}/rotate [post]
func (s *Server) handleRotateClient(w http.ResponseWriter, r *http.Request) {
	svc := s.clientsService
	if svc == nil {
		s.writeError(w, r, http.StatusServiceUnavailable, "client service not available")
		return
	}
	clientID := chi.URLParam(r, "client")
	if !auth.ValidClientID(clientID) {
		s.writeClientBindingError(w, r, http.StatusBadRequest, "", "id", fmt.Sprintf("invalid client id %q", clientID))
		return
	}
	var req RotateClientRequest
	if err := decodeOptionalJSONBody(r, &req); err != nil {
		s.writeClientBindingError(w, r, http.StatusBadRequest, "", "", "invalid request body: "+err.Error())
		return
	}
	actor := actorFromRequest(r)

	cred, err := svc.Get(clientID)
	if err != nil {
		s.writeClientBindingFailure(w, r, err)
		return
	}
	if cred == nil || cred.CredentialState != profile.CredentialStateClient {
		state := profile.CredentialStateNone
		if cred != nil {
			state = cred.CredentialState
		}
		s.writeClientBindingFailure(w, r, &internalRuntime.NoClientCredentialError{ClientID: clientID, State: state})
		return
	}

	def := connect.FindClient(clientID)
	if def != nil && def.Supported {
		connectSvc := s.getConnectService()
		if connectSvc == nil {
			s.writeError(w, r, http.StatusServiceUnavailable, "connect service not available")
			return
		}
		s.reconcileClient(r, clientID)
		result, err := connectSvc.ConnectWithOptions(clientID, "", connect.ConnectOptions{
			Force: true, PreconditionToken: req.PreconditionToken,
			Intent: connect.CredentialIntent{ActorKind: actor.Kind, ActorName: actor.Name, Surface: string(actor.Surface)},
		})
		if err != nil {
			if s.writeIfAccessDenied(w, r, err) || s.writeCredentialFailure(w, r, clientID, err) {
				return
			}
			s.writeError(w, r, http.StatusBadRequest, err.Error())
			return
		}
		if !result.Success && (result.Action == "already_exists" || result.Action == "precondition_failed") {
			s.writeJSON(w, http.StatusConflict, ConnectConflictResponse{Success: false, Data: *result, Error: result.Message, Action: result.Action})
			return
		}
		s.recordCredentialObservation(clientID, profile.CredentialStateClient)
		view, _ := s.clientViewAndWarnings(r.Context(), clientID)
		if view == nil {
			s.writeError(w, r, http.StatusInternalServerError, "failed to read the client after the rotation")
			return
		}
		state := result.Rotation
		if state == "" {
			state = profile.RotationFinalized
		}
		s.writeSuccess(w, RotateClientResponse{Client: *view, Connect: result, Rotation: RotationState{State: state}})
		return
	}

	secret, _, err := svc.Rotate(r.Context(), actor, clientID)
	if err != nil {
		s.writeClientBindingFailure(w, r, err)
		return
	}
	view, _ := s.clientViewAndWarnings(r.Context(), clientID)
	if view == nil {
		s.writeError(w, r, http.StatusInternalServerError, "failed to read the client after the rotation")
		return
	}
	snippet := s.clientSnippet(secret)
	s.writeSuccess(w, RotateClientResponse{Client: *view, Credential: secret, Snippet: &snippet, Rotation: RotationState{State: profile.RotationPending}})
}

// FinalizeClientResponse is the data of POST /clients/{client}/rotate/finalize.
type FinalizeClientResponse struct {
	Client   clientPresence `json:"client"`
	Rotation RotationState  `json:"rotation"`
}

// handleFinalizeClientRotation godoc
// @Summary     Finalize a staged rotation
// @Description Promotes a client's pending secret: the old secret stops authenticating. Idempotent: with no rotation in progress it is a no-op success. A client with no client credential is 409 no_client_credential. Writes one rotate record when it promotes.
// @Tags        clients
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       client path string true "Client id"
// @Success     200 {object} contracts.APIResponse{data=FinalizeClientResponse} "The client row; rotation.state is finalized"
// @Failure     403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure     409 {object} ClientBindingErrorResponse "no_client_credential"
// @Router      /api/v1/clients/{client}/rotate/finalize [post]
func (s *Server) handleFinalizeClientRotation(w http.ResponseWriter, r *http.Request) {
	svc := s.clientsService
	if svc == nil {
		s.writeError(w, r, http.StatusServiceUnavailable, "client service not available")
		return
	}
	clientID := chi.URLParam(r, "client")
	if _, err := svc.FinalizeRotation(r.Context(), actorFromRequest(r), clientID); err != nil {
		s.writeClientBindingFailure(w, r, err)
		return
	}
	view, _ := s.clientViewAndWarnings(r.Context(), clientID)
	if view == nil {
		s.writeError(w, r, http.StatusInternalServerError, "failed to read the client after the finalize")
		return
	}
	s.writeSuccess(w, FinalizeClientResponse{Client: *view, Rotation: RotationState{State: profile.RotationFinalized}})
}

// --- forget ------------------------------------------------------------------------

// ForgetClientResponse is the data of DELETE /clients/{client}.
type ForgetClientResponse struct {
	Revoked         string `json:"revoked"`
	Disconnected    bool   `json:"disconnected"`
	DisconnectError string `json:"disconnect_error,omitempty"`
}

// handleForgetClient godoc
// @Summary     Forget a client (revoke its credential)
// @Description Revokes the client's credential; it stops authenticating at once. With disconnect=true a SUPPORTED client's mcpproxy entry is removed from its config FIRST, but the credential is revoked either way: revocation never waits for a config write, so a failed write (for example a macOS App-Data denial) leaves the client cut off and is reported as disconnected:false with disconnect_error. A client with no credential record is 409 no_client_credential. disconnect=true on a custom client is ignored. Writes one forget record naming the revoked token. The FR-008a guard is not involved: revoking only removes a binding.
// @Tags        clients
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       client     path  string  true  "Client id"
// @Param       disconnect query boolean false "Also remove the entry from the client's config (supported clients)"
// @Success     200 {object} contracts.APIResponse{data=ForgetClientResponse} "Revoked token name and what happened to the config entry"
// @Failure     403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure     409 {object} ClientBindingErrorResponse "no_client_credential"
// @Router      /api/v1/clients/{client} [delete]
func (s *Server) handleForgetClient(w http.ResponseWriter, r *http.Request) {
	svc := s.clientsService
	if svc == nil {
		s.writeError(w, r, http.StatusServiceUnavailable, "client service not available")
		return
	}
	clientID := chi.URLParam(r, "client")
	if !auth.ValidClientID(clientID) {
		s.writeClientBindingError(w, r, http.StatusBadRequest, "", "id", fmt.Sprintf("invalid client id %q", clientID))
		return
	}
	disconnect := false
	if raw := r.URL.Query().Get("disconnect"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			s.writeClientBindingError(w, r, http.StatusBadRequest, "", "disconnect", `"disconnect" must be true or false`)
			return
		}
		disconnect = v
	}
	cred, err := svc.Get(clientID)
	if err != nil {
		s.writeClientBindingFailure(w, r, err)
		return
	}
	if cred == nil {
		s.writeClientBindingFailure(w, r, &internalRuntime.NoClientCredentialError{ClientID: clientID, State: profile.CredentialStateNone})
		return
	}

	resp := ForgetClientResponse{}
	if def := connect.FindClient(clientID); disconnect && def != nil && def.Supported {
		if connectSvc := s.getConnectService(); connectSvc == nil {
			resp.DisconnectError = "connect service not available"
		} else if res, derr := connectSvc.Disconnect(clientID, ""); derr != nil {
			resp.DisconnectError = derr.Error()
		} else if !res.Success && res.Action != "not_found" {
			resp.DisconnectError = res.Message
		} else {
			resp.Disconnected = res.Success
			if res.Success {
				s.recordClientDisconnected(clientID)
			}
		}
	}
	view, err := svc.Forget(r.Context(), actorFromRequest(r), clientID, resp.Disconnected)
	if err != nil {
		s.writeClientBindingFailure(w, r, err)
		return
	}
	resp.Revoked = view.TokenName
	s.writeSuccess(w, resp)
}

// --- bulk assign ---------------------------------------------------------------------

// BulkAssignRequest is the body of POST /clients/bulk-assign.
type BulkAssignRequest struct {
	FromProfile *string `json:"from_profile"`
	ToProfile   *string `json:"to_profile"`
	Mode        *string `json:"mode,omitempty"`
}

// BulkAssignResponse is the data of POST /clients/bulk-assign.
type BulkAssignResponse struct {
	Moved   []string            `json:"moved"`
	Skipped []BulkAssignSkipped `json:"skipped"`
}

// handleBulkAssignClients godoc
// @Summary     Move every client of one profile to another
// @Description Moves every client credential bound to from_profile ("" = All servers) onto to_profile ("" = All servers). The guard and the credential precondition apply PER CLIENT: a refused client is reported in skipped[] with the code the single operation returns, the others move and each writes its own assign record and emits client.binding_changed. Personal edition only.
// @Tags        clients
// @Accept      json
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       body body BulkAssignRequest true "from_profile and to_profile (both required; empty = All servers) and optional mode"
// @Success     200 {object} contracts.APIResponse{data=BulkAssignResponse} "Moved client ids and the clients that were skipped"
// @Failure     400 {object} ClientBindingErrorResponse "Invalid input; field names the offending input"
// @Failure     403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Router      /api/v1/clients/bulk-assign [post]
func (s *Server) handleBulkAssignClients(w http.ResponseWriter, r *http.Request) {
	svc := s.clientsService
	if svc == nil {
		s.writeError(w, r, http.StatusServiceUnavailable, "client service not available")
		return
	}
	var req BulkAssignRequest
	if err := decodeProfileBody(r, &req); err != nil {
		s.badProfileBody(w, r, err)
		return
	}
	if req.FromProfile == nil {
		s.writeClientBindingError(w, r, http.StatusBadRequest, "", "from_profile", `"from_profile" is required (use "" for All servers)`)
		return
	}
	if req.ToProfile == nil {
		s.writeClientBindingError(w, r, http.StatusBadRequest, "", "to_profile", `"to_profile" is required (use "" for All servers)`)
		return
	}
	if cfg, err := s.controller.GetConfig(); err == nil && cfg != nil && *req.ToProfile != "" && !configHasProfile(cfg, *req.ToProfile) {
		s.writeClientBindingError(w, r, http.StatusBadRequest, "", "to_profile", fmt.Sprintf("unknown profile %q", *req.ToProfile))
		return
	}
	moved, skipped, err := svc.BulkAssign(r.Context(), actorFromRequest(r), *req.FromProfile, *req.ToProfile, req.Mode)
	if err != nil {
		s.writeClientBindingFailure(w, r, err)
		return
	}
	if moved == nil {
		moved = []string{}
	}
	if skipped == nil {
		skipped = []internalRuntime.Skipped{}
	}
	s.writeSuccess(w, BulkAssignResponse{Moved: moved, Skipped: skipped})
}

func configHasProfile(cfg *config.Config, name string) bool {
	for i := range cfg.Profiles {
		if cfg.Profiles[i].Name == name {
			return true
		}
	}
	return false
}

// --- admin-key upgrade ---------------------------------------------------------------------

// UpgradeAdminKeyHoldersRequest is the body of POST /clients/upgrade-admin-key-holders.
type UpgradeAdminKeyHoldersRequest struct {
	Profile           *string `json:"profile,omitempty"`
	Mode              *string `json:"mode,omitempty"`
	Apply             bool    `json:"apply,omitempty"`
	PreconditionToken string  `json:"precondition_token,omitempty"`
}

// handleUpgradeAdminKeyHolders godoc
// @Summary     Upgrade every client that holds the admin API key
// @Description Without apply: classifies every supported client whose config still holds the instance admin API key (an on-demand read per client) and returns preview[] (display path, what would change, the masked credential mcp_cli_••••, the binding and a precondition_token per client), a combined precondition_token, and guard when the whole request would be refused by FR-008a; next_step is rotate_admin_api_key when nothing holds the key. Nothing is written or minted. With apply=true: recomputes the preview, refuses with 409 precondition_failed when a sent precondition_token no longer matches, runs the FR-008a guard over the WHOLE request (409 binding_bypassable_without_auth, nothing minted, no file written), then connects each client with force bound to its own token; one client's failure does not stop the others. Each mint writes its own assign record. The guard only applies when a named profile is given. Personal edition only.
// @Tags        clients
// @Accept      json
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       body body UpgradeAdminKeyHoldersRequest false "Optional profile and mode for every upgraded client; apply and precondition_token"
// @Success     200 {object} contracts.APIResponse "UpgradePreview (no apply) or UpgradeResult (apply)"
// @Failure     400 {object} ClientBindingErrorResponse "Invalid input; field names the offending input"
// @Failure     403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure     409 {object} BindingGuardResponse "precondition_failed, or binding_bypassable_without_auth"
// @Failure     503 {object} contracts.ErrorResponse "Service unavailable"
// @Router      /api/v1/clients/upgrade-admin-key-holders [post]
func (s *Server) handleUpgradeAdminKeyHolders(w http.ResponseWriter, r *http.Request) {
	svc := s.clientsService
	if svc == nil {
		s.writeError(w, r, http.StatusServiceUnavailable, "client service not available")
		return
	}
	var req UpgradeAdminKeyHoldersRequest
	if err := decodeOptionalJSONBody(r, &req); err != nil {
		s.writeClientBindingError(w, r, http.StatusBadRequest, "", "", "invalid request body: "+err.Error())
		return
	}
	ur := internalRuntime.UpgradeRequest{Profile: req.Profile, Mode: req.Mode, PreconditionToken: req.PreconditionToken}
	actor := actorFromRequest(r)
	if !req.Apply {
		out, err := svc.PreviewAdminKeyUpgrade(r.Context(), actor, ur)
		if err != nil {
			s.writeClientBindingFailure(w, r, err)
			return
		}
		s.writeSuccess(w, out)
		return
	}
	out, err := svc.ApplyAdminKeyUpgrade(r.Context(), actor, ur)
	if err != nil {
		s.writeClientBindingFailure(w, r, err)
		return
	}
	s.notifyClientPresenceChanged()
	s.writeSuccess(w, out)
}
