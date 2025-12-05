package oauth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

func TestNewCorrelationID(t *testing.T) {
	id1 := NewCorrelationID()
	id2 := NewCorrelationID()

	assert.NotEmpty(t, id1)
	assert.NotEmpty(t, id2)
	assert.NotEqual(t, id1, id2, "Correlation IDs should be unique")
	assert.Len(t, id1, 36, "UUID should be 36 characters")
}

func TestNewOAuthFlowContext(t *testing.T) {
	ctx := NewOAuthFlowContext("test-server")

	assert.Equal(t, "test-server", ctx.ServerName)
	assert.NotEmpty(t, ctx.CorrelationID)
	assert.Equal(t, FlowInitiated, ctx.State)
	assert.False(t, ctx.StartTime.IsZero())
}

func TestOAuthFlowContext_Duration(t *testing.T) {
	ctx := NewOAuthFlowContext("test-server")

	// Duration should be positive after creation
	duration := ctx.Duration()
	assert.True(t, duration >= 0, "Duration should be non-negative")
}

func TestWithFlowContext(t *testing.T) {
	flowCtx := NewOAuthFlowContext("test-server")
	ctx := WithFlowContext(context.Background(), flowCtx)

	// Should be able to retrieve the flow context
	retrieved := GetFlowContext(ctx)
	assert.Equal(t, flowCtx, retrieved)
}

func TestGetFlowContext(t *testing.T) {
	t.Run("returns nil for context without flow context", func(t *testing.T) {
		ctx := context.Background()
		flowCtx := GetFlowContext(ctx)
		assert.Nil(t, flowCtx)
	})

	t.Run("returns flow context when present", func(t *testing.T) {
		flowCtx := NewOAuthFlowContext("test-server")
		ctx := WithFlowContext(context.Background(), flowCtx)

		retrieved := GetFlowContext(ctx)
		require.NotNil(t, retrieved)
		assert.Equal(t, flowCtx.CorrelationID, retrieved.CorrelationID)
		assert.Equal(t, flowCtx.ServerName, retrieved.ServerName)
	})
}

func TestWithCorrelationID(t *testing.T) {
	correlationID := "test-correlation-123"
	ctx := WithCorrelationID(context.Background(), correlationID)

	retrieved := GetCorrelationID(ctx)
	assert.Equal(t, correlationID, retrieved)
}

func TestGetCorrelationID(t *testing.T) {
	t.Run("returns empty string for context without correlation ID", func(t *testing.T) {
		ctx := context.Background()
		id := GetCorrelationID(ctx)
		assert.Empty(t, id)
	})

	t.Run("returns correlation ID when present", func(t *testing.T) {
		correlationID := "my-correlation-id"
		ctx := WithCorrelationID(context.Background(), correlationID)

		id := GetCorrelationID(ctx)
		assert.Equal(t, correlationID, id)
	})

	t.Run("returns correlation ID from flow context", func(t *testing.T) {
		flowCtx := NewOAuthFlowContext("test-server")
		ctx := WithFlowContext(context.Background(), flowCtx)

		id := GetCorrelationID(ctx)
		assert.Equal(t, flowCtx.CorrelationID, id)
	})
}

func TestCorrelationLogger(t *testing.T) {
	logger := zaptest.NewLogger(t)
	correlationID := "test-correlation-456"
	ctx := WithCorrelationID(context.Background(), correlationID)

	correlatedLogger := CorrelationLogger(ctx, logger)

	// The logger should not be nil
	assert.NotNil(t, correlatedLogger)
}

func TestCorrelationLoggerWithFlow(t *testing.T) {
	logger := zaptest.NewLogger(t)
	flowCtx := NewOAuthFlowContext("test-server")
	ctx := WithFlowContext(context.Background(), flowCtx)

	correlatedLogger := CorrelationLoggerWithFlow(ctx, logger)

	// The logger should not be nil
	assert.NotNil(t, correlatedLogger)
}

func TestOAuthFlowState_String(t *testing.T) {
	testCases := []struct {
		state    OAuthFlowState
		expected string
	}{
		{FlowInitiated, "initiated"},
		{FlowAuthenticating, "authenticating"},
		{FlowTokenExchange, "token_exchange"},
		{FlowCompleted, "completed"},
		{FlowFailed, "failed"},
		{OAuthFlowState(99), "unknown"},
	}

	for _, tc := range testCases {
		t.Run(tc.expected, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.state.String())
		})
	}
}

func TestCorrelationLoggerWithEmptyContext(t *testing.T) {
	logger := zap.NewNop()
	ctx := context.Background()

	// Should not panic with empty context
	correlatedLogger := CorrelationLogger(ctx, logger)
	assert.NotNil(t, correlatedLogger)

	correlatedLoggerWithFlow := CorrelationLoggerWithFlow(ctx, logger)
	assert.NotNil(t, correlatedLoggerWithFlow)
}
