package observability

import (
	"context"
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// TracingConfig holds configuration for OpenTelemetry tracing
type TracingConfig struct {
	Enabled        bool    `json:"enabled"`
	ServiceName    string  `json:"service_name"`
	ServiceVersion string  `json:"service_version"`
	OTLPEndpoint   string  `json:"otlp_endpoint"`
	SampleRate     float64 `json:"sample_rate"`
}

// TracingManager manages OpenTelemetry tracing
type TracingManager struct {
	logger   *zap.SugaredLogger
	config   TracingConfig
	tracer   oteltrace.Tracer
	provider *trace.TracerProvider
	enabled  bool
}

// NewTracingManager creates a new tracing manager
func NewTracingManager(logger *zap.SugaredLogger, config TracingConfig) (*TracingManager, error) {
	tm := &TracingManager{
		logger:  logger,
		config:  config,
		enabled: config.Enabled,
	}

	if !config.Enabled {
		logger.Info("OpenTelemetry tracing disabled")
		return tm, nil
	}

	if err := tm.initTracing(); err != nil {
		return nil, fmt.Errorf("failed to initialize tracing: %w", err)
	}

	logger.Infow("OpenTelemetry tracing initialized",
		"service_name", config.ServiceName,
		"otlp_endpoint", config.OTLPEndpoint,
		"sample_rate", config.SampleRate)

	return tm, nil
}

// initTracing initializes OpenTelemetry tracing
func (tm *TracingManager) initTracing() error {
	// Create OTLP exporter
	exporter, err := otlptracehttp.New(context.Background(),
		otlptracehttp.WithEndpoint(tm.config.OTLPEndpoint),
		otlptracehttp.WithInsecure(), // Use HTTP instead of HTTPS for local development
	)
	if err != nil {
		return fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	// Create resource
	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String(tm.config.ServiceName),
			semconv.ServiceVersionKey.String(tm.config.ServiceVersion),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to create resource: %w", err)
	}

	// Create tracer provider
	tm.provider = trace.NewTracerProvider(
		trace.WithBatcher(exporter),
		trace.WithResource(res),
		trace.WithSampler(trace.TraceIDRatioBased(tm.config.SampleRate)),
	)

	// Set global tracer provider
	otel.SetTracerProvider(tm.provider)

	// Set global text map propagator
	otel.SetTextMapPropagator(propagation.TraceContext{})

	// Create tracer
	tm.tracer = otel.Tracer(tm.config.ServiceName)

	return nil
}

// Close shuts down the tracing provider
func (tm *TracingManager) Close(ctx context.Context) error {
	if !tm.enabled || tm.provider == nil {
		return nil
	}

	tm.logger.Info("Shutting down OpenTelemetry tracing")
	return tm.provider.Shutdown(ctx)
}

// StartSpan starts a new trace span
func (tm *TracingManager) StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, oteltrace.Span) {
	if !tm.enabled {
		return ctx, oteltrace.SpanFromContext(ctx)
	}

	return tm.tracer.Start(ctx, name, oteltrace.WithAttributes(attrs...))
}

// HTTPMiddleware returns middleware that adds tracing to HTTP requests
func (tm *TracingManager) HTTPMiddleware() func(http.Handler) http.Handler {
	if !tm.enabled {
		// Return a no-op middleware if tracing is disabled
		return func(next http.Handler) http.Handler {
			return next
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract trace context from incoming request
			ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))

			// Start span
			spanName := fmt.Sprintf("%s %s", r.Method, r.URL.Path)
			ctx, span := tm.tracer.Start(ctx, spanName,
				oteltrace.WithAttributes(
					semconv.HTTPMethodKey.String(r.Method),
					semconv.HTTPURLKey.String(r.URL.String()),
					semconv.HTTPSchemeKey.String(r.URL.Scheme),
					semconv.HTTPHostKey.String(r.Host),
					semconv.HTTPTargetKey.String(r.URL.Path),
					semconv.HTTPUserAgentKey.String(r.UserAgent()),
				),
			)
			defer span.End()

			// Wrap response writer to capture status code
			ww := &tracingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			// Inject trace context into response headers
			otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(w.Header()))

			// Call next handler with traced context
			next.ServeHTTP(ww, r.WithContext(ctx))

			// Set span attributes based on response
			span.SetAttributes(
				semconv.HTTPStatusCodeKey.Int(ww.statusCode),
			)

			// Set span status based on HTTP status code
			if ww.statusCode >= 400 {
				span.SetAttributes(attribute.String("error", "true"))
			}
		})
	}
}

// tracingResponseWriter wraps http.ResponseWriter to capture status code for tracing
type tracingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *tracingResponseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// TraceToolCall creates a span for tool call operations
func (tm *TracingManager) TraceToolCall(ctx context.Context, serverName, toolName string) (context.Context, oteltrace.Span) {
	if !tm.enabled {
		return ctx, oteltrace.SpanFromContext(ctx)
	}

	return tm.tracer.Start(ctx, "tool.call",
		oteltrace.WithAttributes(
			attribute.String("tool.server", serverName),
			attribute.String("tool.name", toolName),
			attribute.String("operation", "call_tool"),
		),
	)
}

// TraceUpstreamConnection creates a span for upstream connection operations
func (tm *TracingManager) TraceUpstreamConnection(ctx context.Context, serverName, operation string) (context.Context, oteltrace.Span) {
	if !tm.enabled {
		return ctx, oteltrace.SpanFromContext(ctx)
	}

	return tm.tracer.Start(ctx, "upstream.connection",
		oteltrace.WithAttributes(
			attribute.String("upstream.server", serverName),
			attribute.String("upstream.operation", operation),
		),
	)
}

// TraceIndexOperation creates a span for index operations
func (tm *TracingManager) TraceIndexOperation(ctx context.Context, operation string, toolCount int) (context.Context, oteltrace.Span) {
	if !tm.enabled {
		return ctx, oteltrace.SpanFromContext(ctx)
	}

	return tm.tracer.Start(ctx, "index.operation",
		oteltrace.WithAttributes(
			attribute.String("index.operation", operation),
			attribute.Int("index.tool_count", toolCount),
		),
	)
}

// TraceStorageOperation creates a span for storage operations
func (tm *TracingManager) TraceStorageOperation(ctx context.Context, operation string) (context.Context, oteltrace.Span) {
	if !tm.enabled {
		return ctx, oteltrace.SpanFromContext(ctx)
	}

	return tm.tracer.Start(ctx, "storage.operation",
		oteltrace.WithAttributes(
			attribute.String("storage.operation", operation),
		),
	)
}

// AddSpanAttributes adds attributes to the current span
func (tm *TracingManager) AddSpanAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	if !tm.enabled {
		return
	}

	span := oteltrace.SpanFromContext(ctx)
	span.SetAttributes(attrs...)
}

// SetSpanError marks the current span as having an error
func (tm *TracingManager) SetSpanError(ctx context.Context, err error) {
	if !tm.enabled {
		return
	}

	span := oteltrace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String("error", "true"))
	span.SetAttributes(attribute.String("error.message", err.Error()))
}

// IsEnabled returns whether tracing is enabled
func (tm *TracingManager) IsEnabled() bool {
	return tm.enabled
}
