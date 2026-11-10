package management

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/core"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func upstreamPolicyDoctorInputs(t *testing.T) (lastError, public string, leaks []string) {
	t.Helper()
	dir := t.TempDir()
	allowed := filepath.Join(dir, "allowed")
	outside := filepath.Join(dir, "outside", "deep", "token.pem")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Dir(outside), 0o700))
	require.NoError(t, os.WriteFile(outside, []byte("x\n"), 0o600))
	w := config.SecretFileDirs{allowed}
	cfg := &config.Config{DataDir: dir, SecretFileDirs: &w}
	r := config.BuildSecretResolver(cfg, "", secret.ScopeUpstream)
	srv := &config.ServerConfig{
		Name:    "denied",
		Command: "true",
		Env:     map[string]string{"TOKEN": "${file:" + outside + "}"},
	}
	_, err := core.NewClientWithOptions("denied", srv, zap.NewNop(), nil, cfg, nil, false, r)
	require.Error(t, err)
	var pe *core.UpstreamSecretPolicyError
	require.ErrorAs(t, err, &pe)
	return err.Error(), pe.Public, []string{outside, "${file:", "TOKEN"}
}
