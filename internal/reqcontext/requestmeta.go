package reqcontext

import "context"

// RequestMetaKey is the context key for RequestMeta.
const RequestMetaKey ContextKey = "request_meta"

// Mount names the HTTP mount a request entered through. It is fixed by the
// mount point (Spec 107 T050), never by a header.
type Mount string

const (
	// MountMCP is the /mcp* family (the MCP proxy surface).
	MountMCP Mount = "mcp"
	// MountAPI is the /api/v1 group and /events (the REST API).
	MountAPI Mount = "api"
)

// RequestMeta is the per-request metadata the audit line needs (Spec 107
// PR-D): the client IP as resolved through the trusted-proxy rule and the
// mount the request arrived on. RemoteAddr lives only on *http.Request, so the
// edition-neutral httpapi middleware stores it here.
type RequestMeta struct {
	ClientIP string
	Mount    Mount
	// Surface is the caller-asserted #1842 surface of a REST request (web,
	// macos, cli or api, from X-MCPProxy-Surface / X-MCPProxy-Client),
	// classified once at the ingress so a built-in dispatched from REST keeps
	// it (Spec 133 client.surface). Untrusted, attribution only. Empty on
	// the MCP mount, whose surface is always mcp.
	Surface string
	// ClientName is the caller-asserted X-MCPProxy-Client value of a REST
	// request (Spec 133 client.name). Untrusted.
	ClientName string
}

// WithRequestMeta stores meta on the context.
func WithRequestMeta(ctx context.Context, meta RequestMeta) context.Context {
	return context.WithValue(ctx, RequestMetaKey, meta)
}

// GetRequestMeta returns the stored meta and whether one was set.
func GetRequestMeta(ctx context.Context) (RequestMeta, bool) {
	if ctx == nil {
		return RequestMeta{}, false
	}
	meta, ok := ctx.Value(RequestMetaKey).(RequestMeta)
	return meta, ok
}
