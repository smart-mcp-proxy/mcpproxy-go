package index

import (
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
	}

	return &BleveIndex{
		index:          index,
		logger:         logger,
		searchPageSize: defaultSearchPageSize,
	}, nil
}

// createBleveIndex creates a new Bleve index with proper mapping
func createBleveIndex(indexPath string) (bleve.Index, error) {
	// Create index mapping
	indexMapping := bleve.NewIndexMapping()

	// Create document mapping for tools
	toolMapping := bleve.NewDocumentMapping()

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

	// Hash field (keyword analyzer)
	hashField := bleve.NewTextFieldMapping()
	hashField.Analyzer = keyword.Name
	hashField.Store = true
	hashField.Index = false // Don't index hash for search
	toolMapping.AddFieldMappingsAt("hash", hashField)

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
	return &config.ToolMetadata{
		Name:             canonical,
		RawName:          strings.TrimPrefix(canonical, serverName+":"),
		ServerName:       serverName,
		Description:      getStringField(fields, "description"),
		ParamsJSON:       getStringField(fields, "params_json"),
		OutputSchemaJSON: getStringField(fields, "output_schema_json"),
		Hash:             getStringField(fields, "hash"),
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
	searchReq.Fields = []string{"tool_name", "full_tool_name", "server_name", "description", "params_json", "output_schema_json", "hash"}
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

// SearchTools searches for tools using multiple query strategies for better results
func (b *BleveIndex) SearchTools(queryStr string, limit int) ([]*config.SearchResult, error) {
	if queryStr == "" {
		return nil, fmt.Errorf("search query cannot be empty")
	}

	searchReq := newToolSearchRequest(buildToolSearchQuery(queryStr), 0, limit)

	b.logger.Debug("Searching tools with enhanced query", zap.String("query", queryStr), zap.Int("limit", limit))

	searchResult, err := b.index.Search(searchReq)
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
// query (no extra clause — a Must term on server_name would change scores)
// and the identical score-then-id sort, and pages through the ranked result
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

	q := buildToolSearchQuery(queryStr)
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

	fields := []string{"tool_name", "full_tool_name", "server_name", "description", "params_json", "output_schema_json", "hash"}

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
