package cli

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 123: a config.db the login client cannot open (a running core holds
// it) is an error with its type kept, never an in-memory fallback: in-memory
// storage cannot see a stored row that must not be replaced.
func TestNewClient_LockedDatabaseIsAnErrorNotAnInMemoryFallback(t *testing.T) {
	t.Setenv(config.EnvOAuthTokenKey, "")
	t.Setenv(config.EnvOAuthTokenKeyFile, "")
	dataDir := t.TempDir()
	holder, err := storage.NewBoltDB(dataDir, zap.NewNop().Sugar(), storage.WithOAuthTokenKey(nil))
	require.NoError(t, err)
	t.Cleanup(func() { _ = holder.Close() })

	cfg := config.DefaultConfig()
	cfg.DataDir = dataDir
	cfg.Servers = []*config.ServerConfig{{Name: "alpha", URL: "https://alpha.example.invalid/mcp", Protocol: "http", Enabled: true}}

	client, err := NewClient("alpha", cfg, "error")
	if client != nil {
		_ = client.Close()
	}
	require.Error(t, err)
	var lockErr *storage.DatabaseLockedError
	require.True(t, errors.As(err, &lockErr), "got %T: %v", err, err)
}
