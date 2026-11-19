package main

import (
	"errors"
	"strings"
	"testing"
)

func fakeLookPath(present ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		for _, p := range present {
			if p == name {
				return "/usr/local/bin/" + name, nil
			}
		}
		return "", errors.New("not found")
	}
}

// A fresh install that runs `mcpproxy upstream list` used to get only "No
// results found": a dead end. The hint names one credential-free reference
// server through the catalog door (so it is quarantined and scanned like any
// catalog add), picked by which launcher this machine has, plus the remote
// route. It never adds or approves anything itself.
func TestFirstServerHint(t *testing.T) {
	cases := []struct {
		name    string
		present []string
		want    []string
		mustNot []string
	}{
		{"uvx", []string{"uvx", "npx"}, []string{"mcpproxy catalog add reference/time", "uvx mcp-server-time", "mcpproxy upstream add <name> <url>", "review"}, []string{"sequentialthinking"}},
		{"npx only", []string{"npx"}, []string{"mcpproxy catalog add reference/sequentialthinking", "mcpproxy upstream add <name> <url>"}, []string{"reference/time"}},
		{"no launcher", nil, []string{"mcpproxy upstream add <name> <url>", "uv", "Node.js"}, []string{"catalog add reference/"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := firstServerHint(fakeLookPath(c.present...), true)
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("hint missing %q:\n%s", w, got)
				}
			}
			for _, m := range c.mustNot {
				if strings.Contains(got, m) {
					t.Errorf("hint must not contain %q:\n%s", m, got)
				}
			}
		})
	}
}

// Without a running daemon, 'catalog add' and 'review' cannot work yet, so
// the hint starts with 'mcpproxy serve'.
func TestFirstServerHint_NoDaemonStartsWithServe(t *testing.T) {
	got := firstServerHint(fakeLookPath("uvx"), false)
	serve := strings.Index(got, "mcpproxy serve")
	add := strings.Index(got, "mcpproxy catalog add reference/time")
	if serve < 0 || add < 0 || serve > add {
		t.Errorf("no-daemon hint must name 'mcpproxy serve' before the catalog add:\n%s", got)
	}
	if strings.Contains(firstServerHint(fakeLookPath("uvx"), true), "mcpproxy serve") {
		t.Errorf("with a daemon running the hint must not tell the user to start one")
	}
}

// A scoped caller (an agent token) sees only the servers it may see; an
// empty list is not an empty install, and catalog add is not its operation.
func TestOutputServers_ScopedCallerGetsNoFirstServerHint(t *testing.T) {
	setOutputGlobals(t, "table", false)
	old := upstreamListSource
	upstreamListSource = listOriginDaemonScoped
	t.Cleanup(func() { upstreamListSource = old })
	out := captureOutput(func() {
		if err := outputServers([]map[string]interface{}{}); err != nil {
			t.Fatalf("outputServers: %v", err)
		}
	})
	if strings.Contains(out, "No upstream servers yet") {
		t.Errorf("a scoped caller must not get the empty-install hint, got:\n%s", out)
	}

	upstreamListSource = listOriginConfig
	out = captureOutput(func() {
		if err := outputServers([]map[string]interface{}{}); err != nil {
			t.Fatalf("outputServers: %v", err)
		}
	})
	if !strings.Contains(out, "mcpproxy serve") {
		t.Errorf("config-file mode (no daemon) must tell the user to start the core, got:\n%s", out)
	}
}

func TestAgentTokenInEnv(t *testing.T) {
	t.Setenv("MCPPROXY_API_KEY_FILE", "")
	t.Setenv("MCPPROXY_API_KEY", "mcp_agt_abcdef")
	if !agentTokenInEnv() {
		t.Errorf("an mcp_agt_ key in MCPPROXY_API_KEY is an agent token")
	}
	t.Setenv("MCPPROXY_API_KEY", "plain-admin-key")
	if agentTokenInEnv() {
		t.Errorf("an admin key is not an agent token")
	}
}

func TestOutputServers_EmptyListPrintsFirstServerHint(t *testing.T) {
	setOutputGlobals(t, "table", false)
	out := captureOutput(func() {
		if err := outputServers([]map[string]interface{}{}); err != nil {
			t.Fatalf("outputServers: %v", err)
		}
	})
	if !strings.Contains(out, "No upstream servers yet") {
		t.Errorf("empty list must print the first-server hint, got:\n%s", out)
	}

	// A --profile list is only that profile's servers: an empty one is not
	// an empty install.
	oldProfile := upstreamListProfile
	upstreamListProfile = "work"
	t.Cleanup(func() { upstreamListProfile = oldProfile })
	out = captureOutput(func() {
		if err := outputServers([]map[string]interface{}{}); err != nil {
			t.Fatalf("outputServers: %v", err)
		}
	})
	if strings.Contains(out, "No upstream servers yet") {
		t.Errorf("an empty --profile list must not print the first-server hint, got:\n%s", out)
	}
	upstreamListProfile = oldProfile

	// Structured output stays pure data.
	setOutputGlobals(t, "json", false)
	out = captureOutput(func() {
		if err := outputServers([]map[string]interface{}{}); err != nil {
			t.Fatalf("outputServers: %v", err)
		}
	})
	if strings.Contains(out, "No upstream servers yet") {
		t.Errorf("json output must not carry the hint, got:\n%s", out)
	}
}
