# Quickstart: CLI Output Formatting System

**Feature**: 014-cli-output-formatting
**Date**: 2025-12-26

## Overview

This guide covers implementing the CLI output formatting system for mcpproxy. The system provides unified output formatting across all CLI commands with support for table, JSON, and YAML formats.

## Prerequisites

- Go 1.24+
- mcpproxy repository cloned
- `golangci-lint` installed

## Quick Implementation Steps

### Step 1: Create the Output Package

```bash
mkdir -p internal/cli/output
```

Create the core formatter interface:

```go
// internal/cli/output/formatter.go
package output

import (
    "fmt"
    "os"
)

// OutputFormatter formats structured data for CLI output
type OutputFormatter interface {
    Format(data interface{}) (string, error)
    FormatError(err StructuredError) (string, error)
    FormatTable(headers []string, rows [][]string) (string, error)
}

// NewFormatter creates a formatter for the specified format
func NewFormatter(format string) (OutputFormatter, error) {
    switch format {
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
```

### Step 2: Implement Formatters

**JSON Formatter** (`internal/cli/output/json.go`):
```go
package output

import "encoding/json"

type JSONFormatter struct {
    Indent bool
}

func (f *JSONFormatter) Format(data interface{}) (string, error) {
    var output []byte
    var err error
    if f.Indent {
        output, err = json.MarshalIndent(data, "", "  ")
    } else {
        output, err = json.Marshal(data)
    }
    if err != nil {
        return "", err
    }
    return string(output), nil
}
```

### Step 3: Add Global Flags to Root Command

In `cmd/mcpproxy/main.go`:

```go
var (
    globalOutputFormat string
    globalJSONOutput   bool
)

func init() {
    rootCmd.PersistentFlags().StringVarP(&globalOutputFormat, "output", "o", "",
        "Output format: table, json, yaml")
    rootCmd.PersistentFlags().BoolVar(&globalJSONOutput, "json", false,
        "Shorthand for -o json")
    rootCmd.MarkFlagsMutuallyExclusive("output", "json")
}

// ResolveOutputFormat determines the output format from flags and env
func ResolveOutputFormat() string {
    if globalJSONOutput {
        return "json"
    }
    if globalOutputFormat != "" {
        return globalOutputFormat
    }
    if envFormat := os.Getenv("MCPPROXY_OUTPUT"); envFormat != "" {
        return envFormat
    }
    return "table"
}
```

### Step 4: Migrate Existing Commands

Replace ad-hoc formatting with formatter calls:

```go
// Before
func outputServers(servers []map[string]interface{}) error {
    switch upstreamOutputFormat {
    case "json":
        output, err := json.MarshalIndent(servers, "", "  ")
        // ...
    case "table":
        fmt.Printf("%-4s %-25s...", ...)
    }
}

// After
func outputServers(servers []map[string]interface{}) error {
    formatter, err := output.NewFormatter(ResolveOutputFormat())
    if err != nil {
        return err
    }
    result, err := formatter.Format(servers)
    if err != nil {
        return err
    }
    fmt.Print(result)
    return nil
}
```

### Step 5: Add --help-json Support

```go
// internal/cli/output/help.go
func AddHelpJSONFlag(cmd *cobra.Command) {
    cmd.Flags().Bool("help-json", false, "Output help as JSON")
    cmd.SetHelpFunc(createHelpFunc(cmd))
}

func createHelpFunc(original *cobra.Command) func(*cobra.Command, []string) {
    return func(cmd *cobra.Command, args []string) {
        helpJSON, _ := cmd.Flags().GetBool("help-json")
        if helpJSON {
            info := ExtractHelpInfo(cmd)
            output, _ := json.MarshalIndent(info, "", "  ")
            fmt.Println(string(output))
            return
        }
        // Default help
        cmd.Help()
    }
}
```

## Testing

### Unit Tests

```bash
go test ./internal/cli/output/... -v
```

### E2E Tests

```bash
# Test JSON output
./mcpproxy upstream list -o json | jq .

# Test YAML output
./mcpproxy upstream list -o yaml

# Test help-json
./mcpproxy --help-json | jq .
./mcpproxy upstream --help-json | jq .

# Test env var
MCPPROXY_OUTPUT=json ./mcpproxy upstream list | jq .

# Test error output
./mcpproxy upstream list --server nonexistent -o json
```

### mcp-eval Scenarios

Update mcp-eval scenarios to use JSON output:
```yaml
- name: list-servers
  command: mcpproxy upstream list -o json
  validate:
    type: json
    schema: ServerListOutput
```

## Common Patterns

### Handling Empty Results

```go
if len(servers) == 0 {
    if format == "json" {
        fmt.Println("[]")
    } else {
        fmt.Println("No servers found")
    }
    return nil
}
```

### Error Formatting

```go
func handleError(err error, format string) {
    structErr := output.StructuredError{
        Code:    "OPERATION_FAILED",
        Message: err.Error(),
    }

    formatter, _ := output.NewFormatter(format)
    errOutput, _ := formatter.FormatError(structErr)
    fmt.Fprintln(os.Stderr, errOutput)
}
```

## Verification Checklist

- [ ] `mcpproxy upstream list -o json` returns valid JSON
- [ ] `mcpproxy upstream list --json` works (alias)
- [ ] `mcpproxy upstream list -o yaml` returns valid YAML
- [ ] `mcpproxy --help-json` returns command structure
- [ ] `MCPPROXY_OUTPUT=json mcpproxy upstream list` uses JSON
- [ ] Errors with `-o json` return structured error JSON
- [ ] `NO_COLOR=1` disables colors in table output
- [ ] All existing commands still work with default table format
