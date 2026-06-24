package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// toolFor builds a minimal ToolMetadata for a given server and tool name.
func toolFor(server, tool string) *config.ToolMetadata {
	return &config.ToolMetadata{
		Name:        server + ":" + tool,
		ServerName:  server,
		Description: "tool " + tool + " on " + server,
		ParamsJSON:  `{"type":"object","properties":{},"required":[]}`,
		Hash:        server + "-" + tool + "-hash",
	}
}

// TestManager_ForProfile_CreatesIsolatedIndex verifies that ForProfile lazily
// creates index.bleve/profiles/<slug>/ and that docs in one profile are isolated
// from other profiles and from the shared default index.
func TestManager_ForProfile_CreatesIsolatedIndex(t *testing.T) {
	dataDir := t.TempDir()
	logger := zap.NewNop()

	m, err := NewManager(dataDir, logger)
	require.NoError(t, err)
	defer m.Close()

	alpha, err := m.ForProfile("alpha")
	require.NoError(t, err)
	require.NoError(t, alpha.BatchIndexTools([]*config.ToolMetadata{
		toolFor("s1", "a"),
		toolFor("s1", "b"),
	}))

	// The per-profile index directory must exist on disk under index.bleve/profiles/<slug>/.
	alphaDir := filepath.Join(dataDir, "index.bleve", "profiles", "alpha")
	info, statErr := os.Stat(alphaDir)
	require.NoError(t, statErr, "expected per-profile index dir at %s", alphaDir)
	require.True(t, info.IsDir())

	beta, err := m.ForProfile("beta")
	require.NoError(t, err)
	require.NoError(t, beta.BatchIndexTools([]*config.ToolMetadata{
		toolFor("s2", "c"),
	}))

	alphaCount, err := alpha.GetDocumentCount()
	require.NoError(t, err)
	assert.Equal(t, uint64(2), alphaCount, "alpha should hold only its own 2 docs")

	betaCount, err := beta.GetDocumentCount()
	require.NoError(t, err)
	assert.Equal(t, uint64(1), betaCount, "beta should hold only its own 1 doc")

	// The shared default index is untouched by profile indexing.
	sharedCount, err := m.GetDocumentCount()
	require.NoError(t, err)
	assert.Equal(t, uint64(0), sharedCount, "shared index must stay empty")
}

// TestManager_ForProfile_SameSlugReturnsSameIndex verifies ForProfile caches one
// index per slug so concurrent callers observe the same underlying documents.
func TestManager_ForProfile_SameSlugReturnsSameIndex(t *testing.T) {
	dataDir := t.TempDir()
	m, err := NewManager(dataDir, zap.NewNop())
	require.NoError(t, err)
	defer m.Close()

	first, err := m.ForProfile("alpha")
	require.NoError(t, err)
	require.NoError(t, first.IndexTool(toolFor("s1", "a")))

	second, err := m.ForProfile("alpha")
	require.NoError(t, err)
	require.Same(t, first, second, "ForProfile must return a cached, stable handle per slug")

	count, err := second.GetDocumentCount()
	require.NoError(t, err)
	assert.Equal(t, uint64(1), count)
}

// TestManager_ForProfile_RejectsInvalidSlug guards against path traversal: only
// validated, filesystem-safe slugs may map to a directory.
func TestManager_ForProfile_RejectsInvalidSlug(t *testing.T) {
	dataDir := t.TempDir()
	m, err := NewManager(dataDir, zap.NewNop())
	require.NoError(t, err)
	defer m.Close()

	for _, bad := range []string{"../escape", "UPPER", "with/slash", "with space", ".."} {
		_, err := m.ForProfile(bad)
		assert.Error(t, err, "slug %q must be rejected", bad)
	}

	// Empty slug returns the shared default manager.
	shared, err := m.ForProfile("")
	require.NoError(t, err)
	assert.Same(t, m, shared)
}

