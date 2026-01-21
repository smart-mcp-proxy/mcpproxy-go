package configimport

import "testing"

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
