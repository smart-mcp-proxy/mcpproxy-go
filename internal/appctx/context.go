package appctx

import (
	"fmt"

	"mcpproxy-go/internal/cache"
	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/index"
	"mcpproxy-go/internal/oauth"
	"mcpproxy-go/internal/secret"
	"mcpproxy-go/internal/storage"
	"mcpproxy-go/internal/upstream"
	"mcpproxy-go/internal/upstream/core"

	"go.uber.org/zap"
)

// ApplicationContext holds all application dependencies using interfaces
type ApplicationContext struct {
	// Core interfaces
	UpstreamManager        UpstreamManager
	IndexManager           IndexManager
	StorageManager         StorageManager
	OAuthTokenManager      OAuthTokenManager
	DockerIsolationManager DockerIsolationManager
	LogManager             LogManager
	CacheManager           CacheManager

	// Configuration
	Config    *config.Config
	LogConfig *config.LogConfig

	// Core logger
	Logger *zap.Logger
}

// NewApplicationContext creates a new application context with concrete implementations
func NewApplicationContext(cfg *config.Config, logConfig *config.LogConfig, logger *zap.Logger) (*ApplicationContext, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger cannot be nil")
	}

	// Initialize storage manager
	storageManager, err := storage.NewManager(cfg.DataDir, logger.Sugar())
	if err != nil {
		return nil, fmt.Errorf("failed to create storage manager: %w", err)
	}

	// Initialize index manager
	indexManager, err := index.NewManager(cfg.DataDir, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create index manager: %w", err)
	}

	// Initialize upstream manager
	secretResolver := secret.NewResolver()
	baseUpstreamManager := upstream.NewManager(logger, cfg, storageManager.GetBoltDB(), secretResolver, storageManager)
	upstreamManager := &UpstreamManagerAdapter{Manager: baseUpstreamManager}

	// Initialize cache manager
	baseCacheManager, err := cache.NewManager(storageManager.GetDB(), logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache manager: %w", err)
	}
	cacheManager := &CacheManagerAdapter{Manager: baseCacheManager}

	// Initialize OAuth token manager (using global instance)
	oauthTokenManager := &OAuthTokenManagerImpl{
		tokenStoreManager: oauth.GetTokenStoreManager(),
		storage:           storageManager.GetBoltDB(),
		logger:            logger,
	}

	// Initialize Docker isolation manager
	dockerIsolationManager := &DockerIsolationManagerImpl{
		isolationManager: core.NewIsolationManager(cfg.DockerIsolation),
		config:           cfg,
		logger:           logger,
	}

	// Initialize log manager
	logManager := &LogManagerImpl{
		logConfig: logConfig,
		logger:    logger,
	}

	// Set log configuration on upstream manager
	if logConfig != nil {
		upstreamManager.SetLogConfig(logConfig)
	}

	return &ApplicationContext{
		UpstreamManager:        upstreamManager,
		IndexManager:           indexManager,
		StorageManager:         storageManager,
		OAuthTokenManager:      oauthTokenManager,
		DockerIsolationManager: dockerIsolationManager,
		LogManager:             logManager,
		CacheManager:           cacheManager,
		Config:                 cfg,
		LogConfig:              logConfig,
		Logger:                 logger,
	}, nil
}

// Close gracefully shuts down all managers
func (ctx *ApplicationContext) Close() error {
	var lastError error

	// Close managers in reverse dependency order
	if ctx.UpstreamManager != nil {
		if err := ctx.UpstreamManager.DisconnectAll(); err != nil {
			ctx.Logger.Warn("Error disconnecting upstream servers", zap.Error(err))
			lastError = err
		}
	}

	if ctx.CacheManager != nil {
		if err := ctx.CacheManager.Close(); err != nil {
			ctx.Logger.Warn("Error closing cache manager", zap.Error(err))
			lastError = err
		}
	}

	if ctx.IndexManager != nil {
		if err := ctx.IndexManager.Close(); err != nil {
			ctx.Logger.Warn("Error closing index manager", zap.Error(err))
			lastError = err
		}
	}

	if ctx.StorageManager != nil {
		if err := ctx.StorageManager.Close(); err != nil {
			ctx.Logger.Warn("Error closing storage manager", zap.Error(err))
			lastError = err
		}
	}

	if ctx.LogManager != nil {
		if err := ctx.LogManager.Close(); err != nil {
			ctx.Logger.Warn("Error closing log manager", zap.Error(err))
			lastError = err
		}
	}

	return lastError
}

// GetUpstreamManager returns the upstream manager interface
func (ctx *ApplicationContext) GetUpstreamManager() UpstreamManager {
	return ctx.UpstreamManager
}

// GetIndexManager returns the index manager interface
func (ctx *ApplicationContext) GetIndexManager() IndexManager {
	return ctx.IndexManager
}

// GetStorageManager returns the storage manager interface
func (ctx *ApplicationContext) GetStorageManager() StorageManager {
	return ctx.StorageManager
}

// GetOAuthTokenManager returns the OAuth token manager interface
func (ctx *ApplicationContext) GetOAuthTokenManager() OAuthTokenManager {
	return ctx.OAuthTokenManager
}

// GetDockerIsolationManager returns the Docker isolation manager interface
func (ctx *ApplicationContext) GetDockerIsolationManager() DockerIsolationManager {
	return ctx.DockerIsolationManager
}

// GetLogManager returns the log manager interface
func (ctx *ApplicationContext) GetLogManager() LogManager {
	return ctx.LogManager
}

// GetCacheManager returns the cache manager interface
func (ctx *ApplicationContext) GetCacheManager() CacheManager {
	return ctx.CacheManager
}
