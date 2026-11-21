// Package clientrelay forwards an upstream server's server-to-client requests
// (elicitation/create, sampling/createMessage, roots/list) to the MCP client
// whose tool call caused them (#1964, #1965, #1966).
//
// An upstream connection is shared by every client of mcpproxy, so the only
// client that may answer is the one whose call the request belongs to. That
// client's mcp-go session travels in the call's context; the upstream layer
// hands this package that context (Handler.Caller). With no such client (no
// call in flight, a REST or CLI call, a client without the capability), the
// request is answered without asking anyone: elicitation is declined, sampling
// fails and roots/list returns no roots. roots/list alone also asks the last
// client that called the server (Handler.LastCaller), because servers ask for
// roots when they connect and when told the roots changed, outside any call.
package clientrelay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// ErrNoClient means no MCP client that can answer the request is attached to
// the call.
var ErrNoClient = errors.New("no MCP client in this call can answer")

// lastCallerTimeout bounds a roots/list asked of the last caller, whose
// session may be gone.
const lastCallerTimeout = 10 * time.Second

// Capabilities is what mcpproxy declares to an upstream it can relay for.
func Capabilities() mcp.ClientCapabilities {
	return mcp.ClientCapabilities{
		Roots: &struct {
			ListChanged bool `json:"listChanged,omitempty"`
		}{ListChanged: true},
		Sampling:    &mcp.SamplingCapability{},
		Elicitation: &mcp.ElicitationCapability{Form: &struct{}{}, URL: &struct{}{}},
	}
}

// Event is one upstream request and how it was answered, for the activity log.
type Event struct {
	Server    string
	Method    mcp.MCPMethod
	SessionID string // the client asked; "" when nobody was
	Params    any    // what the upstream asked
	Result    any    // what the upstream got back (also when nobody was asked)
	Err       error  // wraps ErrNoClient when nobody could be asked
	Duration  time.Duration
}

// Handler answers one upstream's server-to-client requests. It implements
// mcp-go's client.ElicitationHandler, client.SamplingHandler and
// client.RootsHandler.
type Handler struct {
	// Server is the upstream's name. Elicitation messages are prefixed with
	// "[Server] " so the user can tell which server is asking.
	Server string
	// Caller maps the context mcp-go hands the handler to the context of the
	// call the request belongs to, which carries the client's session.
	Caller func(context.Context) context.Context
	// LastCaller returns a context carrying the session of the last client
	// that called this server, or nil. Used by roots/list only.
	LastCaller func() context.Context
	// URLs remembers where URL-mode elicitations went, for their completion
	// notification. nil forwards none.
	URLs *URLElicitations
	// Audit receives every request and its answer. nil records nothing.
	Audit func(Event)
}

// Options returns the mcp-go client options that declare Capabilities and
// install h. Only for transports that can receive requests from the server.
func (h Handler) Options() []client.ClientOption {
	return []client.ClientOption{
		client.WithClientCapabilities(Capabilities()),
		client.WithElicitationHandler(h),
		client.WithSamplingHandler(h),
		client.WithRootsHandler(h),
	}
}

// Elicit forwards elicitation/create, or declines when no client can answer.
func (h Handler) Elicit(ctx context.Context, req mcp.ElicitationRequest) (res *mcp.ElicitationResult, err error) {
	start := time.Now()
	ctx = h.caller(ctx)
	asked := req.Params
	if h.Server != "" {
		req.Params.Message = "[" + h.Server + "] " + req.Params.Message
	}
	if req.Params.Mode == mcp.ElicitationModeURL && h.URLs != nil {
		// Recorded before asking: the flow can finish as soon as the user opens the URL.
		forget := h.URLs.add(req.Params.ElicitationID, mcpserver.ClientSessionFromContext(ctx))
		defer func() {
			if res == nil || res.Action != mcp.ElicitationResponseActionAccept {
				forget() // declined or failed: no completion will follow
			}
		}()
	}
	res, err = Elicit(ctx, req)
	reported := err
	if errors.Is(err, ErrNoClient) {
		res, err = &mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{Action: mcp.ElicitationResponseActionDecline}}, nil
	}
	h.audit(ctx, mcp.MethodElicitationCreate, asked, res, reported, start)
	return res, err
}

