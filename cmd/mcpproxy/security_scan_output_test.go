package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

func TestPrintScanSummaryOutputFormats(t *testing.T) {
	for _, format := range []string{"json", "yaml", "table"} {
		t.Run(format, func(t *testing.T) {
			setOutputGlobals(t, format, false)
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "/scan/report") {
					fmt.Fprint(w, `{"success":true,"data":{"job_id":"persisted-job","risk_score":6,"verdict":"warnings"}}`)
					return
				}
				fmt.Fprint(w, `{"success":true,"data":{"scanner_statuses":[]}}`)
			}))
			defer ts.Close()
			client := cliclient.NewClient(ts.URL, zap.NewNop().Sugar())
			var err error
			out := captureStdout(t, func() { err = printScanSummary(client, context.Background(), "qa") })
			if err != nil {
				t.Fatal(err)
			}
			if format == "table" {
				if !strings.Contains(out, "Scan completed for") || !strings.Contains(out, "Security Report: qa") {
					t.Fatalf("missing human-readable summary: %s", out)
				}
				return
			}
			var report map[string]interface{}
			if format == "json" {
				err = json.Unmarshal([]byte(out), &report)
			} else {
				err = yaml.Unmarshal([]byte(out), &report)
			}
			if err != nil || report["job_id"] != "persisted-job" || report["verdict"] != "warnings" {
				t.Fatalf("stdout must contain only the structured report: %q (parse: %v)", out, err)
			}
		})
	}
}

func TestPrintScanSummaryMachineOutputReportUnavailable(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		for _, tc := range []struct {
			name   string
			status int
			body   string
		}{
			{"server error", http.StatusServiceUnavailable, `{"success":false,"error":"report unavailable"}`},
			{"invalid report", http.StatusOK, `not-json`},
		} {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				setOutputGlobals(t, format, false)
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.status)
					fmt.Fprint(w, tc.body)
				}))
				defer ts.Close()
				var err error
				out := captureStdout(t, func() {
					err = printScanSummary(cliclient.NewClient(ts.URL, zap.NewNop().Sugar()), context.Background(), "qa")
				})
				if err == nil || out != "" {
					t.Fatalf("report failures must return an error without a text success hint; output=%q err=%v", out, err)
				}
			})
		}
	}
}

func TestSecurityScanAllMachineOutput(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		for _, terminal := range []string{"completed", "idle"} {
			t.Run(format+"/"+terminal, func(t *testing.T) {
				clearDaemonEnv(t)
				setOutputGlobals(t, format, false)
				oldConfig, oldDataDir, oldAsync, oldScanners := configFile, dataDir, secScanAsync, secScanners
				t.Cleanup(func() { configFile, dataDir, secScanAsync, secScanners = oldConfig, oldDataDir, oldAsync, oldScanners })
				dataDir, secScanAsync, secScanners = "", false, ""
				var polls atomic.Int32
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/api/v1/status":
						fmt.Fprint(w, `{"success":true,"data":{"running":true}}`)
					case "/api/v1/security/scan-all":
						w.WriteHeader(http.StatusAccepted)
						fmt.Fprint(w, `{"success":true,"data":{"status":"running"}}`)
					case "/api/v1/security/queue":
						status := terminal
						if polls.Add(1) == 1 && terminal != "idle" {
							status = "running"
						}
						fmt.Fprintf(w, `{"success":true,"data":{"status":%q,"total":2,"completed":1}}`, status)
					default:
						http.NotFound(w, r)
					}
				}))
				defer ts.Close()
				cfg, err := json.Marshal(map[string]interface{}{
					"listen": strings.TrimPrefix(ts.URL, "http://"), "api_key": "test-key", "data_dir": t.TempDir(),
				})
				if err != nil {
					t.Fatal(err)
				}
				configFile = filepath.Join(t.TempDir(), "config.json")
				if err := os.WriteFile(configFile, cfg, 0o600); err != nil {
					t.Fatal(err)
				}
				var runErr error
				out := captureStdout(t, func() { runErr = runSecurityScanAll() })
				if runErr != nil {
					t.Fatal(runErr)
				}
				var result map[string]interface{}
				if format == "json" {
					err = json.Unmarshal([]byte(out), &result)
				} else {
					err = yaml.Unmarshal([]byte(out), &result)
				}
				if err != nil || result["status"] != terminal {
					t.Fatalf("batch stdout must contain only its final snapshot: %q (parse: %v)", out, err)
				}
			})
		}
	}
}
