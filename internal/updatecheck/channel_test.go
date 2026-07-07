package updatecheck

import (
	"errors"
	"os"
	"runtime/debug"
	"testing"
)

// testDetector returns a channelDetector with every probe stubbed to a
// "nothing detected" baseline so each table test flips exactly one signal.
func testDetector() *channelDetector {
	return &channelDetector{
		marker:         "",
		goos:           "linux",
		ldflagsVersion: "v1.0.0",
		execPath:       func() (string, error) { return "/home/user/bin/mcpproxy", nil },
		evalSymlinks:   func(p string) (string, error) { return p, nil },
		statFile:       func(string) error { return os.ErrNotExist },
		readBuildInfo:  func() (*debug.BuildInfo, bool) { return nil, false },
	}
}

func TestDetectChannel_MarkerWins(t *testing.T) {
	tests := []struct {
		name   string
		marker string
		want   string
	}{
		{"docker marker", ChannelDocker, ChannelDocker},
		{"windows-installer marker", ChannelWindowsInstaller, ChannelWindowsInstaller},
		{"homebrew marker", ChannelHomebrew, ChannelHomebrew},
		{"unrecognized marker never guesses", "flatpak", ChannelUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := testDetector()
			d.marker = tt.marker
			// Poison a heuristic signal to prove the marker short-circuits:
			// a Homebrew path must NOT override an explicit marker.
			d.execPath = func() (string, error) { return "/opt/homebrew/bin/mcpproxy", nil }
			if got := d.detect(); got != tt.want {
				t.Errorf("detect() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectChannel_Homebrew(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"apple silicon prefix", "/opt/homebrew/bin/mcpproxy"},
		{"cellar path (intel mac)", "/usr/local/Cellar/mcpproxy/0.47.0/bin/mcpproxy"},
		{"linuxbrew prefix", "/home/linuxbrew/.linuxbrew/bin/mcpproxy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := testDetector()
			d.goos = "darwin"
			d.execPath = func() (string, error) { return tt.path, nil }
			if got := d.detect(); got != ChannelHomebrew {
				t.Errorf("detect() = %q, want %q", got, ChannelHomebrew)
			}
		})
	}
}

func TestDetectChannel_HomebrewResolvesSymlinks(t *testing.T) {
	// Intel-mac layout: /usr/local/bin/mcpproxy is a symlink into the Cellar.
	d := testDetector()
	d.goos = "darwin"
	d.execPath = func() (string, error) { return "/usr/local/bin/mcpproxy", nil }
	d.evalSymlinks = func(string) (string, error) {
		return "/usr/local/Cellar/mcpproxy/0.47.0/bin/mcpproxy", nil
	}
	if got := d.detect(); got != ChannelHomebrew {
		t.Errorf("detect() = %q, want %q", got, ChannelHomebrew)
	}
}

func TestDetectChannel_Docker(t *testing.T) {
	d := testDetector()
	d.statFile = func(p string) error {
		if p == "/.dockerenv" {
			return nil
		}
		return os.ErrNotExist
	}
	if got := d.detect(); got != ChannelDocker {
		t.Errorf("detect() = %q, want %q", got, ChannelDocker)
	}
}

func TestDetectChannel_DebRequiresBothSignals(t *testing.T) {
	dpkgList := "/var/lib/dpkg/info/mcpproxy.list"

	t.Run("path + dpkg entry -> deb", func(t *testing.T) {
		d := testDetector()
		d.execPath = func() (string, error) { return "/usr/bin/mcpproxy", nil }
		d.statFile = func(p string) error {
			if p == dpkgList {
				return nil
			}
			return os.ErrNotExist
		}
		if got := d.detect(); got != ChannelDeb {
			t.Errorf("detect() = %q, want %q", got, ChannelDeb)
		}
	})

	t.Run("dpkg entry without the path -> not deb", func(t *testing.T) {
		d := testDetector()
		d.execPath = func() (string, error) { return "/usr/local/bin/mcpproxy", nil }
		d.statFile = func(p string) error {
			if p == dpkgList {
				return nil
			}
			return os.ErrNotExist
		}
		if got := d.detect(); got != ChannelUnknown {
			t.Errorf("detect() = %q, want %q", got, ChannelUnknown)
		}
	})

	t.Run("path without any package db (AUR/manual copy) -> unknown", func(t *testing.T) {
		d := testDetector()
		d.execPath = func() (string, error) { return "/usr/bin/mcpproxy", nil }
		if got := d.detect(); got != ChannelUnknown {
			t.Errorf("detect() = %q, want %q", got, ChannelUnknown)
		}
	})
}

func TestDetectChannel_RPMRequiresBothSignals(t *testing.T) {
	t.Run("path + rpmdb.sqlite -> rpm", func(t *testing.T) {
		d := testDetector()
		d.execPath = func() (string, error) { return "/usr/bin/mcpproxy", nil }
		d.statFile = func(p string) error {
			if p == "/var/lib/rpm/rpmdb.sqlite" {
				return nil
			}
			return os.ErrNotExist
		}
		if got := d.detect(); got != ChannelRPM {
			t.Errorf("detect() = %q, want %q", got, ChannelRPM)
		}
	})

	t.Run("path + legacy Packages db -> rpm", func(t *testing.T) {
		d := testDetector()
		d.execPath = func() (string, error) { return "/usr/bin/mcpproxy", nil }
		d.statFile = func(p string) error {
			if p == "/var/lib/rpm/Packages" {
				return nil
			}
			return os.ErrNotExist
		}
		if got := d.detect(); got != ChannelRPM {
			t.Errorf("detect() = %q, want %q", got, ChannelRPM)
		}
	})

	t.Run("both dpkg and rpm evidence is ambiguous -> unknown", func(t *testing.T) {
		d := testDetector()
		d.execPath = func() (string, error) { return "/usr/bin/mcpproxy", nil }
		d.statFile = func(string) error { return nil } // everything exists
		// Note statFile(nil-for-everything) also matches /.dockerenv, which is
		// checked first; scope it to the deb/rpm probes only.
		d.statFile = func(p string) error {
			if p == "/.dockerenv" {
				return os.ErrNotExist
			}
			return nil
		}
		if got := d.detect(); got != ChannelUnknown {
			t.Errorf("detect() = %q, want %q", got, ChannelUnknown)
		}
	})
}

func TestDetectChannel_DMG(t *testing.T) {
	d := testDetector()
	d.goos = "darwin"
	d.execPath = func() (string, error) {
		return "/Applications/MCPProxy.app/Contents/MacOS/mcpproxy", nil
	}
	if got := d.detect(); got != ChannelDMG {
		t.Errorf("detect() = %q, want %q", got, ChannelDMG)
	}

	t.Run("app bundle path on linux is not dmg", func(t *testing.T) {
		d := testDetector()
		d.goos = "linux"
		d.execPath = func() (string, error) {
			return "/opt/whatever.app/Contents/MacOS/mcpproxy", nil
		}
		if got := d.detect(); got != ChannelUnknown {
			t.Errorf("detect() = %q, want %q", got, ChannelUnknown)
		}
	})
}

func TestDetectChannel_GoInstall(t *testing.T) {
	buildInfo := func(mainVersion string) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) {
			bi := &debug.BuildInfo{}
			bi.Main.Version = mainVersion
			return bi, true
		}
	}

	t.Run("no ldflags version + stamped module version -> go-install", func(t *testing.T) {
		d := testDetector()
		// Traced reality: a `go install …@latest` build carries no
		// -X httpapi.buildVersion ldflag, so the checker receives the
		// "development" default (internal/server/server.go SetVersion path).
		d.ldflagsVersion = "development"
		d.readBuildInfo = buildInfo("v0.47.0")
		if got := d.detect(); got != ChannelGoInstall {
			t.Errorf("detect() = %q, want %q", got, ChannelGoInstall)
		}
	})

	t.Run("pseudo-version from go install @commit -> go-install", func(t *testing.T) {
		d := testDetector()
		d.ldflagsVersion = "development"
		d.readBuildInfo = buildInfo("v0.0.0-20260701123456-abcdef123456")
		if got := d.detect(); got != ChannelGoInstall {
			t.Errorf("detect() = %q, want %q", got, ChannelGoInstall)
		}
	})

	t.Run("local go build ((devel)) -> unknown", func(t *testing.T) {
		d := testDetector()
		d.ldflagsVersion = "development"
		d.readBuildInfo = buildInfo("(devel)")
		if got := d.detect(); got != ChannelUnknown {
			t.Errorf("detect() = %q, want %q", got, ChannelUnknown)
		}
	})

	t.Run("release build with ldflags version -> not go-install", func(t *testing.T) {
		d := testDetector()
		d.ldflagsVersion = "v0.47.0"
		d.readBuildInfo = buildInfo("v0.47.0")
		if got := d.detect(); got != ChannelUnknown {
			t.Errorf("detect() = %q, want %q", got, ChannelUnknown)
		}
	})

	t.Run("no build info -> unknown", func(t *testing.T) {
		d := testDetector()
		d.ldflagsVersion = "development"
		d.readBuildInfo = func() (*debug.BuildInfo, bool) { return nil, false }
		if got := d.detect(); got != ChannelUnknown {
			t.Errorf("detect() = %q, want %q", got, ChannelUnknown)
		}
	})
}

func TestDetectChannel_ExecPathErrorFallsThrough(t *testing.T) {
	d := testDetector()
	d.execPath = func() (string, error) { return "", errors.New("no exec path") }
	if got := d.detect(); got != ChannelUnknown {
		t.Errorf("detect() = %q, want %q", got, ChannelUnknown)
	}
}

func TestDetectChannel_EvalSymlinksErrorUsesRawPath(t *testing.T) {
	d := testDetector()
	d.goos = "darwin"
	d.execPath = func() (string, error) { return "/opt/homebrew/bin/mcpproxy", nil }
	d.evalSymlinks = func(string) (string, error) { return "", errors.New("boom") }
	if got := d.detect(); got != ChannelHomebrew {
		t.Errorf("detect() = %q, want %q", got, ChannelHomebrew)
	}
}

func TestDetectChannel_DefaultUnknown(t *testing.T) {
	if got := testDetector().detect(); got != ChannelUnknown {
		t.Errorf("detect() = %q, want %q", got, ChannelUnknown)
	}
}
