package index

import (
	"path/filepath"
	"testing"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/keyword"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/standard"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// preAnnotationsMapping replicates createBleveIndex's mapping as it existed
// before the annotations_json field was added (Spec 109 PR-a), so tests can
// simulate opening an index created by an older mcpproxy version.
func preAnnotationsMapping() *mapping.IndexMappingImpl {
	indexMapping := bleve.NewIndexMapping()
	toolMapping := bleve.NewDocumentMapping()

	toolNameFieldKeyword := bleve.NewTextFieldMapping()
	toolNameFieldKeyword.Analyzer = keyword.Name
	toolNameFieldKeyword.Store = true
	toolNameFieldKeyword.Index = true
	toolMapping.AddFieldMappingsAt("tool_name", toolNameFieldKeyword)

	fullToolNameField := bleve.NewTextFieldMapping()
	fullToolNameField.Analyzer = keyword.Name
	fullToolNameField.Store = true
	fullToolNameField.Index = true
	toolMapping.AddFieldMappingsAt("full_tool_name", fullToolNameField)

	serverNameField := bleve.NewTextFieldMapping()
	serverNameField.Analyzer = keyword.Name
	serverNameField.Store = true
	serverNameField.Index = true
	toolMapping.AddFieldMappingsAt("server_name", serverNameField)

	descriptionField := bleve.NewTextFieldMapping()
	descriptionField.Analyzer = standard.Name
	descriptionField.Store = true
	descriptionField.Index = true
	toolMapping.AddFieldMappingsAt("description", descriptionField)

	paramsField := bleve.NewTextFieldMapping()
	paramsField.Analyzer = standard.Name
	paramsField.Store = true
	paramsField.Index = true
	toolMapping.AddFieldMappingsAt("params_json", paramsField)

	hashField := bleve.NewTextFieldMapping()
	hashField.Analyzer = keyword.Name
	hashField.Store = true
	hashField.Index = false
	toolMapping.AddFieldMappingsAt("hash", hashField)

	// Deliberately no annotations_json field mapping: this is the point.

	tagsField := bleve.NewTextFieldMapping()
	tagsField.Analyzer = standard.Name
	tagsField.Store = true
	tagsField.Index = true
	toolMapping.AddFieldMappingsAt("tags", tagsField)

	searchableTextField := bleve.NewTextFieldMapping()
	searchableTextField.Analyzer = standard.Name
	searchableTextField.Store = false
	searchableTextField.Index = true
	toolMapping.AddFieldMappingsAt("searchable_text", searchableTextField)

	indexMapping.AddDocumentMapping("tool", toolMapping)
	indexMapping.DefaultMapping = toolMapping

	return indexMapping
}

// TestNewBleveIndexAt_WarnsOnPreAnnotationsMapping is a regression test for
// review round 6, finding 1 (high): opening an index created before the
// annotations_json field mapping existed must surface a warning, since
// bleve.Open never migrates the mapping and the resulting dynamic-field
// leakage into full-text search is otherwise silent.
func TestNewBleveIndexAt_WarnsOnPreAnnotationsMapping(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "index.bleve")

	oldIdx, err := bleve.New(indexPath, preAnnotationsMapping())
	require.NoError(t, err)
	require.NoError(t, oldIdx.Close())

	core, logs := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	bi, err := newBleveIndexAt(indexPath, logger)
	require.NoError(t, err)
	defer bi.Close()

	warnings := logs.FilterMessageSnippet("predates the annotations_json field mapping")
	assert.Equal(t, 1, warnings.Len(), "opening a pre-annotations index must log exactly one actionable warning")
}

// TestNewBleveIndexAt_AutoRebuildsPreAnnotationsMapping is a regression test
// for review round 7, finding 2 (medium): round 6 only warned and left an
// in-place-upgraded index with the old mapping (and its dynamic-field
// leakage) in place forever unless an operator noticed the log line and
// deleted the directory by hand. Opening a pre-annotations index must now
// self-heal: end up with the current mapping with no manual step, and log
// that the automatic rebuild happened.
func TestNewBleveIndexAt_AutoRebuildsPreAnnotationsMapping(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "index.bleve")

	oldIdx, err := bleve.New(indexPath, preAnnotationsMapping())
	require.NoError(t, err)
	require.NoError(t, oldIdx.Close())

	core, logs := observer.New(zap.InfoLevel)
	logger := zap.New(core)

	bi, err := newBleveIndexAt(indexPath, logger)
	require.NoError(t, err)
	defer bi.Close()

	fm := bi.index.Mapping().FieldMappingForPath("annotations_json")
	assert.NotEmpty(t, fm.Type, "the index must carry the CURRENT mapping after opening a pre-annotations index, with no operator step")

	rebuilds := logs.FilterMessageSnippet("Rebuilt Bleve index with the current field mapping")
	assert.Equal(t, 1, rebuilds.Len(), "the automatic rebuild must be observable in the log")

	// The rebuilt index must behave like any other current-mapping index:
	// annotations_json is stored-only and must not leak into `_all`.
	require.NoError(t, bi.index.Index("doc1", map[string]interface{}{
		"tool_name":        "delete_everything",
		"annotations_json": "should not become searchable via _all",
	}))
	res, err := bi.index.Search(bleve.NewSearchRequest(bleve.NewMatchQuery("should not become searchable via _all")))
	require.NoError(t, err)
	assert.Equal(t, uint64(0), res.Total, "annotations_json must not be free-text searchable after the rebuild")
}

// TestNewBleveIndexAt_NoWarningOnCurrentMapping guards against a false
// positive: an index created (or previously opened) by the CURRENT code must
// never trigger the migration warning.
func TestNewBleveIndexAt_NoWarningOnCurrentMapping(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "index.bleve")

	core, logs := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	// First open creates the index with the current mapping.
	bi, err := newBleveIndexAt(indexPath, logger)
	require.NoError(t, err)
	require.NoError(t, bi.Close())

	// Re-opening it (the common restart path) must not warn either.
	bi2, err := newBleveIndexAt(indexPath, logger)
	require.NoError(t, err)
	defer bi2.Close()

	warnings := logs.FilterMessageSnippet("predates the annotations_json field mapping")
	assert.Equal(t, 0, warnings.Len(), "a current-mapping index must never trigger the migration warning")
}

// TestCreateBleveIndex_DynamicMappingDisabled locks in the defense-in-depth
// half of the round-6 finding-1 fix: a freshly created index must not fall
// back to bleve's dynamic-field defaults for an unmapped field, so adding a
// future ToolDocument field without also updating createBleveIndex fails
// loudly (the field is simply not indexed/stored) instead of silently
// leaking into full-text search and `_all`, the way annotations_json did on
// indexes created before this PR.
func TestCreateBleveIndex_DynamicMappingDisabled(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "index.bleve")

	idx, err := createBleveIndex(indexPath)
	require.NoError(t, err)
	defer idx.Close()

	require.NoError(t, idx.Index("doc1", map[string]interface{}{
		"tool_name":     "delete_everything",
		"totally_novel": "should not become searchable via _all",
	}))

	res, err := idx.Search(bleve.NewSearchRequest(bleve.NewMatchQuery("should not become searchable via _all")))
	require.NoError(t, err)
	assert.Equal(t, uint64(0), res.Total, "an unmapped field must not be dynamically indexed once Dynamic=false")
}
