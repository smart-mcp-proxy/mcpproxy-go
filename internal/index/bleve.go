package index

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/keyword"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/standard"
	"github.com/blevesearch/bleve/v2/mapping"
	bquery "github.com/blevesearch/bleve/v2/search/query"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// defaultSearchPageSize is the page size used when paginating full-coverage
// scans (GetToolsByServer, DeleteAll). It is a generous upper bound on the
// number of docs in a single page; pagination loops over as many pages as
// needed, so total coverage is never bounded by this value (MCP-3319).
const defaultSearchPageSize = 10000

const (
	// maxUnderscoreSearchSegments bounds the extra wildcard clauses generated for
	// identifier-style queries. Typical MCP tool names stay well below this cap.
	maxUnderscoreSearchSegments = 16
	underscoreSegmentBoost      = 5.0
)

// BleveIndex wraps Bleve index operations
type BleveIndex struct {
	index  bleve.Index
	path   string // on-disk index directory, for RebuildIndex
	logger *zap.Logger
	// searchPageSize bounds a single search page during paginated full scans.
	// Defaults to defaultSearchPageSize; overridable in tests.
	searchPageSize int
}

// ToolDocument represents a tool document in the index
type ToolDocument struct {
	ToolName         string `json:"tool_name"`      // Just the tool name (without server prefix)
	FullToolName     string `json:"full_tool_name"` // Complete server:tool format
	ServerName       string `json:"server_name"`
	Description      string `json:"description"`
	ParamsJSON       string `json:"params_json"`
	OutputSchemaJSON string `json:"output_schema_json,omitempty"`
	Hash             string `json:"hash"`
	Tags             string `json:"tags"`
	SearchableText   string `json:"searchable_text"` // Combined searchable content
	// AnnotationsJSON is the tool's MCP behavior-hint annotations
	// (config.ToolAnnotations), marshaled once at index time. Stored but not
	// indexed for search, same as Hash — it exists so a search hit's tier
	// (Spec 109 FR-028) reflects the tool's real annotations instead of
	// always reading TierUnannotated (review round 1: GET /index/search never
	// stored annotations at all, so a search hit's tier could not agree with
	// the same tool's tier on GET /servers/{id}/tools).
	AnnotationsJSON string `json:"annotations_json,omitempty"`
}

// NewBleveIndex creates (or opens) the shared default Bleve index at
// <dataDir>/index.bleve.
func NewBleveIndex(dataDir string, logger *zap.Logger) (*BleveIndex, error) {
	return newBleveIndexAt(filepath.Join(dataDir, "index.bleve"), logger)
}

// newBleveIndexAt opens an existing Bleve index at indexPath, or creates one if
// it does not yet exist. The parent directory is created as needed so callers
// may nest a per-profile index under the shared index dir
// (<dataDir>/index.bleve/profiles/<slug>/, see Manager) without pre-creating
// it. Nothing else may be nested there: migrating or recovering the shared
// index removes every entry of its directory except profilesDirName.
//
// An existing index whose persisted mapping or schema version is not the
// current one (see indexStaleReason) is migrated before it is returned, so
// every caller sees the same field-mapping behavior regardless of which
// mcpproxy version first created the index.
func newBleveIndexAt(indexPath string, logger *zap.Logger) (*BleveIndex, error) {
	// Rebuild and retired directories only survive an interrupted or
	// partially cleaned-up migration; the live index at indexPath is
	// authoritative either way.
	removeRebuildLeftovers(indexPath, logger)

	// Try to open existing index
	index, err := bleve.Open(indexPath)
	if err != nil {
		// If index doesn't exist, create a new one
		if mkErr := os.MkdirAll(filepath.Dir(indexPath), 0o755); mkErr != nil {
			return nil, fmt.Errorf("failed to create index parent dir: %w", mkErr)
		}
		if errors.Is(err, bleve.ErrorIndexMetaMissing) {
			// The directory exists but holds no openable index: a migration
			// swap was interrupted after the old index_meta.json was removed
			// (swapIndexDir removes it first and restores it last). Clear the
			// leftovers so bleve.New can create the store again. A live index
			// always has index_meta.json, so this never touches one.
			logger.Warn("Bleve index directory has no index metadata; recreating it empty",
				zap.String("path", indexPath))
			if clearErr := retireIndexEntries(indexPath); clearErr != nil {
				return nil, clearErr
			}
		}
		logger.Info("Creating new Bleve index", zap.String("path", indexPath))
		index, err = createBleveIndex(indexPath)
		if err != nil {
			return nil, fmt.Errorf("failed to create Bleve index: %w", err)
		}
	} else {
		logger.Info("Opened existing Bleve index", zap.String("path", indexPath))
	}

	b := &BleveIndex{
		index:          index,
		path:           indexPath,
		logger:         logger,
		searchPageSize: defaultSearchPageSize,
	}

	reason, err := indexStaleReason(index)
	if err != nil {
		_ = index.Close()
		return nil, err
	}
	if reason != "" {
		logger.Warn("Bleve index mapping is stale; migrating to the current mapping",
			zap.String("path", indexPath), zap.String("reason", reason))
		if err := b.RebuildIndex(); err != nil {
			if b.index == nil {
				return nil, fmt.Errorf("failed to migrate Bleve index at %s: %w", indexPath, err)
			}
			// The index is a derived search cache: serving the stale-mapping
			// index (failure before the swap) or an empty one the discovery
			// path re-populates (failure after it) beats failing startup.
			// The next open retries the migration.
			logger.Error("Bleve index migration failed; continuing with the current index",
				zap.String("path", indexPath), zap.Error(err))
		}
	}

	return b, nil
}

// indexSchemaVersion versions how ToolDocument is DERIVED from tool metadata
// (e.g. what goes into searchable_text), which the persisted mapping cannot
// reveal. Bump it when toolDocument or documentFromStoredFields changes in a
// way that alters an indexed field; every index is then rebuilt once on open.
// Mapping changes need no bump: they are detected by comparing the persisted
// mapping itself (indexStaleReason).
//
// Version history:
//
//	(absent) indexes created before versioning existed (dynamic mapping on;
//	         annotations_json and output_schema_json unmapped and full-text
//	         indexed into _all)
//	"2"      explicit mapping for every field, Dynamic=false
const indexSchemaVersion = "2"

