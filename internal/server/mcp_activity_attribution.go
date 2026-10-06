package server

import (
	"context"

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
			if attr.TokenName == "" {
				attr.TokenName = info.TokenName
			}
			// Spec 108-j J15: the ctx-less internal-tool funnel has no
			// AuthContext, so the prefix comes from the session, but only for
			// the token name the session itself recorded.
			if attr.TokenPrefix == "" && attr.TokenName != "" && attr.TokenName == info.TokenName {
				attr.TokenPrefix = info.TokenPrefix
			}
			if attr.ClientID == "" {
				attr.ClientID = info.ClientID
			}
			if attr.ProfileSource == "" {
				attr.Profile = info.Profile
				attr.ProfileSource = info.ProfileSource
			}
		}
	}
	return attr
}
