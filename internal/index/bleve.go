package index

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/keyword"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/standard"
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
// (<dataDir>/index.bleve/<slug>/) without pre-creating it.
func newBleveIndexAt(indexPath string, logger *zap.Logger) (*BleveIndex, error) {
	// Try to open existing index
	index, err := bleve.Open(indexPath)
	if err != nil {
		// If index doesn't exist, create a new one
		if mkErr := os.MkdirAll(filepath.Dir(indexPath), 0o755); mkErr != nil {
			return nil, fmt.Errorf("failed to create index parent dir: %w", mkErr)
		}
		logger.Info("Creating new Bleve index", zap.String("path", indexPath))
		index, err = createBleveIndex(indexPath)
		if err != nil {
			return nil, fmt.Errorf("failed to create Bleve index: %w", err)
		}
	} else {
		logger.Info("Opened existing Bleve index", zap.String("path", indexPath))
		index, err = rebuildIfMappingPredatesAnnotations(index, indexPath, logger)
		if err != nil {
			return nil, err
		}
	}

	return &BleveIndex{
		index:          index,
		logger:         logger,
		searchPageSize: defaultSearchPageSize,
	}, nil
}

// rebuildIfMappingPredatesAnnotations replaces an already-open index whose
// on-disk mapping was created before the annotations_json field mapping below
// (Spec 109 FR-028 tier-badge support) existed, with a fresh index that has
// the current mapping.
//
// bleve.Open never re-applies createBleveIndex's mapping to an index that
// already exists on disk — the mapping is baked in at creation time, and
// vendored bleve v2.6.1 exposes no SetMapping or other migration path
// (confirmed by reading mapping.IndexMapping's method set). An index that
// predates this field keeps its old mapping forever, with bleve's
// dynamic-field defaults (IndexDynamic=true) still enabled for it: the very
// first write that carries annotations_json (the differential-update
// backfill in runtime/lifecycle.go) then gets indexed as free-text,
// including into the field-less `_all` composite field that
// buildToolSearchQuery's clause 5 (the bare MatchQuery) searches — silently
// making annotation JSON term-searchable and shifting BM25 corpus stats for
// every query on an upgraded (not freshly installed) deployment (review
// round 6, finding 1: high).
//
// Round 6 only logged a warning and left the fix to an operator manually
// deleting indexPath (review round 7, finding 2: the underlying gap was still
// open on any upgrade nobody noticed the log line for). Round 7 then made
// this self-healing, but did so destructive-before-construct (Close ->
// os.RemoveAll(indexPath) -> createBleveIndex(indexPath)): any failure after
// the wipe (disk full, a permission error on a leftover file) propagated all
// the way through index.NewManager -> runtime.New and failed daemon startup
// on every retry, taking a previously-degraded-but-working deployment (stale
// mapping, but an openable, functional index) down to a hard outage that
// needed an operator to intervene (review round 8, finding 1: high).
//
// This builds the replacement at a temporary sibling path FIRST and only
// touches the existing index once that succeeds. index.bleve is a derived
// search cache, not a source of truth, so once the swap does happen an empty
// index is fine — the normal discovery path
// (applyDifferentialToolUpdate) unconditionally reindexes everything as each
// server (re)connects during startup, the same way it already backfills a
// brand-new install. But if construction fails, the original index must
// still be there and still work: this logs the error and returns the
// original (stale-mapping) index unchanged, the same degraded-but-running
// outcome round 6 had, rather than failing startup.
// Manager.RebuildIndex (internal/index/manager.go) is a separate, currently
// unwired no-op stub for an operator-triggered full rebuild and is not used
// here — this path only ever runs once, at index-open time, against a
// mapping that is provably stale.
//
// The finalize step retires the original by renaming it aside
// (os.Rename(indexPath, indexPath+".rebuild-old")) rather than deleting it
// with os.RemoveAll. A partial RemoveAll failure — one permission-impaired
// or locked leftover file inside the index directory — can delete most-but-
// not-all of the original and still return an error; on the next startup
// bleve.Open(indexPath) then fails on the mangled remnant, falls through to
// createBleveIndex(indexPath), and if index_meta.json survived the partial
// delete, vendored bleve's O_CREATE|O_EXCL index_meta.Save makes bleve.New
// return ErrorIndexPathExists — the same operator-intervention outage round
// 8 fixed, reachable through a narrower door (review round 9, finding 1).
// os.Rename is a single directory-entry move: it needs write access to
// indexPath's PARENT, never to anything inside indexPath itself, so it
// cannot fail this way. If the second rename (moving the replacement into
// place) fails, the aside copy is renamed back so the original stays
// available rather than leaving indexPath empty.
func rebuildIfMappingPredatesAnnotations(idx bleve.Index, indexPath string, logger *zap.Logger) (bleve.Index, error) {
	fm := idx.Mapping().FieldMappingForPath("annotations_json")
	if fm.Type != "" {
		// Explicitly mapped (Type is only set by NewTextFieldMapping and
		// friends, never by the zero-value fallback FieldMappingForPath
		// returns for a path with no static mapping) — this index already
		// has the current mapping.
		return idx, nil
	}

	logger.Warn("Bleve index predates the annotations_json field mapping; "+
		"annotation JSON may have been indexed as free text and skewed "+
		"tool-search ranking. Rebuilding the index automatically with the "+
		"current mapping (see internal/index/bleve.go:rebuildIfMappingPredatesAnnotations).",
		zap.String("path", indexPath))

	// Construct the replacement before destroying anything. tmpPath is a
	// sibling of indexPath, never indexPath itself, so a failure here never
	// touches the original index.
	tmpPath := indexPath + ".rebuild-tmp"
	_ = os.RemoveAll(tmpPath) // best-effort: clear any leftover from a previous failed attempt

	fresh, err := createBleveIndex(tmpPath)
	if err != nil {
		logger.Error("Failed to build replacement Bleve index with the current mapping; "+
			"continuing with the stale-mapping index instead of failing startup "+
			"(annotation JSON may still be indexed as free text until this is retried)",
			zap.Error(err), zap.String("path", indexPath))
		_ = os.RemoveAll(tmpPath)
		return idx, nil
	}
	if closeErr := fresh.Close(); closeErr != nil {
		_ = os.RemoveAll(tmpPath)
		logger.Error("Failed to close freshly built replacement Bleve index; "+
			"continuing with the stale-mapping index instead of failing startup",
			zap.Error(closeErr), zap.String("path", indexPath))
		return idx, nil
	}

	// From here on the replacement exists on disk and is known-good, so it
	// is safe to retire the original.
	if err := idx.Close(); err != nil {
		_ = os.RemoveAll(tmpPath)
		return nil, fmt.Errorf("failed to close stale-mapping Bleve index for rebuild: %w", err)
	}

	// Move the original aside instead of deleting it: os.Rename either
	// succeeds atomically or leaves indexPath completely untouched, so a
	// file inside it that RemoveAll could not delete (permission-impaired,
	// locked) can no longer corrupt it into a half-removed, unopenable-and-
	// uncreatable state (see the function doc comment, round 9 finding 1).
	oldPath := indexPath + ".rebuild-old"
	_ = os.RemoveAll(oldPath) // best-effort: clear any leftover from a previous failed attempt
	if err := os.Rename(indexPath, oldPath); err != nil {
		_ = os.RemoveAll(tmpPath)
		return nil, fmt.Errorf("failed to move aside stale-mapping Bleve index at %s: %w", indexPath, err)
	}
	if err := os.Rename(tmpPath, indexPath); err != nil {
		// indexPath is now empty (just moved to oldPath): restore the
		// original so a functional, if stale-mapping, index is still there
		// rather than leaving nothing at all.
		if restoreErr := os.Rename(oldPath, indexPath); restoreErr != nil {
			return nil, fmt.Errorf("failed to move rebuilt Bleve index into place at %s (%w), and failed to restore the original from %s: %w",
				indexPath, err, oldPath, restoreErr)
		}
		return nil, fmt.Errorf("failed to move rebuilt Bleve index into place at %s: %w", indexPath, err)
	}

	reopened, err := bleve.Open(indexPath)
	if err != nil {
		// The just-built, cleanly-closed replacement failed to reopen (rare —
		// e.g. a transient I/O error in the gap between the two calls).
		// Restore the original rather than leaving indexPath occupied by an
		// unopenable index_meta.json: otherwise a later restart whose
		// bleve.Open(indexPath) also fails falls through to
		// createBleveIndex(indexPath), which hits the same O_CREATE|O_EXCL
		// ErrorIndexPathExists this fix exists to prevent (zcode round-9
		// review, finding 1 — the identical outage shape round 8 fixed,
		// reachable through this narrower door too).
		failedPath := indexPath + ".failed-reopen"
		_ = os.RemoveAll(failedPath)
		if renameErr := os.Rename(indexPath, failedPath); renameErr != nil {
			return nil, fmt.Errorf("failed to reopen rebuilt Bleve index at %s (%w), and failed to move it aside to restore the original: %w",
				indexPath, err, renameErr)
		}
		if restoreErr := os.Rename(oldPath, indexPath); restoreErr != nil {
			return nil, fmt.Errorf("failed to reopen rebuilt Bleve index at %s (%w), and failed to restore the original from %s: %w",
				indexPath, err, oldPath, restoreErr)
		}
		_ = os.RemoveAll(failedPath) // best-effort: drop the unopenable replacement
		return nil, fmt.Errorf("failed to reopen rebuilt Bleve index at %s: %w", indexPath, err)
	}

	// Best-effort cleanup of the retired original. It is never opened or
	// referenced by any other code path, so a failure here (the same class
	// of permission-impaired leftover this fix exists to route around) is
	// harmless and must not fail startup.
	_ = os.RemoveAll(oldPath)

	logger.Info("Rebuilt Bleve index with the current field mapping; "+
		"tools will reappear as each server reconnects", zap.String("path", indexPath))
	return reopened, nil
}

