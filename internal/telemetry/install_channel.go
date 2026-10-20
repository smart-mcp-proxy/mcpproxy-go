package telemetry

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/updatecheck"
)

// InstallChannel is the closed, low-cardinality label for how a running
// install was obtained (Spec 121). It is the ONLY install-provenance value
// that may reach the heartbeat: never a path, a file's contents beyond one
// validated token, a hostname or a user name.
type InstallChannel string

const (
	InstallChannelBrew             InstallChannel = "brew"
	InstallChannelDMG              InstallChannel = "dmg"
	InstallChannelWinget           InstallChannel = "winget"
	InstallChannelScoop            InstallChannel = "scoop"
	InstallChannelWindowsInstaller InstallChannel = "windows-installer"
	InstallChannelDeb              InstallChannel = "deb"
	InstallChannelRPM              InstallChannel = "rpm"
	InstallChannelDocker           InstallChannel = "docker"
	InstallChannelRegistry         InstallChannel = "registry"
	InstallChannelCurl             InstallChannel = "curl"
	InstallChannelTarball          InstallChannel = "tarball"
	InstallChannelGoInstall        InstallChannel = "go-install"
	InstallChannelUnknown          InstallChannel = "unknown"
)

const (
	// InstallChannelEnvVar lets any wrapper (a container run from the registry
	// entry, a script) declare the channel explicitly.
	InstallChannelEnvVar = "MCPPROXY_INSTALL_CHANNEL"
	// installChannelMarkerName is the one-token marker file. Beside the
	// executable it is installer-owned (authoritative); in the data directory
	// it is a weak hint written by the agent install prompt.
	installChannelMarkerName = "install-channel"
	// markerMaxBytes caps what is ever read from a marker.
	markerMaxBytes = 64
)

// AllInstallChannels returns the canonical, ordered closed set.
func AllInstallChannels() []InstallChannel {
	return []InstallChannel{
		InstallChannelBrew, InstallChannelDMG, InstallChannelWinget, InstallChannelScoop,
		InstallChannelWindowsInstaller, InstallChannelDeb, InstallChannelRPM, InstallChannelDocker,
		InstallChannelRegistry, InstallChannelCurl, InstallChannelTarball, InstallChannelGoInstall,
		InstallChannelUnknown,
	}
}

// Valid reports membership in the closed set.
func (c InstallChannel) Valid() bool {
	for _, v := range AllInstallChannels() {
		if c == v {
			return true
		}
	}
	return false
}

// MapDetectorChannel maps an internal/updatecheck channel identifier onto the
// telemetry set. It is total: anything unrecognised (including "") is
// "unknown", so a future detector value can never leak into the payload.
func MapDetectorChannel(detected string) InstallChannel {
	switch detected {
	case updatecheck.ChannelHomebrew:
		return InstallChannelBrew
	case updatecheck.ChannelDMG:
		return InstallChannelDMG
	case updatecheck.ChannelDeb:
		return InstallChannelDeb
	case updatecheck.ChannelRPM:
		return InstallChannelRPM
	case updatecheck.ChannelDocker:
		return InstallChannelDocker
	case updatecheck.ChannelGoInstall:
		return InstallChannelGoInstall
	case updatecheck.ChannelWindowsInstaller:
		return InstallChannelWindowsInstaller
	case updatecheck.ChannelScoop:
		return InstallChannelScoop
	case updatecheck.ChannelTarball:
		return InstallChannelTarball
	default:
		return InstallChannelUnknown
	}
}

// installChannelDeps injects every OS probe so the resolver is a pure,
// table-testable function.
type installChannelDeps struct {
	getenv   func(string) string
	exeDir   func() string
	dataDir  string
	detect   func() string
	readFile func(path string, max int64) ([]byte, error)
}

