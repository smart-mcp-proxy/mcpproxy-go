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

// Review #1 (PR #1884): references that resolved before #1721 (any absolute
// path) and fall outside the allow-list now are named at startup.
func TestUpstreamFileRefsOutsideAllowList(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "allowed")
	outside := filepath.Join(dir, "outside")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	require.NoError(t, os.MkdirAll(outside, 0o700))
	inside := filepath.Join(allowed, "tok")
	require.NoError(t, os.WriteFile(inside, []byte("v"), 0o600))

	dirs := SecretFileDirs{allowed}
	cfg := &Config{DataDir: filepath.Join(dir, "data"), SecretFileDirs: &dirs, Servers: []*ServerConfig{
		{Name: "ok", Env: map[string]string{"T": "${file:" + inside + "}"}},
		{Name: "env-outside", Env: map[string]string{"T": "${file:" + filepath.Join(outside, "tok") + "}"}},
		{Name: "header-outside", URL: "http://127.0.0.1:9/mcp", Headers: map[string]string{"Authorization": "Bearer ${file:" + filepath.Join(outside, "h") + "}"}},
		{Name: "args-outside", Args: []string{"--token", "${file:" + filepath.Join(outside, "a") + "}"}},
		{Name: "no-file-refs", Env: map[string]string{"T": "${env:HOME}", "K": "${keyring:x}"}},
	}}

	assert.Equal(t, []string{"args-outside", "env-outside", "header-outside"}, UpstreamFileRefsOutsideAllowList(cfg))

	// Wildcard mode lets every path through: nothing to warn about.
	wild := SecretFileDirs{"*"}
	cfg.SecretFileDirs = &wild
	assert.Empty(t, UpstreamFileRefsOutsideAllowList(cfg))

	// [] disables ${file:}: every server with a file reference is named.
	none := SecretFileDirs{}
	cfg.SecretFileDirs = &none
	assert.Equal(t, []string{"args-outside", "env-outside", "header-outside", "ok"}, UpstreamFileRefsOutsideAllowList(cfg))
}

// Cross-review R1 (PR #1884): only the fields the upstream resolver expands
// count. A ${file:} inside an opaque block it never walks (auth_broker in the
// personal build) must not warn on every start.
func TestUpstreamFileRefsOutsideAllowList_OnlyResolvedFields(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "allowed")
	dirs := SecretFileDirs{allowed}

	var sc ServerConfig
	require.NoError(t, json.Unmarshal([]byte(`{"name":"broker","url":"http://127.0.0.1:9/mcp",
		"auth_broker":{"mode":"oauth_connect","client_secret":"${file:/outside/secret}"}}`), &sc))
	cfg := &Config{DataDir: filepath.Join(dir, "data"), SecretFileDirs: &dirs, Servers: []*ServerConfig{&sc}}

	want := []string(nil)
	if refs := secret.StructFileRefPaths(CopyServerConfig(&sc)); len(refs) > 0 {
		want = []string{"broker"} // server build: auth_broker is a typed struct the resolver walks
	}
	assert.Equal(t, want, UpstreamFileRefsOutsideAllowList(cfg))
}
