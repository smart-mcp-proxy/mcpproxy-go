package connect

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestConfigPathRejectsUnknownAndTraversalClientIDs pins the invariant that
// makes CodeQL alert #164 (go/path-injection on the read/stat seams) a false
// positive: the client ID reaching those seams comes from a URL path parameter
// (`chi.URLParam(r, "client")`), but it is never joined into a path directly.
// ConfigPath switches on the closed set of registry IDs and returns "" for
// anything else, so an attacker-supplied ID cannot name a file.
//
// If someone later replaces the switch with a lookup that interpolates the ID
// (e.g. filepath.Join(home, clientID+".json")), this test fails and the alert
// stops being a false positive — which is exactly when we want to hear about it.
func TestConfigPathRejectsUnknownAndTraversalClientIDs(t *testing.T) {
	hostile := []string{
		"../../../../etc/passwd",
		"..%2f..%2fetc%2fpasswd",
		"claude-code/../../../etc/passwd",
		"./../.ssh/id_rsa",
		"/etc/passwd",
		"bogus",
		"",
	}

	for _, id := range hostile {
		if got := ConfigPath(id, "/home/u"); got != "" {
			t.Errorf("ConfigPath(%q) = %q, want \"\" — an unknown client ID must never name a file", id, got)
		}
	}
}

// TestConfigPathKnownClientsCannotEscapeTheirRoot is the positive half: every
// registry client resolves to a clean, traversal-free path, and any client
// whose location derives from the supplied home directory stays inside it.
//
// Not every client derives from home — on Windows several correctly resolve to
// %APPDATA% / %LOCALAPPDATA% instead — so membership is detected rather than
// assumed: call ConfigPath with two different homes and see whether the answer
// moves. That keeps the assertion meaningful on every platform instead of
// encoding Unix layout.
func TestConfigPathKnownClientsCannotEscapeTheirRoot(t *testing.T) {
	homeA := filepath.Join(string(filepath.Separator), "home", "a")
	homeB := filepath.Join(string(filepath.Separator), "home", "b")

	for _, def := range GetAllClients() {
		path := ConfigPath(def.ID, homeA)
		if path == "" {
			continue // not supported on this platform
		}

		if path != filepath.Clean(path) {
			t.Errorf("ConfigPath(%q) = %q, want a cleaned path", def.ID, path)
		}
		for _, seg := range strings.Split(path, string(filepath.Separator)) {
			if seg == ".." {
				t.Errorf("ConfigPath(%q) = %q, want no parent-directory segments", def.ID, path)
			}
		}

		// Home-derived? Then it must stay under the home it was given.
		if ConfigPath(def.ID, homeB) == path {
			continue // OS-standard location (e.g. %APPDATA%), independent of home
		}
		rel, err := filepath.Rel(homeA, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Errorf("ConfigPath(%q) = %q, want a path under %q (rel=%q, err=%v)", def.ID, path, homeA, rel, err)
		}
	}
}
