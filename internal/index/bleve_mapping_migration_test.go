package index

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/keyword"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/standard"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// preAnnotationsMapping replicates createBleveIndex's mapping as it existed
// before the annotations_json field was added (Spec 109 PR-a), so tests can
// simulate opening an index created by an older mcpproxy version. It keeps
// bleve's default dynamic mapping, so both annotations_json and
// output_schema_json fall back to the dynamic defaults (full-text indexed,
// included in `_all`) exactly as they did on released indexes.
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

	// Deliberately no annotations_json / output_schema_json mapping: this is
	// the point.

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

// Tokens that appear ONLY inside annotations_json / output_schema_json of the
// migration fixtures, so a field-less match on them can only come from those
// two fields leaking into `_all`.
const (
	annotationsOnlyToken  = "zebracorn"
	outputSchemaOnlyToken = "quokkafield"
)

func migrationFixtureTools() []*config.ToolMetadata {
	destructive := true
	readOnly := true
	return []*config.ToolMetadata{
		{
			Name:             "delete_repo",
			ServerName:       "github",
			Description:      "Delete a repository permanently",
			ParamsJSON:       `{"type":"object","properties":{"repo":{"type":"string"}}}`,
			OutputSchemaJSON: `{"type":"object","properties":{"` + outputSchemaOnlyToken + `":{"type":"string"}}}`,
			Hash:             "hash-delete",
			Annotations: &config.ToolAnnotations{
				Title:           "Delete " + annotationsOnlyToken,
				DestructiveHint: &destructive,
			},
		},
		{
			Name:        "list_repos",
			ServerName:  "github",
			Description: "List repositories for a user",
			ParamsJSON:  `{"type":"object","properties":{"user":{"type":"string"}}}`,
			Hash:        "hash-list",
			Annotations: &config.ToolAnnotations{ReadOnlyHint: &readOnly},
		},
		{
			Name:        "read_file",
			ServerName:  "fs",
			Description: "Read a file from disk and list its repository metadata",
			ParamsJSON:  `{"type":"object","properties":{"path":{"type":"string"}}}`,
			Hash:        "hash-read",
		},
	}
}

// writeLegacyIndex creates an index at indexPath with the pre-annotations
// mapping and writes tools exactly as toolDocument would, simulating an index
// populated by an older mcpproxy version (no schema-version stamp either).
func writeLegacyIndex(t *testing.T, indexPath string, tools []*config.ToolMetadata) {
	t.Helper()
	idx, err := bleve.New(indexPath, preAnnotationsMapping())
	require.NoError(t, err)
	for _, tool := range tools {
		docID, doc := toolDocument(tool)
		require.NoError(t, idx.Index(docID, doc))
	}
	require.NoError(t, idx.Close())
}

func fieldlessMatchTotal(t *testing.T, idx bleve.Index, text string) uint64 {
	t.Helper()
	res, err := idx.Search(bleve.NewSearchRequest(bleve.NewMatchQuery(text)))
	require.NoError(t, err)
	return res.Total
}

// assertCurrentMapping asserts idx carries exactly the mapping createBleveIndex
// writes today and the current schema-version stamp.
func assertCurrentMapping(t *testing.T, idx bleve.Index) {
	t.Helper()
	want, err := currentMappingJSON()
	require.NoError(t, err)
	got, err := idx.GetInternal(bleveMappingInternalKey)
	require.NoError(t, err)
	assert.JSONEq(t, string(want), string(got), "persisted mapping must equal the current mapping")
	ver, err := idx.GetInternal(indexSchemaVersionKey)
	require.NoError(t, err)
	assert.Equal(t, indexSchemaVersion, string(ver), "index must carry the current schema-version stamp")
}

