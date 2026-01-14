package httpapi

import (
	"context"
	"net/http"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
)

// RequestIDMiddleware extracts or generates a request ID for each request.
// If the client provides a valid X-Request-Id header, it is used.
// Otherwise, a new UUID v4 is generated.
// The request ID is:
// - Added to the request context
// - Set in the response header (before calling next handler)
// - Available for logging via GetRequestID(ctx)
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get or generate request ID
		providedID := r.Header.Get(reqcontext.RequestIDHeader)
		requestID := reqcontext.GetOrGenerateRequestID(providedID)

		// Set response header BEFORE calling next handler
		// This ensures the header is present even if the handler panics
		w.Header().Set(reqcontext.RequestIDHeader, requestID)

		// Add request ID to context
		ctx := reqcontext.WithRequestID(r.Context(), requestID)

		// Call next handler with updated context
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDLoggerMiddleware creates a logger with the request ID field and adds it to context.
// This middleware should be registered AFTER RequestIDMiddleware.
func RequestIDLoggerMiddleware(logger *zap.SugaredLogger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			// Get request ID from context (set by RequestIDMiddleware)
			requestID := reqcontext.GetRequestID(ctx)

			// Create logger with request_id field
			requestLogger := logger.With("request_id", requestID)

			// Also add correlation_id if present
			if correlationID := reqcontext.GetCorrelationID(ctx); correlationID != "" {
				requestLogger = requestLogger.With("correlation_id", correlationID)
			}

			// Store logger in context
			ctx = WithLogger(ctx, requestLogger)

			// Call next handler with updated context
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// WithLogger adds a logger to the context
func WithLogger(ctx context.Context, logger *zap.SugaredLogger) context.Context {
	return context.WithValue(ctx, reqcontext.LoggerKey, logger)
}

// GetLogger retrieves the logger from context, or returns a nop logger if not found
func GetLogger(ctx context.Context) *zap.SugaredLogger {
	if ctx == nil {
		return zap.NewNop().Sugar()
	}
	if logger, ok := ctx.Value(reqcontext.LoggerKey).(*zap.SugaredLogger); ok && logger != nil {
		return logger
	}
	return zap.NewNop().Sugar()
}
