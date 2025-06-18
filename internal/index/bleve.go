package index

import (
	"fmt"
	"path/filepath"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/keyword"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/standard"
	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
)

// BleveIndex wraps Bleve index operations
type BleveIndex struct {
	index  bleve.Index
	logger *zap.Logger
}

// ToolDocument represents a tool document in the index
type ToolDocument struct {
	ToolName    string `json:"tool_name"`
	ServerName  string `json:"server_name"`
	Description string `json:"description"`
	ParamsJSON  string `json:"params_json"`
	Hash        string `json:"hash"`
	Tags        string `json:"tags"`
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

	// Tool name field (keyword analyzer - exact match)
	toolNameField := bleve.NewTextFieldMapping()
	toolNameField.Analyzer = keyword.Name
	toolNameField.Store = true
	toolNameField.Index = true
	toolMapping.AddFieldMappingsAt("tool_name", toolNameField)

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
	doc := &ToolDocument{
		ToolName:    toolMeta.Name,
		ServerName:  toolMeta.ServerName,
		Description: toolMeta.Description,
		ParamsJSON:  toolMeta.ParamsJSON,
		Hash:        toolMeta.Hash,
		Tags:        "", // Can be extended later
	}

	// Use server:tool format as document ID for uniqueness
	docID := fmt.Sprintf("%s:%s", toolMeta.ServerName, toolMeta.Name)

	b.logger.Debug("Indexing tool", zap.String("doc_id", docID))
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

// SearchTools searches for tools using BM25 scoring
func (b *BleveIndex) SearchTools(query string, limit int) ([]*config.SearchResult, error) {
	if query == "" {
		return nil, fmt.Errorf("search query cannot be empty")
	}

	// Create a match query for full-text search
	matchQuery := bleve.NewMatchQuery(query)

	// Create search request
	searchReq := bleve.NewSearchRequest(matchQuery)
	searchReq.Size = limit
	searchReq.Fields = []string{"tool_name", "server_name", "description", "params_json", "hash"}
	searchReq.Highlight = bleve.NewHighlight()

	b.logger.Debug("Searching tools", zap.String("query", query), zap.Int("limit", limit))

	searchResult, err := b.index.Search(searchReq)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	// Convert results
	var results []*config.SearchResult
	for _, hit := range searchResult.Hits {
		toolMeta := &config.ToolMetadata{
			Name:        getStringField(hit.Fields, "tool_name"),
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

	b.logger.Debug("Found tools matching query", zap.Int("count", len(results)), zap.String("query", query))
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
		doc := &ToolDocument{
			ToolName:    toolMeta.Name,
			ServerName:  toolMeta.ServerName,
			Description: toolMeta.Description,
			ParamsJSON:  toolMeta.ParamsJSON,
			Hash:        toolMeta.Hash,
			Tags:        "",
		}

		docID := fmt.Sprintf("%s:%s", toolMeta.ServerName, toolMeta.Name)
		batch.Index(docID, doc)
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

// Helper function to get string field from search results
func getStringField(fields map[string]interface{}, fieldName string) string {
	if val, ok := fields[fieldName]; ok {
		if strVal, ok := val.(string); ok {
			return strVal
		}
	}
	return ""
}
