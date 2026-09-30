package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
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
// @Success 200 {object} contracts.APIResponse{data=internalRuntime.AccessExplanation} "The explanation"
// @Failure 400 {object} contracts.ErrorResponse "Not exactly one subject, a built-in tool, or token=client-<id>"
// @Failure 403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure 404 {object} contracts.ErrorResponse "client / token / profile not found"
// @Failure 503 {object} contracts.ErrorResponse "Service unavailable"
// @Router /api/v1/access/explain [get]
func (s *Server) handleAccessExplain(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	client, token, prof := q.Get("client"), q.Get("token"), q.Get("profile")
	anonymous := q.Get("anonymous") == "true"
	n := 0
	for _, set := range []bool{client != "", token != "", prof != "", anonymous} {
		if set {
			n++
		}
	}
	if n != 1 {
		s.writeError(w, r, http.StatusBadRequest, "exactly one of client, token, profile, anonymous is required")
		return
	}
	tool := q.Get("tool")
	if !looksLikeUpstreamTool(tool) {
		s.writeError(w, r, http.StatusBadRequest, profile.ErrExplainBuiltinTool.Error())
		return
	}

	var subject profile.AccessSubject
	switch {
	case client != "":
		if !clientRoutesSupported {
			s.writeError(w, r, http.StatusNotFound, errClientNotFound)
			return
		}
		subject = profile.AccessSubject{Kind: profile.AccessSubjectClient, ClientID: client}
		if state, ok := s.clientCredentialState(client); ok {
			subject.CredentialState = state
		}
	case token != "":
		if strings.HasPrefix(token, auth.ClientTokenName("")) {
			s.writeError(w, r, http.StatusBadRequest, "use client=<id> for a client credential")
			return
		}
		subject = profile.AccessSubject{Kind: profile.AccessSubjectToken, TokenName: token}
	case prof != "":
		subject = profile.AccessSubject{Kind: profile.AccessSubjectProfile, Profile: prof}
	default:
		subject = profile.AccessSubject{Kind: profile.AccessSubjectAnonymous}
	}

	res, err := s.profiles().Explain(r.Context(), subject, tool)
	if err != nil {
		switch {
		case errors.Is(err, profile.ErrUnknownToken):
			s.writeError(w, r, http.StatusNotFound, "token not found")
		case errors.Is(err, profile.ErrClientCredentialToken):
			s.writeError(w, r, http.StatusBadRequest, profile.ErrClientCredentialToken.Error())
		case errors.Is(err, profile.ErrExplainBuiltinTool):
			s.writeError(w, r, http.StatusBadRequest, profile.ErrExplainBuiltinTool.Error())
		default:
			s.writeProfileServiceError(w, r, err)
		}
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
