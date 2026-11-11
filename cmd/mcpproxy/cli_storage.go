package main

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
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

// clearOAuthTokenStandalone discards serverName's stored OAuth token for the
// standalone `auth logout` (no daemon). It is a delete on the bucket key
// alone, so it does NOT resolve the OAuth token key (Spec 123 review): the
// documented recovery for a lost key is this logout, and the usual way a key
// is lost is a key reference that no longer resolves (a missing secret mount
// or key file), which would otherwise refuse the logout with exit 4. Opening
// without a key is safe here because nothing is written to oauth_tokens: the
// row is deleted whatever its shape (plaintext, sealed under any key, or
// damaged). A running core holding the lock is still a *DatabaseLockedError.
func clearOAuthTokenStandalone(cfg *config.Config, serverName string, logger *zap.SugaredLogger) error {
	var srv *config.ServerConfig
	for _, s := range cfg.Servers {
		if s != nil && s.Name == serverName {
			srv = s
			break
		}
	}
	if srv == nil {
		return fmt.Errorf("server '%s' not found in configuration", serverName)
	}
	if cfg.DataDir == "" {
		return fmt.Errorf("storage not available - cannot clear OAuth token")
	}
	db, err := storage.NewBoltDB(cfg.DataDir, logger)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer db.Close()
	// Same key as PersistentTokenStore. Standalone mode only: this process
	// opened config.db itself, so no daemon instance shares it (Spec 116
	// PR2b, review R8.3).
	return db.ClearOAuthTokenForStandaloneLogout(oauth.GenerateServerKey(srv.Name, srv.URL))
}
