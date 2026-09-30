package httpapi

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 108 FR-031: the backend MEANING of the profile / client / token scope
// filters on activity, sessions, tools and servers (Spec 109-k owns the URL
// contract and the gate in scope_filters.go; this file owns what the values
// select). Error texts are part of the contract (contracts/rest-api.md
// "Filters on grids") and pinned by tests.
const (
	errClientNameUnsupported   = "client_name is not supported on this endpoint; filter by client"
	errTokenAgentMismatch      = "token and agent must name the same token (agent is an alias of token)"
	errClientAndProfile        = "use either client or profile, not both"
	errUnattributedOnlyOnGrids = "'-' (unattributed) is only valid on activity and session filters"
)

// errTokenAgentConflict is returned by scopeTokenParam when ?token= and
// ?agent= name different tokens.
var errTokenAgentConflict = errors.New(errTokenAgentMismatch)

// scopeTokenParam resolves the token filter. "agent" is a kept alias of
// "token" (Spec 028 predates Spec 108): both name the same filter, and naming
// two different tokens is a client bug answered with 400, never a silent pick.
func scopeTokenParam(q url.Values) (string, error) {
	token, agent := q.Get("token"), q.Get("agent")
	if token != "" && agent != "" && token != agent {
		return "", errTokenAgentConflict
	}
	if token != "" {
		return token, nil
	}
	return agent, nil
}

// rejectClientNameParam answers 400 for `client_name` on an endpoint that does
// not filter on the advisory self-reported name (summary, usage, sessions).
// It returns true when the caller may proceed.
func (s *Server) rejectClientNameParam(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Query().Get("client_name") == "" {
		return true
	}
	s.writeError(w, r, http.StatusBadRequest, errClientNameUnsupported)
	return false
}

// sessionFilterFromQuery builds the storage session filter for GET /sessions.
func sessionFilterFromQuery(q url.Values, limit int, status string) (storage.SessionFilter, error) {
	token, err := scopeTokenParam(q)
	if err != nil {
		return storage.SessionFilter{}, err
	}
	return storage.SessionFilter{
		Limit:     limit,
		Status:    status,
		Profile:   q.Get("profile"),
		ClientID:  q.Get("client"),
		TokenName: token,
	}, nil
}
