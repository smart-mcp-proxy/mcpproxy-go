package server

import (
	"context"
	"sync"
)

// codeExecCapture is the side channel that carries a code_execution REFUSAL's
// typed identity out of the MCP dispatch layer.
//
// The MCP contract makes handleCodeExecution answer a refusal with (result,
// nil) — an isError result, never a transport error — so by the time
// CallToolDirect sees it, the only thing left is a string. But the REST surface
// has to distinguish "the operator switched this feature off" (403) and "no
// such stored script" (404, carrying the available names) from a genuine
// execution fault (500), and classifying those by re-parsing prose would break
// the moment a message is reworded. The handler therefore drops the typed error
// into this box on the way out, exactly as the concurrency shed does for its
// 429 (see concurrency_shed.go).
type codeExecCapture struct {
	mu  sync.Mutex
	err error
}

type codeExecCaptureKeyType struct{}

var codeExecCaptureKey codeExecCaptureKeyType

// withCodeExecCapture installs a capture box on ctx. Only the REST/CLI dispatch
// entry point (CallToolDirect) installs one; on the MCP transport path
// recordCodeExecRefusal is a no-op, since there the isError result IS the
// answer.
func withCodeExecCapture(ctx context.Context) (context.Context, *codeExecCapture) {
	box := &codeExecCapture{}
	return context.WithValue(ctx, codeExecCaptureKey, box), box
}

// recordCodeExecRefusal stores the typed refusal on the context's capture box,
// if any.
func recordCodeExecRefusal(ctx context.Context, err error) {
	if ctx == nil || err == nil {
		return
	}
	box, ok := ctx.Value(codeExecCaptureKey).(*codeExecCapture)
	if !ok || box == nil {
		return
	}
	box.mu.Lock()
	box.err = err
	box.mu.Unlock()
}

// take returns the captured refusal, if the handler refused the call.
func (c *codeExecCapture) take() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// codeExecDispatchError carries the agent-readable refusal text out of the MCP
// dispatch layer while keeping the typed identity reachable through
// errors.As/errors.Is — which is what lets the REST handler answer 403/404/400
// instead of the blanket 500 a flattened string produces.
type codeExecDispatchError struct {
	err     error
	message string
}

func (e *codeExecDispatchError) Error() string { return e.message }

func (e *codeExecDispatchError) Unwrap() error { return e.err }
