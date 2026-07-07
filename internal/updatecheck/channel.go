package updatecheck

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// buildChannel is the build-time install-channel marker (Spec 079 FR-008).
// Packaging pipelines that produce a single-channel artifact stamp it via:
//
//	-X github.com/smart-mcp-proxy/mcpproxy-go/internal/updatecheck.buildChannel=<channel>
//
// NOTE: the -X path must be the FULL module path above — Go silently ignores
// -X flags whose symbol path does not resolve, so a short "updatecheck.buildChannel"
// would build fine and stamp nothing (this exact bug shipped as a P1 with the
// httpapi.buildVersion stamp in the v0.47.0 rc builds).
//
// The release.yml/prerelease.yml matrix builds intentionally do NOT stamp it:
// one binary there feeds the tarball, Homebrew, and DMG artifacts, so any
// single value would be wrong for some consumers; those installs rely on the
// runtime heuristics below.
var buildChannel = ""

// Install channel identifiers (Spec 079 FR-008 / key entity "Install channel").
const (
	ChannelHomebrew         = "homebrew"
	ChannelDMG              = "dmg"
	ChannelDeb              = "deb"
	ChannelRPM              = "rpm"
	ChannelDocker           = "docker"
	ChannelGoInstall        = "go-install"
	ChannelWindowsInstaller = "windows-installer"
	ChannelTarball          = "tarball"
	ChannelUnknown          = "unknown"
)

// knownChannels validates the build marker: an unrecognized value must never
// leak into the API or select guidance, so it degrades to unknown.
var knownChannels = map[string]bool{
	ChannelHomebrew:         true,
	ChannelDMG:              true,
	ChannelDeb:              true,
	ChannelRPM:              true,
	ChannelDocker:           true,
	ChannelGoInstall:        true,
	ChannelWindowsInstaller: true,
	ChannelTarball:          true,
	ChannelUnknown:          true,
}

// channelDetector carries injectable OS probes so every heuristic is
// table-testable without touching the real filesystem (Spec 079 US2).
type channelDetector struct {
	// marker is the build-time buildChannel value ("" when unstamped).
	marker string
	// goos is runtime.GOOS.
	goos string
	// ldflagsVersion is the version string the checker actually receives —
	// httpapi.buildVersion via internal/server/server.go
	// rt.SetVersion(httpapi.GetBuildVersion()). Release builds stamp a semver;
	// `go install` and local `go build` leave the "development" default.
	ldflagsVersion string

	execPath      func() (string, error)
	evalSymlinks  func(string) (string, error)
	statFile      func(string) error // nil error ⇒ the path exists
	readBuildInfo func() (*debug.BuildInfo, bool)
	// rpmOwnsBinary reports whether the mcpproxy RPM package owns
	// /usr/bin/mcpproxy (see rpmOwnsUsrBinMcpproxy). Only consulted on Linux
	// when the binary runs from /usr/bin/mcpproxy and an RPM database exists.
	rpmOwnsBinary func() bool
}

func newChannelDetector(ldflagsVersion string) *channelDetector {
	return &channelDetector{
		marker:         buildChannel,
		goos:           runtime.GOOS,
		ldflagsVersion: ldflagsVersion,
		execPath:       os.Executable,
		evalSymlinks:   filepath.EvalSymlinks,
		statFile: func(path string) error {
			_, err := os.Stat(path)
			return err
		},
		readBuildInfo: debug.ReadBuildInfo,
		rpmOwnsBinary: rpmOwnsUsrBinMcpproxy,
	}
}

// rpmProbeTimeout bounds the one-shot `rpm -qf` ownership query run at
// startup on RPM-based hosts. rpm answers from a local database, so 3s is
// generous; on expiry the probe fails closed to ChannelUnknown.
const rpmProbeTimeout = 3 * time.Second

// rpmOwnsUsrBinMcpproxy reports whether the mcpproxy RPM package owns
// /usr/bin/mcpproxy. The RPM database existing (checked by the caller) only
// proves the host is RPM-based — a manual tarball copy to /usr/bin/mcpproxy on
// Fedora would otherwise be misclassified as an rpm install and shown a wrong
// `dnf upgrade` command (FR-009: never guess). Any failure — rpm binary
// missing, file unowned, timeout — degrades to false, i.e. ChannelUnknown.
func rpmOwnsUsrBinMcpproxy() bool {
	ctx, cancel := context.WithTimeout(context.Background(), rpmProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "rpm", "-qf", "--qf", "%{NAME}", "/usr/bin/mcpproxy").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "mcpproxy"
}

