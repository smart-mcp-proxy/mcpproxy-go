package configimport

import (
	"fmt"
	"regexp"
)

// validServerNamePattern matches valid server names: alphanumeric, dash, underscore
var validServerNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ValidServerName checks if a server name is valid for MCPProxy
func ValidServerName(name string) error {
	if name == "" {
		return fmt.Errorf("server name cannot be empty")
	}
	if len(name) > 64 {
		return fmt.Errorf("server name cannot exceed 64 characters")
	}
	// Allow alphanumeric, dash, underscore
	if !validServerNamePattern.MatchString(name) {
		return fmt.Errorf("server name contains invalid characters: %s (only alphanumeric, dash, underscore allowed)", name)
	}
	return nil
}

// SanitizeServerName attempts to create a valid server name from an invalid one.
// Returns the sanitized name and a boolean indicating if sanitization was needed.
func SanitizeServerName(name string) (string, bool) {
	// First check if already valid
	if ValidServerName(name) == nil {
		return name, false
	}

	// Trim leading/trailing whitespace
	trimmed := name
	for len(trimmed) > 0 && (trimmed[0] == ' ' || trimmed[0] == '\t') {
		trimmed = trimmed[1:]
	}
	for len(trimmed) > 0 && (trimmed[len(trimmed)-1] == ' ' || trimmed[len(trimmed)-1] == '\t') {
		trimmed = trimmed[:len(trimmed)-1]
	}

	// Replace invalid characters with underscore
	sanitized := make([]byte, 0, len(trimmed))
	for _, c := range trimmed {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			sanitized = append(sanitized, byte(c))
		} else if c == ' ' || c == '.' || c == '/' || c == '\\' {
			// Common separators become underscores
			if len(sanitized) == 0 || sanitized[len(sanitized)-1] != '_' {
				sanitized = append(sanitized, '_')
			}
		}
		// Skip other characters
	}

	result := string(sanitized)

	// Trim leading/trailing underscores
	for len(result) > 0 && result[0] == '_' {
		result = result[1:]
	}
	for len(result) > 0 && result[len(result)-1] == '_' {
		result = result[:len(result)-1]
	}

	// Truncate to 64 characters
	if len(result) > 64 {
		result = result[:64]
	}

	// If still empty or invalid, return empty
	if result == "" || ValidServerName(result) != nil {
		return "", true
	}

	return result, true
}
