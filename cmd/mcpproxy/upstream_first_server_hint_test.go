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
			got := firstServerHint(fakeLookPath(c.present...))
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
