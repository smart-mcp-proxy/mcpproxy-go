package registries

import "testing"

// Generic domain labels (labs, tools, mcp, dev, app, ai) must verify only on an
// exact repo-owner match, never through the token or affix rules (#1466).
func TestPublisherOwnsRepo_GenericLabels(t *testing.T) {
	cases := []struct {
		id, url string
		want    bool
	}{
		{"com.tools/x", "https://github.com/acme-tools/x", false},
		{"com.mcp/x", "https://github.com/mcp-acme/x", false},
		{"io.labs/x", "https://github.com/acme-labs/x", false},
		{"com.dev/x", "https://github.com/dev_corp/x", false},
		{"com.app/x", "https://github.com/myapp/x", false},
		{"com.app/x", "https://github.com/app-corp/x", false},
		{"com.ai/x", "https://github.com/acme-ai/x", false},
		{"com.tools/x", "https://github.com/tools/x", true},
		{"com.acme/mcp", "https://github.com/acme-corp/x", true},
		{"com.notion/mcp", "https://github.com/makenotion/x", true},
	}
	for _, c := range cases {
		if got := publisherOwnsRepo(c.id, c.url); got != c.want {
			t.Errorf("publisherOwnsRepo(%q, %q) = %v, want %v", c.id, c.url, got, c.want)
		}
	}
}