// (a) An index created with the legacy mapping is rebuilt on open to the
// current mapping, keeping every document and every stored field.
func TestNewBleveIndexAt_MigratesLegacyMapping(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "index.bleve")
	tools := migrationFixtureTools()
	writeLegacyIndex(t, indexPath, tools)

	core, logs := observer.New(zap.InfoLevel)
	bi, err := newBleveIndexAt(indexPath, zap.New(core))
	require.NoError(t, err)

	assertCurrentMapping(t, bi.index)
	assert.Equal(t, 1, logs.FilterMessageSnippet("Migrated Bleve index to the current mapping").Len(),
		"the migration must be observable in the log")

	count, err := bi.GetDocumentCount()
	require.NoError(t, err)
	assert.Equal(t, uint64(len(tools)), count, "migration must keep every document")

	got, err := bi.GetToolsByServer("github")
	require.NoError(t, err)
	require.Len(t, got, 2)
	byName := map[string]*config.ToolMetadata{}
	for _, tm := range got {
		byName[tm.Name] = tm
	}
	del := byName["github:delete_repo"]
	require.NotNil(t, del)
	assert.Equal(t, tools[0].Description, del.Description)
	assert.Equal(t, tools[0].ParamsJSON, del.ParamsJSON)
	assert.Equal(t, tools[0].OutputSchemaJSON, del.OutputSchemaJSON)
	assert.Equal(t, tools[0].Hash, del.Hash)
	require.NotNil(t, del.Annotations)
	assert.Equal(t, tools[0].Annotations.Title, del.Annotations.Title)
	require.NotNil(t, del.Annotations.DestructiveHint)
	assert.True(t, *del.Annotations.DestructiveHint)

	// Reopening the migrated index is the steady-state restart path and must
	// not migrate again.
	require.NoError(t, bi.Close())
	core2, logs2 := observer.New(zap.InfoLevel)
	bi2, err := newBleveIndexAt(indexPath, zap.New(core2))
	require.NoError(t, err)
	defer bi2.Close()
	assert.Equal(t, 0, logs2.FilterMessageSnippet("Migrated Bleve index").Len(),
		"a current index must not be rebuilt on every open")
}

// (a) The point of the migration: a migrated index ranks identically to an
// index freshly created from the same tools, so search results no longer
// depend on when the index was first created.
func TestNewBleveIndexAt_MigratedScoresMatchFreshIndex(t *testing.T) {
	tools := migrationFixtureTools()

	legacyPath := filepath.Join(t.TempDir(), "index.bleve")
	writeLegacyIndex(t, legacyPath, tools)
	migrated, err := newBleveIndexAt(legacyPath, zap.NewNop())
	require.NoError(t, err)
	defer migrated.Close()

	fresh, err := newBleveIndexAt(filepath.Join(t.TempDir(), "index.bleve"), zap.NewNop())
	require.NoError(t, err)
	defer fresh.Close()
	require.NoError(t, fresh.BatchIndex(tools))

	for _, q := range []string{"repository", "delete repository", "list", "read_file", "string"} {
		want, err := fresh.SearchTools(q, 10)
		require.NoError(t, err)
		got, err := migrated.SearchTools(q, 10)
		require.NoError(t, err)
		require.Len(t, got, len(want), "query %q: hit count", q)
		for i := range want {
			assert.Equal(t, want[i].Tool.Name, got[i].Tool.Name, "query %q: rank %d", q, i)
			assert.InDelta(t, want[i].Score, got[i].Score, 1e-9, "query %q: score of %s", q, want[i].Tool.Name)
		}
	}
}

