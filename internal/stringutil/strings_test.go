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
