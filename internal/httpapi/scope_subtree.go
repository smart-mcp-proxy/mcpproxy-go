package httpapi

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// This file extends internal/httpapi/scope.go with the per-server subtree gate
// and the shared "what may this caller see?" projection helpers the activity /
// tool-call doors read. Same rule as scope.go: a read route under /api/v1 does
// not re-derive visibility for itself — it answers from here.

// ---------------------------------------------------------------------------
// Scope vs profile — the boundary this package defends
// ---------------------------------------------------------------------------
//
// Three things in this tree look like "scope" and only ONE of them is an
// authorization boundary:
//
//  1. auth.CanEnumerateServer — token scope (AllowedServers) alone. This is the
//     boundary. It is the only one of the three that decides whether a caller
//     may learn that a server exists, and it is what every read door in this
//     package answers from.
//
//  2. server.serverInScope (internal/server/mcp_visibility.go) — token scope
//     AND the active PROFILE. The profile half is an ERGONOMIC filter: it
//     narrows what an agent is offered so a 300-tool fleet does not drown one
//     task, and the user flips it at will with set_profile. It is deliberately
//     NOT intersected into (1): a profile switch would then silently change
//     what a token is authorized to read, and because the profile is
//     caller-selectable while the token scope is not, a caller could WIDEN its
//     own visibility by switching profiles.
//
//  3. preflight.ResolveScope (the REST /preflight door) — token scope ∩ token
//     PIN ∩ requested profile. The pin (AuthContext.ProfilePin) is operator-set
//     and not caller-selectable, so intersecting it there genuinely narrows;
//     the requested-profile half is again ergonomics, and preflight is a
//     "would these tools resolve under profile X?" question that has to answer
//     under the profile the caller names. See the comment on
//     handleAgentTokenAuth, which describes THAT door and not this predicate.
//
// So (1) ⊇ (2) and (1) ⊇ (3) always. A REST read door must be gated by (1);
// anything (2) or (3) additionally hides is hidden for convenience, not safety.

// scopeAllowedServers returns the server names a scoped caller may see, plus
// whether the caller is scoped at all.
//
// The slice is non-nil for a scoped caller EVEN WHEN EMPTY: the downstream
// filters (storage.ActivityFilter.AllowedServers, storage.ToolCallScope) read
// nil as "unrestricted", so handing them a nil for a token that is allowed
// nothing would open the door it was meant to close. "*" is preserved and
// honoured by those filters exactly as AuthContext.CanAccessServer honours it.
func scopeAllowedServers(ctx context.Context) ([]string, bool) {
	if !auth.IsScopedCaller(ctx) {
		return nil, false
	}
	ac := auth.AuthContextFromContext(ctx)
	out := make([]string, 0, len(ac.AllowedServers))
	out = append(out, ac.AllowedServers...)
	return out, true
}

// sessionsDenialMessage is why /api/v1/sessions and /api/v1/sessions/{id} are
// DENIED to a non-admin caller rather than filtered.
//
// A contracts.MCPSession has no server attribution at all — it describes a
// CLIENT connected to the proxy (client name and version, workspace basename,
// work-session grouping, tool-call and token totals). There is no server name
// to project through canSeeServer, so "filtering" it would mean either handing
// over the operator's whole client-and-workspace inventory or inventing an
// attribution the record does not carry. Same call as /api/v1/config: it is an
// operator document, not a per-server one.
const sessionsDenialMessage = "Agent tokens cannot read MCP session history"

// serverExists reports whether a server with this exact name is configured.
//
// It reads GetAllServers — the same inventory the /servers list and the
// per-server diagnostics door answer from — so "exists" cannot mean one thing
// to the gate and another to the handler behind it. An error reports "does not
// exist": the only caller is the scoped-caller gate below, where failing that
// way hides rather than reveals.
func (s *Server) serverExists(name string) bool {
	if name == "" {
		return false
	}
	servers, err := s.controller.GetAllServers()
	if err != nil {
		return false
	}
	for _, sv := range servers {
		if n, _ := sv["name"].(string); n == name {
			return true
		}
	}
	return false
}

// scopedServerSubtree is the ONE scope gate for /api/v1/servers/{id}/**.
//
// It is a middleware, not a per-handler check, for the same reason
// decodeServerIDParam is: this subtree keeps growing sub-resources (tools,
// logs, tool-calls, scan/status, scan/report, scan/files, integrity,
// tools/export, tools/{tool}/diff …) and each one answered for itself. Only
// /diagnostics had a gate; the rest handed a token scoped to `alpha` beta's
// tool list, its scan report, its scanned file tree, its per-server tool-call
// history — and, sharpest, its raw stdout/stderr, which routinely echoes the
// argv and env the upstream process was launched with. That is a credential
// disclosure that walks straight around the log-redaction work in #1158,
// because nothing redacts a log tail.
//
// It also supplies the EXISTENCE half. The earlier round deferred this subtree
// because these handlers answer 200 for a server that does not exist, so there
// was no 404 to be at status parity WITH. That is a reason to fix the existence
// check, not to leave the hole: for a scoped caller both cases are now the same
// 404 with the same body, so "not yours" and "not there" are indistinguishable.
//
// Admin callers are untouched — including the no-AuthContext bootstrap
// passthrough, per auth.CanEnumerateServer — so the 200-on-absent quirk and
// every existing admin behaviour on this subtree is preserved exactly.
//
// It runs on every method, not only GET. The mutating routes here already
// answer a uniform 403 from requireServerOp before they look anything up, so
// they leak nothing either way; ordering the 404 first just means an agent gets
// one consistent answer about servers it may not see.
func (s *Server) scopedServerSubtree(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !auth.IsScopedCaller(ctx) {
			next.ServeHTTP(w, r)
			return
		}
		// decodeServerIDParam is registered first on this subtree, so the
		// value here is the real server name, not its percent-encoded form.
		id := chi.URLParam(r, "id")
		if canSeeServer(ctx, id) && s.serverExists(id) {
			next.ServeHTTP(w, r)
			return
		}
		// Same status and same message as the absent case on every route in
		// the subtree — this IS the absent case, as far as the caller can tell.
		s.writeError(w, r, http.StatusNotFound, "Server not found: "+id)
	})
}
