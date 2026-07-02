package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
)

// ConnectRequest is the optional JSON body for POST /api/v1/connect/{client}.
type ConnectRequest struct {
	ServerName string `json:"server_name,omitempty"` // Defaults to "mcpproxy"
	Force      bool   `json:"force,omitempty"`       // Overwrite existing entry
}

// handleGetConnectStatus godoc
// @Summary     List client connection status
// @Description Returns the connection status for all known MCP client applications.
// @Description Each entry indicates whether the client config file exists and whether
// @Description MCPProxy is currently registered in it.
// @Tags        connect
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Success     200 {object} contracts.APIResponse "List of ClientStatus objects"
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
// @Tags        connect
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       client path   string true "Client ID (claude-code, claude-desktop, cursor, windsurf, vscode, codex, gemini, opencode)"
// @Success     200    {object} contracts.APIResponse "ClientStatus"
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

	status, err := svc.GetStatus(clientID)
	if err != nil {
		// GetStatus only errors for an unknown client; a permission denial is
		// reported in-band via access_state=denied + remediation, not as an error.
		s.writeError(w, r, http.StatusNotFound, err.Error())
		return
	}
	s.writeSuccess(w, status)
}

// handleConnectClientPreview godoc
// @Summary     Preview the change a connect would make (no write)
// @Description Returns the exact entry a subsequent connect would add to the client's
// @Description config — target path, server key, entry name, and entry contents — WITHOUT
// @Description modifying the file or creating a backup (Spec 078 US1). The embedded API key
// @Description is masked in the payload; contains_api_key flags that a credential is written.
// @Description entry_exists distinguishes a create from an overwrite of a same-named entry.
// @Description Reads the config on demand to classify create-vs-overwrite, so on macOS this
// @Description may raise an App-Data privacy prompt; a denial returns 403 + remediation.
// @Tags        connect
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       client path   string true "Client ID (claude-code, claude-desktop, cursor, windsurf, vscode, codex, gemini, opencode)"
// @Success     200    {object} contracts.APIResponse "ConnectPreview"
// @Failure     403    {object} contracts.ErrorResponse "Permission denied (macOS App-Data block)"
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

	preview, err := svc.Preview(clientID, "")
	if err != nil {
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
// @Tags        connect
// @Accept      json
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       client path   string         true  "Client ID (claude-code, claude-desktop, cursor, windsurf, vscode, codex, gemini, opencode)"
// @Param       body   body   ConnectRequest false "Optional connection parameters"
// @Success     200    {object} contracts.APIResponse "ConnectResult"
// @Failure     400    {object} contracts.ErrorResponse "Bad request"
// @Failure     403    {object} contracts.ErrorResponse "Permission denied (macOS App-Data block)"
// @Failure     404    {object} contracts.ErrorResponse "Unknown client"
// @Failure     409    {object} contracts.ErrorResponse "Already connected (use force=true)"
// @Failure     503    {object} contracts.ErrorResponse "Service unavailable"
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
	if r.Body != nil && r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.writeError(w, r, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
			return
		}
	}

	result, err := svc.Connect(clientID, req.ServerName, req.Force)
	if err != nil {
		// A macOS App-Data (TCC) denial surfaces as 403 carrying remediation,
		// distinct from a generic 400 or a 404 not-found (Spec 075 contract).
		if s.writeIfAccessDenied(w, r, err) {
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

	if !result.Success && result.Action == "already_exists" {
		s.writeJSON(w, http.StatusConflict, map[string]interface{}{
			"success": false,
			"data":    result,
			"error":   result.Message,
		})
		return
	}

	s.writeSuccess(w, result)
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
// @Param       client path   string         true  "Client ID (claude-code, claude-desktop, cursor, windsurf, vscode, codex, gemini, opencode)"
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
	if r.Body != nil && r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&req) // best effort
	}

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

	s.writeSuccess(w, result)
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

// getConnectService returns the connect service, creating it lazily from config if needed.
func (s *Server) getConnectService() *connect.Service {
	if s.connectService != nil {
		return s.connectService
	}
	return nil
}
