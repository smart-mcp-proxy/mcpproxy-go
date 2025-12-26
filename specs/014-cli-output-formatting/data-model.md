# Data Model: CLI Output Formatting System

**Feature**: 014-cli-output-formatting
**Date**: 2025-12-26

## Core Types

### OutputFormatter Interface

```go
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
```

### StructuredError

Represents errors with machine-parseable metadata for AI agent recovery.

```go
type StructuredError struct {
    // Code is a machine-readable error identifier (e.g., "CONFIG_NOT_FOUND")
    Code string `json:"code"`

    // Message is a human-readable error description
    Message string `json:"message"`

    // Guidance provides context on why this error occurred
    Guidance string `json:"guidance,omitempty"`

    // RecoveryCommand suggests a command to fix the issue
    RecoveryCommand string `json:"recovery_command,omitempty"`

    // Context contains additional structured data about the error
    Context map[string]interface{} `json:"context,omitempty"`
}
```

**Field Validation**:
- `Code`: Required, uppercase snake_case (e.g., `SERVER_NOT_FOUND`)
- `Message`: Required, non-empty string
- `Guidance`: Optional, explains the error context
- `RecoveryCommand`: Optional, valid mcpproxy command
- `Context`: Optional, arbitrary key-value pairs

**Error Code Catalog**:

| Code | Message | Guidance | Recovery Command |
|------|---------|----------|------------------|
| `CONFIG_NOT_FOUND` | Configuration file not found | Run daemon to create config | `mcpproxy serve` |
| `SERVER_NOT_FOUND` | Server '{name}' not found | Check available servers | `mcpproxy upstream list` |
| `DAEMON_NOT_RUNNING` | Daemon is not running | Start the daemon first | `mcpproxy serve` |
| `INVALID_OUTPUT_FORMAT` | Unknown format '{format}' | Use table, json, or yaml | - |
| `AUTH_REQUIRED` | OAuth authentication required | Authenticate with server | `mcpproxy auth login --server={name}` |
| `CONNECTION_FAILED` | Failed to connect to server | Check server config | `mcpproxy doctor` |

### HelpInfo

Machine-readable command help for `--help-json` output.

```go
type HelpInfo struct {
    // Name is the command name (e.g., "upstream")
    Name string `json:"name"`

    // Description is a short description of the command
    Description string `json:"description"`

    // Usage shows the command syntax
    Usage string `json:"usage"`

    // Examples provides usage examples
    Examples []string `json:"examples,omitempty"`

    // Subcommands lists child commands (for parent commands)
    Subcommands []CommandInfo `json:"subcommands,omitempty"`

    // Flags lists available flags for this command
    Flags []FlagInfo `json:"flags,omitempty"`
}

type CommandInfo struct {
    Name        string `json:"name"`
    Description string `json:"description"`
}

type FlagInfo struct {
    // Name is the long flag name (e.g., "output")
    Name string `json:"name"`

    // Shorthand is the single-letter alias (e.g., "o")
    Shorthand string `json:"shorthand,omitempty"`

    // Type is the flag value type (e.g., "string", "bool", "int")
    Type string `json:"type"`

    // Description explains the flag's purpose
    Description string `json:"description"`

    // Default is the default value as string
    Default string `json:"default,omitempty"`

    // Required indicates if the flag is mandatory
    Required bool `json:"required,omitempty"`

    // Choices lists valid values for enum-like flags
    Choices []string `json:"choices,omitempty"`
}
```

### FormatConfig

Configuration for output formatting behavior.

```go
type FormatConfig struct {
    // Format is the output format (table, json, yaml)
    Format string

    // NoColor disables ANSI color codes
    NoColor bool

    // Quiet suppresses non-essential output
    Quiet bool

    // Pretty enables human-readable formatting (indentation, etc.)
    Pretty bool
}

// DefaultConfig returns the default format configuration
func DefaultConfig() FormatConfig {
    return FormatConfig{
        Format:  "table",
        NoColor: os.Getenv("NO_COLOR") == "1",
        Quiet:   false,
        Pretty:  true,
    }
}

// FromEnv creates config from environment variables
func FromEnv() FormatConfig {
    cfg := DefaultConfig()
    if format := os.Getenv("MCPPROXY_OUTPUT"); format != "" {
        cfg.Format = format
    }
    return cfg
}
```

## Formatter Implementations

### JSONFormatter

```go
type JSONFormatter struct {
    Indent bool // Whether to pretty-print with indentation
}

// Format marshals data to JSON
func (f *JSONFormatter) Format(data interface{}) (string, error)

// FormatError marshals error to JSON
func (f *JSONFormatter) FormatError(err StructuredError) (string, error)

// FormatTable converts table to JSON array of objects
func (f *JSONFormatter) FormatTable(headers []string, rows [][]string) (string, error)
```

### YAMLFormatter

```go
type YAMLFormatter struct{}

// Format marshals data to YAML
func (f *YAMLFormatter) Format(data interface{}) (string, error)

// FormatError marshals error to YAML
func (f *YAMLFormatter) FormatError(err StructuredError) (string, error)

// FormatTable converts table to YAML list
func (f *YAMLFormatter) FormatTable(headers []string, rows [][]string) (string, error)
```

### TableFormatter

```go
type TableFormatter struct {
    NoColor   bool // Disable ANSI colors
    Unicode   bool // Use Unicode box-drawing characters
    Condensed bool // Simplified output for non-TTY
}

// Format renders data as formatted table
// data must be a slice of structs or maps
func (f *TableFormatter) Format(data interface{}) (string, error)

// FormatError renders error in human-readable format
func (f *TableFormatter) FormatError(err StructuredError) (string, error)

// FormatTable renders explicit headers and rows
func (f *TableFormatter) FormatTable(headers []string, rows [][]string) (string, error)
```

## State Transitions

N/A - Formatters are stateless. Each call produces independent output.

## Relationships

```
┌─────────────────┐
│  CLI Command    │
│  (upstream list)│
└────────┬────────┘
         │ uses
         ▼
┌─────────────────┐     creates      ┌─────────────────┐
│ resolveFormat() │ ───────────────► │ OutputFormatter │
└─────────────────┘                  │   (interface)   │
                                     └────────┬────────┘
                                              │ implemented by
                    ┌─────────────────────────┼─────────────────────────┐
                    ▼                         ▼                         ▼
           ┌────────────────┐        ┌────────────────┐        ┌────────────────┐
           │ JSONFormatter  │        │ YAMLFormatter  │        │ TableFormatter │
           └────────────────┘        └────────────────┘        └────────────────┘
```

## Validation Rules

1. **Format string**: Must be one of `table`, `json`, `yaml` (case-insensitive)
2. **Error code**: Must be uppercase snake_case, max 50 characters
3. **JSON output**: Must be valid JSON, parseable by any JSON parser
4. **YAML output**: Must be valid YAML 1.2
5. **Table output**: Headers must match row column count
6. **Empty data**: JSON returns `[]`, table shows "No results found"
