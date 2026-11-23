package runtime

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func TestWarnUpstreamFileRefsOutsideAllowList(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "allowed")
	dirs := config.SecretFileDirs{allowed}
	cfg := &config.Config{DataDir: filepath.Join(dir, "data"), SecretFileDirs: &dirs, Servers: []*config.ServerConfig{
		{Name: "b-outside", Env: map[string]string{"T": "${file:/elsewhere/tok}"}},
		{Name: "a-outside", Headers: map[string]string{"X": "${file:/other/tok}"}},
		{Name: "inside", Env: map[string]string{"T": "${file:" + filepath.Join(allowed, "tok") + "}"}},
	}}

	core, logs := observer.New(zapcore.WarnLevel)
	warnUpstreamFileRefsOutsideAllowList(zap.New(core), cfg)

	entries := logs.FilterMessage(config.MsgUpstreamFileRefsOutsideAllowList).All()
	require.Len(t, entries, 1, "one WARN line for all affected servers")
	assert.Equal(t, []any{"a-outside", "b-outside"}, entries[0].ContextMap()["servers"])
	for _, f := range entries[0].Context {
		assert.NotContains(t, f.String, "/elsewhere", "paths are not logged")
	}

	// Nothing outside: no warning.
	core2, logs2 := observer.New(zapcore.WarnLevel)
	cfg.Servers = cfg.Servers[2:]
	warnUpstreamFileRefsOutsideAllowList(zap.New(core2), cfg)
	assert.Zero(t, logs2.Len())
}
