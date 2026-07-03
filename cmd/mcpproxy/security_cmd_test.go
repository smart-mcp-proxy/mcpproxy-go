package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestScannerDisplayStatus verifies F-09: scanner status vocabulary is
// consistent and rich enough to distinguish "available" / "pulling" /
// "installed" / "configured" / "error" in BOTH table and JSON outputs.
func TestScannerDisplayStatus(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"available", "available"},
		{"pulling", "pulling"},
		{"installed", "installed"},
		{"configured", "configured"},
		{"error", "error"},
		{"", "unknown"},
		// Future / unexpected values pass through unchanged so they don't
		// silently get hidden behind a hard-coded mapping.
		{"some-new-state", "some-new-state"},
	}
	for _, c := range cases {
		got := scannerDisplayStatus(c.in)
		if got != c.want {
			t.Errorf("scannerDisplayStatus(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestComputeScanHardTimeout verifies F-05: the per-scanner timeout is
// extrapolated into a sensible whole-scan timeout that won't return early
// nor hang for the duration of the universe.
func TestComputeScanHardTimeout(t *testing.T) {
	// Nil config -> 15-minute fallback.
	if got := computeScanHardTimeout(nil, ""); got != 15*time.Minute {
		t.Errorf("nil cfg: got %s, want 15m", got)
	}

	// Config with no security section -> fallback.
	cfg := &config.Config{}
	if got := computeScanHardTimeout(cfg, ""); got != 15*time.Minute {
		t.Errorf("nil security: got %s, want 15m", got)
	}

	// Config with explicit per-scanner timeout, with explicit scanner list:
	// 60s * 3 + 30s = 3m30s, but we floor at 15m for sanity.
	cfg = &config.Config{
		Security: &config.SecurityConfig{
			ScanTimeoutDefault: config.Duration(60 * time.Second),
		},
	}
	if got := computeScanHardTimeout(cfg, "a,b,c"); got != 15*time.Minute {
		t.Errorf("60s*3 with floor: got %s, want 15m", got)
	}

	// Per-scanner 5m, no flag (default 8 scanners): 5m*8 + 30s = 40m30s,
	// capped at 30m.
	cfg = &config.Config{
		Security: &config.SecurityConfig{
			ScanTimeoutDefault: config.Duration(5 * time.Minute),
		},
	}
	if got := computeScanHardTimeout(cfg, ""); got != 30*time.Minute {
		t.Errorf("5m*8 cap: got %s, want 30m", got)
	}

	// Per-scanner 4m, 6 scanners: 4m*6 + 30s = 24m30s — within bounds.
	cfg = &config.Config{
		Security: &config.SecurityConfig{
			ScanTimeoutDefault: config.Duration(4 * time.Minute),
		},
	}
	got := computeScanHardTimeout(cfg, "s1,s2,s3,s4,s5,s6")
	want := 4*time.Minute*6 + 30*time.Second
	if got != want {
		t.Errorf("4m*6: got %s, want %s", got, want)
	}
}

// TestNormalizeOverviewLastScan verifies F-14: Go zero-time `last_scan_at`
// values are scrubbed to JSON null in both table and JSON outputs.
func TestNormalizeOverviewLastScan(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]interface{}
		// We assert nil-ness via key presence and value.
		wantPresent bool
		wantNil     bool
		wantValue   interface{}
	}{
		{
			name:        "missing key inserted as nil",
			in:          map[string]interface{}{},
			wantPresent: true,
			wantNil:     true,
		},
		{
			name:        "explicit empty string -> nil",
			in:          map[string]interface{}{"last_scan_at": ""},
			wantPresent: true,
			wantNil:     true,
		},
		{
			name:        "Go zero-time RFC3339 -> nil",
			in:          map[string]interface{}{"last_scan_at": "0001-01-01T00:00:00Z"},
			wantPresent: true,
			wantNil:     true,
		},
		{
			name:        "real timestamp preserved",
			in:          map[string]interface{}{"last_scan_at": "2025-01-15T10:30:00Z"},
			wantPresent: true,
			wantNil:     false,
			wantValue:   "2025-01-15T10:30:00Z",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			normalizeOverviewLastScan(c.in)
			v, present := c.in["last_scan_at"]
			if present != c.wantPresent {
				t.Errorf("present=%v, want %v", present, c.wantPresent)
			}
			if c.wantNil && v != nil {
				t.Errorf("expected nil value, got %v (%T)", v, v)
			}
			if !c.wantNil && c.wantValue != nil && v != c.wantValue {
				t.Errorf("value = %v, want %v", v, c.wantValue)
			}
		})
	}

	// Nil map should not panic.
	normalizeOverviewLastScan(nil)
}

