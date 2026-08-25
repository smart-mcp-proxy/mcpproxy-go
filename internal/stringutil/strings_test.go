package stringutil

import (
	"testing"

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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, CollapseRepeatedErrorWrappers(tt.in))
		})
	}
}