// DetectChannel identifies the install channel of the running binary.
// ldflagsVersion is the build version handed to the checker (see
// channelDetector.ldflagsVersion). The build-time marker wins; heuristics
// run in decreasing confidence order and never guess: any ambiguity yields
// ChannelUnknown so a wrong channel command is never surfaced (FR-009,
// edge case "channel mis-identification risk").
func DetectChannel(ldflagsVersion string) string {
	return newChannelDetector(ldflagsVersion).detect()
}

func (d *channelDetector) detect() string {
	// (0) Build-time marker wins over every heuristic (FR-008).
	if d.marker != "" {
		if knownChannels[d.marker] {
			return d.marker
		}
		return ChannelUnknown
	}

	path := d.resolvedExecPath()

	// (1) Homebrew: Cellar/prefix paths (Apple Silicon, Intel-mac Cellar,
	// Linuxbrew). Highest-confidence heuristic — these paths are owned by brew.
	if path != "" &&
		(strings.Contains(path, "/Cellar/") ||
			strings.Contains(path, "/opt/homebrew/") ||
			strings.Contains(path, "/home/linuxbrew/.linuxbrew")) {
		return ChannelHomebrew
	}

	// (2) Docker: /.dockerenv is created by the Docker runtime in every
	// container.
	if d.statFile("/.dockerenv") == nil {
		return ChannelDocker
	}

	// (3) deb/rpm: require BOTH the package-manager-owned install path AND
	// package-specific ownership evidence. dpkg keeps a per-package file list
	// (mcpproxy.list); rpm has no per-package file to stat, so the database
	// presence (host is RPM-based) is confirmed with an `rpm -qf` ownership
	// query — the DB alone would misclassify a manual tarball copy to
	// /usr/bin/mcpproxy on any RPM distro. AUR/manual installs have neither
	// and must fall to unknown; both matching is ambiguous and also falls to
	// unknown. This branch is terminal for the /usr/bin/mcpproxy path: never
	// guess (FR-009).
	if d.goos == "linux" && path == "/usr/bin/mcpproxy" {
		hasDeb := d.statFile("/var/lib/dpkg/info/mcpproxy.list") == nil
		rpmDBPresent := d.statFile("/var/lib/rpm/rpmdb.sqlite") == nil ||
			d.statFile("/var/lib/rpm/Packages") == nil
		hasRPM := rpmDBPresent && d.rpmOwnsBinary()
		switch {
		case hasDeb && !hasRPM:
			return ChannelDeb
		case hasRPM && !hasDeb:
			return ChannelRPM
		default:
			return ChannelUnknown
		}
	}

	// (4) macOS DMG install. Three shapes, because the tray stages the
	// bundled core out of the app bundle before running it
	// (cmd/mcpproxy-tray stageBundledCore):
	//   - .app/Contents/MacOS       — the tray itself / direct bundle exec
	//   - .app/Contents/Resources/bin — the bundled core run in place
	//   - ~/Library/Application Support/mcpproxy/bin/mcpproxy — the staged
	//     core, which is the process that actually serves /api/v1/info for
	//     DMG installs; that directory is written only by the tray's
	//     bundle-staging path, so it uniquely implies a DMG install.
	if d.goos == "darwin" &&
		(strings.Contains(path, ".app/Contents/MacOS") ||
			strings.Contains(path, ".app/Contents/Resources/bin") ||
			strings.HasSuffix(path, "/Library/Application Support/mcpproxy/bin/mcpproxy")) {
		return ChannelDMG
	}

	// (5) go install: the Go toolchain stamps the module version into build
	// info for `go install module@version` builds, while such builds carry no
	// -X ldflags version (the checker then receives the "development"
	// default). Requiring both signals keeps release binaries — which stamp a
	// semver via ldflags but report "(devel)" module versions — out of this
	// branch.
	if d.isGoInstall() {
		return ChannelGoInstall
	}

	// (6) No reliable signal — generic guidance only.
	return ChannelUnknown
}

// resolvedExecPath returns the symlink-resolved executable path, or "" when
// the executable path cannot be determined. A failed symlink resolution falls
// back to the raw path.
func (d *channelDetector) resolvedExecPath() string {
	path, err := d.execPath()
	if err != nil || path == "" {
		return ""
	}
	if resolved, err := d.evalSymlinks(path); err == nil && resolved != "" {
		return resolved
	}
	return path
}

func (d *channelDetector) isGoInstall() bool {
	// A stamped ldflags version means a packaged build, not `go install`.
	if semver.IsValid(ensureVPrefix(d.ldflagsVersion)) {
		return false
	}
	bi, ok := d.readBuildInfo()
	if !ok || bi == nil {
		return false
	}
	v := bi.Main.Version
	if v == "" || v == "(devel)" {
		return false
	}
	// Tagged installs ("v0.47.0") and pseudo-versions
	// ("v0.0.0-20260701123456-abcdef123456") are both valid semver.
	return semver.IsValid(v)
}
