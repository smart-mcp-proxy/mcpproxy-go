//go:build !server

package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 132: the administrator surface of the browser login for MCP clients
// (contracts/rest-admin.md). Every route is administrator-only: an agent token
// or a client credential is refused with 403 before anything reaches the
// runtime service. The decisions (what a choice means, what the guard says,
// what the redirect carries) are the service's; the handlers decode, call it
// and map its typed refusals.

// maxOAuthApprovalBody bounds an approve / preview body.
const maxOAuthApprovalBody = 16 << 10

// registerOAuthLoginAdminRoutes mounts the sign-in request routes.
func (s *Server) registerOAuthLoginAdminRoutes(r chi.Router) {
	adminRead := s.requireAdminReadMiddleware("Admin credentials required to read sign-in requests")
	admin := func(h http.HandlerFunc) http.HandlerFunc { return s.requireServerOp(auth.ServerOpConfigWrite, h) }
	r.With(adminRead).Get("/oauth/authorizations", s.handleListOAuthAuthorizations)
	r.With(adminRead).Get("/oauth/authorizations/{id}", s.handleGetOAuthAuthorization)
	// exempt: a preview is read-only and writes nothing; approve runs the
	// FR-008a guard (OAuthLoginService.Approve) before it records the decision.
	r.Post("/oauth/authorizations/{id}/preview", admin(s.handlePreviewOAuthAuthorization))
	r.Post("/oauth/authorizations/{id}/approve", admin(s.handleApproveOAuthAuthorization)) // guarded: OAuthLoginService.Approve
	r.Post("/oauth/authorizations/{id}/deny", admin(s.handleDenyOAuthAuthorization))
}

// writeOAuthLoginError answers 404 feature_disabled (and ok=false) while
// browser login is off or no service is configured.
func (s *Server) oauthLoginEnabledOrWrite(w http.ResponseWriter, r *http.Request) (*internalRuntime.OAuthLoginService, bool) {
	svc := s.oauthLoginService
	if svc == nil {
		s.writeError(w, r, http.StatusServiceUnavailable, "browser login service not available")
		return nil, false
	}
	if !svc.Enabled() {
		s.writeClientBindingError(w, r, http.StatusNotFound, "feature_disabled", "", "browser login is turned off")
		return nil, false
	}
	return svc, true
}

// writeOAuthLoginFailure maps a service refusal to its wire shape: the guard
// refusal keeps its body (bindings and fixes), the typed refusals their code
// and status, everything else a generic 500.
func (s *Server) writeOAuthLoginFailure(w http.ResponseWriter, r *http.Request, err error) {
	var guard *internalRuntime.BindingGuardError
	if errors.As(err, &guard) {
		if s.writeProfilesError(w, r, err) {
			return
		}
	}
	if code, status, ok := internalRuntime.OAuthErrorStatus(err); ok {
		s.writeClientBindingError(w, r, status, code, "", err.Error())
		return
	}
	s.logger.Errorw("browser login operation failed", "error", err)
	s.writeError(w, r, http.StatusInternalServerError, "the sign-in operation failed")
}

// handleListOAuthAuthorizations godoc
// @Summary     List pending sign-in requests
// @Description Pending browser-login requests, newest first: id, a sanitised client label and the times. Used by the tray and Home. 404 feature_disabled while browser login is off. Administrator only.
// @Tags        clients
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Success     200 {object} contracts.APIResponse "Pending requests"
// @Failure     403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure     404 {object} ClientBindingErrorResponse "feature_disabled"
// @Router      /api/v1/oauth/authorizations [get]
func (s *Server) handleListOAuthAuthorizations(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.oauthLoginEnabledOrWrite(w, r)
	if !ok {
		return
	}
	s.writeSuccess(w, svc.ListPending())
}

// handleGetOAuthAuthorization godoc
// @Summary     Read one sign-in request
// @Description Everything the consent view needs: the reported (unverified) name, the redirect URI, the resource, how the address fixes the client, the profiles, whether a profile must be chosen, the previous login of the same registration and, for a fixed client that has a binding, the guard verdict. One uniform 404 for an unknown, expired, decided or cancelled id. Administrator only.
// @Tags        clients
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       id path string true "Request id"
// @Success     200 {object} contracts.APIResponse "The request"
// @Failure     403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure     404 {object} ClientBindingErrorResponse "request_not_found or feature_disabled"
// @Router      /api/v1/oauth/authorizations/{id} [get]
func (s *Server) handleGetOAuthAuthorization(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.oauthLoginEnabledOrWrite(w, r)
	if !ok {
		return
	}
	view, err := svc.Describe(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.writeOAuthLoginFailure(w, r, err)
		return
	}
	s.writeSuccess(w, view)
}

