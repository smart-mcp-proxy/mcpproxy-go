package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyConfig_SecretFileDirsRebuildsResolver(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "allowed")
	outside := filepath.Join(dir, "outside")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	require.NoError(t, os.MkdirAll(outside, 0o700))
	key := filepath.Join(outside, "token")
	require.NoError(t, os.WriteFile(key, []byte("x\n"), 0o600))

	narrow := config.SecretFileDirs{allowed}
	rt, cfgPath := newSpec116Runtime(t, func(c *config.Config) {
		c.DataDir = dir
		c.SecretFileDirs = &narrow
	}, &config.ServerConfig{Name: "s", URL: "http://127.0.0.1:9/mcp", Enabled: false})

	ref := "Bearer ${file:" + key + "}"
	_, err := rt.Commit("update_server", func(tx *CommitTx) error {
		return tx.UpdateServer("s", func(cur *config.ServerConfig) error {
			cur.Headers = map[string]string{"Authorization": ref}
			return nil
		})
	})
	var verr *config.InvalidServerUpdateError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, secret.ErrSecretFileDenied.Error(), verr.Errors[0].Message)

	wide := config.SecretFileDirs{allowed, outside}
	next := cloneRuntimeConfig(t, rt.Config())
	next.SecretFileDirs = &wide
	_, err = rt.ApplyConfig(next, cfgPath)
	require.NoError(t, err)

	_, err = rt.Commit("update_server", func(tx *CommitTx) error {
		return tx.UpdateServer("s", func(cur *config.ServerConfig) error {
			cur.Headers = map[string]string{"Authorization": ref}
			return nil
		})
	})
	require.NoError(t, err)
	got := liveServerConfig(rt, "s")
	require.NotNil(t, got)
	assert.Equal(t, ref, got.Headers["Authorization"])
}

func liveServerConfig(rt *Runtime, name string) *config.ServerConfig {
	for _, sc := range rt.Config().Servers {
		if sc != nil && sc.Name == name {
			return sc
		}
	}
	return nil
}
