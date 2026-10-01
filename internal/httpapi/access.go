package httpapi

import (
	"net/http"
	"strings"
)

// handleAccessExplain godoc
// @Summary Explain why a tool is or is not reachable
// @Description For one subject (exactly one of client=<id>, token=<name>, profile=<name> or anonymous=true) and one upstream tool (server:tool), returns the ordered chain of gates a real call would meet - credential, profile, server_in_scope, tool_rule, tier_cap, token_permission, global_gate, server_state, tool_approval - the overall verdict (allowed, blocked, hidden), the first failing step and the fixes for it in preference order. The chain is the one every discovery and dispatch path walks, so `allowed` equals what a real call does. Built-in management tools are not explained (400). A client credential is explained with client=, never token=. Administrators only.
// @Tags access
// @Produce json
// @Param tool query string true "Upstream tool as server:tool"
// @Param client query string false "Client id"
// @Param token query string false "Regular agent token name"
// @Param profile query string false "Profile name"
// @Param anonymous query boolean false "Explain an anonymous (credential-less) caller"
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.APIResponse{data=AccessExplanationData} "The explanation"
// @Failure 400 {object} contracts.ErrorResponse "Not exactly one subject, a built-in tool, or token=client-<id>"
// @Failure 403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure 404 {object} contracts.ErrorResponse "client / token / profile not found"
// @Failure 503 {object} contracts.ErrorResponse "Service unavailable"
// @Router /api/v1/access/explain [get]
func (s *Server) handleAccessExplain(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := s.ExplainAccess(r.Context(), ExplainRequest{
		Client: q.Get("client"), Token: q.Get("token"), Profile: q.Get("profile"),
		Anonymous: q.Get("anonymous") == "true", Tool: q.Get("tool"),
	})
	if err != nil {
		s.writeProfileServiceError(w, r, err)
		return
	}
	s.writeSuccess(w, res)
}

// looksLikeUpstreamTool reports whether tool is "server:tool" with both parts
// non-empty. A built-in management tool (upstream_servers, retrieve_tools, ...)
// has no server segment.
func looksLikeUpstreamTool(tool string) bool {
	i := strings.Index(tool, ":")
	return i > 0 && i < len(tool)-1
}