// (b) annotations_json is never matchable by a field-less query, whether the
// index was created fresh, migrated from the legacy mapping on open, or
// rebuilt at runtime via RebuildIndex.
func TestAnnotationsJSON_NeverFieldlessMatchable(t *testing.T) {
	tools := migrationFixtureTools()

	// Precondition: on the legacy mapping the token IS matchable — otherwise
	// the assertions below would pass vacuously.
	legacyPath := filepath.Join(t.TempDir(), "index.bleve")
	writeLegacyIndex(t, legacyPath, tools)
	raw, err := bleve.Open(legacyPath)
	require.NoError(t, err)
	require.NotZero(t, fieldlessMatchTotal(t, raw, annotationsOnlyToken),
		"fixture must reproduce the legacy leak of annotations_json into _all")
	require.NoError(t, raw.Close())

	migrated, err := newBleveIndexAt(legacyPath, zap.NewNop())
	require.NoError(t, err)
	defer migrated.Close()

	fresh, err := newBleveIndexAt(filepath.Join(t.TempDir(), "index.bleve"), zap.NewNop())
	require.NoError(t, err)
	defer fresh.Close()
	require.NoError(t, fresh.BatchIndex(tools))

	rebuilt, err := newBleveIndexAt(filepath.Join(t.TempDir(), "index.bleve"), zap.NewNop())
	require.NoError(t, err)
	defer rebuilt.Close()
	require.NoError(t, rebuilt.BatchIndex(tools))
	require.NoError(t, rebuilt.RebuildIndex())

	for name, bi := range map[string]*BleveIndex{"fresh": fresh, "migrated": migrated, "rebuilt": rebuilt} {
		t.Run(name, func(t *testing.T) {
			assert.Zero(t, fieldlessMatchTotal(t, bi.index, annotationsOnlyToken),
				"annotations_json must not reach _all")
			results, err := bi.SearchTools(annotationsOnlyToken, 10)
			require.NoError(t, err)
			assert.Empty(t, results, "SearchTools must not match on annotation JSON")

			// Still stored and read back, just not searchable.
			hits, err := bi.SearchTools("delete_repo", 10)
			require.NoError(t, err)
			require.NotEmpty(t, hits)
			require.NotNil(t, hits[0].Tool.Annotations)
			assert.Equal(t, "Delete "+annotationsOnlyToken, hits[0].Tool.Annotations.Title)
		})
	}
}

// (c) output_schema_json has an explicit stored-only mapping and is kept
// byte-for-byte on fresh and migrated indexes alike (it used to depend on the
// dynamic-mapping fallback everywhere, which Dynamic=false would have
// silently dropped).
func TestOutputSchemaJSON_StoredOnFreshAndMigratedIndexes(t *testing.T) {
	tools := migrationFixtureTools()

	legacyPath := filepath.Join(t.TempDir(), "index.bleve")
	writeLegacyIndex(t, legacyPath, tools)
	migrated, err := newBleveIndexAt(legacyPath, zap.NewNop())
	require.NoError(t, err)
	defer migrated.Close()

	fresh, err := newBleveIndexAt(filepath.Join(t.TempDir(), "index.bleve"), zap.NewNop())
	require.NoError(t, err)
	defer fresh.Close()
	require.NoError(t, fresh.BatchIndex(tools))

	for name, bi := range map[string]*BleveIndex{"fresh": fresh, "migrated": migrated} {
		t.Run(name, func(t *testing.T) {
			fm := bi.index.Mapping().FieldMappingForPath("output_schema_json")
			assert.Equal(t, "text", fm.Type, "output_schema_json must be explicitly mapped")
			assert.True(t, fm.Store, "output_schema_json must be stored")
			assert.False(t, fm.Index, "output_schema_json is JSON, not prose: stored only")

			byServer, err := bi.GetToolsByServer("github")
			require.NoError(t, err)
			var found bool
			for _, tm := range byServer {
				if tm.Name == "github:delete_repo" {
					found = true
					assert.Equal(t, tools[0].OutputSchemaJSON, tm.OutputSchemaJSON)
				}
			}
			assert.True(t, found)

			hits, err := bi.SearchTools("delete_repo", 10)
			require.NoError(t, err)
			require.NotEmpty(t, hits)
			assert.Equal(t, tools[0].OutputSchemaJSON, hits[0].Tool.OutputSchemaJSON,
				"a search hit must carry the output schema")

			assert.Zero(t, fieldlessMatchTotal(t, bi.index, outputSchemaOnlyToken),
				"output_schema_json must behave the same on every index: not in _all")
		})
	}
}

// A current mapping with a missing/older schema-version stamp (a change to how
// documents are derived, not to the mapping) must also trigger a rebuild.
func TestNewBleveIndexAt_StaleSchemaVersionTriggersRebuild(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "index.bleve")
	bi, err := newBleveIndexAt(indexPath, zap.NewNop())
	require.NoError(t, err)
	require.NoError(t, bi.BatchIndex(migrationFixtureTools()))
	require.NoError(t, bi.index.SetInternal(indexSchemaVersionKey, []byte("0")))
	require.NoError(t, bi.Close())

	core, logs := observer.New(zap.InfoLevel)
	bi, err = newBleveIndexAt(indexPath, zap.New(core))
	require.NoError(t, err)
	defer bi.Close()

	assert.Equal(t, 1, logs.FilterMessageSnippet("Migrated Bleve index to the current mapping").Len())
	assertCurrentMapping(t, bi.index)
	count, err := bi.GetDocumentCount()
	require.NoError(t, err)
	assert.Equal(t, uint64(3), count)
}

