# Research: CLI Output Formatting System

**Feature**: 014-cli-output-formatting
**Date**: 2025-12-26

## Research Questions

### 1. How should global flags be implemented with Cobra?

**Decision**: Use `PersistentFlags()` on root command for global flags

**Rationale**: Cobra's `PersistentFlags()` propagates flags to all subcommands automatically. This is the standard pattern used by kubectl, gh, and other Go CLIs.

**Implementation**:
```go
// In main.go
rootCmd.PersistentFlags().StringVarP(&outputFormat, "output", "o", "",
    "Output format: table, json, yaml")
rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false,
    "Shorthand for -o json")

// Make mutually exclusive
rootCmd.MarkFlagsMutuallyExclusive("output", "json")
```

**Alternatives Considered**:
- Per-command flags: Rejected because it requires duplicating flag definitions in every command
- Viper config: Rejected as overkill for simple output format; env var support is sufficient

### 2. How to handle `--json` as alias for `-o json`?

**Decision**: Implement as separate boolean flag with mutual exclusivity

**Rationale**: Cobra doesn't support true flag aliases. Using a boolean flag with mutual exclusivity achieves the same UX while being explicit about the behavior.

**Implementation**:
```go
func resolveOutputFormat() string {
    if jsonOutput {
        return "json"
    }
    if outputFormat != "" {
        return outputFormat
    }
    if envFormat := os.Getenv("MCPPROXY_OUTPUT"); envFormat != "" {
        return envFormat
    }
    return "table"
}
```

**Alternatives Considered**:
- True alias: Not supported by Cobra
- Custom flag type: Overengineered for this use case

### 3. What's the best pattern for OutputFormatter interface?

**Decision**: Functional approach with stateless formatter instances

**Rationale**: Formatters don't need state. A simple interface with `Format(data interface{}) (string, error)` allows for easy testing and composition.

**Implementation**:
```go
// OutputFormatter formats structured data for CLI output
type OutputFormatter interface {
    Format(data interface{}) (string, error)
    FormatError(err StructuredError) (string, error)
}

// Factory function
func NewFormatter(format string) (OutputFormatter, error) {
    switch format {
    case "json":
        return &JSONFormatter{}, nil
    case "yaml":
        return &YAMLFormatter{}, nil
    case "table":
        return &TableFormatter{}, nil
    default:
        return nil, fmt.Errorf("unknown format: %s", format)
    }
}
```

**Alternatives Considered**:
- Single function with switch: Less extensible, harder to test individual formatters
- Plugin architecture: Overkill, no need for dynamic formatter loading

### 4. How to implement `--help-json`?

**Decision**: Hook into Cobra's help system via `SetHelpFunc` + custom flag

**Rationale**: Cobra allows overriding the help function. We can check for `--help-json` and return structured JSON instead of the default help text.

**Implementation**:
```go
// Add flag to every command
cmd.Flags().Bool("help-json", false, "Output help as JSON for machine parsing")

// Override help function
cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
    if helpJSON, _ := cmd.Flags().GetBool("help-json"); helpJSON {
        outputHelpJSON(cmd)
        return
    }
    // Default help behavior
    cmd.Help()
})
```

**Alternatives Considered**:
- Separate `mcpproxy help --json <command>`: Less discoverable, requires remembering different syntax
- Global flag only: Would conflict with command-specific help

### 5. What structure for StructuredError?

**Decision**: Error struct with code, message, guidance, recovery_command, and context

**Rationale**: Matches RFC-001 design. Provides enough information for AI agents to recover from errors automatically.

**Implementation**:
```go
type StructuredError struct {
    Code            string                 `json:"code"`
    Message         string                 `json:"message"`
    Guidance        string                 `json:"guidance,omitempty"`
    RecoveryCommand string                 `json:"recovery_command,omitempty"`
    Context         map[string]interface{} `json:"context,omitempty"`
}
```

**Alternatives Considered**:
- Simple error message: Insufficient for AI agent recovery
- Full stack trace: Too verbose, security concern with internal details

### 6. How to handle table formatting with variable column widths?

**Decision**: Use tabwriter from standard library with dynamic column detection

**Rationale**: Go's `text/tabwriter` handles column alignment automatically. Current implementation uses fixed-width printf which doesn't adapt well.

**Implementation**:
```go
type TableFormatter struct {
    writer *tabwriter.Writer
    buf    *bytes.Buffer
}

func (f *TableFormatter) Format(data interface{}) (string, error) {
    f.buf = &bytes.Buffer{}
    f.writer = tabwriter.NewWriter(f.buf, 0, 0, 2, ' ', 0)

    // Write headers and rows
    // ...

    f.writer.Flush()
    return f.buf.String(), nil
}
```

**Alternatives Considered**:
- Third-party table libraries (tablewriter): Extra dependency for simple use case
- Fixed-width columns: Current approach, doesn't adapt to content

### 7. How to detect TTY for table simplification?

**Decision**: Use golang.org/x/term package (already a dependency via Cobra)

**Rationale**: `term.IsTerminal(int(os.Stdout.Fd()))` is the standard Go approach. Already used in `confirmation.go`.

**Implementation**:
```go
func (f *TableFormatter) Format(data interface{}) (string, error) {
    if !term.IsTerminal(int(os.Stdout.Fd())) {
        // Simplified output without borders
        return f.formatSimple(data)
    }
    return f.formatRich(data)
}
```

### 8. How to integrate formatters with existing commands?

**Decision**: Gradual migration with backward compatibility

**Rationale**: Existing commands work. Migrate one at a time, ensuring output format stays identical.

**Migration Pattern**:
```go
// Before (in upstream_cmd.go)
func outputServers(servers []map[string]interface{}) error {
    switch upstreamOutputFormat {
    case "json":
        output, err := json.MarshalIndent(servers, "", "  ")
        // ...
    }
}

// After
func outputServers(servers []map[string]interface{}) error {
    formatter := output.NewFormatter(resolveOutputFormat())
    result, err := formatter.Format(servers)
    if err != nil {
        return err
    }
    fmt.Print(result)
    return nil
}
```

## Summary

All technical questions resolved. The implementation follows Go idioms:
- Stateless formatters implementing simple interface
- Standard library for JSON/YAML/table formatting
- Cobra's built-in mechanisms for flag handling
- Gradual migration preserving backward compatibility
