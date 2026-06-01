package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRegistryAddCLIE2E exercises the spec-070 CLI MVP end to end against a
// running daemon and a mock registry: list → search → add → assert the server
// shows up quarantined in `upstream list`. The mock registry is an in-process
// httptest server so the test is deterministic and needs no network.
func TestRegistryAddCLIE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Mock registry: a default-protocol server list with one stdio server that
	// declares no required inputs (so the add succeeds without --env).
	const serversJSON = `[
		{"id":"echo-mcp","name":"echo-mcp","description":"Echo server for testing","installCmd":"npx -y echo-mcp"}
	]`
	mockReg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(serversJSON))
	}))
	defer mockReg.Close()

	tmpDir := filepath.Join("/tmp", "mcpproxy-test-"+t.Name())
	require.NoError(t, os.MkdirAll(tmpDir, 0700))
	defer os.RemoveAll(tmpDir)

	// Build mcpproxy binary.
	mcpproxyBin := filepath.Join(tmpDir, binaryName("mcpproxy"))
	buildCmd := exec.Command("go", "build", "-o", mcpproxyBin, "./cmd/mcpproxy")
	buildCmd.Dir = filepath.Join("..", "..")
	out, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "Failed to build mcpproxy: %s", string(out))

	// Config with a custom (default-protocol) registry pointing at the mock.
	configPath := filepath.Join(tmpDir, "mcp_config.json")
	cfg := `{
		"listen": "127.0.0.1:18085",
		"data_dir": "` + tmpDir + `",
		"enable_socket": true,
		"check_server_repo": false,
		"registries": [
			{
				"id": "mocktest",
				"name": "Mock Test Registry",
				"description": "Local test registry",
				"url": "` + mockReg.URL + `",
				"protocol": "raw/list",
				"servers_url": "` + mockReg.URL + `/servers"
			}
		],
		"mcpServers": []
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(cfg), 0600))

	// Start daemon.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	daemonCmd := exec.CommandContext(ctx, mcpproxyBin, "serve", "--config", configPath)
	daemonCmd.Env = append(os.Environ(), "MCPPROXY_DATA_DIR="+tmpDir)
	require.NoError(t, daemonCmd.Start())
	defer func() { _ = daemonCmd.Process.Kill() }()

	require.NoError(t, waitForServerReady("127.0.0.1:18085", tmpDir, 20*time.Second), "Daemon failed to become ready")

	run := func(args ...string) (string, error) {
		full := append([]string{}, args...)
		full = append(full, "--config", configPath)
		c := exec.Command(mcpproxyBin, full...)
		c.Env = append(os.Environ(), "MCPPROXY_DATA_DIR="+tmpDir)
		o, e := c.CombinedOutput()
		return string(o), e
	}

	// 1) list → shows the custom registry.
	listOut, err := run("registry", "list")
	require.NoError(t, err, "registry list failed: %s", listOut)
	assert.Contains(t, listOut, "mocktest", "registry list should show the custom registry")

	// 2) search → finds the server.
	searchOut, err := run("registry", "search", "echo", "--registry", "mocktest")
	require.NoError(t, err, "registry search failed: %s", searchOut)
	assert.Contains(t, searchOut, "echo-mcp", "registry search should find the server")

	// 3) add → succeeds and reports quarantined.
	addOut, err := run("registry", "add", "mocktest", "echo-mcp")
	require.NoError(t, err, "registry add failed: %s", addOut)
	assert.Contains(t, strings.ToLower(addOut), "quarantin", "add should report the server is quarantined")

	// 4) upstream list → the added server is present.
	upstreamOut, err := run("upstream", "list")
	require.NoError(t, err, "upstream list failed: %s", upstreamOut)
	assert.Contains(t, upstreamOut, "echo-mcp", "added server should appear in upstream list")
}

// TestRegistryAddCLIMissingInputE2E verifies the actionable error path: a
// server that declares a required input is refused with missing_required_input
// and the CLI names the --env key to supply.
func TestRegistryAddCLIMissingInputE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Install command references ${GITHUB_TOKEN} → detected as a required input.
	const serversJSON = `[
		{"id":"gh-mcp","name":"gh-mcp","description":"GitHub server","installCmd":"npx gh-mcp --token ${GITHUB_TOKEN}"}
	]`
	mockReg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(serversJSON))
	}))
	defer mockReg.Close()

	tmpDir := filepath.Join("/tmp", "mcpproxy-test-"+t.Name())
	require.NoError(t, os.MkdirAll(tmpDir, 0700))
	defer os.RemoveAll(tmpDir)

	mcpproxyBin := filepath.Join(tmpDir, binaryName("mcpproxy"))
	buildCmd := exec.Command("go", "build", "-o", mcpproxyBin, "./cmd/mcpproxy")
	buildCmd.Dir = filepath.Join("..", "..")
	out, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "Failed to build mcpproxy: %s", string(out))

	configPath := filepath.Join(tmpDir, "mcp_config.json")
	cfg := `{
		"listen": "127.0.0.1:18086",
		"data_dir": "` + tmpDir + `",
		"enable_socket": true,
		"check_server_repo": false,
		"registries": [
			{"id":"mocktest","name":"Mock","description":"d","url":"` + mockReg.URL + `","protocol":"raw/list","servers_url":"` + mockReg.URL + `/servers"}
		],
		"mcpServers": []
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(cfg), 0600))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	daemonCmd := exec.CommandContext(ctx, mcpproxyBin, "serve", "--config", configPath)
	daemonCmd.Env = append(os.Environ(), "MCPPROXY_DATA_DIR="+tmpDir)
	require.NoError(t, daemonCmd.Start())
	defer func() { _ = daemonCmd.Process.Kill() }()

	require.NoError(t, waitForServerReady("127.0.0.1:18086", tmpDir, 20*time.Second), "Daemon failed to become ready")

	// add without the required input → refused, names GITHUB_TOKEN.
	c := exec.Command(mcpproxyBin, "registry", "add", "mocktest", "gh-mcp", "--config", configPath)
	c.Env = append(os.Environ(), "MCPPROXY_DATA_DIR="+tmpDir)
	addOut, err := c.CombinedOutput()
	require.Error(t, err, "add should fail when a required input is missing")
	assert.Contains(t, string(addOut), "GITHUB_TOKEN", "error should name the missing --env key")
}
