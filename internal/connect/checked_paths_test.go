package connect

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A "No config found" row must be able to name the exact files that were
// looked for — otherwise a user whose opencode.jsonc exists cannot tell
// whether mcpproxy checked the wrong file or could not see it.

func statusFor(t *testing.T, statuses []ClientStatus, id string) ClientStatus {
	t.Helper()
	for _, st := range statuses {
		if st.ID == id {
			return st
		}
	}
	t.Fatalf("client %q not in status list", id)
	return ClientStatus{}
}

func TestGetAllStatusReportsCheckedPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", "")
	}
	home := t.TempDir()
	s := NewServiceWithHome("127.0.0.1:8080", "key", home)
	statuses := s.GetAllStatus()

	// OpenCode consults both candidates, .jsonc first (it shadows .json), even
	// when neither exists — the row must say so instead of naming only the
	// create-new default.
	oc := statusFor(t, statuses, "opencode")
	want := opencodeConfigCandidates(home)
	if len(oc.CheckedPaths) != len(want) {
		t.Fatalf("opencode checked_paths = %v, want %v", oc.CheckedPaths, want)
	}
	for i := range want {
		if oc.CheckedPaths[i] != want[i] {
			t.Fatalf("opencode checked_paths[%d] = %s, want %s", i, oc.CheckedPaths[i], want[i])
		}
	}
	if filepath.Base(oc.CheckedPaths[0]) != "opencode.jsonc" {
		t.Fatalf("opencode checked_paths[0] = %s, want opencode.jsonc first", oc.CheckedPaths[0])
	}

	// A single-file client reports exactly its one path.
	cursor := statusFor(t, statuses, "cursor")
	if len(cursor.CheckedPaths) != 1 || cursor.CheckedPaths[0] != ConfigPath("cursor", home) {
		t.Fatalf("cursor checked_paths = %v, want [%s]", cursor.CheckedPaths, ConfigPath("cursor", home))
	}
}

// Production Services are built without a homeDir (NewService); the opencode
// candidates must still resolve to absolute paths, or both the jsonc
// preference and checked_paths silently degrade to CWD-relative stats.
func TestOpencodePathsAreAbsoluteWithoutAnExplicitHome(t *testing.T) {
	if _, err := os.UserHomeDir(); err != nil {
		t.Skip("no resolvable home directory")
	}
	s := NewService("127.0.0.1:8080", "key")

	paths := s.checkedPaths("opencode")
	if len(paths) != 2 {
		t.Fatalf("checked paths = %v, want both opencode candidates", paths)
	}
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			t.Fatalf("checked path %q is CWD-relative — empty homeDir must resolve via os.UserHomeDir", p)
		}
	}
	if p := s.configPath("opencode"); !filepath.IsAbs(p) {
		t.Fatalf("configPath %q is CWD-relative — the jsonc preference never fires in production", p)
	}
}

func TestGetStatusReportsCheckedPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", "")
	}
	home := t.TempDir()
	s := NewServiceWithHome("127.0.0.1:8080", "key", home)

	st, err := s.GetStatus("opencode")
	if err != nil {
		t.Fatal(err)
	}
	want := opencodeConfigCandidates(home)
	if len(st.CheckedPaths) != len(want) {
		t.Fatalf("opencode checked_paths = %v, want %v", st.CheckedPaths, want)
	}
	for i := range want {
		if st.CheckedPaths[i] != want[i] {
			t.Fatalf("opencode checked_paths[%d] = %s, want %s", i, st.CheckedPaths[i], want[i])
		}
	}
}
