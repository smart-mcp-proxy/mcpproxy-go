package upstream

import (
	"context"
	"io"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/upstream/types"

	"github.com/mark3labs/mcp-go/mcp"
)

// MCPClient defines the core interface for MCP client operations
type MCPClient interface {
	// Connection management
	Connect(ctx context.Context) error
	Disconnect() error
	IsConnected() bool

	// MCP operations
	ListTools(ctx context.Context) ([]*config.ToolMetadata, error)
	CallTool(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error)

	// Status and info
	GetConnectionInfo() types.ConnectionInfo
	GetServerInfo() *mcp.InitializeResult
}

// StatefulClient extends MCPClient with state management capabilities
type StatefulClient interface {
	MCPClient

	// State management
	GetState() types.ConnectionState
	IsConnecting() bool

	// Advanced connection management
	ShouldRetry() bool
	SetStateChangeCallback(callback func(oldState, newState types.ConnectionState, info *types.ConnectionInfo))
}

// TransportClient defines transport-specific client creation
type TransportClient interface {
	// Transport-specific connection
	ConnectWithTransport(ctx context.Context, transportType string) error

	// Access to underlying transport details
	GetTransportType() string
	GetStderr() io.Reader // For stdio transport
}

// AuthStrategy defines authentication methods
type AuthStrategy interface {
	// Attempt authentication
	Authenticate(ctx context.Context, client MCPClient) error

	// Check if error indicates auth failure
	IsAuthError(err error) bool

	// Get strategy name for logging
	Name() string
}

// ClientFactory creates different types of clients
type ClientFactory interface {
	// Create core client for basic operations
	CreateCoreClient(id string, config *config.ServerConfig) (MCPClient, error)

	// Create managed client with state machine
	CreateManagedClient(id string, config *config.ServerConfig) (StatefulClient, error)

	// Create CLI client for debugging
	CreateCLIClient(id string, config *config.ServerConfig) (MCPClient, error)
}

// ClientPool manages multiple clients
type ClientPool interface {
	// Pool management
	AddClient(id string, client StatefulClient) error
	RemoveClient(id string)
	GetClient(id string) (StatefulClient, bool)
	GetAllClients() map[string]StatefulClient

	// Bulk operations
	ConnectAll(ctx context.Context) error
	DisconnectAll() error

	// Tool operations across all clients
	DiscoverTools(ctx context.Context) ([]*config.ToolMetadata, error)
	CallTool(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error)
}