// TestClearPreviousLines verifies F-16: passing 0 or negative values is a
// safe no-op (so the first redraw cycle doesn't blow up the terminal).
func TestClearPreviousLines(t *testing.T) {
	// We can't easily capture stdout here without restructuring; just verify
	// the function doesn't panic on edge cases.
	clearPreviousLines(0)
	clearPreviousLines(-1)
}

// captureStdout runs fn and returns whatever fn writes to os.Stdout. Used to
// assert that human-readable scan output renders all the fields we promise
// (description, threat info, scan context, failed-scanner reasons).
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()
	w.Close()
	return <-done
}

// TestPrintFindingsListRendersDescription guards the regression that prompted
// this change: previously the CLI rendered only the rule ID, and a user
// staring at "[HIGH] MCP-MC-001 (mcp-ai-scanner)" had no way to learn what
// the rule actually meant without leaving the terminal. Description text
// must appear inline.
func TestPrintFindingsListRendersDescription(t *testing.T) {
	out := captureStdout(t, func() {
		printFindingsList([]interface{}{
			map[string]interface{}{
				"severity":     "high",
				"rule_id":      "MCP-MC-001",
				"title":        "Obfuscated code pattern",
				"description":  "Source code contains obfuscated or encoded payloads that may hide malicious behavior",
				"location":     "tools.json:85",
				"scanner":      "mcp-ai-scanner",
				"threat_level": "dangerous",
				"threat_type":  "malicious_code",
				"cvss_score":   7.5,
			},
		})
	})

	wants := []string{
		"[HIGH] MCP-MC-001",
		"CVSS=7.5",
		"(mcp-ai-scanner)",
		"Title:    Obfuscated code pattern",
		"What:     Source code contains obfuscated",
		"Threat:",
		"dangerous",
		"malicious_code",
		"Location: tools.json:85",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n--- output ---\n%s", w, out)
		}
	}
}

// TestPrintFindingsListSkipsEmptyFields ensures we don't print empty
// "Title:", "What:", "Threat:" rows for findings that lack those fields —
// the CLI should remain compact for plain CVE-style findings.
func TestPrintFindingsListSkipsEmptyFields(t *testing.T) {
	out := captureStdout(t, func() {
		printFindingsList([]interface{}{
			map[string]interface{}{
				"severity": "high",
				"rule_id":  "CVE-2024-0001",
				"title":    "CVE-2024-0001",
				"location": "node_modules/foo/index.js",
			},
		})
	})

	if strings.Contains(out, "What:") {
		t.Errorf("expected no 'What:' row when description == title; got:\n%s", out)
	}
	if strings.Contains(out, "Threat:") {
		t.Errorf("expected no 'Threat:' row when threat_level/type/category are absent; got:\n%s", out)
	}
}

