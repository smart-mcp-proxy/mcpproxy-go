package server

import (
	"context"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// workspaceFetchTimeout bounds one roots round-trip.
const workspaceFetchTimeout = 10 * time.Second

// workspaceFetchAttempts: the roots request travels back to the client on its
// listening SSE stream, which on Streamable HTTP may not be open yet when the
// client fires its very first request. Retry rather than losing the workspace to
// a startup race.
const workspaceFetchAttempts = 3

// workspaceFetchRetryDelay spaces the attempts out.
const workspaceFetchRetryDelay = 2 * time.Second

// workspaceSettleWait is how long the FIRST piece of activity waits for the
// client's roots before being attributed without a project.
//
// This wait is what keeps one connection inside one work session. Without it the
// first tool call resolves a workspace-less key and the second — roots having
// landed in between — resolves a workspace-keyed one, splitting a single
// connection across two work sessions: precisely the fragmentation this feature
// exists to remove. Paid at most once per connection, and not at all by clients
// that never declared roots.
const workspaceSettleWait = 3 * time.Second

// fetchWorkspaceRoot asks the client which project it is working in, and records
// the answer on the session (Spec 082).
//
// Runs in a goroutine off the first request. The timing is the whole trick, and
// two obvious choices are both wrong:
//
//   - AddAfterInitialize runs BEFORE the initialize response is written, so a
//     roots request there DEADLOCKS: the client cannot answer until it has the
//     initialize result it is still waiting for.
//   - notifications/initialized never reaches the hook at all — mcp-go dispatches
//     notifications before beforeAny (request_handler.go).
//
// The first request with an id is the earliest point that both fires the hook
// and finds the client able to answer.
//
// However this turns out, the workspace is marked "settled" on the way out, so
// nothing waits on an answer that is never coming.
func fetchWorkspaceRoot(ctx context.Context, srv *mcpserver.MCPServer, store *SessionStore, logger *zap.Logger) {
	if srv == nil || store == nil {
		return
	}

	session := mcpserver.ClientSessionFromContext(ctx)
	if session == nil {
		return
	}
	sessionID := session.SessionID()

	// Release anything waiting on the workspace, whatever happens below.
	defer store.AbandonWorkspaceFetch(sessionID)

	// Keep the session values (the client session lives in the context) but drop
	// the inbound request's cancellation — that request finishes long before the
	// client answers, and its context dies with it.
	base := context.WithoutCancel(ctx)

	var result *mcp.ListRootsResult
	var err error
	for attempt := 1; attempt <= workspaceFetchAttempts; attempt++ {
		// Stop if the client went away mid-fetch — no point asking a ghost, and
		// no point holding a goroutine open for it.
		if store.GetSession(sessionID) == nil {
			return
		}

		fetchCtx, cancel := context.WithTimeout(base, workspaceFetchTimeout)
		result, err = srv.RequestRoots(fetchCtx, mcp.ListRootsRequest{})
		cancel()

		if err == nil && result != nil && len(result.Roots) > 0 {
			break
		}
		if attempt < workspaceFetchAttempts {
			time.Sleep(workspaceFetchRetryDelay)
		}
	}

	if err != nil {
		// Entirely expected for clients that do not support roots (measured:
		// Codex). Not an error the user should ever see.
		logger.Debug("workspace: client did not provide roots",
			zap.String("session_id", sessionID),
			zap.Error(err),
		)
		return
	}
	if result == nil || len(result.Roots) == 0 {
		logger.Debug("workspace: client returned no roots",
			zap.String("session_id", sessionID),
		)
		return
	}

	// A client may report several roots (a multi-root workspace). Always take the
	// first, so the same workspace always yields the same work session rather
	// than flapping between roots.
	root := result.Roots[0].URI

	store.SetWorkspace(sessionID, root)
	logger.Debug("workspace: resolved from client roots",
		zap.String("session_id", sessionID),
		zap.String("workspace", workspaceDisplayName(root)),
		zap.Int("roots_reported", len(result.Roots)),
	)
}

// principalFromContext identifies who is making the request, for work-session
// grouping (Spec 082 FR-006).
//
// An agent token is the strongest identity available, and it is exactly what
// SEP-2567 tells gateways to key on now that the protocol's own session id is
// being removed. Unauthenticated use yields "", and grouping degrades to
// client + project.
func principalFromContext(ctx context.Context) string {
	ac := auth.AuthContextFromContext(ctx)
	if ac == nil {
		return ""
	}
	switch {
	case ac.AgentName != "":
		return "agent:" + ac.AgentName
	case ac.UserID != "":
		return "user:" + ac.UserID
	default:
		// The local API key is a single principal. Naming it keeps admin work
		// from being lumped in with agent-token work.
		return ac.Type
	}
}

// markSessionWorked records that this session has done something real, and so has
// earned a durable record (Spec 082). Returns the work session it belongs to.
//
// Sessions are NOT persisted at the handshake: on a real machine 99 of 100
// session records were background agents that connected, did nothing, and left —
// every ~15 minutes, around the clock — burying the user's real sessions and
// evicting them from the retention cap within a day.
//
// MUST be called before UpdateSessionStats, which requires the row to exist and
// would otherwise drop that call's counts.
func (p *MCPProxyServer) markSessionWorked(ctx context.Context, sessionID string) string {
	if sessionID == "" || p.sessionStore == nil {
		return ""
	}

	// Wait (briefly, once) for the project, so this connection's first record is
	// not filed under a different work session than its second.
	p.sessionStore.WorkspaceSettled(sessionID, workspaceSettleWait)

	principal := principalFromContext(ctx)

	return p.sessionStore.EnsurePersisted(sessionID, func(info *SessionInfo) string {
		resolve := p.resolveWorkSession()
		if resolve == nil {
			return ""
		}
		return resolve(runtime.WorkSessionIdentity{
			Principal:     principal,
			ClientName:    info.ClientName,
			ClientVersion: info.ClientVersion,
			WorkspaceRoot: info.Workspace,
		})
	})
}

// resolveWorkSession is the runtime's work-session tracker, or the stub a test
// injected in its place. Nil when neither is available.
func (p *MCPProxyServer) resolveWorkSession() func(runtime.WorkSessionIdentity) string {
	if p.workSessionResolver != nil {
		return p.workSessionResolver
	}
	if p.mainServer == nil || p.mainServer.runtime == nil {
		return nil
	}
	return p.mainServer.runtime.ResolveWorkSession
}

// markWorkIfToolCall attributes a request to a work session when — and only when
// — it is real work (Spec 082).
//
// This runs from the beforeAny hook, which every MCP method passes through on
// every server instance (direct / code-exec / call-tool all share these hooks).
// That placement is the point: work used to be marked inside individual tool
// handlers, and the built-ins that did not bother — list_registries,
// upstream_servers, quarantine_security — wrote activity with no work session on
// it. The Web UI grouped those orphaned rows under the raw transport session id
// while the rest of the same connection grouped under its work-session id, so a
// single client appeared as two sessions in the picker. Marking here means a new
// built-in cannot reintroduce that by forgetting a call.
//
// A tools/call is the boundary of "work", deliberately: a connection that only
// initializes and lists tools has done nothing, and persisting it would bury the
// user's real sessions under background agents that connect every few minutes,
// do nothing, and leave.
func (p *MCPProxyServer) markWorkIfToolCall(ctx context.Context, method mcp.MCPMethod) string {
	if method != mcp.MethodToolsCall {
		return ""
	}
	session := mcpserver.ClientSessionFromContext(ctx)
	if session == nil {
		return ""
	}
	return p.markSessionWorked(ctx, session.SessionID())
}
