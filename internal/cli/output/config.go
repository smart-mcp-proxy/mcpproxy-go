package output

import "os"

// FormatConfig holds configuration for output formatting behavior.
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

// DefaultConfig returns the default format configuration.
func DefaultConfig() FormatConfig {
	return FormatConfig{
		Format:  "table",
		NoColor: os.Getenv("NO_COLOR") == "1",
		Quiet:   false,
		Pretty:  true,
	}
}

// FromEnv creates config from environment variables.
func FromEnv() FormatConfig {
	cfg := DefaultConfig()
	if format := os.Getenv("MCPPROXY_OUTPUT"); format != "" {
		cfg.Format = format
	}
	return cfg
}

// WithFormat returns a copy with the specified format.
func (c FormatConfig) WithFormat(format string) FormatConfig {
	c.Format = format
	return c
}

// WithNoColor returns a copy with NoColor set.
func (c FormatConfig) WithNoColor(noColor bool) FormatConfig {
	c.NoColor = noColor
	return c
}

// WithQuiet returns a copy with Quiet set.
func (c FormatConfig) WithQuiet(quiet bool) FormatConfig {
	c.Quiet = quiet
	return c
}
