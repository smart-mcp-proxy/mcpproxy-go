//go:build !server

package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
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

// ClientBindingView is a client's credential as returned by binding writes.
type ClientBindingView struct {
	ID              string                  `json:"id"`
	TokenName       string                  `json:"token_name"`
	Profile         string                  `json:"profile"`
	Mode            string                  `json:"mode"`
	CredentialState profile.CredentialState `json:"credential_state"`
}

// ClientBindingResponse is the data of a successful binding write.
type ClientBindingResponse struct {
	Client   ClientBindingView         `json:"client"`
	Warnings []internalRuntime.Warning `json:"warnings"`
}

// ClientBindingErrorResponse is the 409/400 body of a refused binding write:
// `code` is no_client_credential (409), `field` names the offending input
// (400).
type ClientBindingErrorResponse struct {
	Success   bool   `json:"success"` // Always false
	Error     string `json:"error"`
	Code      string `json:"code,omitempty"`
	Field     string `json:"field,omitempty"`
	RequestID string `json:"request_id,omitempty"`
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
// @Success     200 {object} contracts.APIResponse{data=ClientBindingResponse} "The client's binding after the change"
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

	view, err := svc.SetBinding(r.Context(), actorFromRequest(r), clientID, *req.Profile, req.Mode)
	if err != nil {
		s.writeClientBindingFailure(w, r, err)
		return
	}
	s.writeSuccess(w, ClientBindingResponse{
		Client: ClientBindingView{
			ID: view.ID, TokenName: view.TokenName, Profile: view.Profile,
			Mode: view.Mode, CredentialState: view.CredentialState,
		},
		Warnings: []internalRuntime.Warning{},
	})
}

// writeClientBindingFailure maps a clients-service error to its wire shape.
func (s *Server) writeClientBindingFailure(w http.ResponseWriter, r *http.Request, err error) {
	if s.writeIfBindingGuardRefusal(w, r, err) {
		return
	}
	var noCred *internalRuntime.NoClientCredentialError
	if errors.As(err, &noCred) {
		s.writeClientBindingError(w, r, http.StatusConflict, noCred.Code(), "", noCred.Error())
		return
	}
	var val *internalRuntime.ValidationError
	if errors.As(err, &val) {
		s.writeClientBindingError(w, r, http.StatusBadRequest, "", val.Field, val.Message)
		return
	}
	s.logger.Errorw("client binding change failed", "error", err)
	s.writeError(w, r, http.StatusInternalServerError, "failed to change the client binding")
}

func (s *Server) writeClientBindingError(w http.ResponseWriter, r *http.Request, status int, code, field, msg string) {
	s.writeJSON(w, status, ClientBindingErrorResponse{
		Success: false, Error: msg, Code: code, Field: field,
		RequestID: reqcontext.GetRequestID(r.Context()),
	})
}