// createBleveIndex creates a new Bleve index with proper mapping
func createBleveIndex(indexPath string) (bleve.Index, error) {
	// Create index mapping
	indexMapping := bleve.NewIndexMapping()

	// Create document mapping for tools
	toolMapping := bleve.NewDocumentMapping()
	// Disable dynamic field mapping: every field this index ever writes is
	// declared explicitly below. Without this, adding a new ToolDocument
	// field in the future (as annotations_json itself was added, review
	// round 6 finding 1) would silently fall back to bleve's dynamic-field
	// defaults — full-text-indexed and included in `_all` — on any FRESH
	// index too, not just ones migrating forward. This does not, and cannot,
	// retroactively fix an already-open index created before this line
	// existed; see warnIfMappingPredatesAnnotations above.
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

	// Create the index
	return bleve.New(indexPath, indexMapping)
}

// Close closes the index
func (b *BleveIndex) Close() error {
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

	// Create combined searchable text for better full-text search
	searchableText := fmt.Sprintf("%s %s %s %s",
		toolName,
		toolMeta.Name,
		toolMeta.Description,
		toolMeta.ParamsJSON)

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
		SearchableText:   searchableText,
		AnnotationsJSON:  annotationsJSON,
	}

	return docID, doc
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

// Hit is the canonical registration identity SearchToolsAdmitted hands to its
// predicate (Spec 108 FR-011, data-model.md §2): the exact (server, raw tool)
// pair a hit resolves to, NEVER a stored annotation — the index carries no
// annotation field and gains none (ToolDocument/readToolMetadata are
// unchanged; research.md "Annotation source for SearchToolsAdmitted"). A
// caller that needs the tool's effective annotations resolves them itself,
// through the same identity seam every dispatch path already uses
// (profile.EffectiveAnnotations = resolveExactToolIdentity), keeping
// discovery and execution classifying from one source.
type Hit struct {
	Server string
	Tool   string
}

