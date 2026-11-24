package management

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// #2031: ${file:} references outside secret_file_dirs are a doctor finding
// (and so a GET /api/v1/diagnostics runtime warning), not only a startup WARN.
func TestDoctor_FileRefsOutsideAllowList(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "ok")
	dirs := config.SecretFileDirs{allowed}
	cfg := &config.Config{
		DataDir:        filepath.Join(root, "data"),
		SecretFileDirs: &dirs,
		Servers: []*config.ServerConfig{
			{Name: "alpha", Env: map[string]string{"K": "${file:" + filepath.Join(root, "elsewhere", "k") + "}"}},
			{Name: "delta", Env: map[string]string{"K": "${file:" + filepath.Join(root, "other", "k") + "}"}},
			{Name: "fine", Env: map[string]string{"K": "${file:" + filepath.Join(allowed, "k") + "}"}},
		},
	}
	rt := newMockRuntime()
	svc := NewService(rt, cfg, "", &mockEventEmitter{}, nil, nopSugar()).(*service)

	diag, err := svc.Doctor(auth.WithAuthContext(context.Background(), auth.AdminContext()))
	require.NoError(t, err)
	require.Len(t, diag.RuntimeWarnings, 1)
	w := diag.RuntimeWarnings[0]
	assert.Contains(t, w, "alpha")
	assert.Contains(t, w, "delta")
	assert.NotContains(t, w, "fine")
	assert.NotContains(t, w, root, "paths are never shown")
	assert.Equal(t, 1, diag.TotalIssues)

	scoped := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AllowedServers: []string{"delta"},
	})
	diag, err = svc.Doctor(scoped)
	require.NoError(t, err)
	require.Len(t, diag.RuntimeWarnings, 1)
	assert.Contains(t, diag.RuntimeWarnings[0], "delta")
	assert.NotContains(t, diag.RuntimeWarnings[0], "alpha")
}

func TestDoctor_NoFileRefWarningInWildcardMode(t *testing.T) {
	dirs := config.SecretFileDirs{"*"}
	cfg := &config.Config{SecretFileDirs: &dirs, Servers: []*config.ServerConfig{
		{Name: "alpha", Env: map[string]string{"K": "${file:/etc/k}"}},
	}}
	svc := NewService(newMockRuntime(), cfg, "", &mockEventEmitter{}, nil, nopSugar()).(*service)
	diag, err := svc.Doctor(auth.WithAuthContext(context.Background(), auth.AdminContext()))
	require.NoError(t, err)
	assert.Empty(t, diag.RuntimeWarnings)
}
