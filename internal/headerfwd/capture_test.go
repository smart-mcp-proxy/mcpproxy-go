package headerfwd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func union(names ...string) map[string]struct{} {
	m := map[string]struct{}{}
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}

func req(kv ...string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	for i := 0; i < len(kv); i += 2 {
		r.Header.Add(kv[i], kv[i+1])
	}
	return r
}

func TestCaptureUnionFilterAndDeny(t *testing.T) {
	r := req("X-User-Id", "alice", "X-Other", "nope", "Authorization", "Bearer x")
	s := Capture(r, union("x-user-id", "Authorization", "X-Missing"))
	if got := s.Names(); len(got) != 1 || got[0] != "X-User-Id" {
		t.Fatalf("names = %v", got)
	}
	if s.h["X-User-Id"] != "alice" {
		t.Fatal("value mismatch")
	}
}

func TestCaptureConnectionListed(t *testing.T) {
	r := req("Connection", "keep-alive, X-Hop", "X-Hop", "v", "X-Keep", "k")
	s := Capture(r, union("X-Hop", "X-Keep"))
	if s.Has("X-Hop") || !s.Has("X-Keep") {
		t.Fatalf("names = %v", s.Names())
	}
}

func TestCaptureMultiValueJoin(t *testing.T) {
	r := req("X-Roles", "a", "X-Roles", "b")
	if v := Capture(r, union("X-Roles")).h["X-Roles"]; v != "a, b" {
		t.Fatalf("v = %q", v)
	}
}

func TestCaptureLimits(t *testing.T) {
	r := req("X-Big", strings.Repeat("a", MaxValueBytes+1), "X-Ok", strings.Repeat("b", MaxValueBytes))
	s := Capture(r, union("X-Big", "X-Ok"))
	if s.Has("X-Big") || !s.Has("X-Ok") {
		t.Fatalf("names = %v", s.Names())
	}
	// Total cap: 5 * 4 KiB > 16 KiB.
	r = req()
	var names []string
	for i := 0; i < 5; i++ {
		n := "X-T" + string(rune('a'+i))
		names = append(names, n)
		r.Header.Set(n, strings.Repeat("c", MaxValueBytes))
	}
	if got := Capture(r, union(names...)).Len(); got != 4 {
		t.Fatalf("total cap kept %d, want 4", got)
	}
}

func TestCaptureControlCharsAndEmpty(t *testing.T) {
	r := req("X-Bad", "a\x01b", "X-Del", "a\x7fb", "X-Tab", "a\tb", "X-Empty", "")
	s := Capture(r, union("X-Bad", "X-Del", "X-Tab", "X-Empty"))
	if s.Len() != 1 || !s.Has("X-Tab") {
		t.Fatalf("names = %v", s.Names())
	}
}

func TestCaptureClones(t *testing.T) {
	r := req("X-User-Id", "alice")
	s := Capture(r, union("X-User-Id"))
	r.Header.Set("X-User-Id", "mallory")
	r.Header["X-User-Id"][0] = "mallory"
	if s.h["X-User-Id"] != "alice" {
		t.Fatal("snapshot aliased request header")
	}
}

func TestCaptureNilAndEmptyUnion(t *testing.T) {
	if !Capture(nil, union("X-A")).IsEmpty() || !Capture(req("X-A", "v"), nil).IsEmpty() {
		t.Fatal("expected empty")
	}
}
