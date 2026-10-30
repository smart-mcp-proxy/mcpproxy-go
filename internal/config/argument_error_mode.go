package config

import "fmt"

const (
	ToolArgumentErrorModeDetailed = "detailed"
	ToolArgumentErrorModeRedacted = "redacted"
)

// ValidateToolArgumentErrorMode rejects unknown policies before persistence.
// Empty is the backwards-compatible default on create and leaves an update
// unchanged; callers reset an existing policy with an explicit detailed value.
func ValidateToolArgumentErrorMode(mode string) error {
	switch mode {
	case "", ToolArgumentErrorModeDetailed, ToolArgumentErrorModeRedacted:
		return nil
	default:
		return fmt.Errorf("tool_argument_error_mode must be empty, detailed, or redacted")
	}
}

func (c *Config) validateToolArgumentErrorModes() error {
	for _, server := range c.Servers {
		if server != nil {
			if err := ValidateToolArgumentErrorMode(server.ToolArgumentErrorMode); err != nil {
				return err
			}
		}
	}
	return nil
}
