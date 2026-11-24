package connect

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeCfg(t *testing.T, path string, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

// Issue #1957: Claude Desktop installed via WinGet keeps its config under
// %LOCALAPPDATA%\Claude-Data\claude_<id>\, not %APPDATA%\Claude.
func TestClaudeDesktopWindowsConfigPath(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name  string
		setup func(appData, local string) string // returns expected path
	}{
		{"nothing installed falls back to roaming", func(a, l string) string {
			return filepath.Join(a, "Claude", "claude_desktop_config.json")
		}},
		{"only roaming exists", func(a, l string) string {
			p := filepath.Join(a, "Claude", "claude_desktop_config.json")
			writeCfg(t, p, now)
			return p
		}},
		{"only Claude-Data exists", func(a, l string) string {
			p := filepath.Join(l, "Claude-Data", "claude_pzs8sxrjxfjjc", "claude_desktop_config.json")
			writeCfg(t, p, now)
			return p
		}},
		{"Claude-Data wins over roaming", func(a, l string) string {
			writeCfg(t, filepath.Join(a, "Claude", "claude_desktop_config.json"), now)
			p := filepath.Join(l, "Claude-Data", "claude_pzs8sxrjxfjjc", "claude_desktop_config.json")
			writeCfg(t, p, now.Add(-time.Hour))
			return p
		}},
		{"mixed-case Claude_ directory (Windows is case-insensitive)", func(a, l string) string {
			p := filepath.Join(l, "Claude-Data", "Claude_ABC", "claude_desktop_config.json")
			writeCfg(t, p, now)
			return p
		}},
		{"newest of several Claude-Data dirs", func(a, l string) string {
			writeCfg(t, filepath.Join(l, "Claude-Data", "claude_old", "claude_desktop_config.json"), now.Add(-time.Hour))
			p := filepath.Join(l, "Claude-Data", "claude_new", "claude_desktop_config.json")
			writeCfg(t, p, now)
			return p
		}},
		{"Claude-Data dir without config is ignored", func(a, l string) string {
			if err := os.MkdirAll(filepath.Join(l, "Claude-Data", "claude_x"), 0o755); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(a, "Claude", "claude_desktop_config.json")
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			appData := filepath.Join(root, "Roaming")
			// '[' is legal in a Windows user name and is a glob metacharacter.
			local := filepath.Join(root, "Local[x]")
			want := tc.setup(appData, local)
			if got := claudeDesktopWindowsConfigPath(root, appData, local); got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}

	// ConfigPath's Windows branch must go through the resolver.
	t.Run("ConfigPath wiring", func(t *testing.T) {
		old := connectGOOS
		connectGOOS = "windows"
		t.Cleanup(func() { connectGOOS = old })

		home := t.TempDir()
		appData := filepath.Join(home, "Roaming")
		local := filepath.Join(home, "Local[x]")
		t.Setenv("APPDATA", appData)
		t.Setenv("LOCALAPPDATA", local)

		roaming := filepath.Join(appData, "Claude", "claude_desktop_config.json")
		if got := ConfigPath("claude-desktop", home); got != roaming {
			t.Fatalf("got %q, want %q", got, roaming)
		}
		want := filepath.Join(local, "Claude-Data", "claude_pzs8sxrjxfjjc", "claude_desktop_config.json")
		writeCfg(t, want, now.Add(-time.Hour))
		writeCfg(t, roaming, now) // roaming newer: Claude-Data must still win
		if got := ConfigPath("claude-desktop", home); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("empty env falls back under home", func(t *testing.T) {
		home := t.TempDir()
		want := filepath.Join(home, "AppData", "Roaming", "Claude", "claude_desktop_config.json")
		if got := claudeDesktopWindowsConfigPath(home, "", ""); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		p := filepath.Join(home, "AppData", "Local", "Claude-Data", "claude_a", "claude_desktop_config.json")
		writeCfg(t, p, now)
		if got := claudeDesktopWindowsConfigPath(home, "", ""); got != p {
			t.Fatalf("got %q, want %q", got, p)
		}
	})
}
