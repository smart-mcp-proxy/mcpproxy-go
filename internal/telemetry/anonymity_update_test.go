package telemetry

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// withoutBlockedValues neutralizes the runtime blocklist so these tests
// exercise the structural rules rather than the hostname/token substring rule.
func withoutBlockedValues(t *testing.T) {
	t.Helper()
	prev := BlockedValues
	BlockedValues = nil
	t.Cleanup(func() { BlockedValues = prev })
}

// TestScanForPII_UpdateFailureCodesAccepted — FR-014: the four spec-095 codes
// ride the existing error_code_counts_24h map and must pass the scanner.
func TestScanForPII_UpdateFailureCodesAccepted(t *testing.T) {
	withoutBlockedValues(t)

	clean := []string{
		`{"anonymous_id":"abc","schema_version":8}`,
		`{"anonymous_id":"abc","diagnostics":{"fix_attempted_24h":0,"fix_succeeded_24h":0,"unique_codes_ever":0}}`,
		`{"anonymous_id":"abc","diagnostics":{"error_code_counts_24h":{` +
			`"MCPX_UPDATE_APPCAST_FAILED":1,"MCPX_UPDATE_DOWNLOAD_FAILED":3,` +
			`"MCPX_UPDATE_INSTALL_FAILED":2,"MCPX_UPDATE_OTHER_FAILED":7},` +
			`"fix_attempted_24h":0,"fix_succeeded_24h":0,"unique_codes_ever":4}}`,
		// Codes from other domains keep working alongside the new ones.
		`{"diagnostics":{"error_code_counts_24h":{"MCPX_DOCKER_CLI_NOT_FOUND":1,"MCPX_UPDATE_DOWNLOAD_FAILED":1}}}`,
	}
	for _, js := range clean {
		if err := ScanForPII([]byte(js)); err != nil {
			t.Errorf("clean payload rejected: %v\n%s", err, js)
		}
	}
}

// TestScanForPII_ErrorCodeCountsShapeViolations pins the new structural rule:
// error_code_counts_24h keys must be catalog-registered diagnostics codes and
// values must be non-negative integers. This is the wire-form backstop for
// RecordErrorCode's prefix-only guard — a free-text key (server name, URL) or
// a non-count value is rejected before transmit.
func TestScanForPII_ErrorCodeCountsShapeViolations(t *testing.T) {
	withoutBlockedValues(t)

	dirty := []struct {
		name    string
		payload string
	}{
		{"server name as key",
			`{"diagnostics":{"error_code_counts_24h":{"MY-CANARY-SERVER":1}}}`},
		{"url as key",
			`{"diagnostics":{"error_code_counts_24h":{"https://internal.example.com/mcp":1}}}`},
		{"MCPX_-shaped but uncataloged key",
			`{"diagnostics":{"error_code_counts_24h":{"MCPX_UPDATE_NOT_A_REAL_CODE":1}}}`},
		{"lowercase free text key",
			`{"diagnostics":{"error_code_counts_24h":{"download failed: connection reset":1}}}`},
		{"negative count",
			`{"diagnostics":{"error_code_counts_24h":{"MCPX_UPDATE_DOWNLOAD_FAILED":-1}}}`},
		{"fractional count",
			`{"diagnostics":{"error_code_counts_24h":{"MCPX_UPDATE_DOWNLOAD_FAILED":1.5}}}`},
		{"string count",
			`{"diagnostics":{"error_code_counts_24h":{"MCPX_UPDATE_DOWNLOAD_FAILED":"3"}}}`},
		{"object count",
			`{"diagnostics":{"error_code_counts_24h":{"MCPX_UPDATE_DOWNLOAD_FAILED":{"n":1}}}}`},
		{"counts not an object",
			`{"diagnostics":{"error_code_counts_24h":["MCPX_UPDATE_DOWNLOAD_FAILED"]}}`},
		{"null counts",
			`{"diagnostics":{"error_code_counts_24h":null}}`},
		{"diagnostics not an object",
			`{"diagnostics":"MCPX_UPDATE_DOWNLOAD_FAILED"}`},
		{"null diagnostics",
			`{"diagnostics":null}`},
	}
	for _, tc := range dirty {
		err := ScanForPII([]byte(tc.payload))
		if err == nil {
			t.Errorf("%s: expected an anonymity violation, got nil for %s", tc.name, tc.payload)
			continue
		}
		var v *AnonymityViolation
		if !errors.As(err, &v) {
			t.Errorf("%s: expected *AnonymityViolation, got %T", tc.name, err)
			continue
		}
		// The violation must never echo the offending key back into logs —
		// keys are exactly where a server name or URL would leak in.
		for _, secret := range []string{"MY-CANARY-SERVER", "internal.example.com", "connection reset"} {
			if strings.Contains(v.Pattern, secret) || strings.Contains(v.Reason, secret) {
				t.Errorf("%s: violation echoed %q back: pattern=%q reason=%q",
					tc.name, secret, v.Pattern, v.Reason)
			}
		}
	}
}

