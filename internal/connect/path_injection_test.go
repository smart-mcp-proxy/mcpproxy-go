package connect

import (
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

// TestConfigPathKeepsKnownClientsInsideHome is the positive half: every
// registry client resolves under the supplied home directory, so no known ID
// escapes it either.
func TestConfigPathKeepsKnownClientsInsideHome(t *testing.T) {
	const home = "/home/u"

	for _, def := range GetAllClients() {
		path := ConfigPath(def.ID, home)
		if path == "" {
			continue // not supported on this platform
		}
		if !strings.HasPrefix(path, home+"/") {
			t.Errorf("ConfigPath(%q) = %q, want a path under %q", def.ID, path, home)
		}
		if strings.Contains(path, "..") {
			t.Errorf("ConfigPath(%q) = %q, want no parent-directory segments", def.ID, path)
		}
	}
}
