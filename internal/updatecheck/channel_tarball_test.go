package updatecheck

import (
	"os"
	"runtime/debug"
	"testing"
)

// Spec 092 FR-020: ChannelTarball must be reachable — before this change the
// constant existed but detect() could never return it, so `mcpproxy update`
// had no positively identified self-managed install to act on.
func TestDetectChannel_TarballMarkerIsReachable(t *testing.T) {
	d := testDetector()
	d.marker = ChannelTarball
	d.execPath = func() (string, error) { return "/home/user/.local/bin/mcpproxy", nil }

	if got := d.detect(); got != ChannelTarball {
		t.Fatalf("detect() = %q, want %q", got, ChannelTarball)
	}
}

// The tarball marker is WEAK by design: the release .tar.gz is also what
// Homebrew extracts into its Cellar, so the marker must never mask a positive
// runtime signal. Every case here would be a wrong `mcpproxy update`
// self-replace of a package-manager-owned binary if the marker short-circuited
// (FR-020, SC-005).
func TestDetectChannel_TarballMarkerLosesToPositiveHeuristics(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(d *channelDetector)
		want    string
		rawWant string // documents why the case matters
	}{
		{
			name: "homebrew cellar extraction of the same tarball",
			mutate: func(d *channelDetector) {
				d.goos = "darwin"
				d.execPath = func() (string, error) {
					return "/opt/homebrew/Cellar/mcpproxy/0.55.0/bin/mcpproxy", nil
				}
			},
			want:    ChannelHomebrew,
			rawWant: "brew owns the install; self-update would break its bookkeeping",
		},
		{
			name: "container runtime",
			mutate: func(d *channelDetector) {
				d.statFile = func(p string) error {
					if p == "/.dockerenv" {
						return nil
					}
					return os.ErrNotExist
				}
			},
			want:    ChannelDocker,
			rawWant: "image layers are immutable; the user rebuilds",
		},
		{
			name: "macOS app bundle core",
			mutate: func(d *channelDetector) {
				d.goos = "darwin"
				d.execPath = func() (string, error) {
					return "/Applications/MCPProxy.app/Contents/Resources/bin/mcpproxy", nil
				}
			},
			want:    ChannelDMG,
			rawWant: "FR-022 forbids touching anything inside the app bundle",
		},
		{
			name: "staged core copy of a DMG install",
			mutate: func(d *channelDetector) {
				d.goos = "darwin"
				d.execPath = func() (string, error) {
					return "/Users/u/Library/Application Support/mcpproxy/bin/mcpproxy", nil
				}
			},
			want:    ChannelDMG,
			rawWant: "staged copies belong to the tray, not to the CLI",
		},
		{
			name: "apt-owned /usr/bin install",
			mutate: func(d *channelDetector) {
				d.goos = "linux"
				d.execPath = func() (string, error) { return "/usr/bin/mcpproxy", nil }
				d.statFile = func(p string) error {
					switch p {
					case "/var/lib/dpkg/info/mcpproxy.list", "/etc/apt/sources.list.d/mcpproxy.list":
						return nil
					}
					return os.ErrNotExist
				}
			},
			want:    ChannelDeb,
			rawWant: "dpkg owns the file",
		},
		{
			name: "package-manager directory with no ownership evidence stays unknown",
			mutate: func(d *channelDetector) {
				d.goos = "linux"
				d.execPath = func() (string, error) { return "/usr/bin/mcpproxy", nil }
			},
			want:    ChannelUnknown,
			rawWant: "a standalone .deb from a GitHub release lands here; guidance only",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := testDetector()
			d.marker = ChannelTarball
			tt.mutate(d)
			if got := d.detect(); got != tt.want {
				t.Errorf("detect() = %q, want %q (%s)", got, tt.want, tt.rawWant)
			}
		})
	}
}

// Non-tarball markers keep the original absolute precedence (FR-008): they
// identify single-purpose artifacts that are never redistributed through
// another channel, so no heuristic may override them.
func TestDetectChannel_NonTarballMarkersStayAbsolute(t *testing.T) {
	for _, marker := range []string{ChannelDocker, ChannelWindowsInstaller, ChannelHomebrew, ChannelDMG, ChannelDeb, ChannelRPM, ChannelGoInstall} {
		t.Run(marker, func(t *testing.T) {
			d := testDetector()
			d.marker = marker
			// Poison every heuristic at once.
			d.goos = "darwin"
			d.execPath = func() (string, error) {
				return "/opt/homebrew/Cellar/mcpproxy/0.55.0/bin/mcpproxy", nil
			}
			d.statFile = func(string) error { return nil }
			if got := d.detect(); got != marker {
				t.Errorf("detect() = %q, want %q", got, marker)
			}
		})
	}
}

// An unstamped binary must still be unknown, never tarball: FR-020 requires a
// positive marker before self-update can activate, and "writable location"
// alone is not ownership.
func TestDetectChannel_UnstampedNeverBecomesTarball(t *testing.T) {
	d := testDetector()
	d.execPath = func() (string, error) { return "/home/user/.local/bin/mcpproxy", nil }
	if got := d.detect(); got != ChannelUnknown {
		t.Errorf("detect() = %q, want %q for an unstamped binary", got, ChannelUnknown)
	}
}

// An unrecognized marker must not fall through to the heuristics either — it
// is a packaging bug, and guessing a channel from paths could hand a wrong
// upgrade command (or a self-update) to a packaging system we do not know.
func TestDetectChannel_UnknownMarkerStillTerminal(t *testing.T) {
	d := testDetector()
	d.marker = "flatpak"
	d.goos = "darwin"
	d.execPath = func() (string, error) { return "/opt/homebrew/bin/mcpproxy", nil }
	if got := d.detect(); got != ChannelUnknown {
		t.Errorf("detect() = %q, want %q", got, ChannelUnknown)
	}
}

// go-install detection must not be confused by the tarball marker: a stamped
// release binary carries a semver ldflags version, so isGoInstall() is false
// and the marker applies.
func TestDetectChannel_TarballMarkerWithBuildInfo(t *testing.T) {
	d := testDetector()
	d.marker = ChannelTarball
	d.ldflagsVersion = "v0.55.0"
	d.readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: "v0.55.0"}}, true
	}
	if got := d.detect(); got != ChannelTarball {
		t.Errorf("detect() = %q, want %q", got, ChannelTarball)
	}
}
