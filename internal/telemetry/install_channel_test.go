package telemetry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/updatecheck"
)

// fakeChannelDeps builds resolver dependencies from literal values so each
// table row flips exactly one signal (Spec 121 FR-031).
type fakeChannelDeps struct {
	env      string
	exeDir   string
	dataDir  string
	detected string
	files    map[string]string
}

func (f fakeChannelDeps) deps() installChannelDeps {
	return installChannelDeps{
		getenv:  func(string) string { return f.env },
		exeDir:  func() string { return f.exeDir },
		dataDir: f.dataDir,
		detect:  func() string { return f.detected },
		readFile: func(path string, max int64) ([]byte, error) {
			v, ok := f.files[path]
			if !ok {
				return nil, os.ErrNotExist
			}
			if int64(len(v)) > max {
				v = v[:max]
			}
			return []byte(v), nil
		},
	}
}

func TestMapDetectorChannel_Total(t *testing.T) {
	want := map[string]InstallChannel{
		updatecheck.ChannelHomebrew:         InstallChannelBrew,
		updatecheck.ChannelDMG:              InstallChannelDMG,
		updatecheck.ChannelDeb:              InstallChannelDeb,
		updatecheck.ChannelRPM:              InstallChannelRPM,
		updatecheck.ChannelDocker:           InstallChannelDocker,
		updatecheck.ChannelGoInstall:        InstallChannelGoInstall,
		updatecheck.ChannelWindowsInstaller: InstallChannelWindowsInstaller,
		updatecheck.ChannelScoop:            InstallChannelScoop,
		updatecheck.ChannelTarball:          InstallChannelTarball,
		updatecheck.ChannelUnknown:          InstallChannelUnknown,
		"":                                  InstallChannelUnknown,
		"flatpak":                           InstallChannelUnknown,
		"C:\\Users\\secret":                 InstallChannelUnknown,
	}
	for in, w := range want {
		if got := MapDetectorChannel(in); got != w {
			t.Errorf("MapDetectorChannel(%q) = %q, want %q", in, got, w)
		}
	}
}

func TestInstallChannelEnum_IsClosedAndComplete(t *testing.T) {
	all := AllInstallChannels()
	seen := map[InstallChannel]bool{}
	for _, c := range all {
		if seen[c] {
			t.Errorf("duplicate channel %q", c)
		}
		seen[c] = true
		if !c.Valid() {
			t.Errorf("%q must be Valid()", c)
		}
	}
	for _, must := range []InstallChannel{"brew", "dmg", "winget", "scoop", "windows-installer", "deb", "rpm", "docker", "registry", "curl", "tarball", "go-install", "unknown"} {
		if !seen[must] {
			t.Errorf("enum missing %q", must)
		}
	}
	if InstallChannel("flatpak").Valid() || InstallChannel("").Valid() {
		t.Error("out-of-set values must not be Valid()")
	}
}