// TestPrintFindingsListRendersConfidenceAndSignals verifies Spec-076 US4
// (FR-010 / SC-007): the CLI surfaces each finding's combined confidence and
// the deterministic checks that contributed to it, so an operator can see WHY a
// tool was flagged.
func TestPrintFindingsListRendersConfidenceAndSignals(t *testing.T) {
	out := captureStdout(t, func() {
		printFindingsList([]interface{}{
			map[string]interface{}{
				"severity":     "high",
				"rule_id":      "detect.unicode.hidden",
				"title":        "Hidden Unicode in tool description",
				"location":     "srv:exfiltrate",
				"scanner":      "tpa-descriptions",
				"threat_level": "dangerous",
				"threat_type":  "tool_poisoning",
				"confidence":   0.92,
				"signals":      []interface{}{"unicode.hidden", "directive.imperative"},
			},
		})
	})

	wants := []string{
		"Confidence:",
		"0.92",
		"Signals:",
		"unicode.hidden",
		"directive.imperative",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n--- output ---\n%s", w, out)
		}
	}
}

// TestPrintFindingsListSkipsConfidenceWhenAbsent ensures plain CVE-style
// findings (no deterministic-scanner data) do not grow a noisy empty
// "Confidence:"/"Signals:" row.
func TestPrintFindingsListSkipsConfidenceWhenAbsent(t *testing.T) {
	out := captureStdout(t, func() {
		printFindingsList([]interface{}{
			map[string]interface{}{
				"severity": "high",
				"rule_id":  "CVE-2024-0001",
				"title":    "CVE-2024-0001",
				"location": "node_modules/foo/index.js",
			},
		})
	})
	if strings.Contains(out, "Confidence:") {
		t.Errorf("expected no 'Confidence:' row when confidence is absent; got:\n%s", out)
	}
	if strings.Contains(out, "Signals:") {
		t.Errorf("expected no 'Signals:' row when signals are absent; got:\n%s", out)
	}
}

// TestPrintScanContextSection verifies that the scan-context block renders
// the source method, container, and file/tool counts. This is the signal
// users need to triage a finding (e.g. is "tools.json:85" from a real Docker
// extraction or just from a tool_definitions_only fallback?).
func TestPrintScanContextSection(t *testing.T) {
	out := captureStdout(t, func() {
		printScanContextSection(map[string]interface{}{
			"scan_context": map[string]interface{}{
				"source_method":    "docker_extract",
				"source_path":      "/tmp/mcpproxy-scan-foo-123",
				"server_protocol":  "stdio",
				"docker_isolation": true,
				"container_id":     "f10556008f694b70fcfc7bc157481fab0329811488e06ba8bb5b65df7c12bd0c",
				"total_files":      float64(21),
				"tools_exported":   float64(28),
			},
		})
	})

	wants := []string{
		"Scan Context",
		"Source:           docker_extract",
		"Path:             /tmp/mcpproxy-scan-foo-123",
		"Protocol:         stdio",
		"Docker isolation: true",
		"Container:        f10556008f69", // truncated to 12 chars
		"Files scanned:    21",
		"Tools analyzed:   28",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("scan context missing %q\n--- output ---\n%s", w, out)
		}
	}
}

// TestPrintScanContextSectionSkipsZero verifies that the renderer omits
// counts that are zero so we don't emit misleading "0 files scanned" lines
// for URL-protocol scans that never have a file count.
func TestPrintScanContextSectionSkipsZero(t *testing.T) {
	out := captureStdout(t, func() {
		printScanContextSection(map[string]interface{}{
			"scan_context": map[string]interface{}{
				"source_method":  "url",
				"total_files":    float64(0),
				"tools_exported": float64(0),
			},
		})
	})
	if strings.Contains(out, "Files scanned:") {
		t.Errorf("expected no 'Files scanned:' row when count is 0; got:\n%s", out)
	}
	if strings.Contains(out, "Tools analyzed:") {
		t.Errorf("expected no 'Tools analyzed:' row when count is 0; got:\n%s", out)
	}
}