// indexSchemaVersionKey stores indexSchemaVersion in the index's internal
// key-value space, next to bleve's own persisted mapping.
var indexSchemaVersionKey = []byte("mcpproxy_index_schema_version")

// bleveMappingInternalKey is where bleve.New persists the index mapping
// (bleve/v2/util.MappingInternalKey); bleve.Open loads the mapping from it.
var bleveMappingInternalKey = []byte("_mapping")

// rebuildDirSuffix names the sibling directory a rebuild populates before it
// is swapped into place.
const rebuildDirSuffix = ".rebuild"

// retiredEntryPrefix prefixes the names a replaced index's entries are renamed
// to, inside the index directory, before they are deleted. Bleve only reads
// index_meta.json and store/, so a retired entry that could not be deleted is
// inert; the dot prefix also keeps it out of ExistingProfileDirs.
const retiredEntryPrefix = ".retired-"

// removeRebuildLeftovers best-effort removes the rebuild directory and retired
// entries a previous migration left behind.
func removeRebuildLeftovers(indexPath string, logger *zap.Logger) {
	leftovers, _ := filepath.Glob(filepath.Join(indexPath, retiredEntryPrefix+"*"))
	leftovers = append(leftovers, indexPath+rebuildDirSuffix)
	for _, p := range leftovers {
		if err := os.RemoveAll(p); err != nil {
			logger.Warn("Failed to remove leftover Bleve rebuild directory",
				zap.String("path", p), zap.Error(err))
		}
	}
}

// indexStaleReason reports why idx must be rebuilt before use, or "" when it
// already has the current mapping and schema version.
//
// bleve.Open never re-applies createBleveIndex's mapping to an index that
// already exists on disk: the mapping is persisted at creation time and
// bleve v2 has no way to change it afterwards. An index created before a field
// mapping existed therefore keeps the old behavior forever — for indexes
// created before annotations_json/output_schema_json were mapped explicitly,
// bleve's dynamic defaults full-text index both into the `_all` field that
// SearchTools' field-less MatchQuery searches, so identical corpora ranked
// differently depending only on when the index was created. Comparing the
// persisted mapping byte-for-byte with the current one catches that and every
// future mapping change without anyone having to remember a version bump.
func indexStaleReason(idx bleve.Index) (string, error) {
	want, err := currentMappingJSON()
	if err != nil {
		return "", err
	}
	got, err := idx.GetInternal(bleveMappingInternalKey)
	if err != nil {
		return "", fmt.Errorf("failed to read persisted Bleve index mapping: %w", err)
	}
	if !bytes.Equal(got, want) {
		return "persisted field mapping differs from the current mapping", nil
	}
	ver, err := idx.GetInternal(indexSchemaVersionKey)
	if err != nil {
		return "", fmt.Errorf("failed to read Bleve index schema version: %w", err)
	}
	if string(ver) != indexSchemaVersion {
		return fmt.Sprintf("schema version %q, want %q", ver, indexSchemaVersion), nil
	}
	return "", nil
}

// currentMappingJSON is the mapping createBleveIndex persists, serialized the
// way bleve.New serializes it (encoding/json; map keys sorted, so stable).
func currentMappingJSON() ([]byte, error) {
	b, err := json.Marshal(currentIndexMapping())
	if err != nil {
		return nil, fmt.Errorf("failed to serialize Bleve index mapping: %w", err)
	}
	return b, nil
}

// RebuildIndex re-creates the index with the current mapping and re-indexes
// every document it holds.
//
// The source is the index's own stored fields, not BBolt storage: every
// ToolDocument field except searchable_text is stored (and searchable_text is
// derived from stored fields), whereas storage keeps no annotations or index
// hash. Copying the stored fields also keeps full_tool_name byte-for-byte —
// round-tripping through config.ToolMetadata would rewrite it to the
// canonical id and move scores (see toolDocument).
//
// The new index is built completely in a sibling directory and only then
// swapped in (swapIndexDir), so a failure before the swap leaves the current
// index untouched and open. The caller must hold exclusive access
// (Manager.RebuildIndex takes its write lock).
func (b *BleveIndex) RebuildIndex() error {
	docs, err := b.readAllStoredDocuments()
	if err != nil {
		return err
	}
	b.logger.Info("Rebuilding Bleve index", zap.String("path", b.path), zap.Int("documents", len(docs)))

	tmpPath := b.path + rebuildDirSuffix
	if err := os.RemoveAll(tmpPath); err != nil {
		return fmt.Errorf("failed to clear rebuild directory: %w", err)
	}
	if err := b.populateIndexAt(tmpPath, docs); err != nil {
		_ = os.RemoveAll(tmpPath)
		return err
	}

	// Point of no return: the old index is closed and replaced.
	if err := b.index.Close(); err != nil {
		_ = os.RemoveAll(tmpPath)
		return fmt.Errorf("failed to close Bleve index for rebuild: %w", err)
	}
	idx, err := b.swapInRebuilt(tmpPath)
	if err != nil {
		return b.recoverEmpty(tmpPath, err)
	}
	b.index = idx
	if err := os.RemoveAll(tmpPath); err != nil {
		// The swap is complete; the next open removes the leftover.
		b.logger.Warn("Failed to remove Bleve rebuild directory",
			zap.String("path", tmpPath), zap.Error(err))
	}

	b.logger.Info("Migrated Bleve index to the current mapping",
		zap.String("path", b.path), zap.Int("documents", len(docs)),
		zap.String("schema_version", indexSchemaVersion))
	return nil
}

// swapIndexDirFn is swapIndexDir, replaceable in tests to simulate a failed swap.
var swapIndexDirFn = swapIndexDir

func (b *BleveIndex) swapInRebuilt(tmpPath string) (bleve.Index, error) {
	if err := swapIndexDirFn(tmpPath, b.path); err != nil {
		return nil, err
	}
	idx, err := bleve.Open(b.path)
	if err != nil {
		return nil, fmt.Errorf("failed to open rebuilt Bleve index: %w", err)
	}
	return idx, nil
}

