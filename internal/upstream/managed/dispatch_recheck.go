package managed

import (
	"context"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/core"
)

// ErrDispatchRecheckRefused marks a call that a WithDispatchRecheck check
// refused immediately before the transport. Nothing was sent upstream. It is
// core.ErrDispatchRecheckRefused, so a refusal raised by either layer matches.
var ErrDispatchRecheckRefused = core.ErrDispatchRecheckRefused

// WithDispatchRecheck attaches check to ctx (see core.WithDispatchRecheck).
// callTool runs the attached checks AFTER the admission-control queue wait,
// and the core client runs them again once it holds the SSE request lock,
// immediately before every send (including a session re-init's retry).
func WithDispatchRecheck(ctx context.Context, check func() error) context.Context {
	return core.WithDispatchRecheck(ctx, check)
}

// runDispatchRecheck runs ctx's attached checks; nil when there are none or
// all pass.
func runDispatchRecheck(ctx context.Context) error {
	return core.RunDispatchRecheck(ctx)
}