// resolveInstallChannel applies the precedence of Spec 121 FR-031:
//
//  1. MCPPROXY_INSTALL_CHANNEL (any member except "unknown")
//  2. installer-owned marker beside the executable (only "winget")
//  3. the update-channel detector, mapped
//  4. weak data-directory marker (only "curl"), and only when (3) answered
//     tarball / go-install / unknown, so a stale hint can never mask a later
//     Homebrew, winget or installer install.
//
// Every explicit source is validated against the closed set; an invalid value
// falls through to the next source and is never transmitted.
func resolveInstallChannel(d installChannelDeps) InstallChannel {
	if d.getenv != nil {
		if c := InstallChannel(strings.ToLower(strings.TrimSpace(d.getenv(InstallChannelEnvVar)))); c.Valid() && c != InstallChannelUnknown {
			return c
		}
	}
	if d.exeDir != nil {
		if dir := d.exeDir(); dir != "" {
			if InstallChannel(readChannelMarker(filepath.Join(dir, installChannelMarkerName), d.readFile)) == InstallChannelWinget {
				return InstallChannelWinget
			}
		}
	}
	detected := InstallChannelUnknown
	if d.detect != nil {
		detected = MapDetectorChannel(d.detect())
	}
	switch detected {
	case InstallChannelTarball, InstallChannelGoInstall, InstallChannelUnknown:
		if d.dataDir != "" &&
			InstallChannel(readChannelMarker(filepath.Join(d.dataDir, installChannelMarkerName), d.readFile)) == InstallChannelCurl {
			return InstallChannelCurl
		}
	}
	return detected
}

// readCapped reads a marker file safely. It only ever opens a REGULAR file
// (a FIFO or device would block or stall payload construction), follows a
// symlink only to a regular target, and detects overflow instead of accepting
// a truncated prefix: a file longer than max is an error, so an oversized file
// that merely starts with a valid token ("winget" + 4 KB of whitespace) is
// rejected rather than honoured.
func readCapped(path string, max int64) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errNotRegularMarker
	}
	if fi.Size() > max {
		return nil, errMarkerTooLarge
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, errMarkerTooLarge
	}
	return b, nil
}

var (
	errNotRegularMarker = errors.New("install-channel marker is not a regular file")
	errMarkerTooLarge   = errors.New("install-channel marker exceeds size cap")
)

// readChannelMarker returns the trimmed, lower-cased, size-capped marker
// token, or "" when it is absent, unreadable or empty. Validation against the
// closed set is the caller's job.
func readChannelMarker(path string, read func(string, int64) ([]byte, error)) string {
	if read == nil {
		return ""
	}
	b, err := read(path, markerMaxBytes)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(string(b)))
}

var (
	installChannelOnce sync.Once
	installChannelVal  InstallChannel
)

// CurrentInstallChannel resolves the channel for the running process once;
// provenance cannot change while the process lives, and caching keeps the
// marker reads off the heartbeat hot path. version is the build version the
// update checker also hands to DetectChannel; dataDir is the instance's data
// directory ("" means the default ~/.mcpproxy).
func CurrentInstallChannel(version, dataDir string) InstallChannel {
	installChannelOnce.Do(func() {
		if dataDir == "" {
			if home, err := os.UserHomeDir(); err == nil && home != "" {
				dataDir = filepath.Join(home, ".mcpproxy")
			}
		}
		installChannelVal = resolveInstallChannel(installChannelDeps{
			getenv: os.Getenv,
			exeDir: func() string {
				p, err := os.Executable()
				if err != nil {
					return ""
				}
				if r, err := filepath.EvalSymlinks(p); err == nil {
					p = r
				}
				return filepath.Dir(p)
			},
			dataDir:  dataDir,
			detect:   func() string { return updatecheck.DetectChannel(version) },
			readFile: readCapped,
		})
	})
	return installChannelVal
}

// installChannelResolver is indirected so payload tests can substitute the
// once-per-process resolver without touching the real executable path.
var installChannelResolver = CurrentInstallChannel