// The shared index directory also holds the per-profile indexes
// (index.bleve/profiles/<slug>/). Migrating the shared index must not touch
// them.
func TestNewManager_MigrationPreservesProfileIndexes(t *testing.T) {
	dataDir := t.TempDir()
	sharedPath := filepath.Join(dataDir, "index.bleve")
	writeLegacyIndex(t, sharedPath, migrationFixtureTools())

	profilePath := filepath.Join(sharedPath, profilesDirName, "dev")
	pi, err := newBleveIndexAt(profilePath, zap.NewNop())
	require.NoError(t, err)
	require.NoError(t, pi.BatchIndex(migrationFixtureTools()[:1]))
	require.NoError(t, pi.Close())

	m, err := NewManager(dataDir, zap.NewNop())
	require.NoError(t, err)
	defer m.Close()

	assertCurrentMapping(t, m.bleveIndex.index)
	pm, err := m.ForProfile("dev")
	require.NoError(t, err)
	count, err := pm.GetDocumentCount()
	require.NoError(t, err)
	assert.Equal(t, uint64(1), count, "the profile index must survive the shared index's migration")
}

// A crash midway through the directory swap leaves the index directory without
// index_meta.json (that file is removed first and restored last). The next open
// must recover with a fresh, empty, current index instead of failing startup,
// and must not touch the nested profiles directory.
func TestNewBleveIndexAt_RecoversFromInterruptedSwap(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "index.bleve")
	bi, err := newBleveIndexAt(indexPath, zap.NewNop())
	require.NoError(t, err)
	require.NoError(t, bi.BatchIndex(migrationFixtureTools()))
	require.NoError(t, bi.Close())
	require.NoError(t, os.MkdirAll(filepath.Join(indexPath, profilesDirName, "dev"), 0o755))
	require.NoError(t, os.Remove(filepath.Join(indexPath, "index_meta.json")))

	bi, err = newBleveIndexAt(indexPath, zap.NewNop())
	require.NoError(t, err)
	defer bi.Close()

	assertCurrentMapping(t, bi.index)
	assert.DirExists(t, filepath.Join(indexPath, profilesDirName, "dev"))
}

// A leftover rebuild directory from an interrupted migration is removed on the
// next open.
func TestNewBleveIndexAt_RemovesStaleRebuildDir(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "index.bleve")
	require.NoError(t, os.MkdirAll(indexPath+rebuildDirSuffix, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(indexPath+rebuildDirSuffix, "junk"), []byte("x"), 0o600))

	bi, err := newBleveIndexAt(indexPath, zap.NewNop())
	require.NoError(t, err)
	defer bi.Close()

	assert.NoDirExists(t, indexPath+rebuildDirSuffix)
}

// RebuildIndex (previously a logging no-op) re-creates a live index with the
// current mapping and keeps every document, via the Manager surface too.
func TestManager_RebuildIndexKeepsDocuments(t *testing.T) {
	dataDir := t.TempDir()
	m, err := NewManager(dataDir, zap.NewNop())
	require.NoError(t, err)
	defer m.Close()
	require.NoError(t, m.BatchIndexTools(migrationFixtureTools()))

	before, err := m.SearchTools("repository", 10)
	require.NoError(t, err)
	require.NotEmpty(t, before)

	require.NoError(t, m.RebuildIndex())

	assertCurrentMapping(t, m.bleveIndex.index)
	count, err := m.GetDocumentCount()
	require.NoError(t, err)
	assert.Equal(t, uint64(3), count)
	after, err := m.SearchTools("repository", 10)
	require.NoError(t, err)
	require.Len(t, after, len(before))
	for i := range before {
		assert.Equal(t, before[i].Tool.Name, after[i].Tool.Name)
		assert.InDelta(t, before[i].Score, after[i].Score, 1e-9)
	}

	// Writes keep working against the swapped-in index.
	require.NoError(t, m.IndexTool(&config.ToolMetadata{Name: "new_tool", ServerName: "fs", Description: "brand new"}))
	count, err = m.GetDocumentCount()
	require.NoError(t, err)
	assert.Equal(t, uint64(4), count)
	assert.NoDirExists(t, filepath.Join(dataDir, "index.bleve"+rebuildDirSuffix))
}

