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
	mgr, _, _, err := openStorageForCLIWithKey(cfg, logger, false)
	return mgr, err
}

// openStorageForCLIWithKey is openStorageForCLI that also returns the
// resolved key and its source metadata, for commands that report on the key
// (`auth tokens`). With requireKey, no configured key is exit 4 before
// anything is opened, so a command that can only work with a key never
// creates or touches config.db without one.
func openStorageForCLIWithKey(cfg *config.Config, logger *zap.SugaredLogger, requireKey bool) (*storage.Manager, *tokenseal.Key, config.KeySource, error) {
	key, src, err := config.ResolveOAuthTokenKey(cfg)
	if err != nil {
		return nil, nil, config.KeySource{}, err
	}
	if key == nil && requireKey {
		return nil, nil, config.KeySource{}, config.NewStartupError(config.ExitCodeOAuthTokenKeyError,
			"no OAuth token encryption key is configured: set "+config.OAuthTokenKeyField+" in the configuration, or "+
				config.EnvOAuthTokenKey+" / "+config.EnvOAuthTokenKeyFile+" in the environment")
	}
	mgr, err := storage.NewManager(cfg.DataDir, logger, storage.WithOAuthTokenKey(key))
	if err != nil {
		return nil, nil, config.KeySource{}, fmt.Errorf("failed to create storage manager: %w", err)
	}
	return mgr, key, src, nil
}
