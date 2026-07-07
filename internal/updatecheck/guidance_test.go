package updatecheck

import (
	"strings"
	"testing"
)

func TestUpdateCommand_ExactPerChannel(t *testing.T) {
	tests := []struct {
		channel string
		want    string
	}{
		{ChannelHomebrew, "brew upgrade mcpproxy"},
		{ChannelDeb, "sudo apt update && sudo apt install --only-upgrade mcpproxy"},
		{ChannelRPM, "sudo dnf upgrade mcpproxy"},
		{ChannelGoInstall, "go install github.com/smart-mcp-proxy/mcpproxy-go/cmd/mcpproxy@latest"},
	}
	for _, tt := range tests {
		t.Run(tt.channel, func(t *testing.T) {
			if got := UpdateCommand(tt.channel); got != tt.want {
				t.Errorf("UpdateCommand(%q) = %q, want %q", tt.channel, got, tt.want)
			}
		})
	}
}

func TestUpdateCommand_NoCommandChannels(t *testing.T) {
	// dmg / windows-installer / tarball / docker / unknown must never emit a
	// command that could be wrong for the user's setup (FR-009).
	for _, channel := range []string{ChannelDMG, ChannelWindowsInstaller, ChannelTarball, ChannelDocker, ChannelUnknown, ""} {
		if got := UpdateCommand(channel); got != "" {
			t.Errorf("UpdateCommand(%q) = %q, want empty", channel, got)
		}
	}
}

func TestGuidanceLine_PerChannel(t *testing.T) {
	const url = "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/tag/v0.48.0"

	tests := []struct {
		name     string
		channel  string
		url      string
		contains []string
		excludes []string
	}{
		{
			name:     "dmg points at the DMG download",
			channel:  ChannelDMG,
			url:      url,
			contains: []string{"DMG", url},
		},
		{
			name:     "windows installer points at the installer download",
			channel:  ChannelWindowsInstaller,
			url:      url,
			contains: []string{"installer", url},
		},
		{
			name:    "docker points at pulling/rebuilding the image, no ghcr.io",
			channel: ChannelDocker,
			url:     url,
			// No official image is published today; guidance must not
			// reference a registry that does not exist.
			contains: []string{"image", url},
			excludes: []string{"ghcr.io", "docker pull"},
		},
		{
			name:     "tarball gets the generic releases line",
			channel:  ChannelTarball,
			url:      url,
			contains: []string{"latest release", url},
		},
		{
			name:     "unknown gets the generic releases line",
			channel:  ChannelUnknown,
			url:      url,
			contains: []string{"latest release", url},
		},
		{
			name:     "empty URL falls back to the releases page wording",
			channel:  ChannelDMG,
			url:      "",
			contains: []string{"DMG", "releases page"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GuidanceLine(tt.channel, tt.url)
			if got == "" {
				t.Fatalf("GuidanceLine(%q, %q) = empty, want guidance", tt.channel, tt.url)
			}
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("GuidanceLine(%q) = %q, want it to contain %q", tt.channel, got, want)
				}
			}
			for _, banned := range tt.excludes {
				if strings.Contains(got, banned) {
					t.Errorf("GuidanceLine(%q) = %q, must not contain %q", tt.channel, got, banned)
				}
			}
		})
	}
}

func TestGuidanceLine_EmptyForCommandChannels(t *testing.T) {
	// Channels with a real one-line command surface that command instead;
	// GuidanceLine stays empty so callers do not render both.
	for _, channel := range []string{ChannelHomebrew, ChannelDeb, ChannelRPM, ChannelGoInstall} {
		if got := GuidanceLine(channel, "https://example.com"); got != "" {
			t.Errorf("GuidanceLine(%q) = %q, want empty (channel has a command)", channel, got)
		}
	}
}
