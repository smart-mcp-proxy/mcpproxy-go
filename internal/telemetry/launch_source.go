package telemetry

// LaunchSource identifies how the current mcpproxy process was launched, for
// retention telemetry (spec 044). Detection happens once at process startup
// (via DetectLaunchSourceOnce, added in a later task), with a one-shot
// "installer" override driven by the installer_heartbeat_pending BBolt flag.
//
// Serialization: always lowercase string. The payload builder rejects values
// outside the canonical set.
type LaunchSource string

const (
	// LaunchSourceInstaller: MCPPROXY_LAUNCHED_BY=installer env var at
	// startup. One-shot: cleared after first heartbeat.
	LaunchSourceInstaller LaunchSource = "installer"

	// LaunchSourceTray: the tray socket handshake signalled launched_via=tray.
	LaunchSourceTray LaunchSource = "tray"

	// LaunchSourceLoginItem: OS launched the process as a registered login
	// item (parent = launchd on macOS, explorer.exe on Windows via the Run
	// registry key).
	LaunchSourceLoginItem LaunchSource = "login_item"

	// LaunchSourceCLI: stdin is a TTY and no other rule matched.
	LaunchSourceCLI LaunchSource = "cli"

	// LaunchSourceUnknown: none of the above.
	LaunchSourceUnknown LaunchSource = "unknown"
)

// AllLaunchSources returns the canonical ordered list of LaunchSource values.
func AllLaunchSources() []LaunchSource {
	return []LaunchSource{
		LaunchSourceInstaller,
		LaunchSourceTray,
		LaunchSourceLoginItem,
		LaunchSourceCLI,
		LaunchSourceUnknown,
	}
}

// IsValidLaunchSource reports whether v is one of the canonical LaunchSource
// constants.
func IsValidLaunchSource(v LaunchSource) bool {
	for _, s := range AllLaunchSources() {
		if s == v {
			return true
		}
	}
	return false
}
