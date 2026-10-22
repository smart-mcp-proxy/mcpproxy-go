package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/configimport"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func importScratch(t *testing.T, source string) (string, string) {
	t.Helper()
	home := sandboxHome(t)
	configPath := filepath.Join(home, "config.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{"listen":"127.0.0.1:19998","mcpServers":[]}`), 0600))
	sourcePath := filepath.Join(home, "source.json")
	require.NoError(t, os.WriteFile(sourcePath, []byte(source), 0600))
	oldConfig, oldLocal := configFile, upstreamConfigPath
	oldDry, oldFormat, oldServer, oldNoQuarantine := upstreamImportDryRun, upstreamImportFormat, upstreamImportServer, upstreamImportNoQuarantine
	t.Cleanup(func() {
		configFile, upstreamConfigPath = oldConfig, oldLocal
		upstreamImportDryRun, upstreamImportFormat, upstreamImportServer, upstreamImportNoQuarantine = oldDry, oldFormat, oldServer, oldNoQuarantine
	})
	configFile, upstreamConfigPath = configPath, ""
	upstreamImportDryRun, upstreamImportFormat, upstreamImportServer, upstreamImportNoQuarantine = false, "", "", false
	return configPath, sourcePath
}

func captureImport(t *testing.T, sourcePath string) ([]byte, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	defer func() { os.Stdout = old; _ = r.Close() }()
	runErr := runUpstreamImport(nil, []string{sourcePath})
	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return out, runErr
}

func TestUpstreamImportJSONAppliesBeforeReportingSuccess(t *testing.T) {
	setOutputGlobals(t, "json", false)
	configPath, sourcePath := importScratch(t, `{"mcpServers":{"new-one":{"command":"echo","args":["hello"]}}}`)
	out, err := captureImport(t, sourcePath)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(out, &payload))
	cfg, err := config.LoadFromFile(configPath)
	require.NoError(t, err)
	require.Len(t, cfg.Servers, 1, "JSON success must persist its imported server")
	require.Equal(t, "new-one", cfg.Servers[0].Name)
	require.Equal(t, map[string]any{"total": float64(1), "imported": float64(1), "planned": float64(1), "applied": float64(1), "skipped": float64(0), "failed": float64(0)}, payload["summary"])
	require.Equal(t, false, payload["dry_run"])
}

func TestUpstreamImportTableAndYAMLApply(t *testing.T) {
	for _, format := range []string{"table", "yaml"} {
		t.Run(format, func(t *testing.T) {
			setOutputGlobals(t, format, false)
			configPath, sourcePath := importScratch(t, `{"mcpServers":{"new-one":{"command":"echo"}}}`)
			out, err := captureImport(t, sourcePath)
			require.NoError(t, err)
			cfg, err := config.LoadFromFile(configPath)
			require.NoError(t, err)
			require.Len(t, cfg.Servers, 1)
			if format == "yaml" {
				var payload struct {
					DryRun  bool                                   `yaml:"dry_run"`
					Summary struct{ Planned, Applied, Failed int } `yaml:"summary"`
				}
				require.NoError(t, yaml.Unmarshal(out, &payload))
				require.False(t, payload.DryRun)
				require.Equal(t, 1, payload.Summary.Planned)
				require.Equal(t, 1, payload.Summary.Applied)
			} else {
				require.Contains(t, string(out), "Applied:            1")
			}
		})
	}
}

func TestUpstreamImportDryRunAndDuplicateCounts(t *testing.T) {
	setOutputGlobals(t, "json", false)
	configPath, sourcePath := importScratch(t, `{"mcpServers":{"new-one":{"command":"echo"}}}`)
	upstreamImportDryRun = true
	out, err := captureImport(t, sourcePath)
	require.NoError(t, err)
	var payload struct {
		DryRun  bool                                              `json:"dry_run"`
		Summary struct{ Planned, Applied, Imported, Skipped int } `json:"summary"`
	}
	require.NoError(t, json.Unmarshal(out, &payload))
	require.True(t, payload.DryRun)
	require.Equal(t, 1, payload.Summary.Planned)
	require.Zero(t, payload.Summary.Applied)
	// The legacy imported count continues to mean preview candidates in dry-run.
	require.Equal(t, 1, payload.Summary.Imported)
	cfg, err := config.LoadFromFile(configPath)
	require.NoError(t, err)
	require.Empty(t, cfg.Servers)

	upstreamImportDryRun = false
	_, err = captureImport(t, sourcePath)
	require.NoError(t, err)
	out, err = captureImport(t, sourcePath)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(out, &payload))
	require.Zero(t, payload.Summary.Planned)
	require.Zero(t, payload.Summary.Applied)
	require.Equal(t, 1, payload.Summary.Skipped)
	cfg, err = config.LoadFromFile(configPath)
	require.NoError(t, err)
	require.Len(t, cfg.Servers, 1)
}