// CreateMessage forwards sampling/createMessage, or fails when no client can
// answer (sampling has no "declined" result).
func (h Handler) CreateMessage(ctx context.Context, req mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
	start := time.Now()
	ctx = h.caller(ctx)
	res, err := CreateMessage(ctx, req)
	h.audit(ctx, mcp.MethodSamplingCreateMessage, req.CreateMessageParams, res, err, start)
	return res, err
}

// ListRoots forwards roots/list to the calling client, else to the last client
// that called the server, or returns no roots when neither can answer.
func (h Handler) ListRoots(ctx context.Context, req mcp.ListRootsRequest) (*mcp.ListRootsResult, error) {
	start := time.Now()
	ctx = h.caller(ctx)
	usedLast := false
	if !HasClient(ctx) && h.LastCaller != nil {
		if last := h.LastCaller(); last != nil {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(last, lastCallerTimeout)
			defer cancel()
			usedLast = true
		}
	}
	res, err := ListRoots(ctx, req)
	if err != nil && usedLast && !errors.Is(err, ErrNoClient) {
		// The last caller may be long gone: answer no roots, as for nobody.
		err = fmt.Errorf("%w: last caller: %v", ErrNoClient, err)
	}
	reported := err
	if errors.Is(err, ErrNoClient) {
		res, err = &mcp.ListRootsResult{Roots: []mcp.Root{}}, nil
	}
	h.audit(ctx, mcp.MethodListRoots, nil, res, reported, start)
	return res, err
}

// Answer handles one server-to-client request read off a stream mcp-go does
// not read (mcpproxy's own GET stream to a streamable-HTTP upstream).
func (h Handler) Answer(ctx context.Context, method string, params json.RawMessage) (any, *mcp.JSONRPCErrorDetails) {
	var (
		res any
		err error
	)
	switch mcp.MCPMethod(method) {
	case mcp.MethodElicitationCreate:
		var req mcp.ElicitationRequest
		if err = decode(params, &req.Params); err == nil {
			if err = req.Params.Validate(); err != nil {
				return nil, &mcp.JSONRPCErrorDetails{Code: mcp.INVALID_PARAMS, Message: err.Error()}
			}
			res, err = h.Elicit(ctx, req)
		}
	case mcp.MethodSamplingCreateMessage:
		var req mcp.CreateMessageRequest
		if err = decode(params, &req.CreateMessageParams); err == nil {
			res, err = h.CreateMessage(ctx, req)
		}
	case mcp.MethodListRoots:
		res, err = h.ListRoots(ctx, mcp.ListRootsRequest{})
	default:
		return nil, &mcp.JSONRPCErrorDetails{Code: mcp.METHOD_NOT_FOUND, Message: "method not supported by mcpproxy: " + method}
	}
	if err != nil {
		return nil, &mcp.JSONRPCErrorDetails{Code: mcp.INTERNAL_ERROR, Message: err.Error()}
	}
	return res, nil
}

func decode(params json.RawMessage, v any) error {
	if len(params) == 0 {
		return nil
	}
	return json.Unmarshal(params, v)
}

func (h Handler) caller(ctx context.Context) context.Context {
	if h.Caller == nil {
		return ctx
	}
	return h.Caller(ctx)
}

// audit reports one request. res is passed as any, so a nil pointer result
// becomes a nil interface first.
func (h Handler) audit(ctx context.Context, method mcp.MCPMethod, params, res any, err error, start time.Time) {
	if h.Audit == nil {
		return
	}
	e := Event{Server: h.Server, Method: method, Params: params, Err: err, Duration: time.Since(start)}
	if !isNil(res) {
		e.Result = res
	}
	if s := mcpserver.ClientSessionFromContext(ctx); s != nil && !errors.Is(err, ErrNoClient) {
		e.SessionID = s.SessionID()
	}
	h.Audit(e)
}

