package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/stretchr/testify/require"
)

func TestCLIUpstreamSecretResolver_RefusesOutOfPolicyFileAndMCPProxyEnv(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "allowed")
	outside := filepath.Join(dir, "outside")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	require.NoError(t, os.MkdirAll(outside, 0o700))
	outsideFile := filepath.Join(outside, "secret")
	require.NoError(t, os.WriteFile(outsideFile, []byte("leak\n"), 0o600))

	w := config.SecretFileDirs{allowed}
	cfg := &config.Config{DataDir: dir, Listen: "127.0.0.1:0", SecretFileDirs: &w}
	path := filepath.Join(dir, config.ConfigFileName)
	require.NoError(t, config.SaveConfig(cfg, path))
	cfg, err := config.LoadFromFile(path)
	require.NoError(t, err)

	r := cliUpstreamSecretResolver(cfg)
	srv := config.CopyServerConfig(&config.ServerConfig{
		Name:    "s",
		URL:     "http://127.0.0.1:9/mcp",
		Headers: map[string]string{"Authorization": "Bearer ${file:" + outsideFile + "}"},
	})
	errs := r.ExpandStructSecretsCollectErrors(context.Background(), srv)
	require.NotEmpty(t, errs)

	srv2 := config.CopyServerConfig(&config.ServerConfig{
		Name: "s",
		URL:  "http://127.0.0.1:9/mcp",
		Env:  map[string]string{"K": "${env:MCPPROXY_API_KEY}"},
	})
	errs2 := r.ExpandStructSecretsCollectErrors(context.Background(), srv2)
	require.NotEmpty(t, errs2)
	require.ErrorIs(t, errs2[0].Err, secret.ErrReservedEnvVar)
}
