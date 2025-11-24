package reqcontext

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateCorrelationID(t *testing.T) {
	id1 := GenerateCorrelationID()
	id2 := GenerateCorrelationID()

	assert.NotEmpty(t, id1, "Correlation ID should not be empty")
	assert.NotEmpty(t, id2, "Correlation ID should not be empty")
	assert.NotEqual(t, id1, id2, "Each correlation ID should be unique")
	assert.Len(t, id1, 32, "Correlation ID should be 32 hex characters (16 bytes)")
}

func TestWithCorrelationID(t *testing.T) {
	ctx := context.Background()
	correlationID := "test-correlation-123"

	ctx = WithCorrelationID(ctx, correlationID)
	retrieved := GetCorrelationID(ctx)

	assert.Equal(t, correlationID, retrieved, "Should retrieve the same correlation ID")
}

func TestGetCorrelationID_NoValue(t *testing.T) {
	ctx := context.Background()
	retrieved := GetCorrelationID(ctx)

	assert.Empty(t, retrieved, "Should return empty string when no correlation ID is set")
}

func TestGetCorrelationID_NilContext(t *testing.T) {
	// Use context.TODO() instead of nil as recommended by staticcheck
	retrieved := GetCorrelationID(context.TODO())
	assert.Empty(t, retrieved, "Should return empty string for empty context")
}

func TestWithRequestSource(t *testing.T) {
	tests := []struct {
		name   string
		source RequestSource
	}{
		{"REST API", SourceRESTAPI},
		{"CLI", SourceCLI},
		{"MCP", SourceMCP},
		{"Internal", SourceInternal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = WithRequestSource(ctx, tt.source)
			retrieved := GetRequestSource(ctx)

			assert.Equal(t, tt.source, retrieved, "Should retrieve the same request source")
		})
	}
}

func TestGetRequestSource_NoValue(t *testing.T) {
	ctx := context.Background()
	retrieved := GetRequestSource(ctx)

	assert.Equal(t, SourceUnknown, retrieved, "Should return SourceUnknown when no source is set")
}

func TestGetRequestSource_NilContext(t *testing.T) {
	// Use context.TODO() instead of nil as recommended by staticcheck
	retrieved := GetRequestSource(context.TODO())
	assert.Equal(t, SourceUnknown, retrieved, "Should return SourceUnknown for empty context")
}

func TestWithMetadata(t *testing.T) {
	ctx := context.Background()
	ctx = WithMetadata(ctx, SourceRESTAPI)

	correlationID := GetCorrelationID(ctx)
	source := GetRequestSource(ctx)

	assert.NotEmpty(t, correlationID, "Correlation ID should be set")
	assert.Len(t, correlationID, 32, "Correlation ID should be 32 hex characters")
	assert.Equal(t, SourceRESTAPI, source, "Request source should be set correctly")
}

func TestRequestSourceConstants(t *testing.T) {
	// Verify all constants are defined and unique
	sources := []RequestSource{
		SourceRESTAPI,
		SourceCLI,
		SourceMCP,
		SourceInternal,
		SourceUnknown,
	}

	seen := make(map[RequestSource]bool)
	for _, source := range sources {
		assert.NotEmpty(t, source, "Source constant should not be empty")
		assert.False(t, seen[source], "Source constant should be unique: %s", source)
		seen[source] = true
	}
}

func TestContextKeyCollision(t *testing.T) {
	// Verify that our context keys don't collide with each other
	assert.NotEqual(t, CorrelationIDKey, RequestSourceKey, "Context keys should be different")
}