// OAuthNewClient is a client an approval creates (generic address).
type OAuthNewClient struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
}

// OAuthClientChoice is the client of an approval: an existing id, or a new one.
type OAuthClientChoice struct {
	ID  string          `json:"id,omitempty"`
	New *OAuthNewClient `json:"new,omitempty"`
}

// OAuthApprovalBody documents the body of preview and approve (it is decoded
// into runtime.ApprovalChoice, which has the same wire shape). profile "" means
// All servers; it is ignored when the client already has a binding, which the
// request then names with binding_revision.
type OAuthApprovalBody struct {
	Client          OAuthClientChoice `json:"client"`
	Profile         *string           `json:"profile,omitempty"`
	Mode            string            `json:"mode,omitempty"`
	BindingRevision string            `json:"binding_revision,omitempty"`
	ReplaceGrant    string            `json:"replace_grant,omitempty"`
}

func (s *Server) decodeOAuthApproval(w http.ResponseWriter, r *http.Request) (internalRuntime.ApprovalChoice, bool) {
	var choice internalRuntime.ApprovalChoice
	r.Body = http.MaxBytesReader(w, r.Body, maxOAuthApprovalBody)
	if err := json.NewDecoder(r.Body).Decode(&choice); err != nil {
		s.writeClientBindingError(w, r, http.StatusBadRequest, "", "body", "the body must be a JSON object")
		return choice, false
	}
	return choice, true
}

// handlePreviewOAuthAuthorization godoc
// @Summary     Preview a sign-in decision
// @Description Read-only: for a proposed client (existing or new), profile and mode, returns whether the choice is complete, the access sentence and the binding guard's verdict with its fixes. Writes nothing. The same 422 codes as approve for an invalid choice. Administrator only.
// @Tags        clients
// @Accept      json
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       id   path string true "Request id"
// @Param       body body OAuthApprovalBody true "client, profile, mode"
// @Success     200 {object} contracts.APIResponse "The preview"
// @Failure     403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure     404 {object} ClientBindingErrorResponse "request_not_found, profile_not_found or feature_disabled"
// @Failure     422 {object} ClientBindingErrorResponse "client_fixed, identity_exists, reserved_identity"
// @Router      /api/v1/oauth/authorizations/{id}/preview [post]
func (s *Server) handlePreviewOAuthAuthorization(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.oauthLoginEnabledOrWrite(w, r)
	if !ok {
		return
	}
	choice, ok := s.decodeOAuthApproval(w, r)
	if !ok {
		return
	}
	view, err := svc.Preview(r.Context(), chi.URLParam(r, "id"), choice)
	if err != nil {
		s.writeOAuthLoginFailure(w, r, err)
		return
	}
	s.writeSuccess(w, view)
}

// OAuthDecisionResponse is the data of approve and deny: where the browser goes
// next (the stored redirect URI with the code, or with error=access_denied).
type OAuthDecisionResponse struct {
	RedirectURL string `json:"redirect_url"`
}

// handleApproveOAuthAuthorization godoc
// @Summary     Approve a sign-in request
// @Description Records the decision and issues the code; the login itself is stored when the client exchanges the code. 409 already_decided, 409 binding_changed (the binding differs from binding_revision), 409 binding_bypassable_without_auth (with the guard's fixes), 422 profile_required, client_fixed, identity_exists, reserved_identity, 404 profile_not_found. Writes an approve record naming the administrator. Administrator only.
// @Tags        clients
// @Accept      json
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       id   path string true "Request id"
// @Param       body body OAuthApprovalBody true "client, profile, mode, binding_revision, replace_grant"
// @Success     200 {object} contracts.APIResponse{data=OAuthDecisionResponse} "Where the page navigates"
// @Failure     403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure     404 {object} ClientBindingErrorResponse "request_not_found, profile_not_found or feature_disabled"
// @Failure     409 {object} ClientBindingErrorResponse "already_decided, binding_changed or binding_bypassable_without_auth"
// @Failure     422 {object} ClientBindingErrorResponse "profile_required, client_fixed, identity_exists, reserved_identity"
// @Router      /api/v1/oauth/authorizations/{id}/approve [post]
func (s *Server) handleApproveOAuthAuthorization(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.oauthLoginEnabledOrWrite(w, r)
	if !ok {
		return
	}
	choice, ok := s.decodeOAuthApproval(w, r)
	if !ok {
		return
	}
	redirect, err := svc.Approve(r.Context(), chi.URLParam(r, "id"), choice)
	if err != nil {
		s.writeOAuthLoginFailure(w, r, err)
		return
	}
	s.writeSuccess(w, OAuthDecisionResponse{RedirectURL: redirect})
}