// recoverEmpty handles a failure after the old index was closed: the on-disk
// index may be half-swapped, and b.index must never be left closed or nil (every
// later call, Close included, would fail or panic). It recreates an empty
// current-mapping index in place, which the discovery path re-populates as
// servers reconnect, and returns cause either way.
func (b *BleveIndex) recoverEmpty(tmpPath string, cause error) error {
	b.index = nil
	_ = os.RemoveAll(tmpPath)
	b.logger.Error("Bleve index rebuild failed after the old index was closed; recreating it empty",
		zap.String("path", b.path), zap.Error(cause))
	if err := retireIndexEntries(b.path); err != nil {
		return fmt.Errorf("%w (recovery failed: %w)", cause, err)
	}
	idx, err := createBleveIndex(b.path)
	if err != nil {
		return fmt.Errorf("%w (recovery failed: %w)", cause, err)
	}
	b.index = idx
	return cause
}

// storedDocument is one document read back for a rebuild.
type storedDocument struct {
	id  string
	doc *ToolDocument
}

// readAllStoredDocuments pages through every document with all stored fields.
// Sorting by _id keeps From-based pagination stable.
func (b *BleveIndex) readAllStoredDocuments() ([]storedDocument, error) {
	var docs []storedDocument
	for from := 0; ; from += b.searchPageSize {
		req := bleve.NewSearchRequestOptions(bleve.NewMatchAllQuery(), b.searchPageSize, from, false)
		req.Fields = []string{"*"}
		req.SortBy([]string{"_id"})
		res, err := b.index.Search(req)
		if err != nil {
			return nil, fmt.Errorf("failed to read Bleve index documents for rebuild: %w", err)
		}
		for _, hit := range res.Hits {
			docs = append(docs, storedDocument{id: hit.ID, doc: documentFromStoredFields(hit.Fields)})
		}
		if len(res.Hits) < b.searchPageSize {
			return docs, nil
		}
	}
}

// documentFromStoredFields rebuilds a ToolDocument from its stored fields.
// searchable_text is not stored; it is re-derived exactly as toolDocument
// derives it, from the stored tool_name and full_tool_name.
func documentFromStoredFields(fields map[string]interface{}) *ToolDocument {
	doc := &ToolDocument{
		ToolName:         getStringField(fields, "tool_name"),
		FullToolName:     getStringField(fields, "full_tool_name"),
		ServerName:       getStringField(fields, "server_name"),
		Description:      getStringField(fields, "description"),
		ParamsJSON:       getStringField(fields, "params_json"),
		OutputSchemaJSON: getStringField(fields, "output_schema_json"),
		Hash:             getStringField(fields, "hash"),
		Tags:             getStringField(fields, "tags"),
		AnnotationsJSON:  getStringField(fields, "annotations_json"),
	}
	doc.SearchableText = searchableText(doc.ToolName, doc.FullToolName, doc.Description, doc.ParamsJSON)
	return doc
}

// populateIndexAt creates a current-mapping index at path holding docs, and
// stamps the schema version only after every document is written, so an
// interrupted rebuild never looks complete.
func (b *BleveIndex) populateIndexAt(path string, docs []storedDocument) error {
	idx, err := bleve.New(path, currentIndexMapping())
	if err != nil {
		return fmt.Errorf("failed to create rebuild index: %w", err)
	}
	for start := 0; start < len(docs); start += b.searchPageSize {
		end := min(start+b.searchPageSize, len(docs))
		batch := idx.NewBatch()
		for _, d := range docs[start:end] {
			if err := batch.Index(d.id, d.doc); err != nil {
				_ = idx.Close()
				return fmt.Errorf("failed to re-index document %q: %w", d.id, err)
			}
		}
		if err := idx.Batch(batch); err != nil {
			_ = idx.Close()
			return fmt.Errorf("failed to write rebuild batch: %w", err)
		}
	}
	if err := idx.SetInternal(indexSchemaVersionKey, []byte(indexSchemaVersion)); err != nil {
		_ = idx.Close()
		return fmt.Errorf("failed to stamp rebuild index schema version: %w", err)
	}
	if err := idx.Close(); err != nil {
		return fmt.Errorf("failed to close rebuild index: %w", err)
	}
	return nil
}

// indexMetaFile is the file bleve.Open reads first; without it a directory is
// not an openable index.
const indexMetaFile = "index_meta.json"

// swapIndexDir replaces the closed index at dst with the complete index at src.
// retireIndexEntries takes index_meta.json out first and it is moved in last,
// so a crash at any point leaves dst either the old index, or no openable
// index (which newBleveIndexAt recreates empty) — never a mix of old metadata
// and new store. The discovery path re-populates an empty index as servers
// connect.
func swapIndexDir(src, dst string) error {
	if err := retireIndexEntries(dst); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("failed to read rebuild directory: %w", err)
	}
	for _, e := range entries {
		if e.Name() == indexMetaFile {
			continue
		}
		if err := os.Rename(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return fmt.Errorf("failed to move rebuilt index entry %q: %w", e.Name(), err)
		}
	}
	if err := os.Rename(filepath.Join(src, indexMetaFile), filepath.Join(dst, indexMetaFile)); err != nil {
		return fmt.Errorf("failed to move rebuilt index metadata: %w", err)
	}
	return nil
}

// retireIndexEntries empties an index directory of bleve's own entries by
// renaming each to a retiredEntryPrefix name in the same directory,
// index_meta.json first, and then best-effort deleting them. A same-directory
// rename needs write access to dir only (moving a directory to another parent
// would also need write access to it, to update its ".." entry), so a file
// inside store/ that cannot be deleted (a permission-impaired or locked
// leftover) can neither fail this nor leave a half-deleted index behind with
// its metadata intact; whatever the delete leaves is retried on the next open
// (removeRebuildLeftovers).
//
// The shared index directory also holds the per-profile indexes under
// profilesDirName (see Manager), which are separate indexes and are kept.
func retireIndexEntries(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read index directory: %w", err)
	}
	prefix := fmt.Sprintf("%s%d-", retiredEntryPrefix, time.Now().UnixNano())
	var retired []string
	move := func(name string) error {
		to := filepath.Join(dir, prefix+name)
		if err := os.Rename(filepath.Join(dir, name), to); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("failed to move aside index entry %q: %w", name, err)
		}
		retired = append(retired, to)
		return nil
	}
	if err := move(indexMetaFile); err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if name == profilesDirName || name == indexMetaFile || strings.HasPrefix(name, retiredEntryPrefix) {
			continue
		}
		if err := move(name); err != nil {
			return err
		}
	}
	for _, p := range retired {
		_ = os.RemoveAll(p)
	}
	return nil
}