// TestManager_RebuildProfileFromShared_IsolatesOtherProfiles is the reload-isolation
// invariant: rebuilding profile A from the shared index must not touch profile B.
func TestManager_RebuildProfileFromShared_IsolatesOtherProfiles(t *testing.T) {
	dataDir := t.TempDir()
	m, err := NewManager(dataDir, zap.NewNop())
	require.NoError(t, err)
	defer m.Close()

	// Populate the shared index: s1 has 2 tools, s2 has 3 tools.
	require.NoError(t, m.BatchIndexTools([]*config.ToolMetadata{
		toolFor("s1", "a"), toolFor("s1", "b"),
		toolFor("s2", "c"), toolFor("s2", "d"), toolFor("s2", "e"),
	}))

	require.NoError(t, m.RebuildProfileFromShared("alpha", []string{"s1"}))
	require.NoError(t, m.RebuildProfileFromShared("beta", []string{"s2"}))

	alpha, _ := m.ForProfile("alpha")
	beta, _ := m.ForProfile("beta")

	alphaCount, _ := alpha.GetDocumentCount()
	betaCount, _ := beta.GetDocumentCount()
	assert.Equal(t, uint64(2), alphaCount)
	assert.Equal(t, uint64(3), betaCount)

	// Change alpha's membership to {s1, s2}; beta must be untouched.
	require.NoError(t, m.RebuildProfileFromShared("alpha", []string{"s1", "s2"}))

	alphaCount, _ = alpha.GetDocumentCount()
	betaCountAfter, _ := beta.GetDocumentCount()
	assert.Equal(t, uint64(5), alphaCount, "alpha rebuilt with both servers")
	assert.Equal(t, uint64(3), betaCountAfter, "beta doc-count must be unchanged")
}

// TestManager_RebuildProfileFromShared_PreservesOutputSchema is a regression
// guard (CodexReviewer on PR #756): the profile rebuild path must not drop
// OutputSchemaJSON. The shared index is populated via BatchIndexTools (the real
// indexing path), then a profile is rebuilt from it; the field must survive
// both the read (GetToolsByServer) and the write (BatchIndex) and be returned by
// the profile's search results — the exact path retrieve_tools uses.
func TestManager_RebuildProfileFromShared_PreservesOutputSchema(t *testing.T) {
	dataDir := t.TempDir()
	m, err := NewManager(dataDir, zap.NewNop())
	require.NoError(t, err)
	defer m.Close()

	const outputSchema = `{"type":"object","properties":{"result":{"type":"string"}}}`
	tool := toolFor("s1", "schematool")
	tool.OutputSchemaJSON = outputSchema

	require.NoError(t, m.BatchIndexTools([]*config.ToolMetadata{tool}))
	require.NoError(t, m.RebuildProfileFromShared("alpha", []string{"s1"}))

	alpha, err := m.ForProfile("alpha")
	require.NoError(t, err)

	// Via GetToolsByServer on the profile index.
	got, err := alpha.GetToolsByServer("s1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, outputSchema, got[0].OutputSchemaJSON, "output schema must survive profile rebuild (GetToolsByServer)")

	// Via search on the profile index (what retrieve_tools consumes).
	results, err := alpha.SearchTools("schematool", 10)
	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, outputSchema, results[0].Tool.OutputSchemaJSON, "output schema must survive profile rebuild (SearchTools)")
}

// TestManager_DropProfile_RemovesDir verifies that deleting a profile drops its
// on-disk index directory and that a later ForProfile recreates it empty.
func TestManager_DropProfile_RemovesDir(t *testing.T) {
	dataDir := t.TempDir()
	m, err := NewManager(dataDir, zap.NewNop())
	require.NoError(t, err)
	defer m.Close()

	alpha, err := m.ForProfile("alpha")
	require.NoError(t, err)
	require.NoError(t, alpha.IndexTool(toolFor("s1", "a")))

	alphaDir := filepath.Join(dataDir, "index.bleve", "profiles", "alpha")
	_, statErr := os.Stat(alphaDir)
	require.NoError(t, statErr)

	require.NoError(t, m.DropProfile("alpha"))

	_, statErr = os.Stat(alphaDir)
	assert.True(t, os.IsNotExist(statErr), "profile index dir must be removed on drop")

	// Recreating the profile yields a fresh, empty index.
	recreated, err := m.ForProfile("alpha")
	require.NoError(t, err)
	count, err := recreated.GetDocumentCount()
	require.NoError(t, err)
	assert.Equal(t, uint64(0), count)
}
