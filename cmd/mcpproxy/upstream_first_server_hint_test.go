package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
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

// The list paths themselves set the origin: config-file mode (no daemon)
// tells the user to start the core; client mode decides from the key the
// client actually sends (no key = socket = administrator; an mcp_agt_ key =
// a scoped agent token, which gets no hint).
func TestUpstreamListPaths_SetTheHintOrigin(t *testing.T) {
	setOutputGlobals(t, "table", false)
	old := upstreamListSource
	t.Cleanup(func() { upstreamListSource = old })

	out := captureOutput(func() {
		if err := runUpstreamListFromConfig(&config.Config{}); err != nil {
			t.Fatalf("runUpstreamListFromConfig: %v", err)
		}
	})
	if !strings.Contains(out, "No upstream servers yet") || !strings.Contains(out, "mcpproxy serve") {
		t.Errorf("config-file mode with no servers must print the hint with 'mcpproxy serve', got:\n%s", out)
	}

	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{"servers":[]}}`)
	}))
	t.Cleanup(daemon.Close)

	for _, c := range []struct {
		name, key string
		hint      bool
	}{
		{"socket or no key: administrator", "", true},
		{"instance admin key", "plain-admin-key", true},
		{"agent token", "mcp_agt_0123456789abcdef", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			upstreamListSource = listOriginConfig // must be reset by the path
			client := cliclient.NewClientWithAPIKey(daemon.URL, c.key, nil)
			out := captureOutput(func() {
				if err := runUpstreamListClientMode(context.Background(), client, c.key, nil); err != nil {
					t.Fatalf("runUpstreamListClientMode: %v", err)
				}
			})
			if got := strings.Contains(out, "No upstream servers yet"); got != c.hint {
				t.Errorf("hint printed = %v, want %v:\n%s", got, c.hint, out)
			}
			if strings.Contains(out, "mcpproxy serve") {
				t.Errorf("with a daemon answering, the hint must not say to start one:\n%s", out)
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
