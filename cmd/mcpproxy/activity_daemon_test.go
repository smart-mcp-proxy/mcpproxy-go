package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// resetActivityExportFlags snapshots and restores the package-level flag
// variables used by runActivityExport so tests stay hermetic.
func resetActivityExportFlags(t *testing.T) {
	t.Helper()
	savedConfigFile := configFile
	savedFormat := activityExportFormat
	savedOutput := activityExportOutput
	savedType := activityType
	savedServer := activityServer
	savedTool := activityTool
	savedStatus := activityStatus
	savedSession := activitySessionID
	savedStart := activityStartTime
	savedEnd := activityEndTime
	savedBodies := activityIncludeBodies
	t.Cleanup(func() {
		configFile = savedConfigFile
		activityExportFormat = savedFormat
		activityExportOutput = savedOutput
		activityType = savedType
		activityServer = savedServer
		activityTool = savedTool
		activityStatus = savedStatus
		activitySessionID = savedSession
		activityStartTime = savedStart
		activityEndTime = savedEnd
		activityIncludeBodies = savedBodies
	})
	activityType = ""
	activityServer = ""
	activityTool = ""
	activityStatus = ""
	activitySessionID = ""
	activityStartTime = ""
	activityEndTime = ""
	activityIncludeBodies = false
}

// TestActivityExportUsesSharedDaemonDetection guards QA finding CLI-SOCKET for
// `activity export`: when the unix socket is unavailable the command must go
// through the shared daemon detection (daemonEndpoint), which honors the
// MCPPROXY_API_KEY env override, instead of the old hand-rolled
// cfg.Listen+cfg.APIKey fallback that sent a stale file key.
func TestActivityExportUsesSharedDaemonDetection(t *testing.T) {
	clearDaemonEnv(t)
	t.Setenv("MCPPROXY_API_KEY", "env-key")

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "env-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"running":true}}`))
	})
	mux.HandleFunc("/api/v1/activity/export", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "env-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	dataDir := t.TempDir() // no socket file here → TCP fallback
	cfgJSON, err := json.Marshal(map[string]string{
		"listen":   strings.TrimPrefix(ts.URL, "http://"),
		"api_key":  "stale-file-key", // daemon was started with MCPPROXY_API_KEY instead
		"data_dir": dataDir,
	})
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	cfgPath := filepath.Join(t.TempDir(), "mcp_config.json")
	if err := os.WriteFile(cfgPath, cfgJSON, 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	resetActivityExportFlags(t)
	configFile = cfgPath
	activityExportFormat = "json"
	activityExportOutput = filepath.Join(t.TempDir(), "export.json")

	if err := runActivityExport(&cobra.Command{Use: "export"}, nil); err != nil {
		t.Fatalf("activity export should reach the daemon over TCP with the env API key, got: %v", err)
	}

	data, err := os.ReadFile(activityExportOutput)
	if err != nil {
		t.Fatalf("failed to read export output: %v", err)
	}
	if string(data) != `[]` {
		t.Fatalf("unexpected export payload: %q", string(data))
	}
}

// TestActivityWatchTargetTCPFallback guards QA finding CLI-SOCKET for
// `activity watch`: the SSE target must come from the shared daemon
// detection (socket first, then probed TCP fallback with env>config key).
func TestActivityWatchTargetTCPFallback(t *testing.T) {
	clearDaemonEnv(t)
	t.Setenv("MCPPROXY_API_KEY", "env-key")

	ts := newStatusServer(t, "env-key")
	defer ts.Close()

	cfg := &config.Config{
		DataDir: t.TempDir(), // no socket file here
		Listen:  strings.TrimPrefix(ts.URL, "http://"),
		APIKey:  "stale-file-key",
	}

	sseURL, apiKey, _, ok := activityWatchTarget(cfg, nil)
	if !ok {
		t.Fatal("expected watch target to resolve via TCP fallback when the socket is missing")
	}
	if sseURL != ts.URL+"/events" {
		t.Fatalf("expected SSE URL %s/events, got %s", ts.URL, sseURL)
	}
	if apiKey != "env-key" {
		t.Fatalf("expected env API key to win over the config file key, got %q", apiKey)
	}
	if strings.Contains(sseURL, "apikey=") {
		t.Fatalf("API key must be sent via X-API-Key header, not query param: %s", sseURL)
	}
}

// TestWatchActivityStreamSendsAPIKeyHeader verifies the SSE request carries
// X-API-Key so the TCP fallback authenticates against /events.
func TestWatchActivityStreamSendsAPIKeyHeader(t *testing.T) {
	gotKey := make(chan string, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey <- r.Header.Get("X-API-Key")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: activity.completed\ndata: {}\n\n"))
	}))
	defer ts.Close()

	err := watchActivityStream(context.Background(), ts.URL+"/events", "secret", "table", nil, ts.Client())
	if err != nil {
		t.Fatalf("watchActivityStream failed: %v", err)
	}
	select {
	case key := <-gotKey:
		if key != "secret" {
			t.Fatalf("expected X-API-Key 'secret', got %q", key)
		}
	default:
		t.Fatal("SSE handler was never called")
	}
}
