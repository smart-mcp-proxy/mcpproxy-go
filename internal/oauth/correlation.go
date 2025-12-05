// Package oauth provides OAuth 2.1 authentication support for MCP servers.
// This file implements correlation ID tracking for OAuth flow traceability.
package oauth

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// OAuthFlowState represents the current state of an OAuth authentication flow.
type OAuthFlowState int

const (
	// FlowInitiated indicates the OAuth flow has started.
	FlowInitiated OAuthFlowState = iota
	// FlowAuthenticating indicates the browser is open and waiting for user authentication.
	FlowAuthenticating
	// FlowTokenExchange indicates the authorization code is being exchanged for tokens.
	FlowTokenExchange
	// FlowCompleted indicates the OAuth flow completed successfully.
	FlowCompleted
	// FlowFailed indicates the OAuth flow failed with an error.
	FlowFailed
)

// String returns a human-readable representation of the OAuth flow state.
func (s OAuthFlowState) String() string {
	switch s {
	case FlowInitiated:
		return "initiated"
	case FlowAuthenticating:
		return "authenticating"
	case FlowTokenExchange:
		return "token_exchange"
	case FlowCompleted:
		return "completed"
	case FlowFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// OAuthFlowContext represents the context for a single OAuth authentication flow.
// It contains a unique correlation ID that links all log entries for this flow.
type OAuthFlowContext struct {
	// CorrelationID is a UUID that uniquely identifies this OAuth flow.
	CorrelationID string
	// ServerName is the name of the MCP server being authenticated.
	ServerName string
	// StartTime is when the OAuth flow was initiated.
	StartTime time.Time
	// State is the current state of the OAuth flow.
	State OAuthFlowState
}

// NewOAuthFlowContext creates a new OAuth flow context with a unique correlation ID.
func NewOAuthFlowContext(serverName string) *OAuthFlowContext {
	return &OAuthFlowContext{
		CorrelationID: NewCorrelationID(),
		ServerName:    serverName,
		StartTime:     time.Now(),
		State:         FlowInitiated,
	}
}

// SetState updates the state of the OAuth flow.
func (c *OAuthFlowContext) SetState(state OAuthFlowState) {
	c.State = state
}

// Duration returns the time elapsed since the flow started.
func (c *OAuthFlowContext) Duration() time.Duration {
	return time.Since(c.StartTime)
}

// contextKey is a type for context keys to avoid collisions.
type contextKey string

const (
	// correlationIDKey is the context key for storing correlation IDs.
	correlationIDKey contextKey = "oauth_correlation_id"
	// flowContextKey is the context key for storing the full OAuth flow context.
	flowContextKey contextKey = "oauth_flow_context"
)

// NewCorrelationID generates a new unique correlation ID using UUID v4.
func NewCorrelationID() string {
	return uuid.New().String()
}

// WithCorrelationID returns a new context with the given correlation ID attached.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationIDKey, id)
}

// GetCorrelationID retrieves the correlation ID from the context.
// Returns an empty string if no correlation ID is present.
func GetCorrelationID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(correlationIDKey).(string); ok {
		return id
	}
	return ""
}

// WithFlowContext returns a new context with the OAuth flow context attached.
func WithFlowContext(ctx context.Context, flowCtx *OAuthFlowContext) context.Context {
	ctx = context.WithValue(ctx, flowContextKey, flowCtx)
	return WithCorrelationID(ctx, flowCtx.CorrelationID)
}

// GetFlowContext retrieves the OAuth flow context from the context.
// Returns nil if no flow context is present.
func GetFlowContext(ctx context.Context) *OAuthFlowContext {
	if ctx == nil {
		return nil
	}
	if flowCtx, ok := ctx.Value(flowContextKey).(*OAuthFlowContext); ok {
		return flowCtx
	}
	return nil
}

// CorrelationLogger returns a logger with the correlation_id field added if present in context.
// If no correlation ID is found, returns the original logger unchanged.
func CorrelationLogger(ctx context.Context, logger *zap.Logger) *zap.Logger {
	if ctx == nil {
		return logger
	}
	if id := GetCorrelationID(ctx); id != "" {
		return logger.With(zap.String("correlation_id", id))
	}
	return logger
}

// CorrelationLoggerWithFlow returns a logger with both correlation_id and flow state fields.
func CorrelationLoggerWithFlow(ctx context.Context, logger *zap.Logger) *zap.Logger {
	if ctx == nil {
		return logger
	}

	flowCtx := GetFlowContext(ctx)
	if flowCtx == nil {
		return CorrelationLogger(ctx, logger)
	}

	return logger.With(
		zap.String("correlation_id", flowCtx.CorrelationID),
		zap.String("server", flowCtx.ServerName),
		zap.String("flow_state", flowCtx.State.String()),
		zap.Duration("flow_duration", flowCtx.Duration()),
	)
}
