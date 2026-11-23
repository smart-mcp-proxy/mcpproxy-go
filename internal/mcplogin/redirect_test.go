package mcplogin

import (
	"strings"
	"testing"
)

func TestValidateRegisteredRedirects(t *testing.T) {
	ok := [][]string{
		{"http://127.0.0.1/cb"},
		{"http://127.0.0.1:8123/cb?x=1"},
		{"http://[::1]:9/cb"},
		{"http://localhost:1/cb"},
		{"https://example.com/cb"},
		{"http://127.0.0.1/a", "https://example.com/b"},
		{"https://a/1", "https://a/2", "https://a/3", "https://a/4", "https://a/5"},
	}
	for _, c := range ok {
		if err := ValidateRegisteredRedirects(c); err != nil {
			t.Errorf("%v: %v", c, err)
		}
	}
	bad := [][]string{
		nil,
		{},
		{"https://a/1", "https://a/2", "https://a/3", "https://a/4", "https://a/5", "https://a/6"},
		{"http://example.com/cb"},
		{"http://127.0.0.2/cb"},
		{"http://localhost.evil.com/cb"},
		{"cursor://callback"},
		{"vscode://x/y"},
		{"https://example.com/cb#frag"},
		{"http://127.0.0.1/cb#"},
		{"https://user:pw@example.com/cb"},
		{"https:///cb"},
		{"/relative"},
		{""},
		{"https://example.com/cb", "bad"},
	}
	for _, c := range bad {
		if err := ValidateRegisteredRedirects(c); err == nil {
			t.Errorf("%v accepted", c)
		}
	}
}

func TestMatchRedirect(t *testing.T) {
	reg := []string{"http://127.0.0.1/cb", "https://app.example.com/cb", "http://localhost/x?y=1", "http://[::1]/v6"}
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"http://127.0.0.1:54321/cb", "http://127.0.0.1:54321/cb", true},
		{"http://127.0.0.1/cb", "http://127.0.0.1/cb", true},
		{"http://127.0.0.1:54321/other", "", false},
		{"http://localhost:1/x?y=1", "http://localhost:1/x?y=1", true},
		{"http://localhost:1/x?y=2", "", false},
		{"http://localhost:1/x", "", false},
		{"http://[::1]:7/v6", "http://[::1]:7/v6", true},
		{"http://127.0.0.1:1/cb#f", "", false},
		{"https://app.example.com/cb", "https://app.example.com/cb", true},
		{"https://app.example.com:444/cb", "", false},
		{"https://app.example.com/cb/", "", false},
		{"http://app.example.com/cb", "", false},
		{"https://127.0.0.1/cb", "", false},
		{"http://localhost:1/cb", "", false},
		{"", "", false},
		{"garbage", "", false},
	}
	for _, c := range cases {
		got, ok := MatchRedirect(reg, c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("%q: got %q %v", c.in, got, ok)
		}
	}
}

func TestBuildRedirect(t *testing.T) {
	got, err := BuildRedirect("http://127.0.0.1:5/cb?keep=1", map[string]string{"code": "abc", "state": "a b&c=d/é"})
	if err != nil {
		t.Fatal(err)
	}
	want := "http://127.0.0.1:5/cb?keep=1&code=abc&state=a+b%26c%3Dd%2F%C3%A9"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	got, _ = BuildRedirect("https://a/cb", map[string]string{"state": "s", "error": "access_denied"})
	if got != "https://a/cb?error=access_denied&state=s" {
		t.Fatalf("order: %s", got)
	}
	got, _ = BuildRedirect("https://a/cb", map[string]string{"code": "c"})
	if got != "https://a/cb?code=c" {
		t.Fatalf("no state: %s", got)
	}
	got, _ = BuildRedirect("https://a/cb", map[string]string{"code": "c", "state": ""})
	if got != "https://a/cb?code=c&state=" {
		t.Fatalf("empty passed key kept: %s", got)
	}
	if _, err := BuildRedirect("::bad", nil); err == nil {
		t.Fatal("bad base")
	}
	if got, _ := BuildRedirect("https://a/cb", nil); !strings.HasPrefix(got, "https://a/cb") {
		t.Fatal(got)
	}
}
