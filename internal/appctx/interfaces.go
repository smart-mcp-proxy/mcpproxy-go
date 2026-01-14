package appctx

import (
	"context"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"

	"github.com/mark3labs/mcp-go/client"
	"go.uber.org/zap"
)

// NotificationHandler defines the interface for handling notifications
// This is a simplified version to avoid circular dependencies
type NotificationHandler interface {
	SendNotification(notification interface{})
}

// UpstreamManager interface defines contract for managing upstream MCP servers
type UpstreamManager interface {
	// Server lifecycle management
	AddServerConfig(id string, serverConfig *config.ServerConfig) error
	AddServer(id string, serverConfig *config.ServerConfig) error
	RemoveServer(id string)
	GetClient(id string) (*managed.Client, bool)
	GetAllClients() map[string]*managed.Client
	GetAllServerNames() []string
	ListServers() map[string]*config.ServerConfig

	// Connection management
	ConnectAll(ctx context.Context) error
	DisconnectAll() error
	RetryConnection(serverName string) error

	// Tool operations
	DiscoverTools(ctx context.Context) ([]*config.ToolMetadata, error)
	CallTool(ctx context.Context, toolName string, args map[string]interface{}) (interface{}, error)
	InvalidateAllToolCountCaches()

	// Status and statistics
	GetStats() map[string]interface{}
	GetTotalToolCount() int
	HasDockerContainers() bool

	// Configuration
	SetLogConfig(logConfig *config.LogConfig)

	// OAuth operations
	StartManualOAuth(serverName string, force bool) error

	// Notification handling
	AddNotificationHandler(handler NotificationHandler)
}

// IndexManager interface defines contract for search indexing operations
type IndexManager interface {
	// Tool indexing
	IndexTool(toolMeta *config.ToolMetadata) error
	BatchIndexTools(tools []*config.ToolMetadata) error

	// Search operations
	SearchTools(query string, limit int) ([]*config.SearchResult, error)
	Search(query string, limit int) ([]*config.SearchResult, error)

	// Tool management
	DeleteTool(serverName, toolName string) error
	DeleteServerTools(serverName string) error
	GetToolsByServer(serverName string) ([]*config.ToolMetadata, error)

	// Index management
	RebuildIndex() error
	GetDocumentCount() (uint64, error)
	GetStats() (map[string]interface{}, error)

	// Lifecycle
	Close() error
}

// StorageManager interface defines contract for persistence operations
type StorageManager interface {
	// Upstream server operations
	SaveUpstreamServer(serverConfig *config.ServerConfig) error
	GetUpstreamServer(name string) (*config.ServerConfig, error)
	ListUpstreamServers() ([]*config.ServerConfig, error)
	ListQuarantinedUpstreamServers() ([]*config.ServerConfig, error)
	ListQuarantinedTools(serverName string) ([]map[string]interface{}, error)
	DeleteUpstreamServer(name string) error
	EnableUpstreamServer(name string, enabled bool) error
	QuarantineUpstreamServer(name string, quarantined bool) error

	// Tool statistics operations
	IncrementToolUsage(toolName string) error
	GetToolUsage(toolName string) (*storage.ToolStatRecord, error)
	GetToolStatistics(topN int) (*config.ToolStats, error)

	// Tool hash operations (for change detection)
	SaveToolHash(toolName, hash string) error
	GetToolHash(toolName string) (string, error)
	HasToolChanged(toolName, currentHash string) (bool, error)
	DeleteToolHash(toolName string) error

	// Maintenance operations
	Backup(destPath string) error
	GetSchemaVersion() (uint64, error)
	GetStats() (map[string]interface{}, error)

	// Compatibility aliases
	ListUpstreams() ([]*config.ServerConfig, error)
	AddUpstream(serverConfig *config.ServerConfig) (string, error)
	RemoveUpstream(id string) error
	UpdateUpstream(id string, serverConfig *config.ServerConfig) error
	GetToolStats(topN int) ([]map[string]interface{}, error)

	// Lifecycle
	Close() error
}

// OAuthTokenManager interface defines contract for OAuth token management
type OAuthTokenManager interface {
	// Token store management
	GetOrCreateTokenStore(serverName string) client.TokenStore
	HasTokenStore(serverName string) bool

	// OAuth completion callbacks
	SetOAuthCompletionCallback(callback func(serverName string))
	NotifyOAuthCompletion(serverName string)

	// Token persistence (for persistent stores)
	GetToken(serverName string) (interface{}, error) // Returns oauth2.Token or equivalent
	SaveToken(serverName string, token interface{}) error
	ClearToken(serverName string) error
}

// DockerIsolationManager interface defines contract for Docker isolation operations
type DockerIsolationManager interface {
	// Isolation detection and management
	ShouldIsolate(command string, args []string) bool
	IsDockerAvailable() bool
	GetDockerIsolationWarning(serverConfig *config.ServerConfig) string

	// Container lifecycle
	StartIsolatedCommand(ctx context.Context, command string, args []string, env map[string]string, workingDir string) (interface{}, error) // Returns Process or equivalent
	StopContainer(containerID string) error
	CleanupContainer(containerID string) error

	// Resource management
	SetResourceLimits(memory, cpu string) error
	GetContainerStats(containerID string) (map[string]interface{}, error)

	// Configuration
	GetDefaultImage(command string) string
	SetDefaultImages(images map[string]string) error
}

// LogManager interface defines contract for logging operations
type LogManager interface {
	// Logger creation
	GetServerLogger(serverName string) *zap.Logger
	GetMainLogger() *zap.Logger
	CreateLogger(name string, config *config.LogConfig) *zap.Logger

	// Log management
	RotateLogs() error
	GetLogFiles() ([]string, error)
	GetLogContent(logFile string, lines int) ([]string, error)

	// Configuration
	SetLogLevel(level string) error
	GetLogLevel() string
	UpdateLogConfig(config *config.LogConfig) error

	// Lifecycle
	Sync() error
	Close() error
}

// CacheManager interface defines contract for response caching operations
type CacheManager interface {
	// Cache operations
	Get(key string) (interface{}, bool)
	Set(key string, value interface{}, ttl time.Duration) error
	Delete(key string) error
	Clear() error

	// Cache statistics
	GetStats() map[string]interface{}
	GetHitRate() float64

	// Cache management
	SetTTL(key string, ttl time.Duration) error
	GetTTL(key string) (time.Duration, error)
	Expire(key string) error

	// Lifecycle
	Close() error
}
