package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// ConnectRequest is the optional JSON body for POST /api/v1/connect/{client}.
type ConnectRequest struct {
	ServerName string `json:"server_name,omitempty"` // Defaults to "mcpproxy"
	Force      bool   `json:"force,omitempty"`       // Overwrite existing entry
	// PreconditionToken is the opaque token from the preview this write was
	// confirmed against (Spec 091 FR-005). When present, the core rechecks it
	// at write time and responds 409 with action "precondition_failed" —
	// writing nothing — if the config or the entry MCPProxy would write has
	// drifted since; the caller then re-previews instead of retrying. Absent
	// means exactly the pre-091 behavior. A replace-classified flow sends this
	// TOGETHER with force=true: the token, not the absence of force, is the
	// overwrite safety.
	PreconditionToken string `json:"precondition_token,omitempty"`

	// Profile, Mode and Keyless are the client-credential intent (Spec 108
	// FR-024). Profile names the profile the client's credential binds to;
	// "" is the built-in All servers scope. Omitted means "not specified": a
	// fresh credential defaults to All servers and a reconnect keeps the
	// client's existing binding (a reconnect never silently widens a locked
	// client). Mode is locked|switchable (default locked for a named profile,
	// switchable for All servers). Keyless writes a credential-less entry and
	// is only possible while require_mcp_auth is off; it cannot carry a
	// profile or mode.
	Profile *string `json:"profile,omitempty"`
	Mode    *string `json:"mode,omitempty"`
	Keyless bool    `json:"keyless,omitempty"`
}

// ClientCredentialConflictResponse is the 409 body when the token name
// client-<id> is held by a grandfathered regular agent token: connect never
// touches it and mints nothing (FR-021).
type ClientCredentialConflictResponse struct {
	Success          bool   `json:"success"` // Always false
	Error            string `json:"error"`
	ConflictingToken string `json:"conflicting_token"`
	Remediation      string `json:"remediation"`
	RequestID        string `json:"request_id,omitempty"`
}

// ConnectConflictResponse is the 409 body of POST /api/v1/connect/{client}.
//
// It is a typed response rather than the generic error shape because Action is
// the machine-readable discriminator the contract depends on: "already_exists"
// means "an entry is there, pass force", "precondition_failed" means "your
// preview is stale, re-preview". A client that cannot tell them apart either
// loops forever or forces a write over state the user never saw (research D9),
// so the field must be visible in the OpenAPI document, not only in prose.
type ConnectConflictResponse struct {
	Success bool                  `json:"success"` // Always false
	Data    connect.ConnectResult `json:"data"`    // The full result; its action mirrors the top-level one
	Error   string                `json:"error"`   // Human-readable message
	Action  string                `json:"action"`  // already_exists | precondition_failed
}

// handleGetConnectStatus godoc
// @Summary     List client connection status
// @Description Returns the connection status for all known MCP client applications.
// @Description Each entry indicates whether the client config file exists and whether
// @Description MCPProxy is currently registered in it. This stat-only listing never reads
// @Description a client config (Spec 075), so a client whose config exists reports
// @Description credential_state "unknown"; GET /connect/{client} resolves it.
// @Tags        connect
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Success     200 {object} contracts.APIResponse "List of ClientStatus objects"
// @Failure     403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Router      /api/v1/connect [get]
func (s *Server) handleGetConnectStatus(w http.ResponseWriter, r *http.Request) {
	svc := s.getConnectService()
	if svc == nil {
		s.writeError(w, r, http.StatusServiceUnavailable, "connect service not available")
		return
	}
	statuses := svc.GetAllStatus()
	s.writeSuccess(w, statuses)
}

