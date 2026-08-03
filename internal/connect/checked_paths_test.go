package connect

import (
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
