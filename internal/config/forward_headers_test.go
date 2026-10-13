package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsClientHeaderForwardingEnabled(t *testing.T) {
	f, tr := false, true

	t.Run("nil config and nil field default to enabled", func(t *testing.T) {
		var c *Config
		assert.True(t, c.IsClientHeaderForwardingEnabled())
		assert.True(t, (&Config{}).IsClientHeaderForwardingEnabled())
	})
	t.Run("explicit values", func(t *testing.T) {
		assert.True(t, (&Config{ForwardClientHeaders: &tr}).IsClientHeaderForwardingEnabled())
		assert.False(t, (&Config{ForwardClientHeaders: &f}).IsClientHeaderForwardingEnabled())
	})
	t.Run("env kill switch outranks an explicit true", func(t *testing.T) {
		for _, v := range []string{"false", "0", "off", "OFF", " False "} {
			t.Setenv(EnvForwardClientHeaders, v)
			assert.False(t, (&Config{ForwardClientHeaders: &tr}).IsClientHeaderForwardingEnabled(), v)
			assert.False(t, (&Config{}).IsClientHeaderForwardingEnabled(), v)
		}
	})
	t.Run("env does not force-enable and ignores unknown values", func(t *testing.T) {
		t.Setenv(EnvForwardClientHeaders, "true")
		assert.False(t, (&Config{ForwardClientHeaders: &f}).IsClientHeaderForwardingEnabled())
		t.Setenv(EnvForwardClientHeaders, "maybe")
		assert.True(t, (&Config{}).IsClientHeaderForwardingEnabled())
	})
}

func writeFwdConfig(t *testing.T, extra map[string]any) string {
	t.Helper()
	tmp := t.TempDir()
	m := map[string]any{"listen": "127.0.0.1:0", "data_dir": tmp}
	for k, v := range extra {
		m[k] = v
	}
	raw, err := json.Marshal(m)
	require.NoError(t, err)
	path := filepath.Join(tmp, "mcp_config.json")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	return path
}

func TestForwardClientHeaders_EnvOverrideIsProcessOnly(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeFwdConfig(t, nil)

	t.Setenv(EnvForwardClientHeaders, "off")
	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.ForwardClientHeaders)
	assert.False(t, *cfg.ForwardClientHeaders, "the override is the effective value")
	assert.Contains(t, ProcessOverrideFields(), "forward_client_headers")

	persisted := PersistableConfig(cfg, path)
	assert.Nil(t, persisted.ForwardClientHeaders, "the env value must never reach disk")
}

func TestForwardClientHeaders_EnvUnsetLeavesFileValue(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeFwdConfig(t, map[string]any{"forward_client_headers": false})

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.ForwardClientHeaders)
	assert.False(t, *cfg.ForwardClientHeaders)
	assert.NotContains(t, ProcessOverrideFields(), "forward_client_headers")
}

