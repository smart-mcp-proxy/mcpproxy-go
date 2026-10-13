package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Spec 092 FR-001a: `mcpproxy status` surfaces the running core's launch
// provenance so an operator (and support) can tell a tray-owned core from one
// they started themselves.
func TestStatusTableShowsLaunchedBy(t *testing.T) {
	tests := []struct {
		name       string
		launchedBy string
		wantLine   bool
	}{
		{"tray-launched", "tray", true},
		{"installer-launched", "installer", true},
		// Empty means user-launched/unknown or a pre-092 daemon: printing
		// "unknown" on every `mcpproxy serve` status call would be noise.
		{"user-launched stays quiet", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &StatusInfo{
				State:      "Running",
				Edition:    "personal",
				ListenAddr: "127.0.0.1:8080",
				APIKey:     "a1b2****a1b2",
				WebUIURL:   "http://127.0.0.1:8080/ui/",
				Version:    "v0.55.0",
				LaunchedBy: tt.launchedBy,
			}

			output := captureStdout(t, func() { printStatusTable(info) })

			hasLine := strings.Contains(output, "Launched by:")
			if hasLine != tt.wantLine {
				t.Fatalf("Launched by line present = %v, want %v; output:\n%s", hasLine, tt.wantLine, output)
			}
			if tt.wantLine && !strings.Contains(output, tt.launchedBy) {
				t.Errorf("expected provenance %q in output:\n%s", tt.launchedBy, output)
			}
		})
	}
}

func TestStatusJSONLaunchedBy(t *testing.T) {
	t.Run("present when the core asserted a marker", func(t *testing.T) {
		info := &StatusInfo{State: "Running", LaunchedBy: "tray"}
		out := captureStdout(t, func() {
			if err := printStatusJSON(info); err != nil {
				t.Fatalf("printStatusJSON: %v", err)
			}
		})
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(out), &parsed); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, out)
		}
		if parsed["launched_by"] != "tray" {
			t.Errorf("launched_by = %v, want tray", parsed["launched_by"])
		}
	})

	t.Run("omitted for user-launched / older daemons", func(t *testing.T) {
		info := &StatusInfo{State: "Running"}
		out := captureStdout(t, func() {
			if err := printStatusJSON(info); err != nil {
				t.Fatalf("printStatusJSON: %v", err)
			}
		})
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(out), &parsed); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, out)
		}
		if _, ok := parsed["launched_by"]; ok {
			t.Errorf("launched_by should be omitted when empty, got:\n%s", out)
		}
	})
}
