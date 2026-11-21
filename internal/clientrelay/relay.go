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
// fails and roots/list returns no roots.
package clientrelay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"
)

// ErrNoClient means no MCP client that can answer the request is attached to
// the call.
var ErrNoClient = errors.New("no MCP client in this call can answer")

// Capabilities is what mcpproxy declares to an upstream it can relay for.
// Roots carry no listChanged: mcpproxy does not forward roots changes.
func Capabilities() mcp.ClientCapabilities {
	return mcp.ClientCapabilities{
		Roots: &struct {
			ListChanged bool `json:"listChanged,omitempty"`
		}{},
		Sampling:    &mcp.SamplingCapability{},
		Elicitation: &mcp.ElicitationCapability{Form: &struct{}{}, URL: &struct{}{}},
	}
}

// Handler answers an upstream's server-to-client requests. It implements
// mcp-go's client.ElicitationHandler, client.SamplingHandler and
// client.RootsHandler.
type Handler struct {
	// Caller maps the context mcp-go hands the handler to the context of the
	// call the request belongs to, which carries the client's session.
	Caller func(context.Context) context.Context
	// URLs remembers where URL-mode elicitations went, for their completion
	// notification. nil forwards none.
	URLs   *URLElicitations
	Logger *zap.Logger
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
	ctx = h.caller(ctx)
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
	if errors.Is(err, ErrNoClient) {
		h.log(mcp.MethodElicitationCreate, err)
		return &mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{Action: mcp.ElicitationResponseActionDecline}}, nil
	}
	return res, err
}

// CreateMessage forwards sampling/createMessage, or fails when no client can
// answer (sampling has no "declined" result).
func (h Handler) CreateMessage(ctx context.Context, req mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
	ctx = h.caller(ctx)
	res, err := CreateMessage(ctx, req)
	if errors.Is(err, ErrNoClient) {
		h.log(mcp.MethodSamplingCreateMessage, err)
	}
	return res, err
}

// ListRoots forwards roots/list, or returns no roots when no client can answer.
func (h Handler) ListRoots(ctx context.Context, req mcp.ListRootsRequest) (*mcp.ListRootsResult, error) {
	ctx = h.caller(ctx)
	res, err := ListRoots(ctx, req)
	if errors.Is(err, ErrNoClient) {
		h.log(mcp.MethodListRoots, err)
		return &mcp.ListRootsResult{Roots: []mcp.Root{}}, nil
	}
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

func (h Handler) log(method mcp.MCPMethod, err error) {
	if h.Logger != nil {
		h.Logger.Debug("Answered upstream request without a client", zap.String("method", string(method)), zap.Error(err))
	}
}

// HasClient reports whether ctx carries an MCP client session.
func HasClient(ctx context.Context) bool {
	return mcpserver.ClientSessionFromContext(ctx) != nil
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
