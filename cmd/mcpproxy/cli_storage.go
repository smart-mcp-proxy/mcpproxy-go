package main

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
)

// resolveCLIOAuthTokenKey resolves the OAuth token key (Spec 123) the way the
// daemon does, for a CLI command about to open config.db itself. The error is
// a *config.StartupError (exit 4) and must be returned before anything is
// opened: opening without the key would write plaintext rows the daemon then
// has to seal again, and opening with encryption silently off is never right.
func resolveCLIOAuthTokenKey(cfg *config.Config) (*tokenseal.Key, error) {
	key, _, err := config.ResolveOAuthTokenKey(cfg)
	return key, err
}

// openStorageForCLI opens config.db for a standalone CLI command with the
// resolved OAuth token key. A key error is returned unwrapped (exit 4); a
// storage error keeps its type (a running core holding the lock is exit 3).
func openStorageForCLI(cfg *config.Config, logger *zap.SugaredLogger) (*storage.Manager, error) {
	key, err := resolveCLIOAuthTokenKey(cfg)
	if err != nil {
		return nil, err
	}
	mgr, err := storage.NewManager(cfg.DataDir, logger, storage.WithOAuthTokenKey(key))
	if err != nil {
		return nil, fmt.Errorf("failed to create storage manager: %w", err)
	}
	return mgr, nil
}