// createBleveIndex creates a new, empty Bleve index with the current mapping
// and schema version.
func createBleveIndex(indexPath string) (bleve.Index, error) {
	idx, err := bleve.New(indexPath, currentIndexMapping())
	if err != nil {
		return nil, err
	}
	if err := idx.SetInternal(indexSchemaVersionKey, []byte(indexSchemaVersion)); err != nil {
		_ = idx.Close()
		return nil, fmt.Errorf("failed to stamp index schema version: %w", err)
	}
	return idx, nil
}

// currentIndexMapping is the field mapping every index is created or migrated
// to. Any change here is detected on the next open of an existing index and
// migrates it (indexStaleReason), so there is no version to bump.
func currentIndexMapping() *mapping.IndexMappingImpl {
	indexMapping := bleve.NewIndexMapping()

	// Create document mapping for tools
	toolMapping := bleve.NewDocumentMapping()
	// Disable dynamic field mapping: every field this index ever writes is
	// declared explicitly below. Without this, adding a new ToolDocument
	// field in the future (as annotations_json itself was added, review
	// round 6 finding 1) would silently fall back to bleve's dynamic-field
	// defaults — full-text-indexed and included in `_all` — on any FRESH
	// index too, not just ones migrating forward. Existing indexes created
	// before this line are migrated on open (indexStaleReason).
	toolMapping.Dynamic = false

	// Tool name field (both keyword and standard analyzers for different search types)
	toolNameFieldKeyword := bleve.NewTextFieldMapping()
	toolNameFieldKeyword.Analyzer = keyword.Name
	toolNameFieldKeyword.Store = true
	toolNameFieldKeyword.Index = true
	toolMapping.AddFieldMappingsAt("tool_name", toolNameFieldKeyword)

	// Full tool name field (keyword analyzer for exact matches)
	fullToolNameField := bleve.NewTextFieldMapping()
	fullToolNameField.Analyzer = keyword.Name
	fullToolNameField.Store = true
	fullToolNameField.Index = true
	toolMapping.AddFieldMappingsAt("full_tool_name", fullToolNameField)

	// Server name field (keyword analyzer)
	serverNameField := bleve.NewTextFieldMapping()
	serverNameField.Analyzer = keyword.Name
	serverNameField.Store = true
	serverNameField.Index = true
	toolMapping.AddFieldMappingsAt("server_name", serverNameField)

	// Description field (standard analyzer for full-text search)
	descriptionField := bleve.NewTextFieldMapping()
	descriptionField.Analyzer = standard.Name
	descriptionField.Store = true
	descriptionField.Index = true
	toolMapping.AddFieldMappingsAt("description", descriptionField)

	// Parameters JSON field (standard analyzer)
	paramsField := bleve.NewTextFieldMapping()
	paramsField.Analyzer = standard.Name
	paramsField.Store = true
	paramsField.Index = true
	toolMapping.AddFieldMappingsAt("params_json", paramsField)

	// Output schema JSON field: stored only, never searched (it is JSON, not
	// prose) — same shape as hash and annotations_json below. Previously had
	// no explicit mapping at all and relied entirely on bleve's dynamic-field
	// defaults to be stored for retrieval, which also meant it was
	// full-text-indexed and folded into `_all` on every index, the same class
	// of bug fixed for annotations_json by this mapping (review round 6,
	// finding 1). Made explicit here, and required now that toolMapping.Dynamic
	// is false above — Dynamic=false without this would stop
	// GetToolsByServer/SearchTools from ever retrieving output_schema_json
	// again, since Store also came from the dynamic fallback.
	outputSchemaField := bleve.NewTextFieldMapping()
	outputSchemaField.Analyzer = keyword.Name
	outputSchemaField.Store = true
	outputSchemaField.Index = false
	toolMapping.AddFieldMappingsAt("output_schema_json", outputSchemaField)

	// Hash field (keyword analyzer)
	hashField := bleve.NewTextFieldMapping()
	hashField.Analyzer = keyword.Name
	hashField.Store = true
	hashField.Index = false // Don't index hash for search
	toolMapping.AddFieldMappingsAt("hash", hashField)

	// Annotations field: stored only, never searched (it is JSON, not prose).
	annotationsField := bleve.NewTextFieldMapping()
	annotationsField.Analyzer = keyword.Name
	annotationsField.Store = true
	annotationsField.Index = false
	toolMapping.AddFieldMappingsAt("annotations_json", annotationsField)

	// Tags field (standard analyzer)
	tagsField := bleve.NewTextFieldMapping()
	tagsField.Analyzer = standard.Name
	tagsField.Store = true
	tagsField.Index = true
	toolMapping.AddFieldMappingsAt("tags", tagsField)

	// Searchable text field (standard analyzer) - combines all searchable content
	searchableTextField := bleve.NewTextFieldMapping()
	searchableTextField.Analyzer = standard.Name
	searchableTextField.Store = false // Don't store, just index for search
	searchableTextField.Index = true
	toolMapping.AddFieldMappingsAt("searchable_text", searchableTextField)

	// Add document mapping to index
	indexMapping.AddDocumentMapping("tool", toolMapping)
	indexMapping.DefaultMapping = toolMapping

	return indexMapping
}

// Close closes the index
func (b *BleveIndex) Close() error {
	if b.index == nil {
		return nil // only after a rebuild whose recovery also failed
	}
	return b.index.Close()
}

