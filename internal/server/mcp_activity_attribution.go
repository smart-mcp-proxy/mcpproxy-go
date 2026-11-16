package server

import (
	"context"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 108 FR-029 scope attribution: every activity record of an MCP/REST
// request carries the profile, client and token IN EFFECT when the call ran.
//
// The values are computed ONCE, at emit time, by the same code path that made
// the decision, and stamped on the record — never re-resolved later, so a
// reassignment never rewrites history.

type activityAttributionCtxKey struct{}

// withActivityAttribution installs the dispatch path's own attribution (the
// profile resolution IT decided against) so every activity record of that
// request reports that resolution, whichever funnel emits it.
func withActivityAttribution(ctx context.Context, attr runtime.ActivityAttribution) context.Context {
	return context.WithValue(ctx, activityAttributionCtxKey{}, attr)
}

func activityAttributionFromContext(ctx context.Context) (runtime.ActivityAttribution, bool) {
	attr, ok := ctx.Value(activityAttributionCtxKey{}).(runtime.ActivityAttribution)
	return attr, ok
}

// resolveForDispatch is ResolveProfileV3 for a dispatch path: it also installs
// the resolution it returns as the request's activity attribution, so the
// record of the call reports the profile the call was actually decided under.
// It replaces the bare ResolveProfileV3 call in every handler that emits
// activity for a call it dispatches or refuses.
func (p *MCPProxyServer) resolveForDispatch(ctx context.Context, idx *profileIndex) (context.Context, ProfileResolution) {
	res := p.ResolveProfileV3(ctx, idx)
	attr, _ := activityAttributionFromContext(ctx)
	attr.Profile = res.Name
	attr.ProfileSource = res.Source
	return withActivityAttribution(ctx, attr), res
}

// activityAttribution returns the attribution for a record emitted under ctx.
//
//  1. An attribution installed by the dispatch path (resolveForDispatch) wins
//     for profile/source: it is the resolution the call was decided under.
//  2. auth.AuthContextFromContext supplies the token name, its display prefix
//     and, for a client credential, the client id.
//  3. The session (SessionInfo) supplies the self-reported client name, fills
//     token/client when still empty, and, when the dispatch installed no
//     resolution (the ctx-less internal-tool and prompt funnels), the session's
//     LATEST profile resolution, which the same request has just written.
//
// sessionID may be empty; the ctx's own session is used then.
func (p *MCPProxyServer) activityAttribution(ctx context.Context, sessionID string) runtime.ActivityAttribution {
	attr, _ := activityAttributionFromContext(ctx)

	if ac := auth.AuthContextFromContext(ctx); ac != nil && ac.Type == auth.AuthTypeAgent {
		if attr.TokenName == "" {
			attr.TokenName = ac.AgentName
		}
		if attr.TokenPrefix == "" {
			attr.TokenPrefix = ac.TokenPrefix
		}
		if attr.ClientID == "" && ac.IsClientCredential() {
			attr.ClientID = ac.ClientID
		}
	}

	if sessionID == "" {
		sessionID = sessionIDFromContext(ctx)
	}
	if p.sessionStore != nil && sessionID != "" {
		if info := p.sessionStore.GetSession(sessionID); info != nil {
			if attr.ClientName == "" {
				attr.ClientName = info.ClientName
			}
			// The session's credential stands in only when the emitter has
			// no request credential at all (the ctx-less internal funnels).
			// A request that authenticated as something else on the same
			// connection, an administrator's API key for one, is not the
			// session's token: stamping it as such would hand its rows and
			// its work session to that token (Spec 124 FR-041).
			requestAuth := auth.AuthContextFromContext(ctx)
			sessionCredential := requestAuth == nil
			if attr.TokenName == "" && sessionCredential {
				attr.TokenName = info.TokenName
			}
			// Spec 108-j J15: the ctx-less internal-tool funnel has no
			// AuthContext, so the prefix comes from the session, but only for
			// the token name the session itself recorded.
			if attr.TokenPrefix == "" && attr.TokenName != "" && attr.TokenName == info.TokenName {
				attr.TokenPrefix = info.TokenPrefix
			}
			if attr.ClientID == "" && sessionCredential {
				attr.ClientID = info.ClientID
			}
			if attr.ProfileSource == "" {
				attr.Profile = info.Profile
				attr.ProfileSource = info.ProfileSource
			}
			// Spec 124 FR-040: the project of a connection that disclosed its
			// workspace root. Only the basename; a 2026-07-28 request and a
			// REST call have none.
			if attr.ProjectName == "" && !mcpserver.IsModernRequest(ctx) {
				attr.ProjectName = runtime.WorkspaceName(info.Workspace)
			}
		}
	}

	// Spec 058 US3 (T033): a 2026-07-28 request never ran initialize, so no
	// session knows its client — its session id is "" over Streamable HTTP,
	// or a transport constant the handshake never named. The identity travels
	// on the request itself (research D5), stamped by mcp-go before any hook
	// or handler runs. Still advisory, exactly like the initialize-time name.
	if attr.ClientName == "" {
		if name, version := requestClientInfo(ctx); name != "" {
			attr.ClientName = name
			attr.ClientVersion = version
		}
	}

	// Spec 124: the work session, the owning user and the run name are facts
	// of the request, so they are fixed here, at emit time, with the rest of
	// the attribution. The activity service has no request to read them from.
	if attr.WorkSessionID == "" {
		// The run name is the one the session is filed under: a name the
		// daily allowance refused is not stamped on the unnamed session's
		// rows.
		attr.WorkSessionID, attr.RunName = p.requestWorkSessionRun(ctx, sessionID)
	} else {
		// A session pinned by the caller (code_execution and its sub-calls):
		// the row is activity of that session and moves its idle clock.
		p.touchWorkSessionID(attr.WorkSessionID)
	}
	if ac := auth.AuthContextFromContext(ctx); ac != nil && attr.UserID == "" {
		attr.UserID = ac.UserID
	}
	if attr.Surface == "" {
		attr.Surface = reqcontext.ClientSurface(ctx)
	}
	attr.PolicyRefusal = isPolicyRefusal(ctx)
	return attr
}

type attemptMarkKey int

const (
	companionDecisionKey attemptMarkKey = iota
	policyRefusalKey
)

// withCompanionDecision marks a blocking policy decision that is emitted next
// to a tool_call row of the same attempt (Spec 124 D2). The tool_call row is
// the counted row; the decision explains it and must not be counted again.
// Sites that block and return WITHOUT a tool_call row do not use this.
func withCompanionDecision(ctx context.Context) context.Context {
	return context.WithValue(ctx, companionDecisionKey, true)
}

func isCompanionDecision(ctx context.Context) bool {
	v, _ := ctx.Value(companionDecisionKey).(bool)
	return v
}

// withPolicyRefusal marks an internal tool call that MCPProxy itself refused
// (read-only mode, management disabled, an agent token on a write operation),
// so its row is classified proxy_policy and counted as a policy block, not as
// an ordinary failure (Spec 124 FR-016).
func withPolicyRefusal(ctx context.Context) context.Context {
	return context.WithValue(ctx, policyRefusalKey, true)
}

func isPolicyRefusal(ctx context.Context) bool {
	v, _ := ctx.Value(policyRefusalKey).(bool)
	return v
}

// requestClientInfo returns the client name and version a modern (2026-07-28)
// request declared in its _meta, or empty strings for a legacy request or a
// modern one that declared none.
func requestClientInfo(ctx context.Context) (name, version string) {
	info := mcpserver.RequestProtocolInfoFromContext(ctx)
	if info == nil || !info.Modern || info.ClientInfo == nil {
		return "", ""
	}
	return info.ClientInfo.Name, info.ClientInfo.Version
}
