package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The deb/rpm package ships /etc/mcpproxy/mcp_config.json as 0640
// root:mcpproxy so members of the mcpproxy group can manage the service with
// `mcpproxy --system` (GH discussion #1678). A save (the service persisting
// its generated API key, or `sudo mcpproxy --system ...`) must keep that group
// read, or the group route stops working and a root-written file becomes
// unreadable to the service. Anything else keeps the 0600 default.
func TestSaveConfigKeepsDeliberateGroupRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	tests := []struct {
		name     string
		existing os.FileMode // 0 = no file yet
		want     os.FileMode
	}{
		{"new file", 0, 0o600},
		{"owner only", 0o600, 0o600},
		{"group read, nothing for others", 0o640, 0o640},
		{"group read+write is not widened to the save", 0o660, 0o600},
		{"world readable is tightened", 0o644, 0o600},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp_config.json")
			if tc.existing != 0 {
				if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, tc.existing); err != nil {
					t.Fatal(err)
				}
			}
			if err := SaveConfig(DefaultConfig(), path); err != nil {
				t.Fatalf("SaveConfig: %v", err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != tc.want {
				t.Errorf("mode = %#o, want %#o", got, tc.want)
			}
		})
	}
}
