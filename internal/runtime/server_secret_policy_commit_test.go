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

func TestCommit_CreateServer_RefusesSecretPolicyBeforeSave(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "secrets")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	w := config.SecretFileDirs{allowed}
	rt, _ := newSpec116Runtime(t, func(c *config.Config) {
		c.DataDir = dir
		c.SecretFileDirs = &w
	})
	sc := &config.ServerConfig{
		Name: "bad",
		URL:  "http://127.0.0.1:9/mcp",
		Env:  map[string]string{"K": "${env:MCPPROXY_API_KEY}"},
	}
	_, err := rt.Commit("create_server", func(tx *CommitTx) error { return tx.CreateServer(sc) })
	var verr *config.InvalidServerUpdateError
	require.ErrorAs(t, err, &verr)
	require.NotEmpty(t, verr.Errors)
	assert.Equal(t, secret.ErrReservedEnvVar.Error(), verr.Errors[0].Message)
}

func TestCommit_UpdateServer_RefusesOutOfPolicyFileBeforeSave(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "allowed")
	outside := filepath.Join(dir, "outside")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	require.NoError(t, os.MkdirAll(outside, 0o700))
	outFile := filepath.Join(outside, "k")
	require.NoError(t, os.WriteFile(outFile, []byte("x\n"), 0o600))
	w := config.SecretFileDirs{allowed}
	rt, _ := newSpec116Runtime(t, func(c *config.Config) {
		c.DataDir = dir
		c.SecretFileDirs = &w
	}, &config.ServerConfig{Name: "s", URL: "http://127.0.0.1:9/mcp", Enabled: false})
	_, err := rt.Commit("update_server", func(tx *CommitTx) error {
		return tx.UpdateServer("s", func(cur *config.ServerConfig) error {
			cur.Headers = map[string]string{"Authorization": "Bearer ${file:" + outFile + "}"}
			return nil
		})
	})
	var verr *config.InvalidServerUpdateError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, secret.ErrSecretFileDenied.Error(), verr.Errors[0].Message)
}
