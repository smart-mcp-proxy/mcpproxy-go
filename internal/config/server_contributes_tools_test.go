package config

import "testing"

// Issue #1064: "available tools" had three independent enabled-only predicates
// and three surfaces with no predicate at all, so a quarantined server -- which
// deliberately stays connected and ready so the tray and Web UI can inspect it
// for review -- kept reporting its tools as available while every call to them
// was refused and the search index correctly held nothing.
//
// This truth table is the mirror of runtime.serverEligibleForIndexing's
// (TestServerEligibleForIndexing in internal/runtime): the security predicate
// that decides what may be INDEXED and the availability predicate that decides
// what is COUNTED must give the same answer for the same server, or the two
// surfaces disagree again.
func TestServerContributesTools(t *testing.T) {
	cases := []struct {
		name        string
		enabled     bool
		quarantined bool
		want        bool
	}{
		{"enabled and trusted", true, false, true},
		{"enabled but quarantined", true, true, false},
		{"disabled", false, false, false},
		{"disabled and quarantined", false, true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ServerContributesTools(tc.enabled, tc.quarantined); got != tc.want {
				t.Fatalf("ServerContributesTools(enabled=%v, quarantined=%v) = %v, want %v",
					tc.enabled, tc.quarantined, got, tc.want)
			}

			sc := &ServerConfig{Name: tc.name, Enabled: tc.enabled, Quarantined: tc.quarantined}
			if got := sc.ContributesTools(); got != tc.want {
				t.Fatalf("(%+v).ContributesTools() = %v, want %v", sc, got, tc.want)
			}
		})
	}
}

func TestServerContributesToolsNilReceiver(t *testing.T) {
	var sc *ServerConfig
	if sc.ContributesTools() {
		t.Fatal("a nil server config must contribute no tools")
	}
}
