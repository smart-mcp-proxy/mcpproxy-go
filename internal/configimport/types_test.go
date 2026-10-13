package configimport

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsNoServers(t *testing.T) {
	// Each parser's empty-config error must be recognised.
	cases := []struct {
		name    string
		format  ConfigFormat
		content string
	}{
		{"claude desktop", FormatClaudeDesktop, `{}`},
		{"claude code", FormatClaudeCode, `{"numStartups":3}`},
		{"cursor", FormatCursor, `{"mcpServers":{}}`},
		{"codex", FormatCodex, "[other]\nx=1\n"},
		{"gemini", FormatGemini, `{"mcpServers":{}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Import([]byte(tc.content), &ImportOptions{FormatHint: tc.format, Preview: true})
			if err == nil {
				t.Fatal("expected an error for a config with no servers")
			}
			if !IsNoServers(err) {
				t.Errorf("IsNoServers(%v) = false, want true", err)
			}
			if !IsNoServers(fmt.Errorf("wrapped: %w", err)) {
				t.Error("IsNoServers must see through wrapping")
			}
		})
	}

	for name, err := range map[string]error{
		"parse error":    &ImportError{Type: "parse_error", Message: "invalid JSON"},
		"unknown format": ErrUnknownFormat,
		"nil":            nil,
		"plain":          errors.New("no_servers"),
	} {
		if IsNoServers(err) {
			t.Errorf("IsNoServers(%s) = true, want false", name)
		}
	}
}

func TestConfigFormat_String(t *testing.T) {
	tests := []struct {
		format   ConfigFormat
		expected string
	}{
		{FormatClaudeDesktop, "Claude Desktop"},
		{FormatClaudeCode, "Claude Code"},
		{FormatCursor, "Cursor IDE"},
		{FormatCodex, "Codex CLI"},
		{FormatGemini, "Gemini CLI"},
		{FormatUnknown, "Unknown"},
		{ConfigFormat("invalid"), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(string(tt.format), func(t *testing.T) {
			if got := tt.format.String(); got != tt.expected {
				t.Errorf("ConfigFormat.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestImportError_Error(t *testing.T) {
	err := &ImportError{
		Type:    "parse_error",
		Message: "invalid JSON",
		Line:    10,
		Column:  5,
	}

	if got := err.Error(); got != "invalid JSON" {
		t.Errorf("ImportError.Error() = %q, want %q", got, "invalid JSON")
	}
}