func TestResolveInstallChannel_Precedence(t *testing.T) {
	// Built with filepath.Join: the resolver joins with the OS separator, so
	// literal "/" keys would never match on Windows.
	exeMarker := filepath.Join("/opt/app", "install-channel")
	dataMarker := filepath.Join("/data", "install-channel")
	tests := []struct {
		name string
		f    fakeChannelDeps
		want InstallChannel
	}{
		{"detector only", fakeChannelDeps{detected: updatecheck.ChannelHomebrew}, InstallChannelBrew},
		{"env beats detector (registry over docker)", fakeChannelDeps{env: "registry", detected: updatecheck.ChannelDocker}, InstallChannelRegistry},
		{"env beats exe marker", fakeChannelDeps{env: "curl", exeDir: "/opt/app", files: map[string]string{exeMarker: "winget"}, detected: updatecheck.ChannelWindowsInstaller}, InstallChannelCurl},
		{"env unknown ignored", fakeChannelDeps{env: "unknown", detected: updatecheck.ChannelDeb}, InstallChannelDeb},
		{"env garbage ignored", fakeChannelDeps{env: "C:\\Users\\secret", detected: updatecheck.ChannelDeb}, InstallChannelDeb},
		{"exe marker winget beats windows-installer detector", fakeChannelDeps{exeDir: "/opt/app", files: map[string]string{exeMarker: "winget\n"}, detected: updatecheck.ChannelWindowsInstaller}, InstallChannelWinget},
		{"exe marker only accepts winget", fakeChannelDeps{exeDir: "/opt/app", files: map[string]string{exeMarker: "curl"}, detected: updatecheck.ChannelWindowsInstaller}, InstallChannelWindowsInstaller},
		{"exe marker garbage falls through", fakeChannelDeps{exeDir: "/opt/app", files: map[string]string{exeMarker: strings.Repeat("x", 5000)}, detected: updatecheck.ChannelDMG}, InstallChannelDMG},
		{"weak marker curl applies to tarball", fakeChannelDeps{dataDir: "/data", files: map[string]string{dataMarker: "curl"}, detected: updatecheck.ChannelTarball}, InstallChannelCurl},
		{"weak marker curl applies to unknown", fakeChannelDeps{dataDir: "/data", files: map[string]string{dataMarker: "curl"}, detected: updatecheck.ChannelUnknown}, InstallChannelCurl},
		{"weak marker curl applies to go-install", fakeChannelDeps{dataDir: "/data", files: map[string]string{dataMarker: "curl"}, detected: updatecheck.ChannelGoInstall}, InstallChannelCurl},
		{"weak marker ignored for brew", fakeChannelDeps{dataDir: "/data", files: map[string]string{dataMarker: "curl"}, detected: updatecheck.ChannelHomebrew}, InstallChannelBrew},
		{"weak marker ignored for scoop", fakeChannelDeps{dataDir: "/data", files: map[string]string{dataMarker: "curl"}, detected: updatecheck.ChannelScoop}, InstallChannelScoop},
		{"weak marker ignored for windows-installer", fakeChannelDeps{dataDir: "/data", files: map[string]string{dataMarker: "curl"}, detected: updatecheck.ChannelWindowsInstaller}, InstallChannelWindowsInstaller},
		{"weak marker ignored for docker", fakeChannelDeps{dataDir: "/data", files: map[string]string{dataMarker: "curl"}, detected: updatecheck.ChannelDocker}, InstallChannelDocker},
		{"weak marker only accepts curl", fakeChannelDeps{dataDir: "/data", files: map[string]string{dataMarker: "winget"}, detected: updatecheck.ChannelTarball}, InstallChannelTarball},
		{"missing everything is unknown", fakeChannelDeps{detected: ""}, InstallChannelUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveInstallChannel(tt.f.deps()); got != tt.want {
				t.Errorf("resolve = %q, want %q", got, tt.want)
			}
		})
	}
}

// The marker reader must cap what it reads so a hostile or corrupt file can
// never allocate or leak beyond a single short token.
func TestReadChannelMarker_CapsAndTrims(t *testing.T) {
	t.Run("regular file ok", func(t *testing.T) { testMarkerBasics(t) })
}

func testMarkerBasics(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/install-channel"
	if err := os.WriteFile(path, []byte("  winget \r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readChannelMarker(path, readCapped); got != "winget" {
		t.Errorf("readChannelMarker = %q, want winget", got)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 1<<20)), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readChannelMarker(path, readCapped); got != "" {
		t.Errorf("oversized marker must be rejected, got %q", got)
	}
	// An oversized file that STARTS with a valid token must not be honoured.
	if err := os.WriteFile(path, []byte("winget"+strings.Repeat(" ", 4096)), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readChannelMarker(path, readCapped); got != "" {
		t.Errorf("oversized marker with valid prefix must be rejected, got %q", got)
	}
	if got := readChannelMarker(dir+"/absent", readCapped); got != "" {
		t.Errorf("absent marker = %q, want empty", got)
	}
}

// A FIFO must never block payload construction, and a directory is not a marker.
func TestReadChannelMarker_RejectsNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	if got := readChannelMarker(dir, readCapped); got != "" {
		t.Errorf("directory marker = %q, want empty", got)
	}
	fifo := dir + "/install-channel"
	if err := mkfifo(fifo); err != nil {
		t.Skipf("mkfifo unsupported here: %v", err)
	}
	done := make(chan string, 1)
	go func() { done <- readChannelMarker(fifo, readCapped) }()
	select {
	case got := <-done:
		if got != "" {
			t.Errorf("FIFO marker = %q, want empty", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reading a FIFO marker blocked")
	}
}

// A symlink to a regular file is followed; a symlink to a directory is not a marker.
func TestReadChannelMarker_Symlinks(t *testing.T) {
	dir := t.TempDir()
	real := dir + "/real"
	if err := os.WriteFile(real, []byte("winget"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := dir + "/link"
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if got := readChannelMarker(link, readCapped); got != "winget" {
		t.Errorf("symlink to regular file = %q, want winget", got)
	}
	dlink := dir + "/dlink"
	if err := os.Symlink(dir, dlink); err != nil {
		t.Skip()
	}
	if got := readChannelMarker(dlink, readCapped); got != "" {
		t.Errorf("symlink to directory = %q, want empty", got)
	}
}
