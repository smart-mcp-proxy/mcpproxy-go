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
// The boundary this package defends: grant ∩ pin (Spec 118, issue #1686)
// ---------------------------------------------------------------------------
//
// What a non-administrator caller may learn EXISTS — its visible set — is:
//
//   - a caller with no pin: its token grant (AllowedServers; "*" = every
//     server), and
//   - a PINNED caller (an agent token minted with --profile): its grant
//     intersected with the REACH of its pin, the pinned profile's effective
//     server set under the live configuration.
//
// That set is computed in ONE place — auth.AuthContext.BoundToPinReach, applied
// by bindCallerToPin (pin_boundary.go) at the two seams that mint an agent
// token's context: handleAgentTokenAuth for every REST request and the SSE
// refresher before every identity-bearing frame — and from then on
// AuthContext.AllowedServers IS the visible set. auth.CanEnumerateServer, the
// canSeeServer / visibleServers wrappers in scope.go, scopeAllowedServers
// below, every recomputed count and the one predicate caller outside this
// package (internal/management/diagnostics.go) all read it. A REST read door
// never re-derives visibility: it answers from here.
//
// Why the pin is part of the boundary and not an ergonomic filter: it is set
// by the operator at token creation, it cannot be changed or even confirmed by
// the token, and a --profile token takes its grant FROM the profile, so its
// stored grant is "*". Read alone, that "*" let a pinned token enumerate the
// whole fleet on /status, /events, /review, /attention, /profiles and
// /annotations/coverage while the MCP resolver (pin = highest tier) showed it
// one profile's servers. Intersecting the pin can only narrow, never widen.
//
// Fail closed (FR-002): a pin naming no configured profile, an empty profile,
// a profile whose servers are all unconfigured, or one with no overlap with
// the grant yields an EMPTY visible set — the same shape, on every door, as a
// token whose grant is []. The token cannot tell "profile deleted" from
// "nothing granted". The reach is resolved from the live configuration per
// request and per SSE frame, never cached on the credential (FR-003).
//
// Two things in this tree still look like "scope" and are NOT the boundary —
// they are ERGONOMIC filters that can only narrow further:
//
//  1. The requested profile: ?profile= view-as on REST (view_as.go),
//     set_profile and /mcp/p/<slug> on MCP (server.serverInScope,
//     internal/server/mcp_visibility.go). Caller-selectable, so it is never
//     folded into the boundary — a caller could otherwise widen its own
//     visibility by switching profiles. A pinned caller may select only its
//     own pin (FR-012); every other slug, existing or not, gets the same
//     non-disclosing refusal, exactly as the MCP surface answers it.
//
//  2. preflight.ResolveScope (the REST /preflight door) — token scope ∩ token
//     pin ∩ requested profile. With the pin already bound into the token
//     scope the pin term is idempotent there; the requested-profile half is
//     again ergonomics, because preflight is a "would these tools resolve
//     under profile X?" question.
//
// So the boundary ⊇ (1) and ⊇ (2) always. A REST read door must be gated by
// the boundary; anything (1) or (2) additionally hides is hidden for
// convenience, not safety.

// scopeAllowedServers returns the server names a scoped caller may see, plus
// whether the caller is scoped at all.
//
// The slice is non-nil for a scoped caller EVEN WHEN EMPTY: the downstream
// filters (storage.ActivityFilter.AllowedServers, storage.ToolCallScope) read
// nil as "unrestricted", so handing them a nil for a token that is allowed
// nothing would open the door it was meant to close. "*" is preserved and
// honoured by those filters exactly as AuthContext.CanAccessServer honours it.
// For a pinned caller the context's AllowedServers is already the resolved
// visible set (bindCallerToPin), so this seam — and the usage cache key built
// from it — never hands storage the raw "*" of a --profile token (FR-019).
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

// The round-10 denial messages. Each names a route class whose whole document
// is a DEPLOYMENT-WIDE aggregate: it either enumerates the inventory outright
// or reports scalars computed across it that cannot be re-derived from the
// caller's own slice. Filtering such a document leaves the scalars beside the
// filtered part as an exact oracle for what was hidden, which is why every one
// of them takes the /api/v1/config and /stats/tokens answer — 403 — instead.
const (
	// /security/overview and /security/queue. QueueProgress.Items names every
	// queued server; the overview is fleet-wide scan and finding counts.
	securityFleetDenialMessage = "Agent tokens cannot read deployment-wide security scan state"

	// /telemetry/payload. The heartbeat carries server_count,
	// connected_server_count, tool_count and server_docker_isolated_count —
	// precisely the count oracle removed from /status.
	telemetryPayloadDenialMessage = "Agent tokens cannot read the deployment telemetry payload"

	// /onboarding/state and /onboarding/mark (which echoes the same document).
	// configured_server_count is an inventory size and connected_client_ids is
	// the operator's MCP-client inventory — the same class as /sessions.
	onboardingDenialMessage = "Agent tokens cannot read onboarding state"

	// /secrets/refs and /secrets/config. Values are masked, so this is a
	// credential INVENTORY rather than a disclosure — but it names the secrets
	// belonging to servers the caller may not enumerate, along with the keyring
	// reference text and whether each resolves. It is a strictly narrower view
	// of the document GET /api/v1/config already answers 403 for.
	secretsInventoryDenialMessage = "Agent tokens cannot read the configuration's secret inventory"
)

// serverExists reports whether a server with this exact name is configured.
//
// It reads GetAllServers — the same inventory the /servers list and the
// per-server diagnostics door answer from — so "exists" cannot mean one thing
// to the gate and another to the handler behind it.
//
// The error is RETURNED rather than folded into the boolean (round 10, P8).
// Collapsing it to false made a transient inventory read failure answer "no
// such server" on hot paths like /servers/{id}/logs and /servers/{id}/tools:
// the caller is told its own server has been deleted, retries stop, and the
// operator chases a phantom deletion instead of the read error. "Does not
// exist" and "could not determine" are different answers and get different
// statuses — 404 and 503.
func (s *Server) serverExists(name string) (bool, error) {
	if name == "" {
		return false, nil
	}
	servers, err := s.controller.GetAllServers()
	if err != nil {
		return false, err
	}
	for _, sv := range servers {
		if n, _ := sv["name"].(string); n == name {
			return true, nil
		}
	}
	return false, nil
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
		if !canSeeServer(ctx, id) {
			s.writeError(w, r, http.StatusNotFound, "Server not found: "+id)
			return
		}
		exists, err := s.serverExists(id)
		if err != nil {
			// The caller IS entitled to this name, so no existence fact leaks
			// by admitting we could not read the inventory. Saying 404 here
			// would tell an entitled caller its own server is gone (P8).
			s.logger.Errorw("Scope gate could not read the server inventory",
				"server", id, "error", err)
			s.writeError(w, r, http.StatusServiceUnavailable, "Server inventory temporarily unavailable")
			return
		}
		if exists {
			next.ServeHTTP(w, r)
			return
		}
		// Same status and same message as the absent case on every route in
		// the subtree — this IS the absent case, as far as the caller can tell.
		s.writeError(w, r, http.StatusNotFound, "Server not found: "+id)
	})
}
