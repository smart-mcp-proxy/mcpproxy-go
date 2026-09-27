package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
)

// scopeFilterSupportedFilters lists which of "profile", "client" and "token"
// this build accepts as REST query parameters
// (specs/109-ux-navigation-consistency/contracts/url-filter-contract.md,
// "Backend gate", FR-080a). Empty in this PR — Spec 108-e fills it in once
// profiles land. GET /api/v1/status reports the same list as
// features.scope_filters (omitted while empty, scopeFiltersFeatureValue).
//
// This spec (109-k, T121a) and Spec 108-e (T064) have no edge between them:
// whichever of the two PRs merges first creates this variable and
// rejectUnsupportedScopeFilters below (this signature and body); the second
// reuses both and adds only its own call sites, so no build ever ships a
// handler that parses a scope parameter without the gate.
var scopeFilterSupportedFilters []string

// scopeFiltersFeatureValue returns the value GET /api/v1/status reports for
// features.scope_filters: nil (omitted from the JSON response) while the
// supported list is empty, otherwise the list itself.
func scopeFiltersFeatureValue() []string {
	if len(scopeFilterSupportedFilters) == 0 {
		return nil
	}
	return scopeFilterSupportedFilters
}

// scopeFilterErrorResponse is the wire shape of a 400 the gate returns
// (url-filter-contract.md "Backend gate"): {"error", "code", "param"}.
type scopeFilterErrorResponse struct {
	Success   bool   `json:"success"`
	Error     string `json:"error"`
	Code      string `json:"code"`
	Param     string `json:"param"`
	RequestID string `json:"request_id,omitempty"`
}

func scopeFilterListContains(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}

// rejectUnsupportedScopeFilters answers 400 unsupported_scope_filter for a
// profile/client/token (or the existing "agent" alias of token) query
// parameter this build does not support, before any storage read
// (url-filter-contract.md "Backend gate", FR-080a). It returns true when the
// caller may proceed with the request unmodified; false means it already
// wrote the 400 response and the handler must return immediately.
//
// honoured lists the scope parameter names THIS handler currently honours
// natively:
//   - For "profile", "client" and "token": the parameter is allowed only when
//     it is BOTH in scopeFilterSupportedFilters (the build-wide feature) AND
//     in honoured (this handler's own support) — Spec 108-e fills the first,
//     Spec 108-f/108-c the second, one handler at a time.
//   - For "agent" (the pre-existing, unrelated alias of "token" that
//     GET /activity and /activity/export already honour at 638fa805a): pass
//     "agent" in honoured to exempt it from the gate entirely, independent of
//     scopeFilterSupportedFilters. Every other handler leaves it out, which
//     gates "agent" exactly like "token" until Spec 108-e/f makes it honour
//     scope filters natively (codex round 4).
func rejectUnsupportedScopeFilters(w http.ResponseWriter, r *http.Request, honoured ...string) bool {
	q := r.URL.Query()
	for _, name := range []string{"profile", "client", "token", "agent"} {
		if q.Get(name) == "" {
			continue
		}

		if name == "agent" {
			if scopeFilterListContains(honoured, "agent") {
				continue
			}
			// Gated exactly like "token" (the alias it stands in for).
			if scopeFilterListContains(scopeFilterSupportedFilters, "token") && scopeFilterListContains(honoured, "token") {
				continue
			}
			writeScopeFilterRejection(w, r, name)
			return false
		}

		if scopeFilterListContains(scopeFilterSupportedFilters, name) && scopeFilterListContains(honoured, name) {
			continue
		}
		writeScopeFilterRejection(w, r, name)
		return false
	}
	return true
}

func writeScopeFilterRejection(w http.ResponseWriter, r *http.Request, param string) {
	body := scopeFilterErrorResponse{
		Success:   false,
		Error:     "scope filter '" + param + "' is not supported by this server",
		Code:      "unsupported_scope_filter",
		Param:     param,
		RequestID: reqcontext.GetRequestID(r.Context()),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(body)
}
