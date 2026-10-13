package dialgate

import (
	"context"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// Unwrapper is implemented by every transport WrapTransport returns.
type Unwrapper interface {
	Unwrap() transport.Interface
}

// Unwrap follows Unwrap() until it reaches a transport that is not a
// wrapper. Core asserts optional capabilities (the OAuth handler, the
// Streamable HTTP session) on the unwrapped value (FR-023b).
func Unwrap(t transport.Interface) transport.Interface {
	for t != nil {
		u, ok := t.(Unwrapper)
		if !ok {
			return t
		}
		t = u.Unwrap()
	}
	return t
}

// WrapTransport gates an mcp-go transport (FR-023a): Start, SendRequest and
// SendNotification refuse with ErrRetired once g is retired. It takes no
// lock (review R2.3): the bytes themselves are gated where they are written
// (gatedConn for HTTP and SSE) and the stdio child is killed by Retire, so
// the wrapper only stops a retired instance from starting new work.
//
// mcp-go and core discover optional behaviour by type assertion on the
// transport, so the wrapper preserves every capability its inner kind has
// and no other (FR-023b, review R8.1): the three kinds core builds each get
// their own wrapper type, and an unknown kind gets a generic one that the
// parity test rejects. A nil g returns t unchanged.
func WrapTransport(g *Gate, t transport.Interface) transport.Interface {
	if g == nil || t == nil {
		return t
	}
	if _, already := t.(Unwrapper); already {
		return t
	}
	b := base{g: g, inner: t}
	switch inner := t.(type) {
	case *transport.Stdio:
		return &gatedStdio{base: b, stdio: inner}
	case *transport.StreamableHTTP:
		return &gatedStreamableHTTP{base: b, http: inner}
	case *transport.SSE:
		return &gatedSSE{base: b, sse: inner}
	default:
		return &gatedGeneric{base: b}
	}
}

// base implements transport.Interface for every wrapper.
type base struct {
	g     *Gate
	inner transport.Interface
}

func (b *base) Start(ctx context.Context) error {
	if b.g.Retired() {
		return ErrRetired
	}
	return b.inner.Start(ctx)
}

func (b *base) SendRequest(ctx context.Context, request transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
	if b.g.Retired() {
		return nil, ErrRetired
	}
	return b.inner.SendRequest(ctx, request)
}

func (b *base) SendNotification(ctx context.Context, notification mcp.JSONRPCNotification) error {
	if b.g.Retired() {
		return ErrRetired
	}
	return b.inner.SendNotification(ctx, notification)
}

func (b *base) SetNotificationHandler(handler func(notification mcp.JSONRPCNotification)) {
	b.inner.SetNotificationHandler(handler)
}

func (b *base) Close() error { return b.inner.Close() }

func (b *base) GetSessionId() string { return b.inner.GetSessionId() }

// Unwrap returns the wrapped transport.
func (b *base) Unwrap() transport.Interface { return b.inner }

// gatedRequestHandler answers a server-to-client request with a JSON-RPC
// error, without calling the client's handler, once the gate is retired: a
// retired instance does no work for its upstream (T4.17 f).
func gatedRequestHandler(g *Gate, handler transport.RequestHandler) transport.RequestHandler {
	if handler == nil {
		return nil
	}
	return func(ctx context.Context, request transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
		if g.Retired() {
			return transport.NewJSONRPCErrorResponse(request.ID, mcp.INTERNAL_ERROR, ErrRetired.Error(), nil), nil
		}
		return handler(ctx, request)
	}
}

// gatedStdio wraps *transport.Stdio: BidirectionalInterface.
type gatedStdio struct {
	base
	stdio *transport.Stdio
}

func (w *gatedStdio) SetRequestHandler(handler transport.RequestHandler) {
	w.stdio.SetRequestHandler(gatedRequestHandler(w.g, handler))
}

// gatedStreamableHTTP wraps *transport.StreamableHTTP: BidirectionalInterface,
// HTTPConnection, RequiresLegacyProtocol and the OAuth handler accessors.
type gatedStreamableHTTP struct {
	base
	http *transport.StreamableHTTP
}

func (w *gatedStreamableHTTP) SetRequestHandler(handler transport.RequestHandler) {
	w.http.SetRequestHandler(gatedRequestHandler(w.g, handler))
}

func (w *gatedStreamableHTTP) SetProtocolVersion(version string) { w.http.SetProtocolVersion(version) }

func (w *gatedStreamableHTTP) RequiresLegacyProtocol() bool { return w.http.RequiresLegacyProtocol() }

func (w *gatedStreamableHTTP) GetOAuthHandler() *transport.OAuthHandler {
	return w.http.GetOAuthHandler()
}

func (w *gatedStreamableHTTP) IsOAuthEnabled() bool { return w.http.IsOAuthEnabled() }

// gatedSSE wraps *transport.SSE: HTTPConnection, SetConnectionLostHandler and
// the OAuth handler accessors.
type gatedSSE struct {
	base
	sse *transport.SSE
}

func (w *gatedSSE) SetProtocolVersion(version string) { w.sse.SetProtocolVersion(version) }

func (w *gatedSSE) SetConnectionLostHandler(handler func(error)) {
	w.sse.SetConnectionLostHandler(handler)
}

func (w *gatedSSE) GetOAuthHandler() *transport.OAuthHandler { return w.sse.GetOAuthHandler() }

func (w *gatedSSE) IsOAuthEnabled() bool { return w.sse.IsOAuthEnabled() }

// gatedGeneric wraps a transport kind core does not build. It forwards no
// optional capability; TestWrapTransportCapabilityParity fails for any kind
// that reaches it with one.
type gatedGeneric struct {
	base
}