func TestValidateDetailed_ForwardHeaders(t *testing.T) {
	mk := func(names []string, static map[string]string) *Config {
		return &Config{Servers: []*ServerConfig{{
			Name: "up", URL: "https://example.com/mcp", Protocol: "streamable-http", Enabled: true,
			ForwardHeaders: names, Headers: static,
		}}}
	}
	hasField := func(errs []ValidationError) bool {
		for _, e := range errs {
			if e.Field == "mcpServers[0].forward_headers" {
				return true
			}
		}
		return false
	}

	assert.False(t, hasField(mk([]string{"X-User-Id", "X-Tenant-Id"}, nil).ValidateDetailed()))
	for name, cfg := range map[string]*Config{
		"denied":    mk([]string{"Authorization"}, nil),
		"wildcard":  mk([]string{"X-*"}, nil),
		"invalid":   mk([]string{"bad name"}, nil),
		"duplicate": mk([]string{"X-A", "x-a"}, nil),
		"static":    mk([]string{"X-Api-Thing"}, map[string]string{"x-api-thing": "secret-value"}),
	} {
		errs := cfg.ValidateDetailed()
		assert.True(t, hasField(errs), name)
		for _, e := range errs {
			assert.NotContains(t, e.Message, "secret-value", "messages name headers only")
		}
	}
	tooMany := make([]string, 33)
	for i := range tooMany {
		tooMany[i] = "X-H" + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	assert.True(t, hasField(mk(tooMany, nil).ValidateDetailed()))
}

func TestLoad_BadForwardHeadersNeverBricksBoot(t *testing.T) {
	path := writeFwdConfig(t, map[string]any{"mcpServers": []map[string]any{{
		"name": "up", "url": "https://example.com/mcp", "protocol": "streamable-http", "enabled": true,
		"headers":         map[string]string{"X-Static": "v"},
		"forward_headers": []string{"x-user-id", "Authorization", "X-*", "bad name", "X-USER-ID", "x-static", "X-Tenant"},
	}}})

	cfg, err := LoadFromFile(path)
	require.NoError(t, err, "a bad on-disk allowlist must not fail the load")
	require.Len(t, cfg.Servers, 1)
	assert.Equal(t, []string{"X-User-Id", "X-Tenant"}, cfg.Servers[0].ForwardHeaders)
}

func TestNormalizeForwardHeaders(t *testing.T) {
	assert.Nil(t, NormalizeForwardHeaders(nil))
	cfg := &Config{Servers: []*ServerConfig{
		nil,
		{Name: "clean", ForwardHeaders: []string{"X-A"}},
		{Name: "dirty", ForwardHeaders: []string{"cookie", "X-B"}},
	}}
	got := NormalizeForwardHeaders(cfg)
	require.Len(t, got, 1)
	assert.Equal(t, "dirty", got[0].Server)
	assert.Equal(t, []string{"cookie"}, got[0].Dropped)
	assert.Equal(t, []string{"X-B"}, cfg.Servers[2].ForwardHeaders)
	assert.Empty(t, NormalizeForwardHeaders(cfg), "idempotent")
}

func TestCopyServerConfig_ForwardHeaders(t *testing.T) {
	src := &ServerConfig{Name: "s", ForwardHeaders: []string{"X-A"}}
	dst := CopyServerConfig(src)
	assert.Equal(t, []string{"X-A"}, dst.ForwardHeaders)
	src.ForwardHeaders[0] = "X-Z"
	assert.Equal(t, []string{"X-A"}, dst.ForwardHeaders, "slice must be copied, not aliased")
	assert.Nil(t, CopyServerConfig(&ServerConfig{Name: "n"}).ForwardHeaders)
}

func TestMergeServerConfig_ForwardHeaders(t *testing.T) {
	base := &ServerConfig{Name: "s", Enabled: true, ForwardHeaders: []string{"X-A", "X-B"}}

	t.Run("nil keeps", func(t *testing.T) {
		merged, diff, err := MergeServerConfig(base, &ServerConfig{Name: "s", Enabled: true}, DefaultMergeOptions())
		require.NoError(t, err)
		assert.Equal(t, []string{"X-A", "X-B"}, merged.ForwardHeaders)
		assert.NotContains(t, diff.Modified, "forward_headers")
	})
	t.Run("non-nil replaces and records a diff", func(t *testing.T) {
		merged, diff, err := MergeServerConfig(base, &ServerConfig{Name: "s", Enabled: true, ForwardHeaders: []string{"X-C"}}, DefaultMergeOptions())
		require.NoError(t, err)
		assert.Equal(t, []string{"X-C"}, merged.ForwardHeaders)
		assert.Contains(t, diff.Modified, "forward_headers")
	})
	t.Run("empty slice clears", func(t *testing.T) {
		merged, diff, err := MergeServerConfig(base, &ServerConfig{Name: "s", Enabled: true, ForwardHeaders: []string{}}, DefaultMergeOptions())
		require.NoError(t, err)
		assert.Empty(t, merged.ForwardHeaders)
		assert.NotNil(t, merged.ForwardHeaders, "cleared, not unset")
		assert.Contains(t, diff.Modified, "forward_headers")
	})
	t.Run("merge does not alias the patch", func(t *testing.T) {
		p := &ServerConfig{Name: "s", Enabled: true, ForwardHeaders: []string{"X-C"}}
		merged, _, err := MergeServerConfig(base, p, DefaultMergeOptions())
		require.NoError(t, err)
		p.ForwardHeaders[0] = "X-Z"
		assert.Equal(t, []string{"X-C"}, merged.ForwardHeaders)
	})
}