// toolDocument projects tool metadata onto the stored document and its docID.
//
// Identity (Spec 105 FR-009): the docID is "<server>:<raw name>" where the raw
// name is the exact upstream-reported name (config.RawToolName), so "erase"
// and "ns:erase" on one server are two documents. The previous derivation
// split ToolMetadata.Name at its FIRST colon, which read the namespace prefix
// of a raw "ns:erase" as if it were a server prefix and collapsed both tools
// onto "<server>:erase" (last writer wins). A document written under the old
// derivation is not migrated by hand: the runtime's differential index update
// (applyDifferentialToolUpdate) keys both sides by raw name, so the first
// discovery after upgrade re-hashes the collapsed document under its docID
// and adds the namespaced sibling under its own exact id.
//
// The STORED fields are byte-for-byte what they were before FR-009 for every
// metadata shape that does not carry a colon in its raw name: tool_name is the
// raw name and full_tool_name is ToolMetadata.Name verbatim (the raw name for
// discovery-shaped metadata, the canonical id for index-read or fixture
// metadata). Both are SCORED fields — the exact-match TermQuery on
// full_tool_name (boost 4.0) and the field-less _all MatchQuery in SearchTools
// — so storing the canonical id there instead moved every administrator
// retrieve_tools score for production-shaped documents (SC-005). The tool's
// identity is therefore never derived from full_tool_name on read-back;
// readToolMetadata takes it from the docID.
func toolDocument(toolMeta *config.ToolMetadata) (string, *ToolDocument) {
	toolName := config.RawToolName(toolMeta)
	docID := toolDocID(toolMeta.ServerName, toolName)

	var annotationsJSON string
	if toolMeta.Annotations != nil {
		if b, err := json.Marshal(toolMeta.Annotations); err == nil {
			annotationsJSON = string(b)
		}
		// A marshal error here is unreachable for config.ToolAnnotations (plain
		// strings/bools/pointers, no cyclic or unsupported types) — silently
		// falling back to "no annotations stored" rather than failing the whole
		// index write matches how the rest of this function tolerates partial
		// metadata (e.g. an empty OutputSchemaJSON).
	}

	doc := &ToolDocument{
		ToolName:         toolName,
		FullToolName:     toolMeta.Name,
		ServerName:       toolMeta.ServerName,
		Description:      toolMeta.Description,
		ParamsJSON:       toolMeta.ParamsJSON,
		OutputSchemaJSON: toolMeta.OutputSchemaJSON,
		Hash:             toolMeta.Hash,
		Tags:             "", // Can be extended later
		SearchableText:   searchableText(toolName, toolMeta.Name, toolMeta.Description, toolMeta.ParamsJSON),
		AnnotationsJSON:  annotationsJSON,
	}

	return docID, doc
}

// searchableText is the combined full-text field. It is the one derived field
// that is not stored, so documentFromStoredFields re-derives it through this
// same function during a rebuild; changing it requires an indexSchemaVersion
// bump.
func searchableText(toolName, fullToolName, description, paramsJSON string) string {
	return fmt.Sprintf("%s %s %s %s", toolName, fullToolName, description, paramsJSON)
}

// toolDocID is the single place the "<server>:<raw name>" docID is spelled, so
// IndexTool, BatchIndex and DeleteTool can never disagree on a tool's identity.
func toolDocID(serverName, rawName string) string {
	return config.CanonicalToolName(serverName, rawName)
}

// readToolMetadata rebuilds tool metadata from a stored hit. Identity comes
// from the docID alone (Spec 105 FR-009): the docID is "<server>:<raw name>"
// for documents written by toolDocument, so the canonical Name (#871) IS the
// docID and RawName is the docID with exactly this server's own prefix
// trimmed once — never re-derived from the stored tool_name or
// full_tool_name fields. Those are search fields, not identity: for a
// document written before FR-009 tool_name holds the collapsed suffix
// ("erase" for a raw "ns:erase") and full_tool_name the raw name, and a raw
// name that begins with the server's own prefix ("a:erase" on server "a",
// docID "a:a:erase") would be mistaken by the CanonicalToolName guard for an
// already-canonical "a:erase".
func readToolMetadata(docID string, fields map[string]interface{}) *config.ToolMetadata {
	serverName := getStringField(fields, "server_name")
	canonical := docID
	if canonical == "" {
		// Defensive only: bleve always reports hit.ID. Fall back to the stored
		// name so a malformed hit still renders rather than vanishing.
		canonical = CanonicalToolName(serverName, getStringField(fields, "full_tool_name"))
	}
	var annotations *config.ToolAnnotations
	if raw := getStringField(fields, "annotations_json"); raw != "" {
		var parsed config.ToolAnnotations
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
			annotations = &parsed
		}
		// A malformed stored value (should not happen; toolDocument only ever
		// writes what json.Marshal produced) falls back to nil — TierUnannotated
		// — rather than surfacing a decode error through every search result.
	}

	return &config.ToolMetadata{
		Name:             canonical,
		RawName:          strings.TrimPrefix(canonical, serverName+":"),
		ServerName:       serverName,
		Description:      getStringField(fields, "description"),
		ParamsJSON:       getStringField(fields, "params_json"),
		OutputSchemaJSON: getStringField(fields, "output_schema_json"),
		Hash:             getStringField(fields, "hash"),
		Annotations:      annotations,
	}
}

// IndexTool indexes a tool document
func (b *BleveIndex) IndexTool(toolMeta *config.ToolMetadata) error {
	docID, doc := toolDocument(toolMeta)

	b.logger.Debug("Indexing tool", zap.String("doc_id", docID), zap.String("tool_name", doc.ToolName))
	return b.index.Index(docID, doc)
}

// DeleteTool removes a tool from the index, addressed by its exact raw name.
func (b *BleveIndex) DeleteTool(serverName, toolName string) error {
	docID := toolDocID(serverName, toolName)

	b.logger.Debug("Deleting tool from index", zap.String("doc_id", docID))
	return b.index.Delete(docID)
}

