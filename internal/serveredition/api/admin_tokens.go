//go:build server

package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

type adminAgentTokenStore interface {
	ListAgentTokens() ([]auth.AgentToken, error)
	RevokeAgentTokenForOwner(string, string) error
}

func (h *AdminHandlers) SetAgentTokenStore(store adminAgentTokenStore) { h.agentTokens = store }

type adminAgentTokenResponse struct {
	AgentTokenResponse
	UserID     string `json:"user_id"`
	ProfilePin string `json:"profile_pin,omitempty"`
}

type revokeAgentTokenRequest struct {
	Name string `json:"name"`
}

func (h *AdminHandlers) listAgentTokens(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	if h.agentTokens == nil {
		writeError(w, 503, "Token storage unavailable")
		return
	}
	tokens, err := h.agentTokens.ListAgentTokens()
	if err != nil {
		h.logger.Errorw("failed to list admin tokens", "error", err)
		writeError(w, 500, "Failed to list tokens")
		return
	}
	result := make([]adminAgentTokenResponse, 0, len(tokens))
	for _, t := range tokens {
		result = append(result, adminAgentTokenResponse{UserID: t.UserID, ProfilePin: t.ProfilePin, AgentTokenResponse: AgentTokenResponse{
			Name: t.Name, TokenPrefix: t.TokenPrefix, AllowedServers: t.AllowedServers, Permissions: t.Permissions, ExpiresAt: t.ExpiresAt, CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt, Revoked: t.Revoked,
		}})
	}
	writeJSON(w, 200, map[string]interface{}{"tokens": result})
}

func (h *AdminHandlers) revokeAgentToken(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	h.revokeAgentTokenForOwnerAndName(w, r, chi.URLParam(r, "id"), chi.URLParam(r, "name"))
}

// revokeAgentTokenByBody is the canonical owner-qualified revocation route.
// Token names are storage values rather than URL path segments and may contain
// slashes, percent signs, spaces, or Unicode. JSON preserves them exactly and
// avoids router- and proxy-dependent path decoding.
func (h *AdminHandlers) revokeAgentTokenByBody(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	var req revokeAgentTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, "Token name is required")
		return
	}
	h.revokeAgentTokenForOwnerAndName(w, r, chi.URLParam(r, "id"), req.Name)
}

func (h *AdminHandlers) revokeAgentTokenForOwnerAndName(w http.ResponseWriter, r *http.Request, owner, name string) {
	if h.agentTokens == nil {
		writeError(w, 503, "Token storage unavailable")
		return
	}
	if owner == "" || name == "" {
		writeError(w, 404, "Token not found")
		return
	}
	if err := h.agentTokens.RevokeAgentTokenForOwner(owner, name); err != nil {
		if errors.Is(err, storage.ErrAgentTokenNotFound) {
			writeError(w, 404, "Token not found")
			return
		}
		h.logger.Errorw("failed to revoke admin token", "user_id", owner, "name", name, "error", err)
		writeError(w, 500, "Failed to revoke token")
		return
	}
	actor := auth.AuthContextFromContext(r.Context())
	actorUserID := ""
	if actor != nil {
		actorUserID = actor.UserID
	}
	h.logger.Infow("admin revoked agent token",
		"actor_user_id", actorUserID,
		"target_user_id", owner,
		"token_name", name,
	)
	writeJSON(w, 200, map[string]interface{}{"revoked": true})
}
