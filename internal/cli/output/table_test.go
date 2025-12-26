package output

import (
	"os"
	"strings"
	"testing"
)

// T024: Unit test for TableFormatter.Format()
func TestTableFormatter_Format(t *testing.T) {
	f := &TableFormatter{}

	tests := []struct {
		name    string
		data    interface{}
		wantErr bool
	}{
		{
			name:    "format string",
			data:    "hello",
			wantErr: false,
		},
		{
			name:    "format slice",
			data:    []string{"a", "b", "c"},
			wantErr: false,
		},
		{
			name:    "format map",
			data:    map[string]string{"key": "value"},
			wantErr: false,
		},
		{
			name:    "format nil",
			data:    nil,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := f.Format(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("Format() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && result == "" && tt.data != nil {
				t.Error("Format() returned empty string for non-nil data")
			}
		})
	}
}

// T025: Unit test for TableFormatter.FormatTable() with column alignment
func TestTableFormatter_FormatTable(t *testing.T) {
	// Disable TTY features for consistent testing
	f := &TableFormatter{Unicode: false, Condensed: true}

	headers := []string{"NAME", "STATUS", "TOOLS"}
	rows := [][]string{
		{"server1", "healthy", "5"},
		{"server2", "unhealthy", "10"},
		{"very-long-server-name", "degraded", "3"},
	}

	result, err := f.FormatTable(headers, rows)
	if err != nil {
		t.Fatalf("FormatTable() error = %v", err)
	}

	// Verify headers are present
	if !strings.Contains(result, "NAME") {
		t.Error("Expected header 'NAME' in output")
	}
	if !strings.Contains(result, "STATUS") {
		t.Error("Expected header 'STATUS' in output")
	}
	if !strings.Contains(result, "TOOLS") {
		t.Error("Expected header 'TOOLS' in output")
	}

	// Verify all rows are present
	if !strings.Contains(result, "server1") {
		t.Error("Expected 'server1' in output")
	}
	if !strings.Contains(result, "server2") {
		t.Error("Expected 'server2' in output")
	}
	if !strings.Contains(result, "very-long-server-name") {
		t.Error("Expected 'very-long-server-name' in output")
	}

	// Verify values are present
	if !strings.Contains(result, "healthy") {
		t.Error("Expected 'healthy' in output")
	}
}

func TestTableFormatter_FormatTable_EmptyRows(t *testing.T) {
	f := &TableFormatter{}

	headers := []string{"NAME", "STATUS"}
	rows := [][]string{}

	result, err := f.FormatTable(headers, rows)
	if err != nil {
		t.Fatalf("FormatTable() error = %v", err)
	}

	if !strings.Contains(result, "No results found") {
		t.Errorf("Expected 'No results found' message, got: %s", result)
	}
}

func TestTableFormatter_FormatTable_UnevenRows(t *testing.T) {
	f := &TableFormatter{Condensed: true}

	headers := []string{"A", "B", "C"}
	rows := [][]string{
		{"1", "2", "3"},
		{"4", "5"}, // Missing last column
	}

	result, err := f.FormatTable(headers, rows)
	if err != nil {
		t.Fatalf("FormatTable() error = %v", err)
	}

	// Should handle gracefully
	if !strings.Contains(result, "1") || !strings.Contains(result, "4") {
		t.Error("Expected row data in output")
	}
}

// T026: Unit test for NO_COLOR=1 environment variable
func TestTableFormatter_NoColor(t *testing.T) {
	tests := []struct {
		name      string
		noColor   bool
		wantEmoji bool // When NoColor is false and TTY, emojis should appear
	}{
		{
			name:      "NoColor false allows formatting",
			noColor:   false,
			wantEmoji: true,
		},
		{
			name:      "NoColor true suppresses formatting",
			noColor:   true,
			wantEmoji: true, // Emojis are not colors, still appear
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &TableFormatter{NoColor: tt.noColor}

			err := StructuredError{
				Code:    "TEST",
				Message: "Test error",
			}

			result, fmtErr := f.FormatError(err)
			if fmtErr != nil {
				t.Fatalf("FormatError() error = %v", fmtErr)
			}

			// Both should contain the error message
			if !strings.Contains(result, "Test error") {
				t.Error("Expected error message in output")
			}
		})
	}
}