// TestPrintReportTableRendersFailedScannerReasons guards the most actionable
// improvement in the new output: when a scanner fails, we now show WHY it
// failed (e.g. "GLIBC_2.39 not found"), so users don't have to grep main.log
// to figure out why a scan came back partial.
func TestPrintReportTableRendersFailedScannerReasons(t *testing.T) {
	report := map[string]interface{}{
		"job_id":          "scan-foo-1",
		"risk_score":      float64(25),
		"scanned_at":      "2026-04-26T17:39:05Z",
		"scanners_run":    float64(3),
		"scanners_failed": float64(1),
		"scanners_total":  float64(4),
		"findings":        []interface{}{},
	}
	failed := []failedScannerInfo{
		{ID: "ramparts", Error: "scanner ramparts produced no output (exit code: 1, stderr: ramparts: /lib/aarch64-linux-gnu/libc.so.6: version `GLIBC_2.39' not found (required by ramparts))"},
	}
	out := captureStdout(t, func() {
		_ = printReportTable("foo", report, failed)
	})

	for _, w := range []string{
		"WARNING: Scan coverage incomplete",
		"- ramparts:",
		"GLIBC_2.39",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("expected %q in output; got:\n%s", w, out)
		}
	}
}

// TestFormatScannerDurationMs verifies the per-scanner duration formatter:
// sub-second values render in milliseconds, second-and-up values render as a
// compact "X.Ys", and missing/zero timing renders as a dash rather than "0ms".
func TestFormatScannerDurationMs(t *testing.T) {
	cases := []struct {
		ms   float64
		want string
	}{
		{0, "-"},
		{-5, "-"},
		{850, "850ms"},
		{999, "999ms"},
		{1000, "1.0s"},
		{1500, "1.5s"},
		{12340, "12.3s"},
	}
	for _, c := range cases {
		if got := formatScannerDurationMs(c.ms); got != c.want {
			t.Errorf("formatScannerDurationMs(%v) = %q, want %q", c.ms, got, c.want)
		}
	}
}

// TestScannerDurationMs verifies that the explicit duration_ms field is
// preferred, with a fallback to computing the duration from the
// started_at/completed_at timestamps for reports produced before duration_ms
// was recorded.
func TestScannerDurationMs(t *testing.T) {
	// Explicit duration_ms wins.
	if got := scannerDurationMs(map[string]interface{}{"duration_ms": float64(1200)}); got != 1200 {
		t.Errorf("expected 1200 from explicit duration_ms, got %v", got)
	}
	// Fallback: compute from timestamps when duration_ms is absent.
	got := scannerDurationMs(map[string]interface{}{
		"started_at":   "2026-06-14T10:00:00Z",
		"completed_at": "2026-06-14T10:00:02Z",
	})
	if got != 2000 {
		t.Errorf("expected 2000 from timestamp fallback, got %v", got)
	}
	// No timing data → 0.
	if got := scannerDurationMs(map[string]interface{}{"scanner_id": "x"}); got != 0 {
		t.Errorf("expected 0 with no timing, got %v", got)
	}
}

// TestPrintScannerStatusTableRendersDuration verifies the per-scanner status
// table gained a DURATION column populated from each scanner's timing.
func TestPrintScannerStatusTableRendersDuration(t *testing.T) {
	out := captureStdout(t, func() {
		printScannerStatusTable([]interface{}{
			map[string]interface{}{
				"scanner_id":     "mcp-scan",
				"status":         "completed",
				"findings_count": float64(2),
				"duration_ms":    float64(1500),
			},
			map[string]interface{}{
				"scanner_id":   "trivy-mcp",
				"status":       "completed",
				"started_at":   "2026-06-14T10:00:00Z",
				"completed_at": "2026-06-14T10:00:03Z",
			},
		})
	})
	for _, w := range []string{"DURATION", "mcp-scan", "1.5s", "trivy-mcp", "3.0s"} {
		if !strings.Contains(out, w) {
			t.Errorf("status table missing %q\n--- output ---\n%s", w, out)
		}
	}
}