func isNil(v any) bool {
	switch r := v.(type) {
	case nil:
		return true
	case *mcp.ElicitationResult:
		return r == nil
	case *mcp.CreateMessageResult:
		return r == nil
	case *mcp.ListRootsResult:
		return r == nil
	}
	return false
}

// HasClient reports whether ctx carries an MCP client session.
func HasClient(ctx context.Context) bool {
	return mcpserver.ClientSessionFromContext(ctx) != nil
}

// SessionID returns the id of the MCP client session in ctx, or "".
func SessionID(ctx context.Context) string {
	if s := mcpserver.ClientSessionFromContext(ctx); s != nil {
		return s.SessionID()
	}
	return ""
}

// Elicit sends req to the client session in ctx.
func Elicit(ctx context.Context, req mcp.ElicitationRequest) (*mcp.ElicitationResult, error) {
	s, caps, err := session(ctx)
	if err != nil {
		return nil, err
	}
	es, ok := s.(mcpserver.SessionWithElicitation)
	if !ok || !supportsMode(caps.Elicitation, req.Params.Mode) {
		return nil, fmt.Errorf("%w: client does not support %s elicitation", ErrNoClient, modeName(req.Params.Mode))
	}
	return es.RequestElicitation(ctx, req)
}

// CreateMessage sends req to the client session in ctx.
func CreateMessage(ctx context.Context, req mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
	s, caps, err := session(ctx)
	if err != nil {
		return nil, err
	}
	ss, ok := s.(mcpserver.SessionWithSampling)
	if !ok || caps.Sampling == nil {
		return nil, fmt.Errorf("%w: client does not support sampling", ErrNoClient)
	}
	return ss.RequestSampling(ctx, req)
}

// ListRoots sends req to the client session in ctx.
func ListRoots(ctx context.Context, req mcp.ListRootsRequest) (*mcp.ListRootsResult, error) {
	s, caps, err := session(ctx)
	if err != nil {
		return nil, err
	}
	rs, ok := s.(mcpserver.SessionWithRoots)
	if !ok || caps.Roots == nil {
		return nil, fmt.Errorf("%w: client does not support roots", ErrNoClient)
	}
	return rs.ListRoots(ctx, req)
}

// session returns the client session in ctx and its declared capabilities.
// A client on protocol 2026-07-28 or later cannot receive server-initiated
// requests (SEP-2322), so it counts as no client.
func session(ctx context.Context) (mcpserver.ClientSession, mcp.ClientCapabilities, error) {
	s := mcpserver.ClientSessionFromContext(ctx)
	if s == nil {
		return nil, mcp.ClientCapabilities{}, ErrNoClient
	}
	if mcp.IsModernProtocol(mcpserver.RequestProtocolVersion(ctx)) {
		return nil, mcp.ClientCapabilities{}, fmt.Errorf("%w: client protocol has no server-initiated requests", ErrNoClient)
	}
	info, ok := s.(mcpserver.SessionWithClientInfo)
	if !ok {
		return nil, mcp.ClientCapabilities{}, fmt.Errorf("%w: client capabilities unknown", ErrNoClient)
	}
	return s, info.GetClientCapabilities(), nil
}

// supportsMode reports whether a client's elicitation capability covers mode.
// An empty capability object means form mode only (2025-11-25 back-compat).
func supportsMode(c *mcp.ElicitationCapability, mode string) bool {
	if c == nil {
		return false
	}
	if mode == mcp.ElicitationModeURL {
		return c.URL != nil
	}
	return c.Form != nil || c.URL == nil
}

func modeName(mode string) string {
	if mode == "" {
		return mcp.ElicitationModeForm
	}
	return mode
}