// Rebuild pages through the source index; coverage must not be bounded by a
// single search page.
func TestRebuildIndex_PaginatesPastOnePage(t *testing.T) {
	bi, err := newBleveIndexAt(filepath.Join(t.TempDir(), "index.bleve"), zap.NewNop())
	require.NoError(t, err)
	defer bi.Close()
	bi.searchPageSize = 2
	require.NoError(t, bi.BatchIndex(migrationFixtureTools()))

	require.NoError(t, bi.RebuildIndex())

	count, err := bi.GetDocumentCount()
	require.NoError(t, err)
	assert.Equal(t, uint64(3), count)
}

// TestCreateBleveIndex_DynamicMappingDisabled locks in the defense-in-depth
// half of the fix: a freshly created index must not fall back to bleve's
// dynamic-field defaults for an unmapped field, so a future ToolDocument field
// added without a mapping is simply not indexed instead of silently leaking
// into `_all`.
func TestCreateBleveIndex_DynamicMappingDisabled(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "index.bleve")

	idx, err := createBleveIndex(indexPath)
	require.NoError(t, err)
	defer idx.Close()

	require.NoError(t, idx.Index("doc1", map[string]interface{}{
		"tool_name":     "delete_everything",
		"totally_novel": "should not become searchable via _all",
	}))

	assert.Zero(t, fieldlessMatchTotal(t, idx, "should not become searchable via _all"),
		"an unmapped field must not be dynamically indexed once Dynamic=false")
}

// Every field ToolDocument writes must be explicitly mapped: with
// Dynamic=false an unmapped field is silently neither indexed nor stored.
func TestCurrentIndexMapping_CoversEveryToolDocumentField(t *testing.T) {
	im := currentIndexMapping()
	for _, f := range []string{"tool_name", "full_tool_name", "server_name", "description", "params_json",
		"output_schema_json", "hash", "tags", "searchable_text", "annotations_json"} {
		assert.NotEmpty(t, im.FieldMappingForPath(f).Type, "field %q has no explicit mapping", f)
	}
}

// A failure after the old index is closed (swap or reopen) must never leave
// the BleveIndex without an index: every later call, including Close, would
// nil-deref. It falls back to an empty current index, which the discovery path
// re-populates, and still reports the error.
func TestRebuildIndex_SwapFailureLeavesUsableEmptyIndex(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "index.bleve")
	bi, err := newBleveIndexAt(indexPath, zap.NewNop())
	require.NoError(t, err)
	require.NoError(t, bi.BatchIndex(migrationFixtureTools()))

	orig := swapIndexDirFn
	swapIndexDirFn = func(src, dst string) error {
		// Fail midway: the old metadata is already gone.
		_ = os.Remove(filepath.Join(dst, indexMetaFile))
		return os.ErrPermission
	}
	t.Cleanup(func() { swapIndexDirFn = orig })

	require.Error(t, bi.RebuildIndex())
	require.NotNil(t, bi.index)
	assertCurrentMapping(t, bi.index)
	count, err := bi.GetDocumentCount()
	require.NoError(t, err)
	assert.Zero(t, count)
	require.NoError(t, bi.IndexTool(migrationFixtureTools()[0]))
	assert.NoDirExists(t, indexPath+rebuildDirSuffix)
	require.NoError(t, bi.Close())
}

func skipIfPermissionsUnenforced(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions are not enforced")
	}
}

