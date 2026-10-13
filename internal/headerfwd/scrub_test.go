package headerfwd

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestScrub(t *testing.T) {
	long := `tenant"42\x`
	s := newSnapshot(map[string]string{"X-User-Id": "alice-wonder", "X-Tenant-Id": long, "X-Short": "ab"})
	allow := []string{"X-User-Id", "X-Tenant-Id", "X-Short"}
	cases := []struct{ name, in string }{
		{"exact", "error for alice-wonder happened"},
		{"json escaped", `{"v":"tenant\"42\\x"}`},
		{"name colon", "X-User-Id: alice-wonder"},
		{"name lower colon", "x-user-id:   alice-wonder"},
		{"json name", `{"X-User-Id" : "alice-wonder"}`},
		{"json name tight", `{"X-User-Id":"alice-wonder"}`},
		{"name equals", "X-User-Id=alice-wonder"},
		{"short value name-anchored", "X-Short: ab"},
		{"short json", `{"X-Short":"ab"}`},
		{"short equals upper", "X-SHORT=ab"},
	}
	for _, c := range cases {
		out := Scrub(c.in, s, allow)
		for _, v := range []string{"alice-wonder", "ab", `tenant"42`, `tenant\"42`} {
			if v == "ab" && !strings.Contains(c.name, "short") {
				continue
			}
			if strings.Contains(out, v) {
				t.Errorf("%s: %q still contains %q -> %q", c.name, c.in, v, out)
			}
		}
		if !strings.Contains(out, "[forwarded:") {
			t.Errorf("%s: no marker in %q", c.name, out)
		}
	}
}

func TestScrubBoundaries(t *testing.T) {
	s := newSnapshot(map[string]string{"X-User-Id": "alice-wonder", "X-Short": "ab"})
	// Short value outside a name-anchored form is not scrubbed (>=4 rule).
	if got := Scrub("about a cab", s, nil); got != "about a cab" {
		t.Errorf("short value scrubbed bare: %q", got)
	}
	// Base64 of the value is NOT caught (documented boundary).
	enc := base64.StdEncoding.EncodeToString([]byte("alice-wonder"))
	if got := Scrub("echo "+enc, s, nil); !strings.Contains(got, enc) {
		t.Errorf("base64 unexpectedly scrubbed: %q", got)
	}
	if Scrub("", s, nil) != "" || Scrub("plain", Snapshot{}, nil) != "plain" {
		t.Error("empty inputs")
	}
	if got := Scrub("no secrets here", s, nil); got != "no secrets here" {
		t.Errorf("changed clean text: %q", got)
	}
}

func TestScrubDollarInValue(t *testing.T) {
	s := newSnapshot(map[string]string{"X-A": "pa$$word1"})
	out := Scrub("X-A: pa$$word1 and pa$$word1", s, nil)
	if strings.Contains(out, "pa$$word1") || !strings.Contains(out, "[forwarded:X-A]") {
		t.Errorf("out = %q", out)
	}
}