func TestUpstreamImportDaemonPartialFailureReportsAppliedAndFailed(t *testing.T) {
	setOutputGlobals(t, "json", false)
	_, sourcePath := importScratch(t, `{"mcpServers":{"good":{"command":"echo"},"bad":{"command":"echo"}}}`)
	var added []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"status":"running"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/servers":
			var req struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			added = append(added, req.Name)
			if req.Name == "bad" {
				w.WriteHeader(http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"success":true,"data":{"name":"good"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("MCPPROXY_TRAY_ENDPOINT", server.URL)
	t.Setenv("MCPPROXY_API_KEY", "scratch-key")
	upstreamImportDryRun = true
	preview, err := captureImport(t, sourcePath)
	require.NoError(t, err)
	require.Empty(t, added, "dry-run may probe the daemon but must not add servers")
	var previewPayload struct {
		DryRun  bool                           `json:"dry_run"`
		Summary struct{ Planned, Applied int } `json:"summary"`
	}
	require.NoError(t, json.Unmarshal(preview, &previewPayload))
	require.True(t, previewPayload.DryRun)
	require.Equal(t, 2, previewPayload.Summary.Planned)
	require.Zero(t, previewPayload.Summary.Applied)
	upstreamImportDryRun = false
	out, err := captureImport(t, sourcePath)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to import 1 of 2")
	var payload struct {
		Summary  struct{ Planned, Applied, Imported, Failed int } `json:"summary"`
		Imported []struct {
			Name string `json:"name"`
		} `json:"imported"`
		Failed []struct{ Name, Error string } `json:"failed"`
	}
	require.NoError(t, json.Unmarshal(out, &payload))
	require.Equal(t, 2, payload.Summary.Planned)
	require.Equal(t, 1, payload.Summary.Applied)
	require.Equal(t, 1, payload.Summary.Imported)
	require.Equal(t, 1, payload.Summary.Failed)
	require.Equal(t, "good", payload.Imported[0].Name)
	require.Equal(t, "bad", payload.Failed[0].Name)
	require.ElementsMatch(t, []string{"good", "bad"}, added)
}

func TestUpstreamImportParseFailureRetainsSuccessfulWrites(t *testing.T) {
	setOutputGlobals(t, "json", false)
	configPath, sourcePath := importScratch(t, `{"mcpServers":{"broken":{"url":"https://example.invalid/mcp"},"good":{"command":"echo"}}}`)
	upstreamImportFormat = "claude-desktop"
	out, err := captureImport(t, sourcePath)
	require.Error(t, err)
	var payload struct {
		Summary struct{ Planned, Applied, Failed int } `json:"summary"`
		Failed  []struct{ Name, Error string }         `json:"failed"`
	}
	require.NoError(t, json.Unmarshal(out, &payload))
	require.Equal(t, 1, payload.Summary.Planned)
	require.Equal(t, 1, payload.Summary.Applied)
	require.Equal(t, 1, payload.Summary.Failed)
	require.Equal(t, "broken", payload.Failed[0].Name)
	cfg, loadErr := config.LoadFromFile(configPath)
	require.NoError(t, loadErr)
	require.Len(t, cfg.Servers, 1)
	require.Equal(t, "good", cfg.Servers[0].Name)
}

func TestApplyImportedServersConfigModeFailureReportsNoWrites(t *testing.T) {
	home := sandboxHome(t)
	parent := filepath.Join(home, "parent-file")
	require.NoError(t, os.WriteFile(parent, []byte("sentinel"), 0600))
	oldConfig, oldLocal := configFile, upstreamConfigPath
	t.Cleanup(func() { configFile, upstreamConfigPath = oldConfig, oldLocal })
	configFile, upstreamConfigPath = filepath.Join(parent, "config.json"), ""
	items := []*configimport.ImportedServer{{Server: &config.ServerConfig{Name: "first", Command: "echo"}}, {Server: &config.ServerConfig{Name: "second", Command: "echo"}}}
	cfg := &config.Config{DataDir: home}
	applied, failed, err := applyImportedServers(items, cfg)
	require.Error(t, err)
	require.Empty(t, applied)
	require.Len(t, failed, 2)
	require.Equal(t, "first", failed[0].Name)
	require.Equal(t, "second", failed[1].Name)
	contents, readErr := os.ReadFile(parent)
	require.NoError(t, readErr)
	require.Equal(t, "sentinel", string(contents))
}

func TestApplyImportedServersDaemonModeContinuesAfterFailure(t *testing.T) {
	var added []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		added = append(added, req.Name)
		if req.Name == "bad" {
			w.WriteHeader(http.StatusConflict)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
	}))
	defer server.Close()
	client := cliclient.NewClientWithAPIKey(server.URL, "scratch-key", nil)
	items := []*configimport.ImportedServer{{Server: &config.ServerConfig{Name: "bad"}}, {Server: &config.ServerConfig{Name: "good"}}}
	applied, failed, err := applyImportedServersDaemonMode(context.Background(), client, items)
	require.Error(t, err)
	require.Equal(t, []string{"bad", "good"}, added)
	require.Len(t, applied, 1)
	require.Equal(t, "good", applied[0].Server.Name)
	require.Len(t, failed, 1)
	require.Equal(t, "bad", failed[0].Name)
	require.True(t, strings.Contains(failed[0].Details, "already exists"))
}
