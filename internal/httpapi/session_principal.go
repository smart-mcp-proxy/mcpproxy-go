package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
)

// httpSessionCookieName mirrors internal/serveredition/auth.SessionCookieName
// ("mcpproxy_session", session_store.go:15). This package cannot import that
// build-tagged package (excluded from the personal build), so the literal is
// pinned here independently; both spellings must agree for FR-001 cookie
// extraction to work in either edition.
const httpSessionCookieName = "mcpproxy_session"

// SessionPrincipalResolver resolves a session credential — a cookie value or
// a bearer JWT — to an AuthContext (Spec 107 US4,
// contracts/entitlement-predicate.md §4). It is installed by the server
// edition (internal/serveredition/setup.go); nil in the personal build, which
// keeps today's behaviour: neither the cookie nor a non-admin/non-agent
// bearer authenticates anything.
//
// Returns (nil, nil) when value is not a valid principal for kind (never a
// principal for that request) — the caller must then answer 401, not fall
// through to another credential source. An error is a resolution FAILURE
// (store unavailable), also handled as "not authenticated" by the caller.
type SessionPrincipalResolver func(r *http.Request, kind auth.CredentialKind, value string) (*auth.AuthContext, error)

// SetSessionPrincipalResolver installs the session-principal hook. Only the
// server edition calls this (internal/serveredition/setup.go via
// internal/server/serveredition_wire.go); the personal build never does, so
// s.sessionPrincipalResolver stays nil and every cookie/bearer-JWT credential
// is refused exactly as it is today.
func (s *Server) SetSessionPrincipalResolver(f SessionPrincipalResolver) {
	s.sessionPrincipalResolver = f
}

// tryInstallSessionPrincipal resolves (kind, value) through the installed
// resolver and, on success, installs the AuthContext and applies the
// tenant-session allowlist (FR-002/FR-045) before handing off to next. It
// reports whether the request was handled (either forwarded or refused);
// false means "not a principal — the caller should answer its own 401".
func (s *Server) tryInstallSessionPrincipal(w http.ResponseWriter, r *http.Request, next http.Handler, kind auth.CredentialKind, value string) bool {
	if s.sessionPrincipalResolver == nil {
		return false
	}
	ac, err := s.sessionPrincipalResolver(r, kind, value)
	if err != nil || ac == nil {
		return false
	}
	ac.CredentialKind = kind

	// admin_user is an administrator everywhere on core REST except
	// CanRevealSecrets (already excluded via IsSessionPrincipal there) — no
	// allowlist gate. A tenant (`user`-typed) principal is gated on the core
	// /api/v1 surface and /events: only an allowlisted (method, path) is
	// forwarded, filtered further downstream by the existing scope
	// predicates; everything else there is the fixed 403, emitted here,
	// before any handler — and therefore before any body parse. Routes
	// outside that surface (health checks, embedder-mounted routes) are not
	// this gate's concern and are forwarded untouched.
	if ac.Type == auth.AuthTypeUser {
		routePath := tenantSessionRoutePath(r)
		if isTenantGatedPath(routePath) && !tenantSessionAllowlist(r.Method, routePath) {
			s.writeTenantForbidden(w, r)
			return true
		}
	}

	ctx := auth.WithAuthContext(r.Context(), ac)
	next.ServeHTTP(w, r.WithContext(ctx))
	return true
}

// tenantSessionRoutePath returns the path the allowlist must match on: chi
// routes on r.URL.RawPath when it is set (server.go:888, "chi routes on
// RawPath, so the {id} param arrives percent-encoded"), never on the decoded
// r.URL.Path. A server name containing a literal "/" (nothing in config
// validation forbids one — only ":" is refused) is addressed as a single
// path segment via its percent-encoded form, e.g.
// "/servers/io.github.owner%2Frepo/tool-calls"; matching that request against
// the DECODED r.URL.Path ("/servers/io.github.owner/repo/tool-calls") makes
// strings.Cut see two segments instead of one, so the id/sub split lands in
// the wrong place and the named must-refuse for "/servers/{id}/tool-calls"
// (FR-002/FR-043(k)) never fires — while chi, routing on RawPath, still
// dispatches the request to the real handler. Matching on the same raw form
// chi uses closes that gap; falling back to r.URL.Path when RawPath is empty
// (the common case: no path segment needed non-default percent-encoding)
// keeps every existing literal comparison working unchanged.
func tenantSessionRoutePath(r *http.Request) string {
	if r.URL.RawPath != "" {
		return r.URL.RawPath
	}
	return r.URL.Path
}

// isTenantGatedPath reports whether path is on the surface the tenant-session
// allowlist governs: the core /api/v1 REST tree and /events. Everything else
// (health checks, embedder- or test-mounted routes outside that tree) is not
// this gate's concern.
func isTenantGatedPath(path string) bool {
	return path == "/events" || strings.HasPrefix(path, "/api/v1")
}

// tenantSessionAllowlist reports whether a tenant (`user`-typed) session
// principal may reach (method, path) AT ALL (Spec 107 FR-002/FR-045,
// contracts/rest-endpoints.md §8). Reachable rows are still filtered by the
// existing scope predicates downstream (CanEnumerateServer, visibleServers,
// scopedServerSubtree, ...); everything not matched here is the fixed 403.
//
// This matches on the REQUEST PATH, not chi's RoutePattern: RoutePattern is
// unusable behind the /servers/{id} sub-mux this middleware sits in front of
// (research D9), so the shape of each allowed route is reproduced here by
// hand instead.
func tenantSessionAllowlist(method, path string) bool {
	if path == "/events" {
		return method == http.MethodGet || method == http.MethodHead
	}

	if method == http.MethodPost {
		return path == "/api/v1/preflight"
	}
	if method != http.MethodGet {
		return false
	}

	switch path {
	case "/api/v1/status", "/api/v1/servers", "/api/v1/tools", "/api/v1/index/search",
		"/api/v1/profiles", "/api/v1/profiles/active":
		return true
	}

	// The static /servers/import/paths route (host filesystem paths, not a
	// server subtree) must be denied explicitly, ahead of the {id} subtree
	// rule below — a raw-path matcher cannot otherwise tell "import" from a
	// server id.
	if path == "/api/v1/servers/import/paths" {
		return false
	}

	const serversPrefix = "/api/v1/servers/"
	if strings.HasPrefix(path, serversPrefix) {
		rest := strings.TrimPrefix(path, serversPrefix)
		if rest == "" {
			return false
		}
		id, sub, hasSub := strings.Cut(rest, "/")
		if id == "" {
			return false
		}
		if !hasSub {
			return true // GET /servers/{id}
		}
		if sub == "tool-calls" {
			return false // named must-refuse: /servers/{id}/tool-calls
		}
		return true // every other GET under /servers/{id}/**
	}

	return false
}

// writeTenantForbidden writes the fixed 403 body every refused tenant-session
// route returns, byte-identical regardless of route or body
// (contracts/rest-endpoints.md §8): emitted here, in the auth middleware,
// strictly before any handler runs — so a malformed body on a refused route
// never reaches a handler's own json.Decode and surface as 400.
func (s *Server) writeTenantForbidden(w http.ResponseWriter, r *http.Request) {
	requestID := reqcontext.GetRequestID(r.Context())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":      "forbidden",
		"message":    "this credential is not permitted to access this resource",
		"request_id": requestID,
	})
}
