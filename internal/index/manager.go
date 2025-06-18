package index

import (
	"fmt"
	"sync"

	"mcpproxy-go/internal/config"

	"go.uber.org/zap"
)

// Manager provides a unified interface for indexing operations
type Manager struct {
	bleveIndex *BleveIndex
	mu         sync.RWMutex
	logger     *zap.Logger
}

// NewManager creates a new index manager
func NewManager(dataDir string, logger *zap.Logger) (*Manager, error) {
	bleveIndex, err := NewBleveIndex(dataDir, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create Bleve index: %w", err)
	}

	return &Manager{
		bleveIndex: bleveIndex,
		logger:     logger,
	}, nil
}

// Close closes the index manager
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.bleveIndex != nil {
		return m.bleveIndex.Close()
	}
	return nil
}

// IndexTool indexes a single tool
func (m *Manager) IndexTool(toolMeta *config.ToolMetadata) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.bleveIndex.IndexTool(toolMeta)
}

// BatchIndexTools indexes multiple tools efficiently
func (m *Manager) BatchIndexTools(tools []*config.ToolMetadata) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.bleveIndex.BatchIndex(tools)
}

// SearchTools searches for tools matching the query
func (m *Manager) SearchTools(query string, limit int) ([]*config.SearchResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if limit <= 0 {
		limit = 20 // default limit
	}

	return m.bleveIndex.SearchTools(query, limit)
}

// Search searches for tools matching the query (alias for SearchTools)
func (m *Manager) Search(query string, limit int) ([]*config.SearchResult, error) {
	return m.SearchTools(query, limit)
}

// DeleteTool removes a tool from the index
func (m *Manager) DeleteTool(serverName, toolName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.bleveIndex.DeleteTool(serverName, toolName)
}

// DeleteServerTools removes all tools from a specific server
func (m *Manager) DeleteServerTools(serverName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.bleveIndex.DeleteServerTools(serverName)
}

// GetDocumentCount returns the number of indexed documents
func (m *Manager) GetDocumentCount() (uint64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.bleveIndex.GetDocumentCount()
}

// RebuildIndex rebuilds the entire index
func (m *Manager) RebuildIndex() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.bleveIndex.RebuildIndex()
}

// GetStats returns indexing statistics
func (m *Manager) GetStats() (map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	docCount, err := m.bleveIndex.GetDocumentCount()
	if err != nil {
		return nil, err
	}

	stats := map[string]interface{}{
		"document_count": docCount,
		"index_type":     "bleve",
		"search_backend": "BM25",
	}

	return stats, nil
}
