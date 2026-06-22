package observability

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MCP-32: the OTLP exporter must support both HTTP and gRPC transports.

func TestBuildOTLPExporter_HTTP(t *testing.T) {
	exp, err := buildOTLPExporter(context.Background(), TracingConfig{
		Protocol:     "http",
		OTLPEndpoint: "localhost:4318",
	})
	require.NoError(t, err)
	require.NotNil(t, exp)
	_ = exp.Shutdown(context.Background())
}

func TestBuildOTLPExporter_GRPC(t *testing.T) {
	exp, err := buildOTLPExporter(context.Background(), TracingConfig{
		Protocol:     "grpc",
		OTLPEndpoint: "localhost:4317",
	})
	require.NoError(t, err)
	require.NotNil(t, exp)
	_ = exp.Shutdown(context.Background())
}

func TestBuildOTLPExporter_DefaultsToHTTP(t *testing.T) {
	// An empty protocol falls back to HTTP rather than erroring.
	exp, err := buildOTLPExporter(context.Background(), TracingConfig{
		OTLPEndpoint: "localhost:4318",
	})
	require.NoError(t, err)
	require.NotNil(t, exp)
	_ = exp.Shutdown(context.Background())
}

func TestBuildOTLPExporter_UnsupportedProtocol(t *testing.T) {
	_, err := buildOTLPExporter(context.Background(), TracingConfig{
		Protocol:     "carrier-pigeon",
		OTLPEndpoint: "localhost:4318",
	})
	assert.Error(t, err)
}
