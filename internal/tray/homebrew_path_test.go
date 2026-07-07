//go:build !nogui && !headless && !linux

package tray

import (
	"os"
	"path/filepath"
	"testing"
)

// Spec 079 FR-011: Homebrew installs must keep self-update suppressed. The
// path check must catch every Homebrew layout, including the Intel-mac
// /usr/local/Cellar/ prefix that the pre-079 check missed.
func TestIsHomebrewPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"apple silicon prefix", "/opt/homebrew/bin/mcpproxy-tray", true},
		{"intel mac cellar", "/usr/local/Cellar/mcpproxy/0.47.0/bin/mcpproxy-tray", true},
		{"legacy Homebrew dir", "/usr/local/Homebrew/bin/mcpproxy-tray", true},
		{"linuxbrew", "/home/linuxbrew/.linuxbrew/bin/mcpproxy-tray", true},
		{"app bundle is not homebrew", "/Applications/MCPProxy.app/Contents/MacOS/mcpproxy-tray", false},
		{"home dir binary is not homebrew", "/Users/dev/bin/mcpproxy-tray", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isHomebrewPath(tt.path); got != tt.want {
				t.Errorf("isHomebrewPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// Intel-mac reality: /usr/local/bin/mcpproxy-tray is a symlink into the
// Cellar. The check must resolve symlinks before matching prefixes.
func TestIsHomebrewPath_ResolvesSymlinks(t *testing.T) {
	dir := t.TempDir()

	cellarDir := filepath.Join(dir, "usr", "local", "Cellar", "mcpproxy", "0.47.0", "bin")
	if err := os.MkdirAll(cellarDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(cellarDir, "mcpproxy-tray")
	if err := os.WriteFile(target, []byte("stub"), 0o755); err != nil { //nolint:gosec // test stub binary needs exec bit
		t.Fatal(err)
	}

	binDir := filepath.Join(dir, "usr", "local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(binDir, "mcpproxy-tray")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if !isHomebrewPath(link) {
		t.Errorf("isHomebrewPath(%q) = false, want true (symlink into Cellar)", link)
	}
}
