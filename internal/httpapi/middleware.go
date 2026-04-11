package httpapi

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// XMCPProxyClientHeader is the HTTP header that clients (CLI, web UI, tray)
// set so the server can attribute requests to a surface for Tier 2 telemetry.
// Spec 042 User Story 1.
const XMCPProxyClientHeader = "X-MCPProxy-Client"

// RegistryGetter returns the current Tier 2 telemetry registry. Middlewares
// take a getter rather than the registry directly so the server can install
// the registry after route setup without re-mounting middlewares.
type RegistryGetter func() *telemetry.CounterRegistry

// SurfaceClassifierMiddleware reads the X-MCPProxy-Client header and
// increments the Tier 2 surface counter for the originating client. If the
// registry getter returns nil, the middleware is a no-op.
// Spec 042 User Story 1.
func SurfaceClassifierMiddleware(getReg RegistryGetter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			surface := telemetry.ParseClientSurface(r.Header.Get(XMCPProxyClientHeader))
			if getReg != nil {
				telemetry.RecordSurfaceOn(getReg(), surface)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// statusRecorder wraps http.ResponseWriter so we can capture the status code
// for the REST endpoint histogram.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (sr *statusRecorder) WriteHeader(code int) {
	if !sr.wrote {
		sr.status = code
		sr.wrote = true
	}
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Write(b []byte) (int, error) {
	if !sr.wrote {
		sr.status = http.StatusOK
		sr.wrote = true
	}
	return sr.ResponseWriter.Write(b)
}

// RESTEndpointHistogramMiddleware records every REST request under its Chi
// route template + status class. Templates with path parameters are recorded
// without the actual parameter values; unmatched routes are recorded under
// the literal key UNMATCHED. Spec 042 User Story 3.
func RESTEndpointHistogramMiddleware(getReg RegistryGetter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sr, r)

			if getReg == nil {
				return
			}
			reg := getReg()
			if reg == nil {
				return
			}

			template := "UNMATCHED"
			if rctx := chi.RouteContext(r.Context()); rctx != nil {
				if pat := rctx.RoutePattern(); pat != "" {
					template = pat
				}
			}
			cls := fmt.Sprintf("%dxx", sr.status/100)
			telemetry.RecordRESTRequestOn(reg, r.Method, template, cls)
		})
	}
}

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
