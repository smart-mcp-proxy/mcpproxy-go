package server

import (
	"context"
	"errors"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
)

// asShed reports whether err is a concurrency-limiter SHED — queue full or
// queue timeout. A server-unavailable rejection is not a shed: it means the
// target was disabled/removed while the call was queued, which keeps the
// existing "server not available" semantics (FR-009).
func asShed(err error) (*limiter.LimitError, bool) {
	var limitErr *limiter.LimitError
	if !errors.As(err, &limitErr) {
		return nil, false
	}
	switch limitErr.Reason {
	case limiter.ReasonQueueFull, limiter.ReasonQueueTimeout:
		return limitErr, true
	case limiter.ReasonServerUnavailable:
		return nil, false
	default:
		return nil, false
	}
}

// shedMessage renders the agent-facing explanation of a shed (FR-010). The
// wording lives on the typed error so the MCP result, the REST 429 body and the
// activity record all read identically.
func shedMessage(limitErr *limiter.LimitError) string {
	return limitErr.UserMessage()
}

// shedToolResult turns a shed into a normal tool-call error RESULT (isError:true)
// rather than a protocol error, so an agent session survives the rejection and
// can retry (FR-010).
func shedToolResult(limitErr *limiter.LimitError) *mcp.CallToolResult {
	return mcp.NewToolResultError(shedMessage(limitErr))
}

// shedDispatchError carries the agent-readable shed message out of the MCP
// dispatch layer while keeping the typed limiter identity reachable through
// errors.As — which is what lets the REST handler answer 429 + Retry-After
// (FR-011) instead of the blanket 500 it would produce from a flattened string.
type shedDispatchError struct {
	limitErr *limiter.LimitError
	message  string
}

func (e *shedDispatchError) Error() string { return e.message }

func (e *shedDispatchError) Unwrap() error { return e.limitErr }

// shedCapture is the side channel that carries a shed's TYPED identity out of
// the MCP dispatch layer.
//
// The MCP contract forces the handlers to answer a shed with (result, nil) —
// an isError result, never a transport error. But the REST endpoint has to map
// the same shed to HTTP 429 + Retry-After (FR-011), and by the time
// CallToolDirect sees the result the only thing left is a string. Rather than
// re-parse that string, CallToolDirect installs this box in the context and the
// handler drops the *limiter.LimitError into it on the way out.
type shedCapture struct {
	mu  sync.Mutex
	err *limiter.LimitError
}

type shedCaptureKeyType struct{}

var shedCaptureKey shedCaptureKeyType

// withShedCapture installs a capture box on ctx. Used by the REST dispatch
// entry point (CallToolDirect); the MCP transport path does not install one, so
// recordShed is a no-op there.
func withShedCapture(ctx context.Context) (context.Context, *shedCapture) {
	box := &shedCapture{}
	return context.WithValue(ctx, shedCaptureKey, box), box
}

// recordShed stores the typed rejection on the context's capture box, if any.
func recordShed(ctx context.Context, limitErr *limiter.LimitError) {
	if ctx == nil || limitErr == nil {
		return
	}
	box, ok := ctx.Value(shedCaptureKey).(*shedCapture)
	if !ok || box == nil {
		return
	}
	box.mu.Lock()
	box.err = limitErr
	box.mu.Unlock()
}

// take returns the captured rejection, if the dispatch shed the call.
func (c *shedCapture) take() *limiter.LimitError {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}
