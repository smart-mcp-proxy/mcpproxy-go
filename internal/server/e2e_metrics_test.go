package server

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/testutil"
)

// TestBinaryMetricsEndpoint is the MCP-3135 integration regression: boot the
// real binary with observability.metrics.enabled=true and assert that GET
// /metrics (at the root, NOT under /api) responds 200 with a scrapeable body.
//
// Before the routing fix the /metrics handler was registered on the httpapi chi
// router but never forwarded by the outer mux, so this returned 404. The config
// here intentionally has zero upstream servers, so the test needs no Node/npx
// dependency — it exercises only the proxy's own HTTP listener wiring.
func TestBinaryMetricsEndpoint(t *testing.T) {
	env := testutil.NewBinaryTestEnv(t)
	defer env.Cleanup()

	// Overwrite the default config with a metrics-enabled, zero-upstream config
	// before the binary starts. The data dir was created next to the config by
	// NewBinaryTestEnv (tempDir/data).
	dataDir := filepath.Join(filepath.Dir(env.GetConfigPath()), "data")
	writeMetricsEnabledConfig(t, env.GetConfigPath(), env.GetPort(), dataDir)

	env.Start()

	resp, err := http.Get(env.GetBaseURL() + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode, "GET /metrics should be reachable when metrics enabled; body=%s", string(body))
	assert.Contains(t, string(body), "mcpproxy_uptime_seconds", "metrics body should expose the uptime gauge")
}

func writeMetricsEnabledConfig(t *testing.T, configPath string, port int, dataDir string) {
	t.Helper()
	content := `{
  "listen": ":` + strconv.Itoa(port) + `",
  "data_dir": "` + dataDir + `",
  "api_key": "` + testutil.TestAPIKey + `",
  "enable_tray": false,
  "mcpServers": [],
  "quarantine_enabled": false,
  "docker_isolation": { "enabled": false },
  "observability": {
    "metrics": { "enabled": true }
  }
}`
	require.NoError(t, os.WriteFile(configPath, []byte(content), 0600))
}
