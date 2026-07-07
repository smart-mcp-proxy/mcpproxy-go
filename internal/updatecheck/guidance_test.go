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

func TestPrereleaseUpdateCommand(t *testing.T) {
	// Prereleases live only on the GitHub pre-release channel: the Homebrew
	// tap and apt/dnf repos serve stable artifacts, and `go install @latest`
	// resolves to the newest stable. Only go-install can pin the exact
	// version; every other channel must stay silent (FR-009).
	t.Run("go-install pins the exact prerelease version", func(t *testing.T) {
		want := "go install github.com/smart-mcp-proxy/mcpproxy-go/cmd/mcpproxy@v0.48.0-rc.1"
		if got := PrereleaseUpdateCommand(ChannelGoInstall, "v0.48.0-rc.1"); got != want {
			t.Errorf("PrereleaseUpdateCommand(go-install) = %q, want %q", got, want)
		}
	})

	t.Run("version without v prefix is normalized", func(t *testing.T) {
		want := "go install github.com/smart-mcp-proxy/mcpproxy-go/cmd/mcpproxy@v0.48.0-rc.1"
		if got := PrereleaseUpdateCommand(ChannelGoInstall, "0.48.0-rc.1"); got != want {
			t.Errorf("PrereleaseUpdateCommand(go-install) = %q, want %q", got, want)
		}
	})

	t.Run("go-install without a version stays silent", func(t *testing.T) {
		if got := PrereleaseUpdateCommand(ChannelGoInstall, ""); got != "" {
			t.Errorf("PrereleaseUpdateCommand(go-install, \"\") = %q, want empty", got)
		}
	})

	t.Run("package-manager channels never get a prerelease command", func(t *testing.T) {
		for _, channel := range []string{ChannelHomebrew, ChannelDeb, ChannelRPM, ChannelDMG, ChannelWindowsInstaller, ChannelTarball, ChannelDocker, ChannelUnknown, ""} {
			if got := PrereleaseUpdateCommand(channel, "v0.48.0-rc.1"); got != "" {
				t.Errorf("PrereleaseUpdateCommand(%q) = %q, want empty", channel, got)
			}
		}
	})
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

func TestGuidanceLine_CommandChannelsFallBackToGenericLine(t *testing.T) {
	// Callers only invoke GuidanceLine when no update_command accompanies the
	// update. For the command channels that happens exactly when the offered
	// version is a prerelease (their package managers serve stable artifacts
	// only, so the command was suppressed) — they must fall back to the
	// generic release-page line, not to silence.
	for _, channel := range []string{ChannelHomebrew, ChannelDeb, ChannelRPM, ChannelGoInstall} {
		got := GuidanceLine(channel, "https://example.com/v0.48.0-rc.1")
		if !strings.Contains(got, "latest release") || !strings.Contains(got, "https://example.com/v0.48.0-rc.1") {
			t.Errorf("GuidanceLine(%q) = %q, want the generic release-page line", channel, got)
		}
	}
}