// handleGetConnectClientStatus godoc
// @Summary     Get a single client's connection status (on-demand)
// @Description Resolves one client's status by reading its config file on demand.
// @Description This is the only Connect endpoint that opens a client config file, so
// @Description on macOS it is the sole place an App-Data privacy prompt may legitimately
// @Description appear (scoped to this user action). Resolves access_state to
// @Description accessible|absent|denied|malformed and populates remediation when denied.
// @Description For a connected client it also resolves credential_state (Spec 108 FR-025):
// @Description client (an active per-client credential), admin_key (the instance admin API
// @Description key), none, revoked (a rotated-away or revoked credential) or expired. The
// @Description credential value is classified and never echoed.
// @Tags        connect
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       client path   string true "Client ID (claude-code, claude-desktop, cursor, windsurf, vscode, codex, gemini, opencode, zcode)"
// @Success     200    {object} contracts.APIResponse "ClientStatus"
// @Failure     403    {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure     404    {object} contracts.ErrorResponse "Unknown client"
// @Failure     503    {object} contracts.ErrorResponse "Service unavailable"
// @Router      /api/v1/connect/{client} [get]
func (s *Server) handleGetConnectClientStatus(w http.ResponseWriter, r *http.Request) {
	svc := s.getConnectService()
	if svc == nil {
		s.writeError(w, r, http.StatusServiceUnavailable, "connect service not available")
		return
	}

	clientID := chi.URLParam(r, "client")
	if clientID == "" {
		s.writeError(w, r, http.StatusBadRequest, "client ID is required")
		return
	}

	// A rotation interrupted by a crash is resolved from what the client's
	// config actually holds before it is classified (FR-021a). Best effort.
	s.reconcileClient(r, clientID)
	status, err := svc.GetStatus(clientID)
	if err != nil {
		// GetStatus only errors for an unknown client; a permission denial is
		// reported in-band via access_state=denied + remediation, not as an error.
		s.writeError(w, r, http.StatusNotFound, err.Error())
		return
	}
	// Every on-demand classification is remembered, so the stat-only clients
	// list can still report who holds the admin key (Spec 108-f F11).
	s.recordCredentialObservation(clientID, profile.CredentialState(status.CredentialState))
	s.writeSuccess(w, status)
}

