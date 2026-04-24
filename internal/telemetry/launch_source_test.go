package telemetry

import (
	"testing"
)

// fakeHandshake implements HandshakeChecker.
type fakeHandshake struct {
	launchedViaTray bool
}

func (f fakeHandshake) LaunchedViaTray() bool { return f.launchedViaTray }

// fakePPID implements PPIDChecker.
type fakePPID struct {
	isLaunchdLoginItem bool
}

func (f fakePPID) IsLoginItemParent() bool { return f.isLaunchdLoginItem }

// TestDetectLaunchSource_DecisionTree exercises every branch of the
// precedence rules from research.md R3. Order is: installer env → tray
// handshake → login_item (PPID=launchd/explorer) → cli (TTY) → unknown.
func TestDetectLaunchSource_DecisionTree(t *testing.T) {
	cases := []struct {
		name      string
		env       map[string]string
		handshake HandshakeChecker
		ppid      PPIDChecker
		tty       TTYChecker
		want      LaunchSource
	}{
		{
			name: "installer env var wins over everything",
			env:  map[string]string{"MCPPROXY_LAUNCHED_BY": "installer"},
			// Even if tray handshake and login-item parent and TTY all say
			// otherwise, the installer flag takes precedence.
			handshake: fakeHandshake{launchedViaTray: true},
			ppid:      fakePPID{isLaunchdLoginItem: true},
			tty:       fakeTTY(true),
			want:      LaunchSourceInstaller,
		},
		{
			name:      "tray handshake wins when no installer env",
			env:       map[string]string{},
			handshake: fakeHandshake{launchedViaTray: true},
			ppid:      fakePPID{isLaunchdLoginItem: true},
			tty:       fakeTTY(true),
			want:      LaunchSourceTray,
		},
		{
			name:      "ppid-is-launchd maps to login_item",
			env:       map[string]string{},
			handshake: fakeHandshake{},
			ppid:      fakePPID{isLaunchdLoginItem: true},
			tty:       fakeTTY(false),
			want:      LaunchSourceLoginItem,
		},
		{
			name:      "tty true maps to cli",
			env:       map[string]string{},
			handshake: fakeHandshake{},
			ppid:      fakePPID{},
			tty:       fakeTTY(true),
			want:      LaunchSourceCLI,
		},
		{
			name:      "fallthrough maps to unknown",
			env:       map[string]string{},
			handshake: fakeHandshake{},
			ppid:      fakePPID{},
			tty:       fakeTTY(false),
			want:      LaunchSourceUnknown,
		},
		{
			name:      "nil handshake treated as not-via-tray",
			env:       map[string]string{},
			handshake: nil,
			ppid:      fakePPID{isLaunchdLoginItem: true},
			tty:       fakeTTY(false),
			want:      LaunchSourceLoginItem,
		},
		{
			name:      "nil ppid checker not a login item",
			env:       map[string]string{},
			handshake: fakeHandshake{},
			ppid:      nil,
			tty:       fakeTTY(true),
			want:      LaunchSourceCLI,
		},
		{
			name:      "empty installer env is not treated as installer",
			env:       map[string]string{"MCPPROXY_LAUNCHED_BY": ""},
			handshake: fakeHandshake{},
			ppid:      fakePPID{},
			tty:       fakeTTY(false),
			want:      LaunchSourceUnknown,
		},
		{
			name:      "installer env with non-installer value is ignored",
			env:       map[string]string{"MCPPROXY_LAUNCHED_BY": "tray"},
			handshake: fakeHandshake{},
			ppid:      fakePPID{},
			tty:       fakeTTY(true),
			want:      LaunchSourceCLI,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectLaunchSource(tc.env, tc.handshake, tc.ppid, tc.tty)
			if got != tc.want {
				t.Fatalf("DetectLaunchSource = %q, want %q", got, tc.want)
			}
			if !IsValidLaunchSource(got) {
				t.Fatalf("DetectLaunchSource returned non-canonical %q", got)
			}
		})
	}
}

// TestDetectLaunchSourceOnce_Cached verifies the once-per-process cache.
func TestDetectLaunchSourceOnce_Cached(t *testing.T) {
	// Reset the once to allow repeated calls under test.
	resetLaunchSourceOnce()
	first := DetectLaunchSourceOnce()
	second := DetectLaunchSourceOnce()
	if first != second {
		t.Fatalf("DetectLaunchSourceOnce not cached: %q vs %q", first, second)
	}
	if !IsValidLaunchSource(first) {
		t.Fatalf("DetectLaunchSourceOnce returned invalid %q", first)
	}
}
