package managed

import (
	"context"
	"sync/atomic"
)

// DispatchTally counts the tools/call requests a managed client actually
// handed to the transport on behalf of ONE logical call. It exists so
// telemetry can count upstream dispatch attempts from evidence rather than
// infer them from the final error: a call can end in an error without ever
// reaching the transport (cancelled in the admission queue, limiter retired,
// generation moved, client not connected) and a session-recovery call can
// reach it twice (the first request answered "session terminated", then the
// re-initialized session repeated it).
//
// The caller attaches one with WithDispatchTally before the call and reads
// Count after it returns. Without a tally in the context the client does no
// extra work.
type DispatchTally struct{ n atomic.Int64 }

type dispatchTallyKey struct{}

// WithDispatchTally returns a context carrying a fresh tally.
func WithDispatchTally(ctx context.Context) (context.Context, *DispatchTally) {
	t := &DispatchTally{}
	return context.WithValue(ctx, dispatchTallyKey{}, t), t
}

// Count is the number of tools/call requests sent so far. nil-safe.
func (t *DispatchTally) Count() int64 {
	if t == nil {
		return 0
	}
	return t.n.Load()
}

// noteWireDispatch records one request about to leave for the transport.
func noteWireDispatch(ctx context.Context) {
	if t, ok := ctx.Value(dispatchTallyKey{}).(*DispatchTally); ok && t != nil {
		t.n.Add(1)
	}
}
