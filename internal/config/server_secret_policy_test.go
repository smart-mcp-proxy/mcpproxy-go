package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerSecretPolicyErrors_RefusesReservedEnv(t *testing.T) {
	dir := t.TempDir()
	allowed := SecretFileDirs{filepath.Join(dir, "secrets")}
	require.NoError(t, os.MkdirAll(string(allowed[0]), 0o700))
	cfg := &Config{DataDir: dir, Listen: "127.0.0.1:0", SecretFileDirs: &allowed}
	sc := &ServerConfig{
		Name: "s",
		URL:  "http://127.0.0.1:9/mcp",
		Env:  map[string]string{"K": "${env:MCPPROXY_API_KEY}"},
	}
	errs := ServerSecretPolicyErrors(cfg, filepath.Join(dir, ConfigFileName), sc)
	require.NotEmpty(t, errs)
	assert.Equal(t, secret.ErrReservedEnvVar.Error(), errs[0].Message)
	assert.NotContains(t, errs[0].Message, "/")
}

func TestLoad_APIKeyFileRefOutsideAllowListFailsLoad(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	dir := t.TempDir()
	allowed := filepath.Join(dir, "allowed")
	outside := filepath.Join(dir, "outside")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	require.NoError(t, os.MkdirAll(outside, 0o700))
	key := filepath.Join(outside, "key")
	require.NoError(t, os.WriteFile(key, []byte("secret\n"), 0o600))
	raw, err := jsonMarshalConfig(t, map[string]any{
		"listen":           "127.0.0.1:8080",
		"data_dir":         dir,
		"secret_file_dirs": []string{allowed},
		"api_key":          "${file:" + key + "}",
	})
	require.NoError(t, err)
	path := filepath.Join(dir, ConfigFileName)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	unsetEnv(t, EnvAPIKey)
	t.Setenv(EnvAPIKeyFile, "")
	_, err = LoadFromFile(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "api_key")
}

func jsonMarshalConfig(t *testing.T, m map[string]any) ([]byte, error) {
	t.Helper()
	return json.Marshal(m)
}