// Admission is admit's per-hit verdict for SearchToolsAdmitted.
type Admission int

const (
	// Admit means the hit is visible to this caller and counts toward limit.
	Admit Admission = iota
	// RejectScope means the hit's server is outside the caller's effective
	// scope (agent-token allowed_servers, profile server set). Never counted
	// in hiddenByPolicy — an out-of-scope tool must stay invisible, not merely
	// "hidden by profile" (FR-011).
	RejectScope
	// RejectPolicy means the hit's server was in scope but the tool policy
	// (Spec 108 CompiledPolicy.Decide) excluded it. Counted in hiddenByPolicy.
	RejectPolicy
)

// SearchToolsAdmitted is SearchToolsScoped's hit-level counterpart (Spec 108
// FR-011, data-model.md §2): the pre-limit predicate sees the hit's canonical
// (server, tool) identity — never a stored annotation, the index carries
// none — and returns one of three verdicts instead of a bool, so the caller
// can tell an out-of-scope rejection (never counted, stays invisible) from a
// policy-only one (counted in hiddenByPolicy, the FR-011 `hidden_by_profile`
// figure) over the SAME exhaustive pre-limit scan SearchToolsScoped already
// runs. limit is applied to ADMITTED hits only — a rejected top hit, whatever
// its reason, never shortens the page — and hiddenByPolicy accumulates over
// the full match set, not merely the hits collected before the cut, so a
// caller whose page filled up before scanning every match still gets an
// accurate count.
//
// Identical to SearchToolsScoped in query construction (incl. the
// underscore-segment enhancement) and score-then-id sort. It differs in ONE
// respect, precisely because of the counting requirement above:
// SearchToolsScoped returns as soon as its page fills, while this method
// keeps paging to the end of the exhaustive match set regardless (zcode
// review round 1 — an early return there silently undercounted
// hiddenByPolicy for any RejectPolicy hit ranked below the cut). A predicate
// that only ever returns Admit/RejectScope (never RejectPolicy) still makes
// this method's RESULT SET equal SearchToolsScoped(query, limit, func(s
// string) bool { admit still sees the server only })'s exactly (T016a) —
// the extra scanning costs work, never a different admitted page.
func (b *BleveIndex) SearchToolsAdmitted(queryStr string, limit int, admit func(Hit) Admission) (results []*config.SearchResult, hiddenByPolicy int, err error) {
	if queryStr == "" {
		return nil, 0, fmt.Errorf("search query cannot be empty")
	}
	if admit == nil || limit <= 0 {
		return []*config.SearchResult{}, 0, nil
	}

	q, err := b.augmentedToolSearchQuery(queryStr, limit)
	if err != nil {
		return nil, 0, err
	}
	pageSize := limit
	if pageSize < scopedSearchMinPage {
		pageSize = scopedSearchMinPage
	}

	b.logger.Debug("Searching tools with admitted query", zap.String("query", queryStr), zap.Int("limit", limit))

	results = make([]*config.SearchResult, 0, limit)
	for from := 0; ; from += pageSize {
		searchResult, err := b.index.Search(newToolSearchRequest(q, from, pageSize))
		if err != nil {
			return nil, 0, fmt.Errorf("search failed: %w", err)
		}
		for _, hit := range searchResult.Hits {
			tool := readToolMetadata(hit.ID, hit.Fields)
			switch admit(Hit{Server: tool.ServerName, Tool: config.RawToolName(tool)}) {
			case Admit:
				// The page itself stops growing once it holds `limit`
				// admitted hits, but the SCAN does not stop here: a
				// RejectPolicy hit ranked below the cut must still be
				// counted (FR-011 "over the full match set" — codex/zcode
				// review round 1). Returning as soon as the page filled
				// silently undercounted hiddenByPolicy for every match
				// ranked after the limit-th admitted one.
				if len(results) < limit {
					results = append(results, &config.SearchResult{Tool: tool, Score: hit.Score})
				}
			case RejectPolicy:
				hiddenByPolicy++
			case RejectScope:
				// Invisible: never counted, never collected.
			}
		}
		if len(searchResult.Hits) == 0 || uint64(from+pageSize) >= searchResult.Total {
			break
		}
	}

	b.logger.Debug("Found admitted tools matching query", zap.Int("count", len(results)), zap.Int("hidden_by_policy", hiddenByPolicy), zap.String("query", queryStr))
	return results, hiddenByPolicy, nil
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

// RebuildIndex rebuilds the entire index
func (b *BleveIndex) RebuildIndex() error {
	// Get index stats before rebuild
	count, _ := b.index.DocCount()
	b.logger.Info("Rebuilding index", zap.Uint64("current_docs", count))

	// For now, we'll just log the operation
	// In a full implementation, this would:
	// 1. Create a new index
	// 2. Re-index all tools from storage
	// 3. Atomically swap indices

	return nil
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
