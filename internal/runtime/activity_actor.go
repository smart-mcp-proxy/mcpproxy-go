package runtime

import (
	"context"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

// actorContextKey carries the caller identity of an administrative request
// (issue #1698). The REST layer stamps it once the API-key / token middleware
// has authenticated the request; the MCP management tools leave it unset and
// the runtime derives it from the AuthContext instead.
type actorContextKey struct{}

// ContextWithActor returns ctx carrying a as the request's actor. A later
// RequestActor returns a verbatim, so a surface that knows more than the
// AuthContext (the REST door knows the X-MCPProxy-Surface header, the socket
// bypass) can say so.
func ContextWithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorContextKey{}, a)
}

// RequestActor is the caller identity of the request behind ctx: the actor a
// surface stamped with ContextWithActor, else one derived from the request's
// AuthContext (surface "api"; the tray / CLI socket bypass is reported as
// kind "socket", as actorFromRequest does for profile_change rows). ok is
// false when ctx names no caller at all — a background context from a file
// reload, startup or a scheduled sweep — so the record stays silent about the
// actor rather than inventing one.
func RequestActor(ctx context.Context) (Actor, bool) {
	if ctx == nil {
		return Actor{}, false
	}
	if a, ok := ctx.Value(actorContextKey{}).(Actor); ok {
		return a, true
	}
	if auth.AuthContextFromContext(ctx) == nil {
		return Actor{}, false
	}
	a := ActorFromContext(ctx, profile.SurfaceAPI)
	if transport.GetConnectionSource(ctx) == transport.ConnectionSourceTray {
		a.Kind = string(auth.CredentialKindSocket)
	}
	return a, true
}

// stampActor adds the actor and request-id keys of ctx to an activity event
// payload. It writes nothing for a context without a caller, so a legacy
// consumer sees the payload it always saw. The keys are the profile_change
// metadata vocabulary (actor_kind, actor_name, surface) so one filter reads
// every administrative row. An empty actor_name is omitted.
func stampActor(ctx context.Context, payload map[string]any) (Actor, bool) {
	a, ok := RequestActor(ctx)
	if ok {
		payload["actor_kind"] = a.Kind
		if a.Name != "" {
			payload["actor_name"] = a.Name
		}
		payload["surface"] = string(a.Surface)
	}
	if rid := reqcontext.GetRequestID(ctx); rid != "" {
		payload["request_id"] = rid
	}
	return a, ok
}