// handleConnectClientPreview godoc
// @Summary     Preview the change a connect would make (no write)
// @Description Returns the exact entry a subsequent connect would add to the client's
// @Description config — target path, server key, entry name, and entry contents — WITHOUT
// @Description modifying the file or creating a backup (Spec 078 US1). The per-client credential
// @Description is masked in the payload (credential, always starting mcp_cli_; contains_api_key is
// @Description always false since connect never writes the admin API key); profile, mode and
// @Description keyless echo the requested intent.
// @Description entry_exists distinguishes a create from an overwrite of a same-named entry.
// @Description Reads the config on demand to classify create-vs-overwrite, so on macOS this
// @Description may raise an App-Data privacy prompt; a denial returns 403 + remediation.
// @Description Spec 091 adds three fields: existing_entry_summary (present only when
// @Description entry_exists — a sanitized, non-secret projection of the entry being replaced:
// @Description its name, type, endpoint with query/userinfo stripped, command, and header and
// @Description env NAMES, never values); precondition_token (always present — an opaque keyed
// @Description digest of the raw pre-write state and the pending entry, echoed back on POST
// @Description connect to detect drift); and connect_refusal (present when the write would
// @Description refuse regardless of intent, e.g. a non-create-capable client with no config —
// @Description treat its presence as "Connect unavailable").
// @Tags        connect
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       client      path  string true  "Client ID (claude-code, claude-desktop, cursor, windsurf, vscode, codex, gemini, opencode, zcode)"
// @Param       server_name query string false "Entry name to preview (defaults to mcpproxy); mirror the value passed to POST connect"
// @Param       profile     query string false "Profile the client credential would bind to (empty = All servers); mirror POST connect"
// @Param       mode        query string false "locked|switchable; mirror POST connect" Enums(locked, switchable)
// @Param       keyless     query bool   false "Preview a credential-less entry (only while require_mcp_auth is off)"
// @Success     200    {object} contracts.APIResponse "ConnectPreview"
// @Failure     400    {object} ClientBindingErrorResponse "Invalid intent (field names the input; keyless with auth on or with a profile)"
// @Failure     403    {object} contracts.ErrorResponse "Administrator credentials required or access denied by macOS App Data"
// @Failure     404    {object} contracts.ErrorResponse "Unknown client"
// @Failure     503    {object} contracts.ErrorResponse "Service unavailable"
// @Router      /api/v1/connect/{client}/preview [get]
func (s *Server) handleConnectClientPreview(w http.ResponseWriter, r *http.Request) {
	svc := s.getConnectService()
	if svc == nil {
		s.writeError(w, r, http.StatusServiceUnavailable, "connect service not available")
		return
	}

	clientID := chi.URLParam(r, "client")
	if clientID == "" {
		s.writeError(w, r, http.StatusBadRequest, "client ID is required")
		return
	}

	// Honor an optional server_name so a caller can preview the exact entry name
	// a subsequent POST connect (which accepts server_name) will write; defaults
	// to "mcpproxy" when omitted, matching Connect (Spec 078 FR-002).
	s.reconcileClient(r, clientID)
	preview, err := svc.PreviewWithIntent(clientID, r.URL.Query().Get("server_name"), previewIntent(r))
	if err != nil {
		if s.writeCredentialFailure(w, r, clientID, err) {
			return
		}
		// A macOS App-Data (TCC) denial during the on-demand read surfaces as
		// 403 + remediation, matching connect/disconnect (Spec 078 FR-012).
		if s.writeIfAccessDenied(w, r, err) {
			return
		}
		if connect.FindClient(clientID) == nil {
			s.writeError(w, r, http.StatusNotFound, err.Error())
			return
		}
		s.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	s.writeSuccess(w, preview)
}

// handleConnectClient godoc
// @Summary     Connect MCPProxy to a client
// @Description Register MCPProxy as an MCP server in the specified client's configuration file.
// @Description Creates a backup of the existing config before modifying.
// @Description Optionally accepts precondition_token from a preview (Spec 091): when supplied,
// @Description the core rechecks the raw pre-write state and the entry it would write, and
// @Description refuses a drifted write with 409 before taking any backup. The 409 body's
// @Description action discriminates the two conflict kinds: "precondition_failed" (stale
// @Description preview — re-preview, do not retry) vs "already_exists" (entry present — pass
// @Description force=true). force=true never rescues a stale token.
// @Description
// @Description Spec 108 FR-024: the entry carries a per-client mcp_cli_ credential bound to the
// @Description requested profile (default: All servers, switchable; a reconnect keeps the client's
// @Description existing binding unless a profile is given), never the instance admin API key.
// @Description Reconnecting over an active credential is a staged rotation: the old secret keeps
// @Description working until the config write succeeds. keyless=true writes no credential and is
// @Description only possible while require_mcp_auth is off (400 otherwise, and with a profile).
// @Description A write that would let the client escape its profile by omitting its credential
// @Description while require_mcp_auth is off is refused with 409 binding_bypassable_without_auth
// @Description (nothing minted or written); a token named client-<id> held by a regular agent
// @Description token is refused with 409 conflicting_token.
// @Tags        connect
// @Accept      json
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       client path   string         true  "Client ID (claude-code, claude-desktop, cursor, windsurf, vscode, codex, gemini, opencode, zcode)"
// @Param       body   body   ConnectRequest false "Optional connection parameters (server_name, force, precondition_token, profile, mode, keyless)"
// @Success     200    {object} contracts.APIResponse "ConnectResult (credential is the masked mcp_cli_ value, with token_name, profile, mode, keyless and rotation)"
// @Failure     400    {object} ClientBindingErrorResponse "Bad request; field names the offending input (profile, mode, keyless)"
// @Failure     403    {object} contracts.ErrorResponse "Permission denied (macOS App-Data block)"
// @Failure     404    {object} contracts.ErrorResponse "Unknown client"
// @Failure     409    {object} ConnectConflictResponse "Conflict: action=already_exists (use force=true) or action=precondition_failed (preview is stale; re-preview); or binding_bypassable_without_auth (BindingGuardResponse); or conflicting_token (ClientCredentialConflictResponse); or connect_in_progress (another connect of this client is mid-write) or credential_superseded (the written credential was replaced or revoked before it could be finalized; reconnect)"
// @Failure     503    {object} contracts.ErrorResponse "Service unavailable (or no credential store wired while require_mcp_auth is on)"
// @Router      /api/v1/connect/{client} [post]
func (s *Server) handleConnectClient(w http.ResponseWriter, r *http.Request) {
	svc := s.getConnectService()
	if svc == nil {
		s.writeError(w, r, http.StatusServiceUnavailable, "connect service not available")
		return
	}

	clientID := chi.URLParam(r, "client")
	if clientID == "" {
		s.writeError(w, r, http.StatusBadRequest, "client ID is required")
		return
	}

	var req ConnectRequest
	if err := decodeOptionalJSONBody(r, &req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return
	}

	s.reconcileClient(r, clientID)
	actor := actorFromRequest(r)
	result, err := svc.ConnectWithOptions(clientID, req.ServerName, connect.ConnectOptions{
		Force:             req.Force,
		PreconditionToken: req.PreconditionToken,
		Intent: connect.CredentialIntent{
			Profile: req.Profile, Mode: req.Mode, Keyless: req.Keyless,
			ActorKind: actor.Kind, ActorName: actor.Name, Surface: string(actor.Surface),
		},
	})
	if err != nil {
		// A macOS App-Data (TCC) denial surfaces as 403 carrying remediation,
		// distinct from a generic 400 or a 404 not-found (Spec 075 contract).
		if s.writeIfAccessDenied(w, r, err) {
			return
		}
		if s.writeCredentialFailure(w, r, clientID, err) {
			return
		}
		// Distinguish between "unknown client" and other errors
		client := connect.FindClient(clientID)
		if client == nil {
			s.writeError(w, r, http.StatusNotFound, err.Error())
			return
		}
		s.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	// Both 409 kinds carry their discriminator in `action` — mirrored at the top
	// level so a client can branch without unwrapping `data`. "already_exists"
	// means "an entry is there, pass force"; "precondition_failed" means "your
	// preview is stale, re-preview" (contracts §2). Conflating them would make a
	// replace flow either loop or force a write over unseen state.
	if !result.Success && (result.Action == "already_exists" || result.Action == "precondition_failed") {
		s.writeJSON(w, http.StatusConflict, ConnectConflictResponse{
			Success: false,
			Data:    *result,
			Error:   result.Message,
			Action:  result.Action,
		})
		return
	}

	if result.Success {
		s.recordClientConnected(clientID)
		switch {
		case result.Credential != "":
			s.recordCredentialObservation(clientID, profile.CredentialStateClient)
		case result.Keyless:
			s.recordCredentialObservation(clientID, profile.CredentialStateNone)
		}
	}

	s.writeSuccess(w, result)
}

// recordClientConnected records a successful connect write's timestamp in the
// onboarding record (FR-042, data-model.md §7), so the Verify step and the
// presence layer can tell "connected, hasn't reconnected yet" apart from
// "never connected". Best-effort: a storage hiccup here must never fail the
// connect response the write itself already succeeded on, so it only logs.
func (s *Server) recordClientConnected(clientID string) {
	err := s.controller.UpdateOnboardingState(func(state *storage.OnboardingState) error {
		applyClientConnected(state, clientID, time.Now())
		return nil
	})
	if err != nil && s.logger != nil {
		s.logger.Warnf("onboarding: failed to record client_connected_at for %s: %v", clientID, err)
	}
	if err == nil {
		s.notifyClientPresenceChanged()
	}
}

// applyClientConnected sets state.ClientConnectedAt[clientID] = now, creating
// the map on first use. Shared by every writer of a connect-success timestamp
// (the REST/tray path above, and the CLI's onboarding/mark relay in
// onboarding.go, added in review round 6 to close the parity gap where
// `mcpproxy connect` wrote a client config file directly without ever
// recording it here) so they stay byte-for-byte identical.
func applyClientConnected(state *storage.OnboardingState, clientID string, now time.Time) {
	if state.ClientConnectedAt == nil {
		state.ClientConnectedAt = map[string]time.Time{}
	}
	state.ClientConnectedAt[clientID] = now
	if state.ClientDisconnectedAt != nil {
		delete(state.ClientDisconnectedAt, clientID)
	}
	if client := connect.FindClient(clientID); client != nil && state.ClientLastSeen != nil {
		for _, alias := range client.ClientInfoNames {
			delete(state.ClientLastSeen, strings.ToLower(alias))
		}
	}
}

// handleDisconnectClient godoc
// @Summary     Disconnect MCPProxy from a client
// @Description Remove the MCPProxy entry from the specified client's configuration file.
// @Description Creates a backup of the existing config before modifying.
// @Tags        connect
// @Accept      json
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       client path   string         true  "Client ID (claude-code, claude-desktop, cursor, windsurf, vscode, codex, gemini, opencode, zcode)"
// @Param       body   body   ConnectRequest false "Optional parameters (server_name)"
// @Success     200    {object} contracts.APIResponse "ConnectResult"
// @Failure     400    {object} contracts.ErrorResponse "Bad request"
// @Failure     403    {object} contracts.ErrorResponse "Permission denied (macOS App-Data block)"
// @Failure     404    {object} contracts.ErrorResponse "Unknown client or entry not found"
// @Failure     503    {object} contracts.ErrorResponse "Service unavailable"
// @Router      /api/v1/connect/{client} [delete]
func (s *Server) handleDisconnectClient(w http.ResponseWriter, r *http.Request) {
	svc := s.getConnectService()
	if svc == nil {
		s.writeError(w, r, http.StatusServiceUnavailable, "connect service not available")
		return
	}

	clientID := chi.URLParam(r, "client")
	if clientID == "" {
		s.writeError(w, r, http.StatusBadRequest, "client ID is required")
		return
	}

	var req ConnectRequest
	_ = decodeOptionalJSONBody(r, &req) // best effort

	result, err := svc.Disconnect(clientID, req.ServerName)
	if err != nil {
		if s.writeIfAccessDenied(w, r, err) {
			return
		}
		client := connect.FindClient(clientID)
		if client == nil {
			s.writeError(w, r, http.StatusNotFound, err.Error())
			return
		}
		s.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	if !result.Success && result.Action == "not_found" {
		s.writeError(w, r, http.StatusNotFound, result.Message)
		return
	}
	if result.Success {
		s.recordClientDisconnected(clientID)
	}

	s.writeSuccess(w, result)
}

func (s *Server) recordClientDisconnected(clientID string) {
	err := s.controller.UpdateOnboardingState(func(state *storage.OnboardingState) error {
		applyClientDisconnected(state, clientID, time.Now())
		return nil
	})
	if err != nil && s.logger != nil {
		s.logger.Warnf("onboarding: failed to record client_disconnected_at for %s: %v", clientID, err)
	}
	if err == nil {
		s.notifyClientPresenceChanged()
	}
}

// applyClientDisconnected updates the connection generation atomically for
// REST and CLI disconnects relayed through onboarding/mark.
func applyClientDisconnected(state *storage.OnboardingState, clientID string, now time.Time) {
	if state.ClientDisconnectedAt == nil {
		state.ClientDisconnectedAt = map[string]time.Time{}
	}
	state.ClientDisconnectedAt[clientID] = now
	delete(state.ClientConnectedAt, clientID)
	// The entry is gone, so what it held is no longer observable (Spec 108-f F11).
	delete(state.ClientCredentialObserved, clientID)
}

type clientPresenceNotifier interface{ NotifyClientPresenceChanged() }

func (s *Server) notifyClientPresenceChanged() {
	if notifier, ok := s.controller.(clientPresenceNotifier); ok {
		notifier.NotifyClientPresenceChanged()
	}
}

// UndoConnectRequest is the JSON body for POST /api/v1/connect/{client}/undo.
type UndoConnectRequest struct {
	ServerName string `json:"server_name,omitempty"` // Defaults to "mcpproxy"
	// BackupName is the bare filename (filepath.Base) of the backup returned as
	// backup_path by the preceding connect — a name, never a path. Undo resolves
	// the full path server-side by joining it with the client's own config
	// directory, so a client-supplied value can never contribute a directory
	// component (traversal is impossible by construction). Empty means the
	// connect created the file (no prior file existed), so undo removes it.
	BackupName string `json:"backup_name,omitempty"`
}

// handleUndoConnectClient godoc
// @Summary     Undo a connect, restoring the pre-connect config
// @Description Reverts the connect that produced the named backup (Spec 078 US3):
// @Description restores the client config byte-for-byte from that backup, or — when
// @Description backup_name is empty because the connect created the file — deletes the
// @Description created file. backup_name is the bare filename of the backup the connect
// @Description returned (never a path); undo resolves the full path server-side inside
// @Description the client's own config directory, so a client value cannot escape it.
// @Description Refuses with 409 when the config changed since the connect (undo never
// @Description clobbers later edits; use DELETE /connect/{client} for a surgical entry
// @Description removal instead). Takes its own safety backup first; its path is returned
// @Description as backup_path in the result. Undo is "as if the connect never happened": a
// @Description client credential the connect minted or rotated is revoked unless the restored
// @Description config still holds it, and the result names it in credential_revoked.
// @Tags        connect
// @Accept      json
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       client path   string             true  "Client ID (claude-code, claude-desktop, cursor, windsurf, vscode, codex, gemini, opencode, zcode)"
// @Param       body   body   UndoConnectRequest false "Undo parameters (server_name, backup_name = the bare filename of the backup the preceding connect returned)"
// @Success     200    {object} contracts.APIResponse "ConnectResult (action restored|deleted)"
// @Failure     400    {object} contracts.ErrorResponse "Bad request (e.g. backup_name is a path, or not a backup of this client's config)"
// @Failure     403    {object} contracts.ErrorResponse "Permission denied (macOS App-Data block)"
// @Failure     404    {object} contracts.ErrorResponse "Unknown client or backup no longer exists"
// @Failure     409    {object} contracts.ErrorResponse "Config changed since connect; undo refused"
// @Failure     503    {object} contracts.ErrorResponse "Service unavailable"
// @Router      /api/v1/connect/{client}/undo [post]
func (s *Server) handleUndoConnectClient(w http.ResponseWriter, r *http.Request) {
	svc := s.getConnectService()
	if svc == nil {
		s.writeError(w, r, http.StatusServiceUnavailable, "connect service not available")
		return
	}

	clientID := chi.URLParam(r, "client")
	if clientID == "" {
		s.writeError(w, r, http.StatusBadRequest, "client ID is required")
		return
	}

	var req UndoConnectRequest
	if err := decodeOptionalJSONBody(r, &req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return
	}

	actor := actorFromRequest(r)
	result, err := svc.UndoWithIntent(clientID, req.ServerName, req.BackupName, connect.CredentialIntent{
		ActorKind: actor.Kind, ActorName: actor.Name, Surface: string(actor.Surface),
	})
	if err != nil {
		if s.writeIfAccessDenied(w, r, err) {
			return
		}
		if connect.FindClient(clientID) == nil {
			s.writeError(w, r, http.StatusNotFound, err.Error())
			return
		}
		s.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	if !result.Success {
		switch result.Action {
		case "not_found":
			s.writeError(w, r, http.StatusNotFound, result.Message)
			return
		case "conflict":
			// Mirror the connect already_exists shape: the typed result rides
			// along so the UI can distinguish the refusal from a hard failure.
			s.writeJSON(w, http.StatusConflict, map[string]interface{}{
				"success": false,
				"data":    result,
				"error":   result.Message,
			})
			return
		}
	}
	if result.Success {
		s.recordClientPresenceAfterUndo(svc, clientID)
	}

	s.writeSuccess(w, result)
}

// recordClientPresenceAfterUndo restores presence to match the configuration
// Undo put back. A backup can legitimately contain an existing entry for this
// instance, so an undo is not always a disconnect; read the just-restored
// client config while this user-initiated write is still in scope.
func (s *Server) recordClientPresenceAfterUndo(svc *connect.Service, clientID string) {
	status, err := svc.GetStatus(clientID)
	if err == nil && status.Connected && status.EndpointMatch == connect.EndpointMatchThis {
		// The restored configuration is proven to point at this instance. Keep
		// the previous connection generation and notify derived presenters of
		// the completed write.
		s.notifyClientPresenceChanged()
		return
	}
	// A restored file with no entry, a different or indeterminate endpoint, or
	// an unreadable config must not retain the connect timestamp created by the
	// write we just undid.
	s.recordClientDisconnected(clientID)
}

// decodeOptionalJSONBody decodes an optional JSON request body. An absent or
// empty body leaves out untouched — that is what "no body" means for these
// endpoints — and any other malformed input is returned as an error.
//
// Deliberately NOT gated on Content-Length: a chunked or otherwise streamed
// request arrives with ContentLength == -1, and skipping the decode there
// silently dropped the fields the caller relies on for safety (force and,
// above all, precondition_token), running the write in unguarded legacy mode.
func decodeOptionalJSONBody(r *http.Request, out interface{}) error {
	if r.Body == nil {
		return nil
	}
	if err := json.NewDecoder(r.Body).Decode(out); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// writeIfAccessDenied maps a permission-denied client-config access to a 403
// response whose body carries the remediation text. It returns true when it
// handled the error (a typed *connect.AccessError), so callers can stop. This
// keeps a macOS App-Data block distinct from a generic failure (Spec 075).
func (s *Server) writeIfAccessDenied(w http.ResponseWriter, r *http.Request, err error) bool {
	var accessErr *connect.AccessError
	if errors.As(err, &accessErr) {
		s.writeError(w, r, http.StatusForbidden, accessErr.Error())
		return true
	}
	return false
}

// previewIntent reads the credential intent of a preview from its query:
// ?profile= (present, possibly empty, means a named or All servers scope),
// ?mode=locked|switchable and ?keyless=true.
func previewIntent(r *http.Request) connect.CredentialIntent {
	q := r.URL.Query()
	intent := connect.CredentialIntent{Keyless: q.Get("keyless") == "true"}
	if q.Has("profile") {
		p := q.Get("profile")
		intent.Profile = &p
	}
	if m := q.Get("mode"); m != "" {
		intent.Mode = &m
	}
	return intent
}

// reconcileClient resolves a staged rotation of clientID from what its config
// holds before the client is classified or rewritten (FR-021a, plan D15).
// Best effort: a failure is logged and never fails the request.
func (s *Server) reconcileClient(r *http.Request, clientID string) {
	if s.clientsService == nil {
		return
	}
	if err := s.clientsService.ReconcileClient(r.Context(), clientID); err != nil && s.logger != nil {
		s.logger.Warnw("client rotation reconcile failed", "client", clientID, "error", err)
	}
}

// writeCredentialFailure maps the errors of the connect credential path to
// their wire shapes and reports whether it handled err:
//
//   - FR-008a guard refusal            -> 409 binding_bypassable_without_auth
//   - another connect in flight        -> 409 connect_in_progress
//   - credential replaced before commit -> 409 credential_superseded
//   - token name held by a regular one -> 409 {error, conflicting_token}
//   - invalid profile/mode/id          -> 400 {error, field}
//   - keyless with auth on / a profile -> 400 {error, field:"keyless"}
//   - no credential store wired        -> 503
func (s *Server) writeCredentialFailure(w http.ResponseWriter, r *http.Request, clientID string, err error) bool {
	if s.writeIfBindingGuardRefusal(w, r, err) {
		return true
	}
	var busy *internalRuntime.ConnectInProgressError
	var superseded *internalRuntime.CredentialSupersededError
	if errors.As(err, &busy) || errors.As(err, &superseded) {
		return s.writeProfilesError(w, r, err)
	}
	switch {
	case errors.Is(err, storage.ErrClientCredentialConflict):
		name := auth.ClientTokenName(clientID)
		s.writeJSON(w, http.StatusConflict, ClientCredentialConflictResponse{
			Success:          false,
			Error:            fmt.Sprintf("token name %s is held by a regular agent token", name),
			ConflictingToken: name,
			Remediation:      fmt.Sprintf("revoke or delete token %s, then connect again", name),
			RequestID:        reqcontext.GetRequestID(r.Context()),
		})
		return true
	case errors.Is(err, storage.ErrAgentTokenLimitReached):
		s.writeClientBindingError(w, r, http.StatusConflict, "", "", err.Error())
		return true
	case errors.Is(err, connect.ErrKeylessRequiresAuthOff), errors.Is(err, connect.ErrKeylessWithProfile):
		s.writeClientBindingError(w, r, http.StatusBadRequest, "", "keyless", err.Error())
		return true
	case errors.Is(err, connect.ErrNoCredentialMinter):
		s.writeError(w, r, http.StatusServiceUnavailable, err.Error())
		return true
	}
	var val *internalRuntime.ValidationError
	if errors.As(err, &val) {
		s.writeClientBindingError(w, r, http.StatusBadRequest, "", val.Field, val.Message)
		return true
	}
	return false
}

// getConnectService returns the connect service, creating it lazily from config if needed.
func (s *Server) getConnectService() *connect.Service {
	if s.connectService != nil {
		return s.connectService
	}
	return nil
}

// recordCredentialObservation persists the last on-demand classification of a
// client's config (F11), only when it CHANGED. Best effort: an observation is an
// optimisation of the list, never a reason to fail a read.
func (s *Server) recordCredentialObservation(clientID string, state profile.CredentialState) {
	if clientID == "" || state == "" || state == profile.CredentialStateUnknown {
		return
	}
	err := s.controller.UpdateOnboardingState(func(st *storage.OnboardingState) error {
		if cur, ok := st.ClientCredentialObserved[clientID]; ok && cur.State == string(state) {
			return nil
		}
		if st.ClientCredentialObserved == nil {
			st.ClientCredentialObserved = map[string]storage.ClientCredentialObservation{}
		}
		st.ClientCredentialObserved[clientID] = storage.ClientCredentialObservation{State: string(state), At: time.Now().UTC()}
		return nil
	})
	if err != nil && s.logger != nil {
		s.logger.Debugf("clients: failed to record the credential observation of %s: %v", clientID, err)
	}
}
