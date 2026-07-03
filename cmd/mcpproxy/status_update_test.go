package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStatusVersionSuffix(t *testing.T) {
	tests := []struct {
		name     string
		update   *StatusUpdateInfo
		expected string
	}{
		{
			name:     "nil update shows nothing",
			update:   nil,
			expected: "",
		},
		{
			name: "update available with release URL",
			update: &StatusUpdateInfo{
				Available:     true,
				LatestVersion: "v0.46.0",
				ReleaseURL:    "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/tag/v0.46.0",
			},
			expected: " (update available: v0.46.0 — https://github.com/smart-mcp-proxy/mcpproxy-go/releases/tag/v0.46.0)",
		},
		{
			name: "update available without release URL",
			update: &StatusUpdateInfo{
				Available:     true,
				LatestVersion: "v0.46.0",
			},
			expected: " (update available: v0.46.0)",
		},
		{
			name: "up to date after successful check",
			update: &StatusUpdateInfo{
				Available:     false,
				LatestVersion: "v0.45.0",
			},
			expected: " (latest)",
		},
		{
			name: "check error stays quiet",
			update: &StatusUpdateInfo{
				Available:  false,
				CheckError: "Get \"https://api.github.com\": dial tcp: no route to host",
			},
			expected: "",
		},
		{
			name: "check error with stale availability stays quiet",
			update: &StatusUpdateInfo{
				Available:     true,
				LatestVersion: "v0.46.0",
				CheckError:    "rate limited",
			},
			expected: "",
		},
		{
			name:     "check not completed yet shows nothing",
			update:   &StatusUpdateInfo{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := statusVersionSuffix(tt.update)
			if result != tt.expected {
				t.Errorf("statusVersionSuffix() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestStatusTableShowsUpdateAvailability(t *testing.T) {
	t.Run("update available", func(t *testing.T) {
		info := &StatusInfo{
			State:      "Running",
			Edition:    "personal",
			ListenAddr: "127.0.0.1:8080",
			APIKey:     "a1b2****a1b2",
			WebUIURL:   "http://127.0.0.1:8080/ui/?apikey=test",
			Version:    "v0.45.0",
			Update: &StatusUpdateInfo{
				Available:     true,
				LatestVersion: "v0.46.0",
				ReleaseURL:    "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/tag/v0.46.0",
			},
		}

		output := captureStdout(t, func() { printStatusTable(info) })

		if !strings.Contains(output, "v0.45.0 (update available: v0.46.0 — https://github.com/smart-mcp-proxy/mcpproxy-go/releases/tag/v0.46.0)") {
			t.Errorf("expected update availability on the Version line, output:\n%s", output)
		}
	})

	t.Run("up to date", func(t *testing.T) {
		info := &StatusInfo{
			State:      "Running",
			Edition:    "personal",
			ListenAddr: "127.0.0.1:8080",
			APIKey:     "a1b2****a1b2",
			WebUIURL:   "http://127.0.0.1:8080/ui/?apikey=test",
			Version:    "v0.46.0",
			Update: &StatusUpdateInfo{
				Available:     false,
				LatestVersion: "v0.46.0",
			},
		}

		output := captureStdout(t, func() { printStatusTable(info) })

		if !strings.Contains(output, "v0.46.0 (latest)") {
			t.Errorf("expected '(latest)' on the Version line, output:\n%s", output)
		}
	})

	t.Run("check error shows plain version", func(t *testing.T) {
		info := &StatusInfo{
			State:      "Running",
			Edition:    "personal",
			ListenAddr: "127.0.0.1:8080",
			APIKey:     "a1b2****a1b2",
			WebUIURL:   "http://127.0.0.1:8080/ui/?apikey=test",
			Version:    "v0.45.0",
			Update: &StatusUpdateInfo{
				CheckError: "dial tcp: no route to host",
			},
		}

		output := captureStdout(t, func() { printStatusTable(info) })

		if !strings.Contains(output, "Version:") || !strings.Contains(output, "v0.45.0") {
			t.Errorf("expected plain version line, output:\n%s", output)
		}
		if strings.Contains(output, "update available") || strings.Contains(output, "(latest)") {
			t.Errorf("expected no update annotation on check error, output:\n%s", output)
		}
		if strings.Contains(output, "no route to host") {
			t.Errorf("check error must not be surfaced in human output:\n%s", output)
		}
	})
}

func TestExtractStatusUpdate(t *testing.T) {
	t.Run("full update object", func(t *testing.T) {
		infoData := map[string]interface{}{
			"version": "v0.45.0",
			"update": map[string]interface{}{
				"available":      true,
				"latest_version": "v0.46.0-rc.1",
				"release_url":    "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/tag/v0.46.0-rc.1",
				"checked_at":     "2026-07-02T10:00:00Z",
				"is_prerelease":  true,
			},
		}

		u := extractStatusUpdate(infoData)
		if u == nil {
			t.Fatal("expected non-nil update info")
		}
		if !u.Available {
			t.Error("expected Available=true")
		}
		if u.LatestVersion != "v0.46.0-rc.1" {
			t.Errorf("expected LatestVersion v0.46.0-rc.1, got %q", u.LatestVersion)
		}
		if u.ReleaseURL != "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/tag/v0.46.0-rc.1" {
			t.Errorf("unexpected ReleaseURL %q", u.ReleaseURL)
		}
		if u.CheckedAt != "2026-07-02T10:00:00Z" {
			t.Errorf("expected CheckedAt to be preserved, got %q", u.CheckedAt)
		}
		if !u.IsPrerelease {
			t.Error("expected IsPrerelease=true")
		}
	})

	t.Run("check error preserved for machine output", func(t *testing.T) {
		infoData := map[string]interface{}{
			"update": map[string]interface{}{
				"available":   false,
				"check_error": "rate limited",
			},
		}

		u := extractStatusUpdate(infoData)
		if u == nil {
			t.Fatal("expected non-nil update info")
		}
		if u.CheckError != "rate limited" {
			t.Errorf("expected CheckError 'rate limited', got %q", u.CheckError)
		}
	})

	t.Run("missing update object", func(t *testing.T) {
		infoData := map[string]interface{}{"version": "v0.45.0"}
		if u := extractStatusUpdate(infoData); u != nil {
			t.Errorf("expected nil update info, got %+v", u)
		}
	})
}

func TestStatusJSONIncludesUpdate(t *testing.T) {
	info := &StatusInfo{
		State:      "Running",
		ListenAddr: "127.0.0.1:8080",
		APIKey:     "a1b2****a1b2",
		WebUIURL:   "http://127.0.0.1:8080/ui/?apikey=test",
		Version:    "v0.45.0",
		Update: &StatusUpdateInfo{
			Available:     true,
			LatestVersion: "v0.46.0",
			ReleaseURL:    "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/tag/v0.46.0",
			CheckedAt:     "2026-07-02T10:00:00Z",
			IsPrerelease:  true,
		},
	}

	output := captureStdout(t, func() {
		if err := printStatusJSON(info); err != nil {
			t.Errorf("printStatusJSON failed: %v", err)
		}
	})

	var result StatusInfo
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("invalid JSON: %v\nOutput: %s", err, output)
	}

	if result.Update == nil {
		t.Fatal("expected 'update' field in JSON output")
	}
	if !result.Update.Available {
		t.Error("expected update.available=true in JSON output")
	}
	if result.Update.LatestVersion != "v0.46.0" {
		t.Errorf("expected update.latest_version v0.46.0, got %q", result.Update.LatestVersion)
	}

	// Field names in the wire format must match the /api/v1/info update
	// object (snake_case contract, FR-021).
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	rawUpdate, ok := raw["update"].(map[string]interface{})
	if !ok {
		t.Fatal("expected raw JSON key 'update'")
	}
	if v, ok := rawUpdate["checked_at"].(string); !ok || v != "2026-07-02T10:00:00Z" {
		t.Errorf("expected raw JSON key 'update.checked_at', got %v", rawUpdate["checked_at"])
	}
	if v, ok := rawUpdate["is_prerelease"].(bool); !ok || !v {
		t.Errorf("expected raw JSON key 'update.is_prerelease'=true, got %v", rawUpdate["is_prerelease"])
	}
}

func TestStatusJSONOmitsUpdateWhenAbsent(t *testing.T) {
	info := &StatusInfo{
		State:      "Not running",
		ListenAddr: "127.0.0.1:8080 (configured)",
		APIKey:     "a1b2****a1b2",
		WebUIURL:   "http://127.0.0.1:8080/ui/?apikey=test",
	}

	output := captureStdout(t, func() {
		if err := printStatusJSON(info); err != nil {
			t.Errorf("printStatusJSON failed: %v", err)
		}
	})

	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := raw["update"]; ok {
		t.Error("expected 'update' to be omitted when no update info collected")
	}
}