// DeleteServerTools removes all tools from a specific server
func (b *BleveIndex) DeleteServerTools(serverName string) error {
	// Search for all tools from this server
	query := bleve.NewTermQuery(serverName)
	query.SetField("server_name")

	searchReq := bleve.NewSearchRequest(query)
	searchReq.Size = 1000 // Assume max 1000 tools per server
	searchReq.Fields = []string{"tool_name", "server_name"}

	searchResult, err := b.index.Search(searchReq)
	if err != nil {
		return fmt.Errorf("failed to search for server tools: %w", err)
	}

	// Delete each tool
	for _, hit := range searchResult.Hits {
		if err := b.index.Delete(hit.ID); err != nil {
			b.logger.Warn("Failed to delete tool", zap.String("tool_id", hit.ID), zap.Error(err))
		}
	}

	b.logger.Info("Deleted tools from server",
		zap.Int("count", len(searchResult.Hits)),
		zap.String("server", serverName))
	return nil
}

// buildToolSearchQuery is the ONE boolean query both SearchTools and
// SearchToolsScoped run (Spec 107 T075a): every clause is a Should, so
// matching is boolean and corpus-independent, and no scoped variant may add
// a Must term on server_name — that would change BM25 scores, and the scoped
// path must return the unfiltered search's scores for the hits it keeps.
func buildToolSearchQuery(queryStr string) *bquery.BooleanQuery {
	// Create a boolean query to combine multiple search strategies
	boolQuery := bleve.NewBooleanQuery()

	// 1. Exact match on tool name (highest priority)
	exactToolNameQuery := bleve.NewTermQuery(queryStr)
	exactToolNameQuery.SetField("tool_name")
	exactToolNameQuery.SetBoost(5.0)
	boolQuery.AddShould(exactToolNameQuery)

	// 2. Exact match on full tool name
	exactFullToolNameQuery := bleve.NewTermQuery(queryStr)
	exactFullToolNameQuery.SetField("full_tool_name")
	exactFullToolNameQuery.SetBoost(4.0)
	boolQuery.AddShould(exactFullToolNameQuery)

	// 3. Prefix match on tool name for partial matches
	prefixToolNameQuery := bleve.NewPrefixQuery(queryStr)
	prefixToolNameQuery.SetField("tool_name")
	prefixToolNameQuery.SetBoost(3.0)
	boolQuery.AddShould(prefixToolNameQuery)

	// 4. Wildcard search for underscore-separated terms
	if strings.Contains(queryStr, "_") {
		wildcardQuery := bleve.NewWildcardQuery("*" + queryStr + "*")
		wildcardQuery.SetField("tool_name")
		wildcardQuery.SetBoost(2.5)
		boolQuery.AddShould(wildcardQuery)
	}

	// 5. Full-text search across all fields
	matchQuery := bleve.NewMatchQuery(queryStr)
	matchQuery.SetBoost(1.0)
	boolQuery.AddShould(matchQuery)

	// 6. Search in combined searchable text
	searchableTextQuery := bleve.NewMatchQuery(queryStr)
	searchableTextQuery.SetField("searchable_text")
	searchableTextQuery.SetBoost(1.5)
	boolQuery.AddShould(searchableTextQuery)

	return boolQuery
}

// newToolSearchRequest builds the ranked-window request SearchTools and
// SearchToolsScoped share: the same fields, highlight and the deterministic
// score-then-id sort, over the window [from, from+size).
func newToolSearchRequest(q bquery.Query, from, size int) *bleve.SearchRequest {
	searchReq := bleve.NewSearchRequest(q)
	searchReq.From = from
	searchReq.Size = size
	searchReq.Fields = []string{"tool_name", "full_tool_name", "server_name", "description", "params_json", "output_schema_json", "hash", "annotations_json"}
	searchReq.Highlight = bleve.NewHighlight()

	// Deterministic tie-break: primary sort by score descending (bleve's
	// default), secondary by document ID (server:tool) ascending. Without the
	// secondary key, equally-scored hits come back in bleve's internal order,
	// which varies between otherwise-identical searches (Spec 085 SC-002:
	// full/compact ranked-ID identity, and the golden byte-identity fixtures).
	// SortBy is applied before truncating to Size, so it also stabilizes which
	// tied hits survive the limit boundary.
	searchReq.SortBy([]string{"-_score", "_id"})
	return searchReq
}

// augmentedToolSearchQuery is buildToolSearchQuery, augmented with the
// underscore-segment enhancement using the identical adaptive rule SearchTools
// has always applied: identifier queries often include only the meaningful
// segments of a longer tool name, so when the plain query's top `probeSize`
// hits contain no canonical exact match, an additional segment-aware clause
// is added to reward boundary matches over substring hits. Extracted so
// SearchToolsScoped (Spec 105 FR-005 G1 review finding) makes the SAME
// decision an unscoped SearchTools(queryStr, probeSize) call would — the
// decision is a function of the query text and the corpus alone, never of
// scope, so a scoped caller's ranking for an underscore-style query (e.g.
// "create_issue") can no longer silently diverge from what an equal-limit
// unscoped call would have used.
func (b *BleveIndex) augmentedToolSearchQuery(queryStr string, probeSize int) (*bquery.BooleanQuery, error) {
	boolQuery := buildToolSearchQuery(queryStr)

	segmentQuery := underscoreSegmentQuery(queryStr)
	if segmentQuery == nil {
		return boolQuery, nil
	}

	probe, err := b.index.Search(newToolSearchRequest(boolQuery, 0, probeSize))
	if err != nil {
		return nil, fmt.Errorf("underscore segment probe failed: %w", err)
	}
	for _, hit := range probe.Hits {
		if fieldsContainExactToolName(hit.Fields, queryStr) {
			return boolQuery, nil // exact match already at the top: no boost needed
		}
	}

	boolQuery.AddShould(segmentQuery)
	return boolQuery, nil
}

