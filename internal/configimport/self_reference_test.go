package configimport

import (
	"testing"
	"time"
)

func TestPointsToSelf(t *testing.T) {
	tests := []struct {
		name   string
		listen []string
		url    string
		want   bool
	}{
		{"exact base url", []string{"127.0.0.1:8080"}, "http://127.0.0.1:8080/mcp", true},
		{"trailing slash", []string{"127.0.0.1:8080"}, "http://127.0.0.1:8080/mcp/", true},
		{"legacy apikey query", []string{"127.0.0.1:8080"}, "http://127.0.0.1:8080/mcp?apikey=abc", true},
		{"direct surface", []string{"127.0.0.1:8080"}, "http://127.0.0.1:8080/mcp/all", true},
		{"call surface", []string{"127.0.0.1:8080"}, "http://127.0.0.1:8080/mcp/call", true},
		{"code surface", []string{"127.0.0.1:8080"}, "http://127.0.0.1:8080/mcp/code", true},
		{"localhost alias", []string{"127.0.0.1:8080"}, "http://localhost:8080/mcp", true},
		{"ipv6 loopback", []string{"127.0.0.1:8080"}, "http://[::1]:8080/mcp", true},
		{"host-less listen", []string{":18123"}, "http://127.0.0.1:18123/mcp", true},
		{"all-interfaces listen", []string{"0.0.0.0:18123"}, "http://localhost:18123/mcp", true},
		{"named listen host", []string{"myhost.lan:9000"}, "http://MyHost.lan:9000/mcp", true},
		{"https default port", []string{"127.0.0.1:443"}, "https://127.0.0.1/mcp", true},
		{"second listen addr", []string{"", "127.0.0.1:8080"}, "http://127.0.0.1:8080/mcp", true},

		{"different port", []string{"127.0.0.1:8080"}, "http://127.0.0.1:18412/mcp", false},
		{"different path", []string{"127.0.0.1:8080"}, "http://127.0.0.1:8080/api/v1", false},
		{"path prefix lookalike", []string{"127.0.0.1:8080"}, "http://127.0.0.1:8080/mcpx", false},
		{"remote host", []string{"127.0.0.1:8080"}, "https://api.github.com:8080/mcp", false},
		{"loopback url, lan-only listen", []string{"192.168.1.5:8080"}, "http://127.0.0.1:8080/mcp", false},
		{"no listen address", nil, "http://127.0.0.1:8080/mcp", false},
		{"not a url", []string{"127.0.0.1:8080"}, "-y", false},
		{"non-http scheme", []string{"127.0.0.1:8080"}, "ftp://127.0.0.1:8080/mcp", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newSelfMatcher(tt.listen)
			if got := m.matchesURL(tt.url); got != tt.want {
				t.Errorf("matchesURL(%q) with listen %v = %v, want %v", tt.url, tt.listen, got, tt.want)
			}
		})
	}
}

// TestImport_SkipsSelfReference covers the onboarding self-import bug: after
// Connect writes an `mcpproxy` entry pointing at this instance into a client
// config, importing that config must not offer the entry back as an upstream
// (it would proxy mcpproxy through itself).
func TestImport_SkipsSelfReference(t *testing.T) {
	content := []byte(`{
  "mcpServers": {
    "mcpproxy": {"type": "http", "url": "http://127.0.0.1:18123/mcp"},
    "mcpproxy-direct": {"type": "http", "url": "http://localhost:18123/mcp/all"},
    "bridge": {"command": "npx", "args": ["-y", "mcp-remote", "http://127.0.0.1:18123/mcp", "--header", "X-API-Key: k"]},
    "other-proxy": {"type": "http", "url": "http://127.0.0.1:18412/mcp"},
    "github": {"type": "http", "url": "https://api.githubcopilot.com/mcp/"},
    "fs": {"command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]}
  }
}`)

	result, err := Import(content, &ImportOptions{
		FormatHint:      FormatClaudeCode,
		SelfListenAddrs: []string{"127.0.0.1:18123"},
		Now:             time.Now(),
	})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	imported := map[string]bool{}
	for _, s := range result.Imported {
		imported[s.Server.Name] = true
	}
	for _, want := range []string{"other-proxy", "github", "fs"} {
		if !imported[want] {
			t.Errorf("expected %q to be importable, imported=%v", want, imported)
		}
	}

	skipped := map[string]string{}
	for _, s := range result.Skipped {
		skipped[s.Name] = s.Reason
	}
	for _, self := range []string{"mcpproxy", "mcpproxy-direct", "bridge"} {
		if imported[self] {
			t.Errorf("self-referencing entry %q must not be importable", self)
		}
		if skipped[self] != SkipReasonSelfReference {
			t.Errorf("skipped[%q] = %q, want %q", self, skipped[self], SkipReasonSelfReference)
		}
	}
	if result.Summary.Imported != 3 || result.Summary.Skipped != 3 {
		t.Errorf("summary = %+v, want 3 imported / 3 skipped", result.Summary)
	}
}

// Without a listen address the filter is inert, so callers that cannot know
// the instance address keep the previous behavior.
func TestImport_NoSelfListenAddrsKeepsEntries(t *testing.T) {
	content := []byte(`{"mcpServers":{"mcpproxy":{"type":"http","url":"http://127.0.0.1:18123/mcp"}}}`)
	result, err := Import(content, &ImportOptions{FormatHint: FormatClaudeCode})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported != 1 {
		t.Errorf("Summary.Imported = %d, want 1", result.Summary.Imported)
	}
}
