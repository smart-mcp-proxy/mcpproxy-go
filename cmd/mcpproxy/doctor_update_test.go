package main

import "testing"

// Spec 079 US2: doctor prints a guided-update action line after the
// Download line — the exact channel command when one exists, otherwise the
// channel-appropriate guidance (FR-009).
func TestDoctorUpdateActionLine(t *testing.T) {
	tests := []struct {
		name       string
		updateInfo map[string]interface{}
		expected   string
	}{
		{
			name: "channel with command renders Run line",
			updateInfo: map[string]interface{}{
				"available":       true,
				"latest_version":  "v0.48.0",
				"install_channel": "homebrew",
				"update_command":  "brew upgrade mcpproxy",
			},
			expected: "Run: brew upgrade mcpproxy",
		},
		{
			name: "guidance-only channel renders Update line",
			updateInfo: map[string]interface{}{
				"available":       true,
				"latest_version":  "v0.48.0",
				"install_channel": "windows-installer",
			},
			expected: "Update: Download the latest Windows installer from the releases page",
		},
		{
			name: "older daemon without channel info renders nothing",
			updateInfo: map[string]interface{}{
				"available":      true,
				"latest_version": "v0.48.0",
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := doctorUpdateActionLine(tt.updateInfo); got != tt.expected {
				t.Errorf("doctorUpdateActionLine() = %q, want %q", got, tt.expected)
			}
		})
	}
}