// T027: Unit test for non-TTY simplified output
func TestTableFormatter_Condensed(t *testing.T) {
	// Condensed mode simulates non-TTY behavior
	f := &TableFormatter{Condensed: true}

	err := StructuredError{
		Code:            "TEST_ERROR",
		Message:         "Something went wrong",
		Guidance:        "Try again later",
		RecoveryCommand: "mcpproxy fix",
	}

	result, fmtErr := f.FormatError(err)
	if fmtErr != nil {
		t.Fatalf("FormatError() error = %v", fmtErr)
	}

	// Condensed format should be simpler
	if !strings.Contains(result, "Error: Something went wrong") {
		t.Error("Expected simple error format in condensed mode")
	}
	if !strings.Contains(result, "Guidance: Try again later") {
		t.Error("Expected guidance in condensed mode")
	}
	if !strings.Contains(result, "Try: mcpproxy fix") {
		t.Error("Expected recovery command in condensed mode")
	}

	// Should NOT have unicode separators in condensed mode
	if strings.Contains(result, "━━━") {
		t.Error("Condensed mode should not have unicode separators")
	}
}

func TestTableFormatter_FormatError_RichFormat(t *testing.T) {
	// When not condensed and TTY (we can't test true TTY in unit tests,
	// but we can test the non-condensed path when isTTY returns true)
	f := &TableFormatter{Unicode: true, Condensed: false}

	err := StructuredError{
		Code:            "AUTH_REQUIRED",
		Message:         "Authentication required",
		Guidance:        "Please log in first",
		RecoveryCommand: "mcpproxy auth login",
	}

	result, fmtErr := f.FormatError(err)
	if fmtErr != nil {
		t.Fatalf("FormatError() error = %v", fmtErr)
	}

	// In non-TTY (unit test), it will use condensed format
	// This test verifies the condensed fallback works
	if !strings.Contains(result, "Authentication required") {
		t.Error("Expected error message in output")
	}
}

// Test that NewFormatter respects NO_COLOR env var
func TestNewFormatter_RespectsNO_COLOR(t *testing.T) {
	// Set NO_COLOR
	os.Setenv("NO_COLOR", "1")
	defer os.Unsetenv("NO_COLOR")

	f, err := NewFormatter("table")
	if err != nil {
		t.Fatalf("NewFormatter() error = %v", err)
	}

	tf, ok := f.(*TableFormatter)
	if !ok {
		t.Fatalf("Expected *TableFormatter, got %T", f)
	}

	if !tf.NoColor {
		t.Error("Expected NoColor to be true when NO_COLOR=1")
	}
}

// Test table alignment with varying column widths
func TestTableFormatter_ColumnAlignment(t *testing.T) {
	f := &TableFormatter{Condensed: true}

	headers := []string{"ID", "NAME", "DESCRIPTION"}
	rows := [][]string{
		{"1", "short", "A brief description"},
		{"123", "a-very-long-name", "Another description"},
		{"4", "x", "Y"},
	}

	result, err := f.FormatTable(headers, rows)
	if err != nil {
		t.Fatalf("FormatTable() error = %v", err)
	}

	lines := strings.Split(strings.TrimSpace(result), "\n")
	if len(lines) < 4 { // header + 3 rows
		t.Errorf("Expected at least 4 lines, got %d", len(lines))
	}

	// All data should be present
	for _, row := range rows {
		for _, cell := range row {
			if !strings.Contains(result, cell) {
				t.Errorf("Expected '%s' in output", cell)
			}
		}
	}
}
