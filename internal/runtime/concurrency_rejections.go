package runtime

import (
	"context"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
)

// RejectionMetricSink counts one shed call. It runs on the caller's goroutine
// at the moment of rejection, so implementations must be non-blocking (a
// Prometheus counter increment is).
type RejectionMetricSink func(server, reason, scope string)

// SetRejectionMetricSink installs the synchronous rejection counter (spec 093
// FR-013). Passing nil detaches it.
func (r *Runtime) SetRejectionMetricSink(sink RejectionMetricSink) {
	if r == nil {
		return
	}
	if sink == nil {
		r.rejectionMetric.Store(nil)
		return
	}
	r.rejectionMetric.Store(&sink)
}

// installRejectionObserver wires the concurrency limiter's shed seam to the
// activity log and the rejection counter (spec 093 FR-012/FR-013). The observer
// runs at the admission point inside the managed client, which is BELOW the MCP
// dispatch layer — that is what makes the "rejected" record origin-independent:
// sandboxed code-execution scripts and activity replay never touch
// internal/server, yet their sheds land in the activity log with the same shape
// as an MCP one.
//
// Both effects are SYNCHRONOUS here, not projections of the event bus. The bus
// drops events when a subscriber's channel is full, which under a burst of
// sheds — the only load where these numbers matter — would silently lose
// exactly the rows and counter increments an operator is looking at.
func (r *Runtime) installRejectionObserver() {
	if r == nil || r.upstreamManager == nil {
		return
	}
	r.upstreamManager.SetRejectionObserver(func(ctx context.Context, rej limiter.Rejection) {
		if sink := r.rejectionMetric.Load(); sink != nil {
			(*sink)(rej.Server, string(rej.Reason), string(rej.Scope))
		}
		r.EmitActivityToolCallRejected(
			rej.Server,
			rej.Tool,
			activitySourceFromContext(ctx),
			reqcontext.GetRequestID(ctx),
			string(rej.Reason),
			string(rej.Scope),
			rej.Message,
			rej.Limit,
			rej.RetryAfter.Milliseconds(),
			rej.Waited.Milliseconds(),
		)
	})
}

// ConcurrencyStats reports the live occupancy of the global aggregate limiter
// and of every per-server limiter (spec 093 FR-013). Sampled by the metrics
// bridge to publish the queue-depth gauges.
func (r *Runtime) ConcurrencyStats() (global limiter.Stats, servers map[string]limiter.Stats) {
	if r == nil || r.upstreamManager == nil {
		return limiter.Stats{}, nil
	}
	return r.upstreamManager.ConcurrencyStats()
}

// activitySourceFromContext maps the request-source context value onto the
// activity-log source vocabulary, matching what the MCP dispatch layer records.
// An unset source means the call did not come through an external surface: that
// is code execution or activity replay, which are internal.
func activitySourceFromContext(ctx context.Context) string {
	switch reqcontext.GetRequestSource(ctx) {
	case reqcontext.SourceCLI:
		return "cli"
	case reqcontext.SourceRESTAPI:
		return "api"
	case reqcontext.SourceMCP:
		return "mcp"
	case reqcontext.SourceInternal:
		return "internal"
	case reqcontext.SourceUnknown:
		return "internal"
	default:
		return "internal"
	}
}
