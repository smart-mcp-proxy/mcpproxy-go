package reqcontext

import "context"

type toolResultEnvelopeKey struct{}

// WithToolResultEnvelope opts a REST tool call into the full MCP result shape.
func WithToolResultEnvelope(ctx context.Context) context.Context {
	return context.WithValue(ctx, toolResultEnvelopeKey{}, true)
}

func ToolResultEnvelope(ctx context.Context) bool {
	return ctx != nil && ctx.Value(toolResultEnvelopeKey{}) == true
}
