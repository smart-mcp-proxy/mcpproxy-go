package server

import (
	"context"
	"net/http"
	"sync"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 124: the work session of a call is derived from the REQUEST, not from
// the MCP connection, so that the three kinds of call that have no usable
// connection get one too: a REST tool call, a request on the 2026-07-28
// revision (which belongs to no connection), and a second credential on a
// connection another credential opened.

// runNameWarned remembers which principals have already been told that their
// X-MCPProxy-Run header was ignored (FR-004: once per principal per process).
// It is bounded: a principal carries its credential generation, so credential
// churn would otherwise leave one entry per retired secret forever. When the
// bound is reached the set is emptied, at the cost of one repeated warning.
var runNameWarned = &warnedSet{seen: make(map[string]struct{})}

const maxRunNameWarned = 1024

type warnedSet struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

// first reports whether this is the first time key is seen.
func (w *warnedSet) first(key string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.seen[key]; ok {
		return false
	}
	if len(w.seen) >= maxRunNameWarned {
		w.seen = make(map[string]struct{})
	}
	w.seen[key] = struct{}{}
	return true
}

// runNameMiddleware copies the X-MCPProxy-Run header onto the request context
// for MCP over HTTP. The value is stored raw; requestRunName validates it.
func runNameMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if raw := r.Header.Get(reqcontext.RunNameHeader); raw != "" {
			r = r.WithContext(reqcontext.WithRunHeader(r.Context(), raw))
		}
		next.ServeHTTP(w, r)
	})
}

// requestRunName returns the validated run name of the request, or "". An
// invalid name never fails the call: it is ignored and logged once per
// principal (FR-004).
func (p *MCPProxyServer) requestRunName(ctx context.Context, principal string) string {
	raw, ok := reqcontext.RunHeader(ctx)
	if !ok {
		return ""
	}
	if reqcontext.ValidRunName(raw) {
		return raw
	}
	if runNameWarned.first(principal) && p.logger != nil {
		p.logger.Warn("Ignoring an invalid "+reqcontext.RunNameHeader+" header: it must be 1 to 128 printable characters",
			zap.String("principal_kind", principalKind(ctx)))
	}
	return ""
}

// principalKind names the kind of caller for a log line without naming the
// caller itself.
func principalKind(ctx context.Context) string {
	if ac := auth.AuthContextFromContext(ctx); ac != nil {
		return ac.Type
	}
	return "none"
}

// workSessionKey is the per-connection cache key of one credential and run.
func workSessionKey(principal, runName string) string {
	return principal + "\x00" + runName
}

// requestWorkSession returns the work session the request in ctx belongs to.
// sessionID may be empty; the ctx's own MCP session is used then.
//
//   - A legacy MCP connection keeps the id its credential was first given on
//     that connection (FR-006), one id per credential (FR-009), and every call
//     moves the idle clock (FR-019).
//   - Everything else (a 2026-07-28 request, a REST call, a connection that has
//     not been persisted) is derived from the request: principal, client name
//     or surface, run name.
func (p *MCPProxyServer) requestWorkSession(ctx context.Context, sessionID string) string {
	principal := principalFromContext(ctx)
	runName := p.requestRunName(ctx, principal)
	if sessionID == "" {
		sessionID = sessionIDFromContext(ctx)
	}

	if sessionID != "" && p.sessionStore != nil && !mcpserver.IsModernRequest(ctx) {
		if info := p.sessionStore.GetSession(sessionID); info != nil {
			identity := runtime.WorkSessionIdentity{
				Principal:     principal,
				ClientName:    info.ClientName,
				ClientVersion: info.ClientVersion,
				WorkspaceRoot: info.Workspace,
				RunName:       runName,
			}
			// effective is the identity the session is filed under: the run
			// name is dropped when the credential is over its bound, and the
			// cache and the idle clock must then use the unnamed identity.
			effective := identity
			id := p.sessionStore.WorkSessionForKey(sessionID, workSessionKey(principal, runName), func(*SessionInfo) (string, string) {
				var resolved string
				resolved, effective = p.resolveWorkSessionEffective(identity)
				return resolved, workSessionKey(principal, effective.RunName)
			})
			if id != "" {
				p.touchWorkSession(id, effective)
				return id
			}
		}
	}

	identity := runtime.WorkSessionIdentity{Principal: principal, RunName: runName}
	if name, version := requestClientInfo(ctx); name != "" {
		identity.ClientName, identity.ClientVersion = name, version
	} else {
		identity.ClientName = reqcontext.ClientSurface(ctx)
	}
	id, _ := p.resolveWorkSessionEffective(identity)
	return id
}

// touchWorkSession moves the idle clock of a reused work session.
func (p *MCPProxyServer) touchWorkSession(id string, identity runtime.WorkSessionIdentity) {
	if p.workSessionToucher != nil {
		p.workSessionToucher(id, identity)
		return
	}
	if p.mainServer == nil || p.mainServer.runtime == nil {
		return
	}
	p.mainServer.runtime.TouchWorkSession(id, identity)
}

// requestSessionFacts is what the runtime needs to stamp a row it writes on
// its own (a limiter rejection), from the request context it was handed.
func (p *MCPProxyServer) requestSessionFacts(ctx context.Context) (workSessionID, userID, runName string) {
	if ac := auth.AuthContextFromContext(ctx); ac != nil {
		userID = ac.UserID
	}
	return p.requestWorkSession(ctx, ""), userID, p.requestRunName(ctx, principalFromContext(ctx))
}
