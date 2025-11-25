package reqcontext

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

// ContextKey is the type for context keys to avoid collisions
type ContextKey string

const (
	// CorrelationIDKey is the context key for correlation IDs
	CorrelationIDKey ContextKey = "correlation_id"

	// RequestSourceKey is the context key for request source
	RequestSourceKey ContextKey = "request_source"
)

// RequestSource indicates where the request originated
type RequestSource string

const (
	// SourceRESTAPI indicates request came from HTTP REST API
	SourceRESTAPI RequestSource = "REST_API"

	// SourceCLI indicates request came from CLI command
	SourceCLI RequestSource = "CLI"

	// SourceMCP indicates request came from MCP protocol
	SourceMCP RequestSource = "MCP"

	// SourceInternal indicates internal/background operation
	SourceInternal RequestSource = "INTERNAL"

	// SourceUnknown indicates source could not be determined
	SourceUnknown RequestSource = "UNKNOWN"
)

// GenerateCorrelationID generates a new unique correlation ID
func GenerateCorrelationID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID if random fails
		return "fallback-" + hex.EncodeToString([]byte{0, 1, 2, 3, 4, 5, 6, 7})
	}
	return hex.EncodeToString(b)
}

// WithCorrelationID adds a correlation ID to the context
func WithCorrelationID(ctx context.Context, correlationID string) context.Context {
	return context.WithValue(ctx, CorrelationIDKey, correlationID)
}

// GetCorrelationID retrieves the correlation ID from context
func GetCorrelationID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(CorrelationIDKey).(string); ok {
		return id
	}
	return ""
}

// WithRequestSource adds request source to the context
func WithRequestSource(ctx context.Context, source RequestSource) context.Context {
	return context.WithValue(ctx, RequestSourceKey, source)
}

// GetRequestSource retrieves the request source from context
func GetRequestSource(ctx context.Context) RequestSource {
	if ctx == nil {
		return SourceUnknown
	}
	if source, ok := ctx.Value(RequestSourceKey).(RequestSource); ok {
		return source
	}
	return SourceUnknown
}

// WithMetadata adds both correlation ID and request source to context
func WithMetadata(ctx context.Context, source RequestSource) context.Context {
	correlationID := GenerateCorrelationID()
	ctx = WithCorrelationID(ctx, correlationID)
	ctx = WithRequestSource(ctx, source)
	return ctx
}
