// Package output provides unified output formatting for CLI commands.
// It supports multiple output formats (table, JSON, YAML) and structured errors.
package output

import (
	"fmt"
	"os"
	"strings"
)

// OutputFormatter formats structured data for CLI output.
// Implementations are stateless and thread-safe.
type OutputFormatter interface {
	// Format converts data to formatted string output.
	// data should be a struct, slice, or map that can be marshaled.
	Format(data interface{}) (string, error)

	// FormatError converts a structured error to formatted output.
	FormatError(err StructuredError) (string, error)

	// FormatTable formats tabular data with headers.
	// headers defines column names, rows contains data.
	FormatTable(headers []string, rows [][]string) (string, error)
}

// NewFormatter creates a formatter for the specified format.
// Supported formats: table, json, yaml (case-insensitive).
func NewFormatter(format string) (OutputFormatter, error) {
	switch strings.ToLower(format) {
	case "json":
		return &JSONFormatter{Indent: true}, nil
	case "yaml":
		return &YAMLFormatter{}, nil
	case "table", "":
		return &TableFormatter{
			NoColor: os.Getenv("NO_COLOR") == "1",
			Unicode: true,
		}, nil
	default:
		return nil, fmt.Errorf("unknown output format: %s (valid: table, json, yaml)", format)
	}
}

// ResolveFormat determines the output format from flags and environment.
// Priority: explicit flag > --json alias > MCPPROXY_OUTPUT env var > default (table)
func ResolveFormat(outputFlag string, jsonFlag bool) string {
	if jsonFlag {
		return "json"
	}
	if outputFlag != "" {
		return outputFlag
	}
	if envFormat := os.Getenv("MCPPROXY_OUTPUT"); envFormat != "" {
		return envFormat
	}
	return "table"
}