// Ported from PR 1378 review round 8, finding 1: a migration that fails
// before the old index is closed (here: the rebuild directory cannot be
// created) must keep serving the stale-mapping index, not fail startup.
func TestNewBleveIndexAt_MigrationFailureKeepsStaleIndexServing(t *testing.T) {
	skipIfPermissionsUnenforced(t)
	parent := t.TempDir()
	indexPath := filepath.Join(parent, "index.bleve")
	writeLegacyIndex(t, indexPath, migrationFixtureTools())

	require.NoError(t, os.Chmod(parent, 0o555))
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	core, logs := observer.New(zap.WarnLevel)
	bi, err := newBleveIndexAt(indexPath, zap.New(core))
	require.NoError(t, err, "a failed migration must not fail index startup")
	defer bi.Close()

	assert.Equal(t, 1, logs.FilterMessageSnippet("Bleve index migration failed").Len(),
		"the swallowed failure must be logged")
	count, err := bi.GetDocumentCount()
	require.NoError(t, err)
	assert.Equal(t, uint64(3), count, "the stale index keeps serving every document")
	results, err := bi.SearchTools("repository", 10)
	require.NoError(t, err)
	assert.NotEmpty(t, results)
	reason, err := indexStaleReason(bi.index)
	require.NoError(t, err)
	assert.NotEmpty(t, reason, "the stale index is returned as-is and is migrated on a later open")
}

// Ported from PR 1378 review round 9, finding 1: a file inside the old index's
// store that cannot be deleted (write bit stripped from store/) must not strand
// the swap. Old entries are renamed aside, which needs write access to the
// index directory only.
func TestNewBleveIndexAt_MigrationSurvivesUnremovableStore(t *testing.T) {
	skipIfPermissionsUnenforced(t)
	indexPath := filepath.Join(t.TempDir(), "index.bleve")
	writeLegacyIndex(t, indexPath, migrationFixtureTools())

	storeDir := filepath.Join(indexPath, "store")
	require.DirExists(t, storeDir, "test assumption: scorch lays out indexPath/store")
	require.NoError(t, os.Chmod(storeDir, 0o555))
	t.Cleanup(func() {
		_ = os.Chmod(storeDir, 0o755)
		chmodRetiredStores(indexPath)
	})

	bi, err := newBleveIndexAt(indexPath, zap.NewNop())
	require.NoError(t, err)
	assertCurrentMapping(t, bi.index)
	count, err := bi.GetDocumentCount()
	require.NoError(t, err)
	assert.Equal(t, uint64(3), count)
	require.NoError(t, bi.Close())

	// The undeletable retired copy must not break the next open either.
	bi, err = newBleveIndexAt(indexPath, zap.NewNop())
	require.NoError(t, err)
	defer bi.Close()
	assertCurrentMapping(t, bi.index)
}

// The interrupted-swap recovery must also route around an undeletable store:
// otherwise a directory without index_meta.json and with an unremovable store
// fails every startup.
func TestNewBleveIndexAt_InterruptedSwapRecoverySurvivesUnremovableStore(t *testing.T) {
	skipIfPermissionsUnenforced(t)
	indexPath := filepath.Join(t.TempDir(), "index.bleve")
	bi, err := newBleveIndexAt(indexPath, zap.NewNop())
	require.NoError(t, err)
	require.NoError(t, bi.Close())
	require.NoError(t, os.Remove(filepath.Join(indexPath, indexMetaFile)))

	storeDir := filepath.Join(indexPath, "store")
	require.NoError(t, os.Chmod(storeDir, 0o555))
	t.Cleanup(func() {
		_ = os.Chmod(storeDir, 0o755)
		chmodRetiredStores(indexPath)
	})

	bi, err = newBleveIndexAt(indexPath, zap.NewNop())
	require.NoError(t, err)
	defer bi.Close()
	assertCurrentMapping(t, bi.index)
}

// chmodRetiredStores restores write access to stores renamed aside by
// retireIndexEntries so t.TempDir's cleanup can delete them.
func chmodRetiredStores(indexPath string) {
	stores, _ := filepath.Glob(filepath.Join(indexPath, retiredEntryPrefix+"*store"))
	for _, d := range stores {
		_ = os.Chmod(d, 0o755)
	}
}