// handleDenyOAuthAuthorization godoc
// @Summary     Deny a sign-in request
// @Description The redirect carries error=access_denied. 409 already_decided. Writes a deny record naming the administrator. Administrator only.
// @Tags        clients
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       id path string true "Request id"
// @Success     200 {object} contracts.APIResponse{data=OAuthDecisionResponse} "Where the page navigates"
// @Failure     403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure     404 {object} ClientBindingErrorResponse "request_not_found or feature_disabled"
// @Failure     409 {object} ClientBindingErrorResponse "already_decided"
// @Router      /api/v1/oauth/authorizations/{id}/deny [post]
func (s *Server) handleDenyOAuthAuthorization(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.oauthLoginEnabledOrWrite(w, r)
	if !ok {
		return
	}
	redirect, err := svc.Deny(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.writeOAuthLoginFailure(w, r, err)
		return
	}
	s.writeSuccess(w, OAuthDecisionResponse{RedirectURL: redirect})
}

// --- logins on clients (T042) ----------------------------------------------------

// RevokeCredentialResponse is the data of the per-credential revokes.
type RevokeCredentialResponse struct {
	Changed bool `json:"changed"`
}

// handleRevokeClientLogin godoc
// @Summary     Revoke one browser login of a client
// @Description Revokes the login (reason admin): its access token stops authenticating on the next request, live sessions are told to re-list, and a revoke record names the administrator. Works while browser login is on and while it is off after use. An already revoked login answers changed:false; an unknown login (or one of another client) is 404. Administrator only.
// @Tags        clients
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       client   path string true "Client id"
// @Param       grant_id path string true "Login id"
// @Success     200 {object} contracts.APIResponse{data=RevokeCredentialResponse} "Whether the login changed"
// @Failure     403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure     404 {object} contracts.ErrorResponse "login not found"
// @Router      /api/v1/clients/{client}/logins/{grant_id} [delete]
func (s *Server) handleRevokeClientLogin(w http.ResponseWriter, r *http.Request) {
	svc := s.oauthLoginService
	if svc == nil {
		s.writeError(w, r, http.StatusServiceUnavailable, "browser login service not available")
		return
	}
	changed, err := svc.RevokeGrant(r.Context(), chi.URLParam(r, "client"), chi.URLParam(r, "grant_id"))
	switch {
	case errors.Is(err, internalRuntime.ErrOAuthGrantNotFound):
		s.writeError(w, r, http.StatusNotFound, "login not found")
	case err != nil:
		s.logger.Errorw("revoking a browser login failed", "error", err)
		s.writeError(w, r, http.StatusInternalServerError, "the revoke failed")
	default:
		s.writeSuccess(w, RevokeCredentialResponse{Changed: changed})
	}
}

// handleRevokeClientAPIKey godoc
// @Summary     Revoke a client's API key only
// @Description Revokes the client's mcp_cli_ credential alone: its browser logins and its binding stay. 404 no_client_credential when the client has no active API key. Writes a revoke record. Administrator only.
// @Tags        clients
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       client path string true "Client id"
// @Success     200 {object} contracts.APIResponse{data=RevokeCredentialResponse} "The key was revoked"
// @Failure     400 {object} ClientBindingErrorResponse "Invalid client id"
// @Failure     403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure     404 {object} ClientBindingErrorResponse "no_client_credential"
// @Router      /api/v1/clients/{client}/api-key [delete]
func (s *Server) handleRevokeClientAPIKey(w http.ResponseWriter, r *http.Request) {
	svc := s.clientsService
	if svc == nil {
		s.writeError(w, r, http.StatusServiceUnavailable, "client service not available")
		return
	}
	clientID := chi.URLParam(r, "client")
	if !auth.ValidClientID(clientID) {
		s.writeClientBindingError(w, r, http.StatusBadRequest, "", "id", "invalid client id")
		return
	}
	if _, err := svc.RevokeAPIKey(r.Context(), actorFromRequest(r), clientID); err != nil {
		var none *internalRuntime.NoClientCredentialError
		if errors.As(err, &none) {
			s.writeClientBindingError(w, r, http.StatusNotFound, profile.ErrorCodeNoClientCredential, "", "the client has no API key")
			return
		}
		s.writeClientBindingFailure(w, r, err)
		return
	}
	s.writeSuccess(w, RevokeCredentialResponse{Changed: true})
}

// contracts is referenced by the swagger annotations above (swag resolves the
// type through this file's imports).
var _ contracts.APIResponse
