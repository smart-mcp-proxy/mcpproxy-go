package mcplogin

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

type resCase struct {
	id      string
	generic bool
	ok      bool
}

func TestOrigin(t *testing.T) {
	r := httptest.NewRequest("GET", "/x", nil)
	r.Host = "127.0.0.1:8080"
	if Origin(r) != "http://127.0.0.1:8080" {
		t.Fatal(Origin(r))
	}
	r.TLS = &tls.ConnectionState{}
	r.Host = "LocalHost:8443"
	if Origin(r) != "https://localhost:8443" {
		t.Fatal(Origin(r))
	}
	if Origin(&http.Request{Host: "a"}) != "http://a" {
		t.Fatal("plain request")
	}
}

func TestResourcePath(t *testing.T) {
	if ResourcePath("") != "/mcp/oauth" || ResourcePath("cursor") != "/mcp/oauth/cursor" {
		t.Fatal("ResourcePath")
	}
}

func TestParseResourcePath(t *testing.T) {
	cases := map[string]resCase{
		"/mcp/oauth":          {"", true, true},
		"/mcp/oauth/":         {"", true, true},
		"/mcp/oauth/cursor":   {"cursor", false, true},
		"/mcp/oauth/cursor/":  {"cursor", false, true},
		"/mcp/oauth/cursor/x": {},
		"/mcp/oauth//":        {},
		"/mcp/oauth/Bad":      {},
		"/mcp/oauth/-x":       {},
		"/mcp/oauthx":         {},
		"/mcp":                {},
		"/mcp/oauth/a/b/c":    {},
		"":                    {},
	}
	for p, want := range cases {
		id, gen, ok := ParseResourcePath(p)
		if (resCase{id, gen, ok}) != want {
			t.Errorf("%q: got %q %v %v", p, id, gen, ok)
		}
	}
}

func TestParseResource(t *testing.T) {
	o := "http://127.0.0.1:8080"
	cases := map[string]resCase{
		o + "/mcp/oauth":                   {"", true, true},
		o + "/mcp/oauth/cursor":            {"cursor", false, true},
		o + "/mcp/oauth/cursor/":           {"cursor", false, true},
		"http://localhost:8080/mcp/oauth":  {},
		"https://127.0.0.1:8080/mcp/oauth": {},
		o + "/mcp/oauth?x=1":               {},
		o + "/mcp/oauth?":                  {},
		o + "/mcp/oauth#f":                 {},
		o + "/mcp/oauth/cursor/x":          {},
		o + "/mcp":                         {},
		"/mcp/oauth":                       {},
		"::bad":                            {},
		"":                                 {},
	}
	for in, want := range cases {
		id, gen, ok := ParseResource(o, in)
		if (resCase{id, gen, ok}) != want {
			t.Errorf("%q: got %q %v %v", in, id, gen, ok)
		}
	}
}
