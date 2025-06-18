package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/index"
	"mcpproxy-go/internal/storage"
	"mcpproxy-go/internal/upstream"
)

// Server wraps the MCP proxy server with all its dependencies
type Server struct {
	config          *config.Config
	logger          *zap.Logger
	storageManager  *storage.Manager
	indexManager    *index.Manager
	upstreamManager *upstream.Manager
	mcpProxy        *MCPProxyServer
}

// NewServer creates a new server instance
func NewServer(cfg *config.Config, logger *zap.Logger) (*Server, error) {
	// Initialize storage manager
	storageManager, err := storage.NewManager(cfg.DataDir, logger.Sugar())
	if err != nil {
		return nil, fmt.Errorf("failed to initialize storage manager: %w", err)
	}

	// Initialize index manager
	indexManager, err := index.NewManager(cfg.DataDir, logger)
	if err != nil {
		storageManager.Close()
		return nil, fmt.Errorf("failed to initialize index manager: %w", err)
	}

	// Initialize upstream manager
	upstreamManager := upstream.NewManager(logger)

	// Create MCP proxy server
	mcpProxy := NewMCPProxyServer(storageManager, indexManager, upstreamManager, logger)

	server := &Server{
		config:          cfg,
		logger:          logger,
		storageManager:  storageManager,
		indexManager:    indexManager,
		upstreamManager: upstreamManager,
		mcpProxy:        mcpProxy,
	}

	// Load configured servers from storage and add to upstream manager
	if err := server.loadConfiguredServers(); err != nil {
		return nil, fmt.Errorf("failed to load configured servers: %w", err)
	}

	return server, nil
}

// loadConfiguredServers loads servers from config and storage, then adds them to upstream manager
func (s *Server) loadConfiguredServers() error {
	// First load servers from config file
	for _, serverConfig := range s.config.Servers {
		if serverConfig.Enabled {
			// Store in persistent storage
			id, err := s.storageManager.AddUpstream(serverConfig)
			if err != nil {
				s.logger.Warn("Failed to store server config",
					zap.String("name", serverConfig.Name),
					zap.Error(err))
				continue
			}

			// Add to upstream manager
			if err := s.upstreamManager.AddServer(id, serverConfig); err != nil {
				s.logger.Warn("Failed to add upstream server",
					zap.String("name", serverConfig.Name),
					zap.Error(err))
			}
		}
	}

	// Then load any additional servers from storage
	storedServers, err := s.storageManager.ListUpstreams()
	if err != nil {
		return fmt.Errorf("failed to list stored upstreams: %w", err)
	}

	for _, serverConfig := range storedServers {
		if serverConfig.Enabled {
			// Check if already added from config
			if !s.isServerInConfig(serverConfig.Name) {
				if err := s.upstreamManager.AddServer(serverConfig.Name, serverConfig); err != nil {
					s.logger.Warn("Failed to add stored upstream server",
						zap.String("id", serverConfig.Name),
						zap.String("name", serverConfig.Name),
						zap.Error(err))
				}
			}
		}
	}

	return nil
}

// isServerInConfig checks if a server name is already in the config
func (s *Server) isServerInConfig(name string) bool {
	for _, serverConfig := range s.config.Servers {
		if serverConfig.Name == name {
			return true
		}
	}
	return false
}

// Start starts the MCP proxy server
func (s *Server) Start(ctx context.Context) error {
	s.logger.Info("Starting MCP proxy server")

	// Connect to upstream servers
	if err := s.upstreamManager.ConnectAll(ctx); err != nil {
		s.logger.Warn("Some upstream servers failed to connect", zap.Error(err))
	}

	// Discover and index tools
	if err := s.discoverAndIndexTools(ctx); err != nil {
		s.logger.Error("Failed to discover and index tools", zap.Error(err))
	}

	// Determine transport mode based on listen address
	if s.config.Listen != "" && s.config.Listen != ":0" {
		// Start the MCP server in HTTP mode (Streamable HTTP)
		s.logger.Info("Starting MCP server",
			zap.String("transport", "streamable-http"),
			zap.String("listen", s.config.Listen))

		// Create Streamable HTTP server with custom routing
		streamableServer := server.NewStreamableHTTPServer(s.mcpProxy.GetMCPServer())

		// Create custom HTTP server for handling multiple routes
		if err := s.startCustomHTTPServer(streamableServer); err != nil {
			return fmt.Errorf("MCP Streamable HTTP server error: %w", err)
		}
	} else {
		// Start the MCP server in stdio mode
		s.logger.Info("Starting MCP server", zap.String("transport", "stdio"))

		// Serve using stdio (standard MCP transport)
		if err := server.ServeStdio(s.mcpProxy.GetMCPServer()); err != nil {
			return fmt.Errorf("MCP server error: %w", err)
		}
	}

	return nil
}

// discoverAndIndexTools discovers tools from upstream servers and indexes them
func (s *Server) discoverAndIndexTools(ctx context.Context) error {
	s.logger.Info("Discovering and indexing tools...")

	tools, err := s.upstreamManager.DiscoverTools(ctx)
	if err != nil {
		return fmt.Errorf("failed to discover tools: %w", err)
	}

	if len(tools) == 0 {
		s.logger.Warn("No tools discovered from upstream servers")
		return nil
	}

	// Index tools
	if err := s.indexManager.BatchIndexTools(tools); err != nil {
		return fmt.Errorf("failed to index tools: %w", err)
	}

	s.logger.Info("Successfully indexed tools", zap.Int("count", len(tools)))
	return nil
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown() error {
	s.logger.Info("Shutting down MCP proxy server...")

	// Disconnect upstream servers
	if err := s.upstreamManager.DisconnectAll(); err != nil {
		s.logger.Error("Failed to disconnect upstream servers", zap.Error(err))
	}

	// Close managers
	if err := s.indexManager.Close(); err != nil {
		s.logger.Error("Failed to close index manager", zap.Error(err))
	}

	if err := s.storageManager.Close(); err != nil {
		s.logger.Error("Failed to close storage manager", zap.Error(err))
	}

	s.logger.Info("MCP proxy server shutdown complete")
	return nil
}

// startCustomHTTPServer creates a custom HTTP server that handles both /mcp and /mcp/ routes
func (s *Server) startCustomHTTPServer(streamableServer *server.StreamableHTTPServer) error {
	mux := http.NewServeMux()

	// Handle both /mcp and /mcp/ patterns
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		// Redirect /mcp to /mcp/ for consistency
		if r.URL.Path == "/mcp" {
			http.Redirect(w, r, "/mcp/", http.StatusMovedPermanently)
			return
		}
		streamableServer.ServeHTTP(w, r)
	})

	mux.HandleFunc("/mcp/", func(w http.ResponseWriter, r *http.Request) {
		streamableServer.ServeHTTP(w, r)
	})

	// Create HTTP server
	httpServer := &http.Server{
		Addr:    s.config.Listen,
		Handler: mux,
	}

	s.logger.Info("Starting custom HTTP server with /mcp routing")
	return httpServer.ListenAndServe()
}
