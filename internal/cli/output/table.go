package output

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"
)

// TableFormatter formats output as a human-readable table.
type TableFormatter struct {
	NoColor   bool // Disable ANSI colors
	Unicode   bool // Use Unicode box-drawing characters
	Condensed bool // Simplified output for non-TTY
}

// Format renders data as a formatted table.
// data must be a slice of structs or maps.
func (f *TableFormatter) Format(data interface{}) (string, error) {
	// For generic data, delegate to JSON and indicate table not available
	// This is a placeholder - full implementation will use reflection
	return fmt.Sprintf("%v", data), nil
}

// FormatError renders an error in human-readable format.
func (f *TableFormatter) FormatError(err StructuredError) (string, error) {
	var buf bytes.Buffer

	// Use simple format for non-TTY or condensed mode
	if f.Condensed || !f.isTTY() {
		buf.WriteString(fmt.Sprintf("Error: %s\n", err.Message))
		if err.Guidance != "" {
			buf.WriteString(fmt.Sprintf("  Guidance: %s\n", err.Guidance))
		}
		if err.RecoveryCommand != "" {
			buf.WriteString(fmt.Sprintf("  Try: %s\n", err.RecoveryCommand))
		}
		return buf.String(), nil
	}

	// Rich format with unicode
	buf.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	buf.WriteString(fmt.Sprintf("❌ Error [%s]\n", err.Code))
	buf.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	buf.WriteString(fmt.Sprintf("\n%s\n", err.Message))

	if err.Guidance != "" {
		buf.WriteString(fmt.Sprintf("\n💡 %s\n", err.Guidance))
	}

	if err.RecoveryCommand != "" {
		buf.WriteString(fmt.Sprintf("\n🔧 Try: %s\n", err.RecoveryCommand))
	}

	buf.WriteString("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")

	return buf.String(), nil
}

// FormatTable renders tabular data with headers and alignment.
func (f *TableFormatter) FormatTable(headers []string, rows [][]string) (string, error) {
	if len(rows) == 0 {
		return "No results found\n", nil
	}

	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)

	// Write separator line if unicode enabled and TTY
	if f.Unicode && f.isTTY() {
		separator := strings.Repeat("━", 80)
		fmt.Fprintln(w, separator)
	}

	// Write headers
	headerLine := strings.Join(headers, "\t")
	fmt.Fprintln(w, headerLine)

	// Write header separator
	if f.Unicode && f.isTTY() {
		separators := make([]string, len(headers))
		for i := range separators {
			separators[i] = strings.Repeat("─", len(headers[i])+2)
		}
		fmt.Fprintln(w, strings.Join(separators, "\t"))
	}

	// Write rows
	for _, row := range rows {
		rowLine := strings.Join(row, "\t")
		fmt.Fprintln(w, rowLine)
	}

	// Flush tabwriter
	if err := w.Flush(); err != nil {
		return "", err
	}

	return buf.String(), nil
}

// isTTY checks if stdout is a terminal.
func (f *TableFormatter) isTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}
