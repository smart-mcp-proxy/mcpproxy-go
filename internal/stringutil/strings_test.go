package stringutil

import (
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestContainsIgnoreCase(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		substr   string
		expected bool
	}{
		{"exact match", "hello", "hello", true},
		{"case insensitive match", "Hello World", "hello", true},
		{"case insensitive match upper", "hello world", "WORLD", true},
		{"mixed case", "HeLLo WoRLD", "ello wor", true},
		{"no match", "hello", "goodbye", false},
		{"empty substr", "hello", "", true},
		{"empty string", "", "hello", false},
		{"both empty", "", "", true},
		{"substr longer than string", "hi", "hello", false},
		{"special chars", "error: invalid_grant", "INVALID_GRANT", true},
		{"network error", "connection timeout", "TIMEOUT", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ContainsIgnoreCase(tt.s, tt.substr)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCollapseRepeatedErrorWrappers(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		expected string
	}{
		{
			name:     "no separator is returned unchanged",
			in:       "plain error",
			expected: "plain error",
		},
		{
			name:     "distinct wrappers are preserved",
			in:       "failed to connect: transport error: dial tcp",
			expected: "failed to connect: transport error: dial tcp",
		},
		{
			name: "mcp-go double send wrapper collapses once",
			in: `failed to connect: MCP initialize failed during no-auth strategy: transport error: ` +
				`failed to send request: failed to send request: Post "https://example.invalid/mcp": ` +
				`dial tcp: lookup example.invalid: no such host`,
			expected: `failed to connect: MCP initialize failed during no-auth strategy: transport error: ` +
				`failed to send request: Post "https://example.invalid/mcp": ` +
				`dial tcp: lookup example.invalid: no such host`,
		},
		{
			name:     "three repeats collapse to one",
			in:       "a: a: a: boom",
			expected: "a: boom",
		},
		{
			name:     "non-adjacent repeats are kept",
			in:       "a: b: a: boom",
			expected: "a: b: a: boom",
		},
		{
			name:     "trailing repeat keeps the root cause",
			in:       "boom: boom",
			expected: "boom: boom",
		},
		{
			name:     "empty string",
			in:       "",
			expected: "",
		},

		// Payload must survive verbatim even when it repeats. ": " is a Go
		// wrapping boundary AND a separator inside quoted URLs, JSON, stderr
		// and validation text; only prose-shaped wrappers may be dropped.
		{
			name:     "repeated JSON fragment is payload, not a wrapper",
			in:       `parse failed: {"a": "x", "b": "x"}`,
			expected: `parse failed: {"a": "x", "b": "x"}`,
		},
		{
			name:     "repeated quoted URL is preserved",
			in:       `redirect loop: "https://a.example/x": "https://a.example/x": giving up`,
			expected: `redirect loop: "https://a.example/x": "https://a.example/x": giving up`,
		},
		{
			name:     "repeated key=value payload is preserved",
			in:       `bad env: FOO=bar: FOO=bar: rejected`,
			expected: `bad env: FOO=bar: FOO=bar: rejected`,
		},
		{
			name:     "repeated path-shaped segment is preserved",
			in:       `stat /tmp/a/b: /tmp/a/b: no such file`,
			expected: `stat /tmp/a/b: /tmp/a/b: no such file`,
		},
		{
			name: "a repeat longer than a wrapper phrase is preserved",
			in: "outer: " +
				"this line is far too long to plausibly be a fmt Errorf wrapper phrase indeed: " +
				"this line is far too long to plausibly be a fmt Errorf wrapper phrase indeed: " +
				"boom",
			expected: "outer: " +
				"this line is far too long to plausibly be a fmt Errorf wrapper phrase indeed: " +
				"this line is far too long to plausibly be a fmt Errorf wrapper phrase indeed: " +
				"boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, CollapseRepeatedErrorWrappers(tt.in))
		})
	}
}

func TestUTF8Prefix(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"ascii shorter than limit", "hello", 10, "hello"},
		{"ascii exact", "hello", 5, "hello"},
		{"ascii cut", "hello world", 5, "hello"},
		{"zero limit", "hello", 0, ""},
		{"negative limit", "hello", -1, ""},
		{"cut on 2-byte boundary", "aéb", 3, "aé"},
		{"cut inside 2-byte rune", "aéb", 2, "a"},
		{"cut inside 3-byte rune", "a世b", 3, "a"},
		{"cut inside 3-byte rune tail", "a世b", 2, "a"},
		{"cut inside 4-byte rune", "a😀b", 4, "a"},
		{"limit smaller than first rune", "世", 2, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := UTF8Prefix(tc.in, tc.n)
			if got != tc.want {
				t.Fatalf("UTF8Prefix(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("UTF8Prefix(%q, %d) returned invalid UTF-8: %q", tc.in, tc.n, got)
			}
		})
	}
}

func TestUTF8PrefixMalformedInput(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		// A stray continuation byte after a complete rune must not swallow it.
		{"stray continuation after ascii", "a\x80b", 1, "a"},
		{"stray continuation after rune", "é\x80\x80\x80\x80b", 2, "é"},
		// A run of continuation bytes longer than a rune is malformed; keep the
		// raw cut rather than discarding the whole prefix.
		{"long continuation run", "ab\x80\x80\x80\x80\x80", 4, "ab\x80\x80"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := UTF8Prefix(tc.in, tc.n); got != tc.want {
				t.Fatalf("UTF8Prefix(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
			}
		})
	}
}
