package index

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestBleveIndex_SearchTools_PreservesAnnotations is a regression test for a
// Spec 109 PR-a review finding (round 1, medium): GET /index/search (the Web
// UI's Tools search box and the macOS tools search) always rendered every hit
// as "Unannotated" regardless of the tool's real destructive/read/write
// hints. The bug sits one level below the missing "annotations" map key in
// server.searchResultsToMaps that a fix there alone could not have closed:
// the Bleve ToolDocument never stored annotations at all, so readToolMetadata
// could only ever hand back a nil Annotations pointer for a search hit, no
// matter what the map-shaping code around it does with it.
func TestBleveIndex_SearchTools_PreservesAnnotations(t *testing.T) {
	destructive := true
	tool := &config.ToolMetadata{
		Name:        "danger:delete_everything",
		ServerName:  "danger",
		Description: "irreversibly deletes everything, no confirmation",
		Annotations: &config.ToolAnnotations{
			DestructiveHint: &destructive,
		},
	}

	tmpDir, err := os.MkdirTemp("", "bleve_annotations_test_*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	bleveIndex, err := NewBleveIndex(tmpDir, zap.NewNop())
	require.NoError(t, err)
	defer bleveIndex.Close()

	require.NoError(t, bleveIndex.IndexTool(tool))

	results, err := bleveIndex.SearchTools("delete_everything", 10)
	require.NoError(t, err)
	require.Len(t, results, 1)

	require.NotNil(t, results[0].Tool.Annotations, "a search hit must carry its tool's annotations through the index round-trip")
	require.NotNil(t, results[0].Tool.Annotations.DestructiveHint)
	assert.True(t, *results[0].Tool.Annotations.DestructiveHint)
}

// TestBleveIndex_SearchToolsScoped_PreservesAnnotations covers the scoped
// twin (used by every scoped REST caller — Spec 107), which shares
// newToolSearchRequest with SearchTools but is a separate code path from the
// hit through to the caller.
func TestBleveIndex_SearchToolsScoped_PreservesAnnotations(t *testing.T) {
	readOnly := true
	tool := &config.ToolMetadata{
		Name:        "safe:list_things",
		ServerName:  "safe",
		Description: "lists things without changing anything",
		Annotations: &config.ToolAnnotations{
			ReadOnlyHint: &readOnly,
		},
	}

	tmpDir, err := os.MkdirTemp("", "bleve_annotations_scoped_test_*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	bleveIndex, err := NewBleveIndex(tmpDir, zap.NewNop())
	require.NoError(t, err)
	defer bleveIndex.Close()

	require.NoError(t, bleveIndex.IndexTool(tool))

	results, err := bleveIndex.SearchToolsScoped("list_things", 10, func(string) bool { return true })
	require.NoError(t, err)
	require.Len(t, results, 1)

	require.NotNil(t, results[0].Tool.Annotations)
	require.NotNil(t, results[0].Tool.Annotations.ReadOnlyHint)
	assert.True(t, *results[0].Tool.Annotations.ReadOnlyHint)
}