// SearchTools searches for tools using multiple query strategies for better results
func (b *BleveIndex) SearchTools(queryStr string, limit int) ([]*config.SearchResult, error) {
	if queryStr == "" {
		return nil, fmt.Errorf("search query cannot be empty")
	}

	boolQuery, err := b.augmentedToolSearchQuery(queryStr, limit)
	if err != nil {
		return nil, err
	}

	b.logger.Debug("Searching tools with enhanced query", zap.String("query", queryStr), zap.Int("limit", limit))

	searchResult, err := b.index.Search(newToolSearchRequest(boolQuery, 0, limit))
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	// Convert results
	var results []*config.SearchResult
	for _, hit := range searchResult.Hits {
		results = append(results, &config.SearchResult{
			Tool:  readToolMetadata(hit.ID, hit.Fields),
			Score: hit.Score,
		})
	}

	b.logger.Debug("Found tools matching query", zap.Int("count", len(results)), zap.String("query", queryStr))
	return results, nil
}

// scopedSearchMinPage is the smallest page SearchToolsScoped walks the ranked
// result with; the page is max(limit, scopedSearchMinPage).
const scopedSearchMinPage = 256

// SearchToolsScoped is SearchTools for a caller who may see only some servers
// (Spec 107 T075a; Spec 105 "Ranking under scope"): the result is the top-
// `limit` of the SAME ranked search, filtered to servers `inScope` admits
// BEFORE the cut, with the unfiltered scores. It runs the identical boolean
// query — including the underscore-segment enhancement SearchTools(queryStr,
// limit) would apply for the same query and limit (augmentedToolSearchQuery;
// Spec 105 FR-005 G1 review finding — a scoped caller's ranking for an
// identifier-style query must not silently miss the boost an equal-limit
// unscoped call would have used), and never a Must term on server_name
// (that would change scores) — and the identical score-then-id sort, and
// pages through the ranked result
// EXHAUSTIVELY with From/Size: each page is filtered through inScope, and
// paging stops only when `limit` in-scope hits have been collected or the
// window has passed searchResult.Total. There is deliberately NO result cap:
// any cap would let a hidden population larger than the cap displace an
// entitled hit, and make membership and `total` differ between a corpus that
// contains the hidden servers and one that does not — the existence oracle
// FR-010 forbids. A caller whose predicate is nil sees nothing (fail closed)
// without a search.
func (b *BleveIndex) SearchToolsScoped(queryStr string, limit int, inScope func(serverName string) bool) ([]*config.SearchResult, error) {
	if queryStr == "" {
		return nil, fmt.Errorf("search query cannot be empty")
	}
	if inScope == nil || limit <= 0 {
		return []*config.SearchResult{}, nil
	}

	q, err := b.augmentedToolSearchQuery(queryStr, limit)
	if err != nil {
		return nil, err
	}
	pageSize := limit
	if pageSize < scopedSearchMinPage {
		pageSize = scopedSearchMinPage
	}

	b.logger.Debug("Searching tools with scoped query", zap.String("query", queryStr), zap.Int("limit", limit))

	results := make([]*config.SearchResult, 0, limit)
	for from := 0; ; from += pageSize {
		searchResult, err := b.index.Search(newToolSearchRequest(q, from, pageSize))
		if err != nil {
			return nil, fmt.Errorf("search failed: %w", err)
		}
		for _, hit := range searchResult.Hits {
			tool := readToolMetadata(hit.ID, hit.Fields)
			if !inScope(tool.ServerName) {
				continue
			}
			results = append(results, &config.SearchResult{Tool: tool, Score: hit.Score})
			if len(results) >= limit {
				return results, nil
			}
		}
		if len(searchResult.Hits) == 0 || uint64(from+pageSize) >= searchResult.Total {
			break
		}
	}

	b.logger.Debug("Found scoped tools matching query", zap.Int("count", len(results)), zap.String("query", queryStr))
	return results, nil
}

func fieldsContainExactToolName(fields map[string]interface{}, queryStr string) bool {
	for _, field := range []string{"tool_name", "full_tool_name"} {
		if value, ok := fields[field].(string); ok && value == queryStr {
			return true
		}
	}
	return false
}

// underscoreSegmentQuery matches each underscore-delimited query segment at a
// complete segment boundary in the keyword-indexed tool_name field. Segment
// order is intentionally irrelevant, but every segment is required.
func underscoreSegmentQuery(queryStr string) bquery.Query {
	segments := strings.Split(queryStr, "_")
	if len(segments) < 2 || len(segments) > maxUnderscoreSearchSegments {
		return nil
	}

	segmentQueries := make([]bquery.Query, 0, len(segments))
	for _, segment := range segments {
		if !isASCIIAlphanumeric(segment) {
			return nil
		}

		exact := bleve.NewTermQuery(segment)
		exact.SetField("tool_name")
		exact.SetBoost(underscoreSegmentBoost)

		prefix := bleve.NewPrefixQuery(segment + "_")
		prefix.SetField("tool_name")
		prefix.SetBoost(underscoreSegmentBoost)

		middle := bleve.NewWildcardQuery("*_" + segment + "_*")
		middle.SetField("tool_name")
		middle.SetBoost(underscoreSegmentBoost)

		suffix := bleve.NewWildcardQuery("*_" + segment)
		suffix.SetField("tool_name")
		suffix.SetBoost(underscoreSegmentBoost)

		segmentQueries = append(segmentQueries, bleve.NewDisjunctionQuery(exact, prefix, middle, suffix))
	}

	return bleve.NewConjunctionQuery(segmentQueries...)
}

