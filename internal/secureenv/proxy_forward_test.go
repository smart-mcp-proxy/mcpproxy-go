package secureenv

import (
	"strings"
	"testing"
)

// envValue returns the value of key in a KEY=VALUE slice, and whether it was present.
func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return strings.TrimPrefix(e, prefix), true
		}
	}
	return "", false
}

func TestRedactProxyCredentials(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no userinfo http", "http://proxy.example.com:8080", "http://proxy.example.com:8080"},
		{"user and pass", "http://user:pass@proxy.example.com:8080", "http://proxy.example.com:8080"},
		{"user only", "https://user@proxy.example.com:3128", "https://proxy.example.com:3128"},
		{"socks scheme", "socks5://user:secret@127.0.0.1:1080", "socks5://127.0.0.1:1080"},
		{"no scheme host list", "localhost,127.0.0.1,.internal", "localhost,127.0.0.1,.internal"},
		{"empty", "", ""},
		{"unparseable kept", "://bad url with spaces", "://bad url with spaces"},
		// Schemeless proxy values: url.Parse misreads "user" as the scheme and
		// leaves Userinfo nil, so naive redaction would leak the credentials
		// (Codex PR #704 review). Must still be stripped.
		{"schemeless user and pass", "user:pass@proxy.example.com:8080", "proxy.example.com:8080"},
		{"schemeless user only", "user@proxy.example.com:3128", "proxy.example.com:3128"},
		{"schemeless no userinfo", "proxy.example.com:8080", "proxy.example.com:8080"},
		{"schemeless at-in-path not stripped", "proxy.example.com:8080/path@x", "proxy.example.com:8080/path@x"},
		// Whitespace-wrapped credentialed URLs would otherwise make url.Parse
		// error and fall through, forwarding creds verbatim (PR #704 non-blocking
		// review note). Surrounding whitespace is trimmed before redaction.
		{"whitespace-wrapped scheme creds", "  http://user:pass@proxy.example.com:8080  ", "http://proxy.example.com:8080"},
		{"whitespace-wrapped schemeless creds", "\tuser:pass@proxy.example.com:8080\n", "proxy.example.com:8080"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactProxyCredentials(tc.in)
			if got != tc.want {
				t.Fatalf("redactProxyCredentials(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// By default (no opt-in) proxy vars in the process environment MUST NOT be
// forwarded to upstream spawns — this is the credential-leak guard (MCP-2769).
func TestBuildSecureEnvironment_ProxyForwardingDisabledByDefault(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://user:pass@proxy.example.com:8080")
	t.Setenv("HTTP_PROXY", "http://user:pass@proxy.example.com:8080")
	t.Setenv("NO_PROXY", "localhost")

	m := NewManager(&EnvConfig{InheritSystemSafe: true, AllowedSystemVars: DefaultEnvConfig().AllowedSystemVars})
	env := m.BuildSecureEnvironment()

	for _, key := range []string{"HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY"} {
		if _, ok := envValue(env, key); ok {
			t.Errorf("proxy var %s leaked to upstream env without opt-in", key)
		}
	}
}

// When opted in, proxy vars are forwarded with credentials redacted.
func TestBuildSecureEnvironment_ForwardsProxyWhenOptedIn(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://user:pass@proxy.example.com:8080")
	t.Setenv("NO_PROXY", "localhost,127.0.0.1")

	// InheritSystemSafe:false proves forwarding is an independent explicit opt-in,
	// not a side effect of the system-env allow-list.
	m := NewManager(&EnvConfig{InheritSystemSafe: false, ForwardProxyEnv: true})
	env := m.BuildSecureEnvironment()

	got, ok := envValue(env, "HTTPS_PROXY")
	if !ok {
		t.Fatalf("HTTPS_PROXY not forwarded when opted in; env=%v", env)
	}
	if want := "http://proxy.example.com:8080"; got != want {
		t.Errorf("HTTPS_PROXY = %q, want redacted %q", got, want)
	}
	if got, ok := envValue(env, "NO_PROXY"); !ok || got != "localhost,127.0.0.1" {
		t.Errorf("NO_PROXY = %q (present=%v), want %q unchanged", got, ok, "localhost,127.0.0.1")
	}
}

// A schemeless proxy value carrying credentials must not be forwarded verbatim.
func TestBuildSecureEnvironment_ForwardsSchemelessProxyRedacted(t *testing.T) {
	t.Setenv("HTTP_PROXY", "user:pass@proxy.example.com:8080")

	m := NewManager(&EnvConfig{InheritSystemSafe: false, ForwardProxyEnv: true})
	got, ok := envValue(m.BuildSecureEnvironment(), "HTTP_PROXY")
	if !ok {
		t.Fatal("HTTP_PROXY not forwarded when opted in")
	}
	if strings.Contains(got, "pass") || strings.Contains(got, "@") {
		t.Errorf("schemeless proxy credentials leaked to upstream: %q", got)
	}
	if want := "proxy.example.com:8080"; got != want {
		t.Errorf("HTTP_PROXY = %q, want redacted %q", got, want)
	}
}

// Case-alias awareness: an explicitly-configured spelling must win and suppress
// forwarding of the other-cased spelling from the ambient environment.
func TestBuildSecureEnvironment_ProxyExplicitValueWinsOverAlias(t *testing.T) {
	t.Setenv("http_proxy", "http://ambient:leak@proxy.example.com:8080")

	m := NewManager(&EnvConfig{
		InheritSystemSafe: false,
		ForwardProxyEnv:   true,
		CustomVars:        map[string]string{"HTTP_PROXY": "http://explicit.example.com:3128"},
	})
	env := m.BuildSecureEnvironment()

	if got, ok := envValue(env, "HTTP_PROXY"); !ok || got != "http://explicit.example.com:3128" {
		t.Errorf("HTTP_PROXY = %q (present=%v), want explicit custom value", got, ok)
	}
	if _, ok := envValue(env, "http_proxy"); ok {
		t.Error("lowercase http_proxy alias was forwarded despite explicit HTTP_PROXY being set")
	}
}
