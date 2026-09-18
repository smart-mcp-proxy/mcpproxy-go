package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// The first-run anonymous_id generation persists the WHOLE config the daemon
// handed to telemetry.New — the effective one, carrying every `serve` flag and
// MCPPROXY_* env override. Those apply to this process only and must never
// reach the file, or the next unflagged start inherits them.
func TestEnsureAnonymousID_DoesNotPersistProcessOverrides(t *testing.T) {
	t.Cleanup(config.ResetProcessOverrides)
	config.ResetProcessOverrides()

	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "mcp_config.json")
	raw := `{"listen": "127.0.0.1:8080", "data_dir": ` + jsonQuote(tmp) + `, "read_only_mode": false, "mcpServers": []}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(raw), 0o600))

	cfg, err := config.DecodeConfigFile(cfgPath)
	require.NoError(t, err)
	config.OverrideForProcess(cfg, config.FieldListen, config.OverrideSourceFlag, ":0")
	config.OverrideForProcess(cfg, config.FieldReadOnlyMode, config.OverrideSourceFlag, true)
	config.OverrideForProcess(cfg, config.FieldAPIKey, config.OverrideSourceEnv, "env-secret")

	svc := New(cfg, cfgPath, "v1.0.0", "personal", zap.NewNop())
	svc.ensureAnonymousID()
	require.NotEmpty(t, cfg.GetAnonymousID())

	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))

	tel, _ := m["telemetry"].(map[string]any)
	require.NotNil(t, tel, "the generated id must be persisted")
	assert.Equal(t, cfg.GetAnonymousID(), tel["anonymous_id"])

	assert.Equal(t, "127.0.0.1:8080", m["listen"], "--listen must not be persisted")
	assert.NotEqual(t, true, m["read_only_mode"], "--read-only must not be persisted")
	assert.NotEqual(t, "env-secret", m["api_key"], "MCPPROXY_API_KEY must not be persisted")
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
