package updatecheck

import "testing"

// Spec 121 FR-016: a scoop-owned binary must be recognised as its own channel.
// The Windows release zip scoop installs is stamped "tarball" (weak), which
// would otherwise let `mcpproxy update` self-replace a package-manager-owned
// file and desynchronise scoop's version/hash bookkeeping.
func TestDetectChannel_Scoop(t *testing.T) {
	tests := []struct {
		name   string
		marker string
		path   string
		want   string
	}{
		{"scoop current shim target", ChannelTarball, `C:\Users\u\scoop\apps\mcpproxy\current\mcpproxy.exe`, ChannelScoop},
		{"scoop versioned dir", ChannelTarball, `C:\Users\u\scoop\apps\mcpproxy\0.71.1\mcpproxy.exe`, ChannelScoop},
		{"scoop global install", ChannelTarball, `C:\ProgramData\scoop\apps\mcpproxy\current\mcpproxy.exe`, ChannelScoop},
		{"mixed case and slashes", ChannelTarball, `c:/Users/U/Scoop/Apps/MCPProxy/current/mcpproxy.exe`, ChannelScoop},
		{"unstamped build still scoop", "", `C:\Users\u\scoop\apps\mcpproxy\current\mcpproxy.exe`, ChannelScoop},
		{"other scoop app is not mcpproxy", ChannelTarball, `C:\Users\u\scoop\apps\othertool\current\mcpproxy.exe`, ChannelTarball},
		{"plain extracted zip stays tarball", ChannelTarball, `C:\tools\mcpproxy\mcpproxy.exe`, ChannelTarball},
		{"program files installer stays unknown without marker", "", `C:\Program Files\MCPProxy\mcpproxy.exe`, ChannelUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := testDetector()
			d.goos = "windows"
			d.marker = tt.marker
			path := tt.path
			d.execPath = func() (string, error) { return path, nil }
			if got := d.detect(); got != tt.want {
				t.Errorf("detect() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A scoop path must not override the absolute markers: a windows-installer
// binary relocated under a scoop-looking directory is still the installer.
func TestDetectChannel_ScoopDoesNotBeatAbsoluteMarker(t *testing.T) {
	d := testDetector()
	d.goos = "windows"
	d.marker = ChannelWindowsInstaller
	d.execPath = func() (string, error) { return `C:\Users\u\scoop\apps\mcpproxy\current\mcpproxy.exe`, nil }
	if got := d.detect(); got != ChannelWindowsInstaller {
		t.Errorf("detect() = %q, want %q", got, ChannelWindowsInstaller)
	}
}

func TestScoopKnownChannel(t *testing.T) {
	if !knownChannels[ChannelScoop] {
		t.Fatal("scoop must be a recognised build marker/channel")
	}
}
