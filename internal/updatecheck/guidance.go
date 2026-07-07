package updatecheck

import "fmt"

// UpdateCommand returns the exact one-line update command for the given
// install channel, or "" when no command can be safely offered (Spec 079
// FR-009: never emit a channel-specific command that could be wrong).
//
// Only package-manager/toolchain channels get a command; dmg,
// windows-installer, tarball, docker, and unknown installs get guidance text
// via GuidanceLine instead.
func UpdateCommand(channel string) string {
	switch channel {
	case ChannelHomebrew:
		return "brew upgrade mcpproxy"
	case ChannelDeb:
		return "sudo apt update && sudo apt install --only-upgrade mcpproxy"
	case ChannelRPM:
		return "sudo dnf upgrade mcpproxy"
	case ChannelGoInstall:
		return "go install github.com/smart-mcp-proxy/mcpproxy-go/cmd/mcpproxy@latest"
	default:
		return ""
	}
}

// PrereleaseUpdateCommand returns the one-line update command when the
// offered release is a prerelease. Prereleases are published only to the
// GitHub pre-release channel (docs/prerelease-builds.md) — the Homebrew tap
// and apt/dnf repos serve stable artifacts, and Go's `@latest` module query
// resolves to the newest stable release — so the generic UpdateCommand
// output would not deliver the advertised version (FR-009: never emit a
// command that could be wrong). Only go-install can name the exact version;
// every other channel returns "" and falls back to guidance/release-URL.
func PrereleaseUpdateCommand(channel, version string) string {
	if channel == ChannelGoInstall && version != "" {
		return "go install github.com/smart-mcp-proxy/mcpproxy-go/cmd/mcpproxy@" + ensureVPrefix(version)
	}
	return ""
}

// GuidanceLine returns a human-readable guided-update line for updates that
// carry no safe one-line command, deep-linking the release when releaseURL is
// provided (FR-010). Callers must only invoke it when no update_command
// accompanies the update (all callers gate on that), so it never renders next
// to a command. The command channels (homebrew, deb, rpm, go-install) still
// reach this function when the offered version is a prerelease — their
// package managers serve stable artifacts only, so the command was suppressed
// (see PrereleaseUpdateCommand) — and get the generic release-page line
// rather than nothing.
func GuidanceLine(channel, releaseURL string) string {
	target := "the releases page"
	if releaseURL != "" {
		target = releaseURL
	}

	switch channel {
	case ChannelDMG:
		return fmt.Sprintf("Download the latest DMG from %s", target)
	case ChannelWindowsInstaller:
		return fmt.Sprintf("Download the latest Windows installer from %s", target)
	case ChannelDocker:
		// No official image is published today, so no registry/pull command
		// can be named — the user owns the image reference in their
		// deployment.
		return fmt.Sprintf("Pull or rebuild the newer image for your deployment (see %s)", target)
	default: // tarball, unknown, prerelease-suppressed command channels, anything unrecognized
		return fmt.Sprintf("Download the latest release from %s", target)
	}
}
