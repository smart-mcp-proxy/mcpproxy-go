package index

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/keyword"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/standard"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// BleveIndex wraps Bleve index operations
type BleveIndex struct {
	index  bleve.Index
	logger *zap.Logger
}

// ToolDocument represents a tool document in the index
type ToolDocument struct {
	ToolName       string `json:"tool_name"`      // Just the tool name (without server prefix)
	FullToolName   string `json:"full_tool_name"` // Complete server:tool format
	ServerName     string `json:"server_name"`
	Description    string `json:"description"`
	ParamsJSON     string `json:"params_json"`
	Hash           string `json:"hash"`
	Tags           string `json:"tags"`
	SearchableText string `json:"searchable_text"` // Combined searchable content
}

// NewBleveIndex creates a new Bleve index
func NewBleveIndex(dataDir string, logger *zap.Logger) (*BleveIndex, error) {
	indexPath := filepath.Join(dataDir, "index.bleve")

	// Try to open existing index
	index, err := bleve.Open(indexPath)
	if err != nil {
		// If index doesn't exist, create a new one
		logger.Info("Creating new Bleve index", zap.String("path", indexPath))
		index, err = createBleveIndex(indexPath)
		if err != nil {
			return nil, fmt.Errorf("failed to create Bleve index: %w", err)
		}
	} else {
		logger.Info("Opened existing Bleve index", zap.String("path", indexPath))
	}

	return &BleveIndex{
		index:  index,
		logger: logger,
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

// IndexTool indexes a tool document
func (b *BleveIndex) IndexTool(toolMeta *config.ToolMetadata) error {
	// Extract just the tool name (remove server prefix)
	toolName := toolMeta.Name
	if parts := strings.SplitN(toolMeta.Name, ":", 2); len(parts) == 2 {
		toolName = parts[1]
	}

	// Create combined searchable text for better full-text search
	searchableText := fmt.Sprintf("%s %s %s %s",
		toolName,
		toolMeta.Name,
		toolMeta.Description,
		toolMeta.ParamsJSON)

	doc := &ToolDocument{
		ToolName:       toolName,
		FullToolName:   toolMeta.Name,
		ServerName:     toolMeta.ServerName,
		Description:    toolMeta.Description,
		ParamsJSON:     toolMeta.ParamsJSON,
		Hash:           toolMeta.Hash,
		Tags:           "", // Can be extended later
		SearchableText: searchableText,
	}

	// Use server:tool format as document ID for uniqueness
	docID := fmt.Sprintf("%s:%s", toolMeta.ServerName, toolName)

	b.logger.Debug("Indexing tool", zap.String("doc_id", docID), zap.String("tool_name", toolName))
	return b.index.Index(docID, doc)
}

// DeleteTool removes a tool from the index
func (b *BleveIndex) DeleteTool(serverName, toolName string) error {
	docID := fmt.Sprintf("%s:%s", serverName, toolName)

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

// SearchTools searches for tools using multiple query strategies for better results
func (b *BleveIndex) SearchTools(queryStr string, limit int) ([]*config.SearchResult, error) {
	if queryStr == "" {
		return nil, fmt.Errorf("search query cannot be empty")
	}

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

	// Create search request
	searchReq := bleve.NewSearchRequest(boolQuery)
	searchReq.Size = limit
	searchReq.Fields = []string{"tool_name", "full_tool_name", "server_name", "description", "params_json", "hash"}
	searchReq.Highlight = bleve.NewHighlight()

	b.logger.Debug("Searching tools with enhanced query", zap.String("query", queryStr), zap.Int("limit", limit))

	searchResult, err := b.index.Search(searchReq)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	// Convert results
	var results []*config.SearchResult
	for _, hit := range searchResult.Hits {
		toolMeta := &config.ToolMetadata{
			Name:        getStringField(hit.Fields, "full_tool_name"),
			ServerName:  getStringField(hit.Fields, "server_name"),
			Description: getStringField(hit.Fields, "description"),
			ParamsJSON:  getStringField(hit.Fields, "params_json"),
			Hash:        getStringField(hit.Fields, "hash"),
		}

		results = append(results, &config.SearchResult{
			Tool:  toolMeta,
			Score: hit.Score,
		})
	}

	b.logger.Debug("Found tools matching query", zap.Int("count", len(results)), zap.String("query", queryStr))
	return results, nil
}

// GetDocumentCount returns the number of documents in the index
func (b *BleveIndex) GetDocumentCount() (uint64, error) {
	return b.index.DocCount()
}

// Batch operations for efficiency

// BatchIndex indexes multiple tools in a single batch
func (b *BleveIndex) BatchIndex(tools []*config.ToolMetadata) error {
	batch := b.index.NewBatch()

	for _, toolMeta := range tools {
		// Extract just the tool name (remove server prefix)
		toolName := toolMeta.Name
		if parts := strings.SplitN(toolMeta.Name, ":", 2); len(parts) == 2 {
			toolName = parts[1]
		}

		// Create combined searchable text
		searchableText := fmt.Sprintf("%s %s %s %s",
			toolName,
			toolMeta.Name,
			toolMeta.Description,
			toolMeta.ParamsJSON)

		doc := &ToolDocument{
			ToolName:       toolName,
			FullToolName:   toolMeta.Name,
			ServerName:     toolMeta.ServerName,
			Description:    toolMeta.Description,
			ParamsJSON:     toolMeta.ParamsJSON,
			Hash:           toolMeta.Hash,
			Tags:           "",
			SearchableText: searchableText,
		}

		docID := fmt.Sprintf("%s:%s", toolMeta.ServerName, toolName)
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

	// Create search request with high limit to get all tools
	searchReq := bleve.NewSearchRequest(query)
	searchReq.Size = 10000 // Maximum tools per server
	searchReq.Fields = []string{"tool_name", "full_tool_name", "server_name", "description", "params_json", "hash"}

	b.logger.Debug("Querying tools by server", zap.String("server", serverName))

	searchResult, err := b.index.Search(searchReq)
	if err != nil {
		return nil, fmt.Errorf("failed to query tools by server: %w", err)
	}

	// Convert results to ToolMetadata
	var tools []*config.ToolMetadata
	for _, hit := range searchResult.Hits {
		toolMeta := &config.ToolMetadata{
			Name:        getStringField(hit.Fields, "full_tool_name"),
			ServerName:  getStringField(hit.Fields, "server_name"),
			Description: getStringField(hit.Fields, "description"),
			ParamsJSON:  getStringField(hit.Fields, "params_json"),
			Hash:        getStringField(hit.Fields, "hash"),
		}
		tools = append(tools, toolMeta)
	}

	b.logger.Debug("Found tools for server",
		zap.String("server", serverName),
		zap.Int("count", len(tools)))

	return tools, nil
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
