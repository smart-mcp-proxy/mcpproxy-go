package httpapi

import (
	"encoding/json"
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

// handleConnectClient godoc
// @Summary     Connect MCPProxy to a client
// @Description Register MCPProxy as an MCP server in the specified client's configuration file.
// @Description Creates a backup of the existing config before modifying.
// @Tags        connect
// @Accept      json
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       client path   string         true  "Client ID (claude-code, cursor, windsurf, vscode, codex, gemini)"
// @Param       body   body   ConnectRequest false "Optional connection parameters"
// @Success     200    {object} contracts.APIResponse "ConnectResult"
// @Failure     400    {object} contracts.ErrorResponse "Bad request"
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
// @Param       client path   string         true  "Client ID (claude-code, cursor, windsurf, vscode, codex, gemini)"
// @Param       body   body   ConnectRequest false "Optional parameters (server_name)"
// @Success     200    {object} contracts.APIResponse "ConnectResult"
// @Failure     400    {object} contracts.ErrorResponse "Bad request"
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

// getConnectService returns the connect service, creating it lazily from config if needed.
func (s *Server) getConnectService() *connect.Service {
	if s.connectService != nil {
		return s.connectService
	}
	return nil
}