func isASCIIAlphanumeric(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// GetDocumentCount returns the number of documents in the index
func (b *BleveIndex) GetDocumentCount() (uint64, error) {
	return b.index.DocCount()
}

// DeleteAll removes every document from the index, leaving an empty index in
// place. Used to rebuild a per-profile index from scratch without recreating
// its on-disk directory.
func (b *BleveIndex) DeleteAll() error {
	query := bleve.NewMatchAllQuery()
	// Delete in pages until the index is empty. Each iteration enumerates a
	// page from offset 0 and deletes exactly those docs, so the next search
	// surfaces the following page — no From offset bookkeeping, and coverage is
	// not bounded by a single search page (MCP-3319).
	for {
		searchReq := bleve.NewSearchRequest(query)
		searchReq.Size = b.searchPageSize
		searchResult, err := b.index.Search(searchReq)
		if err != nil {
			return fmt.Errorf("failed to enumerate documents for delete-all: %w", err)
		}
		if len(searchResult.Hits) == 0 {
			return nil
		}

		batch := b.index.NewBatch()
		for _, hit := range searchResult.Hits {
			batch.Delete(hit.ID)
		}
		if err := b.index.Batch(batch); err != nil {
			return fmt.Errorf("failed to delete documents for delete-all: %w", err)
		}
	}
}

// Batch operations for efficiency

// BatchIndex indexes multiple tools in a single batch
func (b *BleveIndex) BatchIndex(tools []*config.ToolMetadata) error {
	batch := b.index.NewBatch()

	for _, toolMeta := range tools {
		docID, doc := toolDocument(toolMeta)
		_ = batch.Index(docID, doc)
	}

	b.logger.Debug("Batch indexing tools", zap.Int("count", len(tools)))
	return b.index.Batch(batch)
}

// GetToolsByServer retrieves all tools from a specific server
func (b *BleveIndex) GetToolsByServer(serverName string) ([]*config.ToolMetadata, error) {
	// Create a term query for the server name
	query := bleve.NewTermQuery(serverName)
	query.SetField("server_name")

	fields := []string{"tool_name", "full_tool_name", "server_name", "description", "params_json", "output_schema_json", "hash", "annotations_json"}

	b.logger.Debug("Querying tools by server", zap.String("server", serverName))

	// Paginate so a server exposing more than one search page of tools is fully
	// covered — a single capped search would silently drop the overflow
	// (MCP-3319). A short final page (fewer hits than the page size) ends the loop.
	var tools []*config.ToolMetadata
	for from := 0; ; from += b.searchPageSize {
		searchReq := bleve.NewSearchRequest(query)
		searchReq.From = from
		searchReq.Size = b.searchPageSize
		searchReq.Fields = fields

		searchResult, err := b.index.Search(searchReq)
		if err != nil {
			return nil, fmt.Errorf("failed to query tools by server: %w", err)
		}

		for _, hit := range searchResult.Hits {
			tools = append(tools, readToolMetadata(hit.ID, hit.Fields))
		}

		if len(searchResult.Hits) < b.searchPageSize {
			break
		}
	}

	b.logger.Debug("Found tools for server",
		zap.String("server", serverName),
		zap.Int("count", len(tools)))

	return tools, nil
}

// ScopedDocumentCount returns the number of indexed documents belonging to
// servers inScope admits (Spec 105 FR-005 G4): a `server_name` facet term
// count, summed over only the terms inScope admits, so a scoped caller's
// `debug.total_indexed_tools` counts its own authorized population rather
// than the whole index regardless of which physical index (shared or
// per-profile) backs the search that produced the response. A nil inScope
// admits nothing (fail closed, matching SearchToolsScoped).
//
// The facet's term size is the document count itself, never a fixed
// constant: a `server_name` facet returns at most that many distinct terms
// (one document contributes to exactly one term), so this is a PROVEN exact
// upper bound rather than a "should be big enough" guess — a fixed cap (the
// pattern GetAllIndexedServerNames uses) can silently spill excess servers
// into Bleve's "Other" bucket and undercount an authorized population once
// the fleet exceeds it (Spec 105 PR C review finding).
func (b *BleveIndex) ScopedDocumentCount(inScope func(serverName string) bool) (uint64, error) {
	if inScope == nil {
		return 0, nil
	}

	docCount, err := b.index.DocCount()
	if err != nil {
		return 0, fmt.Errorf("failed to read document count for scoped facet sizing: %w", err)
	}
	if docCount == 0 {
		return 0, nil
	}

	query := bleve.NewMatchAllQuery()
	searchReq := bleve.NewSearchRequest(query)
	searchReq.Size = 0 // facet-only, like GetAllIndexedServerNames

	facet := bleve.NewFacetRequest("server_name", int(docCount))
	searchReq.AddFacet("servers", facet)

	searchResult, err := b.index.Search(searchReq)
	if err != nil {
		return 0, fmt.Errorf("failed to query scoped document count: %w", err)
	}

	facetResult, ok := searchResult.Facets["servers"]
	if !ok {
		return 0, nil // no facet result means no documents
	}

	var total uint64
	for _, term := range facetResult.Terms.Terms() {
		if inScope(term.Term) {
			total += uint64(term.Count)
		}
	}
	return total, nil
}

// GetAllIndexedServerNames returns the unique set of server names present in the index.
func (b *BleveIndex) GetAllIndexedServerNames() ([]string, error) {
	// Use a MatchAll query to scan every document, requesting only the server_name field
	query := bleve.NewMatchAllQuery()
	searchReq := bleve.NewSearchRequest(query)
	searchReq.Size = 0 // We only need facets, not results

	// Add a facet on server_name to get unique values
	facet := bleve.NewFacetRequest("server_name", 10000) // generous upper bound
	searchReq.AddFacet("servers", facet)

	searchResult, err := b.index.Search(searchReq)
	if err != nil {
		return nil, fmt.Errorf("failed to query indexed server names: %w", err)
	}

	facetResult, ok := searchResult.Facets["servers"]
	if !ok {
		return nil, nil // no facet result means no documents
	}

	var names []string
	for _, term := range facetResult.Terms.Terms() {
		names = append(names, term.Term)
	}

	b.logger.Debug("Retrieved indexed server names",
		zap.Int("count", len(names)))
	return names, nil
}

// CanonicalToolName returns the tool's full "server:tool" identity. Discovery
// stores the bare tool name in the index (ToolMetadata{ServerName:"github",
// Name:"create_issue"}), so the read seams must reattach the server prefix for
// consumers (retrieve_tools/describe_tool/call_tool_*) that require it (#871).
// The double-prefix guard is mandatory: legacy index data and test fixtures may
// already store a prefixed name — those pass through unchanged.
func CanonicalToolName(serverName, name string) string {
	if serverName == "" || strings.HasPrefix(name, serverName+":") {
		return name
	}
	return serverName + ":" + name
}

// Helper function to get string field from search results
func getStringField(fields map[string]interface{}, fieldName string) string {
	if val, ok := fields[fieldName]; ok {
		if strVal, ok := val.(string); ok {
			return strVal
		}
	}
	return ""
}
