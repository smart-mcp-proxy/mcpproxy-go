package server

import (
	"context"

	"mcpproxy-go/internal/config"

	"github.com/stretchr/testify/mock"
)

// MockStorage is a mock implementation of the storage interface
type MockStorage struct {
	mock.Mock
}

func (m *MockStorage) IncrementToolUsage(toolName string) error {
	args := m.Called(toolName)
	return args.Error(0)
}

func (m *MockStorage) GetToolStats(topN int) ([]config.ToolStatEntry, error) {
	args := m.Called(topN)
	return args.Get(0).([]config.ToolStatEntry), args.Error(1)
}

func (m *MockStorage) ListUpstreamServers() ([]*config.ServerConfig, error) {
	args := m.Called()
	return args.Get(0).([]*config.ServerConfig), args.Error(1)
}

func (m *MockStorage) ListUpstreams() ([]*config.ServerConfig, error) {
	args := m.Called()
	return args.Get(0).([]*config.ServerConfig), args.Error(1)
}

func (m *MockStorage) AddUpstream(serverConfig *config.ServerConfig) (string, error) {
	args := m.Called(serverConfig)
	return args.String(0), args.Error(1)
}

func (m *MockStorage) RemoveUpstream(id string) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockStorage) UpdateUpstream(id string, serverConfig *config.ServerConfig) error {
	args := m.Called(id, serverConfig)
	return args.Error(0)
}

// MockIndex is a mock implementation of the index interface
type MockIndex struct {
	mock.Mock
}

func (m *MockIndex) Search(query string, limit int) ([]*config.SearchResult, error) {
	args := m.Called(query, limit)
	return args.Get(0).([]*config.SearchResult), args.Error(1)
}

func (m *MockIndex) IndexTool(toolMeta *config.ToolMetadata) error {
	args := m.Called(toolMeta)
	return args.Error(0)
}

func (m *MockIndex) BatchIndexTools(tools []*config.ToolMetadata) error {
	args := m.Called(tools)
	return args.Error(0)
}

func (m *MockIndex) DeleteTool(serverName, toolName string) error {
	args := m.Called(serverName, toolName)
	return args.Error(0)
}

func (m *MockIndex) DeleteServerTools(serverName string) error {
	args := m.Called(serverName)
	return args.Error(0)
}

func (m *MockIndex) GetDocumentCount() (uint64, error) {
	args := m.Called()
	return args.Get(0).(uint64), args.Error(1)
}

func (m *MockIndex) RebuildIndex() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockIndex) GetStats() (map[string]interface{}, error) {
	args := m.Called()
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

// MockUpstreamManager is a mock implementation of the upstream manager interface
type MockUpstreamManager struct {
	mock.Mock
}

func (m *MockUpstreamManager) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (interface{}, error) {
	mockArgs := m.Called(ctx, toolName, args)
	return mockArgs.Get(0), mockArgs.Error(1)
}

func (m *MockUpstreamManager) AddServer(id string, serverConfig *config.ServerConfig) error {
	args := m.Called(id, serverConfig)
	return args.Error(0)
}

func (m *MockUpstreamManager) RemoveServer(id string) {
	m.Called(id)
}

func (m *MockUpstreamManager) GetTotalToolCount() int {
	args := m.Called()
	return args.Int(0)
}
