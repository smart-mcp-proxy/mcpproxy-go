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

// HandshakeChecker abstracts the tray-socket handshake that signals
// launched_via=tray. Tests inject a constant answer; production wiring is
// a no-op (defaultHandshakeChecker) until tray→core handshake is added.
type HandshakeChecker interface {
	LaunchedViaTray() bool
}

// PPIDChecker abstracts the OS-specific "is my parent a login-item launcher?"
// check. On macOS the production impl verifies parent process name ==
// "launchd"; on Windows parent == "explorer.exe"; otherwise false.
type PPIDChecker interface {
	IsLoginItemParent() bool
}

// DetectLaunchSource is the pure classifier implementing the precedence
// rules from research.md R3:
//  1. MCPPROXY_LAUNCHED_BY=installer env var → installer
//  2. handshake.LaunchedViaTray() → tray
//  3. ppid.IsLoginItemParent() → login_item
//  4. tty.IsTerminal() → cli
//  5. fallthrough → unknown
//
// All parameters are nil-safe: a nil HandshakeChecker / PPIDChecker / TTYChecker
// simply contributes "false" to its respective branch.
func DetectLaunchSource(env map[string]string, handshake HandshakeChecker, ppid PPIDChecker, tty TTYChecker) LaunchSource {
	if env != nil {
		if v, ok := env["MCPPROXY_LAUNCHED_BY"]; ok && v == "installer" {
			return LaunchSourceInstaller
		}
	}
	if handshake != nil && handshake.LaunchedViaTray() {
		return LaunchSourceTray
	}
	if ppid != nil && ppid.IsLoginItemParent() {
		return LaunchSourceLoginItem
	}
	if tty != nil && tty.IsTerminal() {
		return LaunchSourceCLI
	}
	return LaunchSourceUnknown
}

// launchSourceOnce / launchSourceCached memoize DetectLaunchSourceOnce.
var (
	launchSourceOnce   launchSourceOnceT
	launchSourceCached LaunchSource
)

// launchSourceOnceT is a test-friendly sync.Once clone with reset support.
type launchSourceOnceT struct {
	done bool
}

func (o *launchSourceOnceT) Do(f func()) {
	if o.done {
		return
	}
	f()
	o.done = true
}

// resetLaunchSourceOnce is exposed for tests (lower-case).
func resetLaunchSourceOnce() {
	launchSourceOnce = launchSourceOnceT{}
	launchSourceCached = ""
}

// DetectLaunchSourceOnce classifies the current process's launch source
// exactly once per process lifetime and caches the result.
func DetectLaunchSourceOnce() LaunchSource {
	launchSourceOnce.Do(func() {
		launchSourceCached = DetectLaunchSource(
			envMap(),
			defaultHandshakeChecker{},
			defaultPPIDChecker{},
			defaultTTYChecker{},
		)
	})
	return launchSourceCached
}

// defaultHandshakeChecker is the production HandshakeChecker. Until
// tray→core handshake wiring lands, this always returns false; the actual
// tray-mediated launch is still correctly classified because launch_source
// is then "installer" (first run) or "login_item" (subsequent).
type defaultHandshakeChecker struct{}

func (defaultHandshakeChecker) LaunchedViaTray() bool { return false }

// defaultPPIDChecker delegates to the per-OS isLoginItemParent helper in
// launch_source_ppid.go. Errors are swallowed — a failed lookup maps to
// false, erring on the side of LaunchSourceUnknown rather than false-
// positive login_item.
type defaultPPIDChecker struct{}

func (defaultPPIDChecker) IsLoginItemParent() bool {
	return isLoginItemParent()
}