// TestPrintReportTableRendersScannerTimings verifies the scan report renders a
// per-scanner timing block sourced from scanner_statuses.
func TestPrintReportTableRendersScannerTimings(t *testing.T) {
	report := map[string]interface{}{
		"job_id":     "scan-foo-1",
		"risk_score": float64(0),
		"scanned_at": "2026-04-26T17:39:05Z",
		"findings":   []interface{}{},
		"scanner_statuses": []interface{}{
			map[string]interface{}{
				"scanner_id":  "mcp-scan",
				"status":      "completed",
				"duration_ms": float64(1200),
			},
		},
	}
	out := captureStdout(t, func() {
		_ = printReportTable("foo", report, nil)
	})
	for _, w := range []string{"Scanner timing:", "mcp-scan", "1.2s"} {
		if !strings.Contains(out, w) {
			t.Errorf("report missing %q\n--- output ---\n%s", w, out)
		}
	}
}

// TestPrintReportTableFlagsDegradedRiskScore verifies MCP-2401: when coverage
// is incomplete the risk-score line itself carries a degraded marker, so a low
// "X/100" number is not read as a trustworthy all-clear.
func TestPrintReportTableFlagsDegradedRiskScore(t *testing.T) {
	report := map[string]interface{}{
		"job_id":          "scan-foo-1",
		"risk_score":      float64(0),
		"scanned_at":      "2026-04-26T17:39:05Z",
		"scanners_run":    float64(3),
		"scanners_failed": float64(2),
		"scanners_total":  float64(5),
		"findings":        []interface{}{},
	}
	out := captureStdout(t, func() {
		_ = printReportTable("foo", report, nil)
	})
	if !strings.Contains(out, "Risk Score:") || !strings.Contains(out, "degraded") {
		t.Errorf("expected degraded marker on the Risk Score line; got:\n%s", out)
	}
	if !strings.Contains(out, "2 of 5") {
		t.Errorf("expected coverage detail '2 of 5' on the Risk Score line; got:\n%s", out)
	}
}

// TestPrintReportTableFullCoverageNoDegradedMarker ensures a complete scan does
// not get a degraded marker on the risk-score line.
func TestPrintReportTableFullCoverageNoDegradedMarker(t *testing.T) {
	report := map[string]interface{}{
		"job_id":          "scan-foo-2",
		"risk_score":      float64(0),
		"scanned_at":      "2026-04-26T17:39:05Z",
		"scanners_run":    float64(5),
		"scanners_failed": float64(0),
		"scanners_total":  float64(5),
		"findings":        []interface{}{},
	}
	out := captureStdout(t, func() {
		_ = printReportTable("foo", report, nil)
	})
	// The "Risk Score:" line must not be annotated as degraded.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Risk Score:") && strings.Contains(line, "degraded") {
			t.Errorf("did not expect degraded marker for full coverage; got line: %q", line)
		}
	}
}

// TestScannerEnableHint verifies audit FIX 3b at the CLI layer: the hint the
// core attaches to a scanner-enable response (Docker scanner enabled while
// security.deep_scan.enabled=false) is extracted and surfaced; responses
// without a hint (deep scan on, in-process scanner, older cores) yield "".
func TestScannerEnableHint(t *testing.T) {
	withHint := []byte(`{"success":true,"data":{"status":"enabled","id":"mcp-scan",` +
		`"hint":"scanner enabled, but it will not run until security.deep_scan.enabled=true"}}`)
	if got := scannerEnableHint(withHint); !strings.Contains(got, "security.deep_scan.enabled=true") {
		t.Errorf("expected the deep-scan hint to be extracted, got %q", got)
	}

	noHint := []byte(`{"success":true,"data":{"status":"enabled","id":"tpa-descriptions"}}`)
	if got := scannerEnableHint(noHint); got != "" {
		t.Errorf("expected no hint, got %q", got)
	}

	if got := scannerEnableHint([]byte(`not-json`)); got != "" {
		t.Errorf("malformed body must yield no hint, got %q", got)
	}
}