// TestPayload_UpdateFailureCountsSurfaceInHeartbeat — SC-004: an occurrence
// recorded through the real store shows up as a per-stage count in the payload
// the preview endpoint renders, and that payload passes the privacy scanner.
func TestPayload_UpdateFailureCountsSurfaceInHeartbeat(t *testing.T) {
	withoutBlockedValues(t)
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CI", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")

	db, err := bbolt.Open(filepath.Join(t.TempDir(), "diag.db"), 0o600, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("open bbolt: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := EnsureDiagnosticsCountersBucket(db); err != nil {
		t.Fatalf("ensure bucket: %v", err)
	}

	store := NewDiagnosticsCounterStore()
	for _, code := range []string{
		"MCPX_UPDATE_DOWNLOAD_FAILED",
		"MCPX_UPDATE_DOWNLOAD_FAILED",
		"MCPX_UPDATE_APPCAST_FAILED",
	} {
		if err := store.RecordErrorCode(db, code); err != nil {
			t.Fatalf("record %s: %v", code, err)
		}
	}

	cfg := &config.Config{Telemetry: &config.TelemetryConfig{
		AnonymousID:          "550e8400-e29b-41d4-a716-446655440000",
		AnonymousIDCreatedAt: "2026-04-10T12:00:00Z",
	}}
	svc := New(cfg, "", "v1.2.3", "personal", zap.NewNop())
	svc.SetDiagnosticsCounterStore(store, db)

	payload := svc.BuildPayload()
	if payload.Diagnostics == nil {
		t.Fatal("payload.diagnostics = nil, want the per-stage counts")
	}
	if got := payload.Diagnostics.ErrorCodeCounts24h["MCPX_UPDATE_DOWNLOAD_FAILED"]; got != 2 {
		t.Errorf("download count = %d, want 2", got)
	}
	if got := payload.Diagnostics.ErrorCodeCounts24h["MCPX_UPDATE_APPCAST_FAILED"]; got != 1 {
		t.Errorf("appcast count = %d, want 1", got)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(data)
	for _, required := range []string{`"MCPX_UPDATE_DOWNLOAD_FAILED":2`, `"MCPX_UPDATE_APPCAST_FAILED":1`} {
		if !strings.Contains(js, required) {
			t.Errorf("expected payload to contain %s, missing from:\n%s", required, js)
		}
	}
	// FR-009: no free-form failure content ever accompanies the counts. The
	// diagnostics sub-object must be codes → integers and nothing else — no
	// URL, no lowercase stage string, no error description.
	diagJSON, err := json.Marshal(payload.Diagnostics)
	if err != nil {
		t.Fatalf("marshal diagnostics: %v", err)
	}
	for _, forbidden := range []string{"://", `"download"`, `"appcast"`, `"install"`, `"other"`} {
		if strings.Contains(string(diagJSON), forbidden) {
			t.Errorf("diagnostics carries forbidden failure content %q:\n%s", forbidden, diagJSON)
		}
	}
	if scanErr := ScanForPII(data); scanErr != nil {
		t.Fatalf("payload with update-failure counts must pass ScanForPII, got: %v\n%s", scanErr, js)
	}
}
