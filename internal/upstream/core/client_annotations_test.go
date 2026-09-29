package core

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func TestToolAnnotationsFromWirePreservesEmptyCapturedAnnotations(t *testing.T) {
	annotations := toolAnnotationsFromWire(mcp.ToolAnnotation{})
	require.NotNil(t, annotations, "a fresh tools/list definition with no hints is captured, not legacy-unknown")
	require.Empty(t, annotations.Title)
	require.Nil(t, annotations.ReadOnlyHint)
	require.Nil(t, annotations.DestructiveHint)
}
