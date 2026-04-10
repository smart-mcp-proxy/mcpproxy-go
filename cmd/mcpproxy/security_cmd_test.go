package main

import (
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
