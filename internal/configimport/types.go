// Package configimport provides functionality to import MCP server configurations
// from external tools like Claude Desktop, Claude Code, Cursor IDE, Codex CLI, and Gemini CLI.
package configimport

import (
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// ConfigFormat represents supported configuration formats
type ConfigFormat string

const (
	FormatUnknown       ConfigFormat = "unknown"
	FormatClaudeDesktop ConfigFormat = "claude_desktop"
	FormatClaudeCode    ConfigFormat = "claude_code"
	FormatCursor        ConfigFormat = "cursor"
	FormatCodex         ConfigFormat = "codex"
	FormatGemini        ConfigFormat = "gemini"
)

// String returns human-readable format name for display
func (f ConfigFormat) String() string {
	switch f {
	case FormatClaudeDesktop:
		return "Claude Desktop"
	case FormatClaudeCode:
		return "Claude Code"
	case FormatCursor:
		return "Cursor IDE"
	case FormatCodex:
		return "Codex CLI"
	case FormatGemini:
		return "Gemini CLI"
	default:
		return "Unknown"
	}
}

// ImportSource represents the source content to be imported.
type ImportSource struct {
	// Content is the raw file content (JSON or TOML)
	Content []byte

	// FilePath is optional path for error messages (may be empty for pasted content)
	FilePath string

	// FormatHint is optional user-provided format override
	FormatHint ConfigFormat
}

// DetectionResult contains the result of format auto-detection.
type DetectionResult struct {
	// Format is the detected configuration format
	Format ConfigFormat

	// Confidence indicates detection certainty: "high", "medium", "low"
	Confidence string

	// Indicators lists the detection signals found
	Indicators []string
}

// ParsedServer represents a server parsed from source config, before mapping to MCPProxy format.
type ParsedServer struct {
	// Name is the server identifier from the source config
	Name string

	// SourceFormat indicates which format this was parsed from
	SourceFormat ConfigFormat

	// Fields contains all parsed fields (format-specific)
	// Common fields: command, args, env, url, headers, type/protocol
	Fields map[string]interface{}

	// Warnings contains non-fatal issues found during parsing
	Warnings []string
}

// ImportedServer represents a server ready to be added to MCPProxy (mapped to ServerConfig).
type ImportedServer struct {
	// Server is the MCPProxy-compatible server configuration
	Server *config.ServerConfig

	// SourceFormat indicates the original format
	SourceFormat ConfigFormat

	// OriginalName is the name from the source (may differ if sanitized)
	OriginalName string

	// FieldsSkipped lists source fields that couldn't be mapped
	FieldsSkipped []string

	// Warnings from parsing and mapping
	Warnings []string
}

// ImportResult contains the complete result of an import operation.
type ImportResult struct {
	// Format is the detected source format
	Format ConfigFormat `json:"format"`

	// FormatDisplayName is human-readable format name
	FormatDisplayName string `json:"format_display_name"`

	// Imported contains servers successfully imported
	Imported []*ImportedServer `json:"imported"`

	// Skipped contains servers that were skipped (e.g., duplicates)
	Skipped []SkippedServer `json:"skipped"`

	// Failed contains servers that failed to parse/map
	Failed []FailedServer `json:"failed"`

	// Warnings are non-fatal issues across the import
	Warnings []string `json:"warnings,omitempty"`

	// Summary provides counts for display
	Summary ImportSummary `json:"summary"`
}

// ImportSummary provides counts for display.
type ImportSummary struct {
	Total    int `json:"total"`
	Imported int `json:"imported"`
	Skipped  int `json:"skipped"`
	Failed   int `json:"failed"`
}

// SkippedServer represents a server that was skipped during import.
type SkippedServer struct {
	Name   string `json:"name"`
	Reason string `json:"reason"` // "already_exists", "filtered_out", "invalid_name"
}

// FailedServer represents a server that failed to import.
type FailedServer struct {
	Name    string `json:"name"`
	Error   string `json:"error"`
	Details string `json:"details,omitempty"`
}

// ImportOptions configures the import behavior.
type ImportOptions struct {
	// Preview if true, returns preview without actually importing
	Preview bool

	// ServerNames if set, only import servers with these names
	ServerNames []string

	// FormatHint is optional format override for auto-detection
	FormatHint ConfigFormat

	// ExistingServers is used to check for duplicates
	ExistingServers []string

	// SkipQuarantine if true, imported servers are not quarantined.
	// By default, all imported servers are quarantined for security review.
	SkipQuarantine bool

	// Now is the timestamp to use for Created field (default: time.Now())
	Now time.Time
}

// ImportError represents a structured error for import failures.
type ImportError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
}

// Error implements the error interface.
func (e *ImportError) Error() string {
	return e.Message
}
