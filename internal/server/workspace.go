package server

import (
	"context"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"
)

// workspaceFetchTimeout bounds the roots round-trip. The answer is a nicety — a
// project name in the UI — so it must never hold anything up. A client that does
// not answer promptly simply has no workspace.
const workspaceFetchTimeout = 10 * time.Second

// workspaceFetchAttempts: the roots request travels back to the client on its
// listening SSE stream, and on Streamable HTTP that stream may not be open yet
// when the client fires its very first request. Retry a couple of times rather
// than losing the workspace to a startup race.
const workspaceFetchAttempts = 3

// workspaceFetchRetryDelay spaces the attempts out.
const workspaceFetchRetryDelay = 2 * time.Second

// fetchWorkspaceRoot asks the client which project it is working in, and records
// the answer on the session (Spec 082).
//
// Called in a goroutine from the notifications/initialized hook. That timing is
// not incidental — it is the whole trick:
//
//   - AddAfterInitialize runs BEFORE the initialize response is written, so a
//     roots request there would deadlock: the client cannot answer until it has
//     the initialize result it is still waiting for.
//   - notifications/initialized is the first moment the client is able to
//     answer. Measured against real clients (Claude Code, Gemini, opencode), a
//     roots request at this point is answered immediately with the project path.
//
// Clients that do not support roots (measured: Codex) are skipped without a
// request; their sessions simply carry no workspace, and every surface degrades
// to naming the client alone.
func fetchWorkspaceRoot(ctx context.Context, srv *mcpserver.MCPServer, store *SessionStore, logger *zap.Logger) {
	if srv == nil || store == nil {
		return
	}

	session := mcpserver.ClientSessionFromContext(ctx)
	if session == nil {
		return
	}
	sessionID := session.SessionID()

	// The caller already claimed the fetch (TryClaimWorkspaceFetch), which
	// checked the session exists and declares roots.

	// Keep the session values (the client session lives in the context) but drop
	// the inbound request's cancellation — that request finishes long before the
	// client answers, and its context dies with it.
	base := context.WithoutCancel(ctx)

	var result *mcp.ListRootsResult
	var err error
	for attempt := 1; attempt <= workspaceFetchAttempts; attempt++ {
		fetchCtx, cancel := context.WithTimeout(base, workspaceFetchTimeout)
		result, err = srv.RequestRoots(fetchCtx, mcp.ListRootsRequest{})
		cancel()

		if err == nil && result != nil && len(result.Roots) > 0 {
			break
		}
		if attempt < workspaceFetchAttempts {
			logger.Debug("workspace: roots not available yet, retrying",
				zap.String("session_id", sessionID),
				zap.Int("attempt", attempt),
				zap.Error(err),
			)
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

	// A client may report several roots (a multi-root workspace). Take the first
	// and always the first, so the same workspace always yields the same work
	// session rather than flapping between roots.
	root := result.Roots[0].URI

	store.SetWorkspace(sessionID, root)
	logger.Debug("workspace: resolved from client roots",
		zap.String("session_id", sessionID),
		zap.String("workspace", workspaceDisplayName(root)),
		zap.Int("roots_reported", len(result.Roots)),
	)
}

// markSessionWorked records that this session has done something real, and so
// has earned a durable record (Spec 082).
//
// Sessions are NOT persisted at the handshake: on a real machine 99 of 100
// session records were background agents that connected, did nothing, and left —
// every ~15 minutes, around the clock — burying the user's actual sessions and
// evicting them from the retention cap within a day.
//
// This MUST be called before UpdateSessionStats, which requires the row to
// already exist and would otherwise drop the first call's counts.
func (p *MCPProxyServer) markSessionWorked(sessionID string) {
	if sessionID == "" || p.sessionStore == nil {
		return
	}
	workSessionID := ""
	if p.mainServer != nil && p.mainServer.runtime != nil {
		workSessionID = p.mainServer.runtime.WorkSessionFor(sessionID)
	}
	p.sessionStore.EnsurePersisted(sessionID, workSessionID)
}
