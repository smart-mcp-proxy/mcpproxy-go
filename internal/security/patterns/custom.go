// Package patterns provides sensitive data detection patterns for various credential types.
package patterns

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// CustomPatternError represents an error when loading a custom pattern
type CustomPatternError struct {
	PatternName string
	Message     string
}

// Error implements the error interface
func (e *CustomPatternError) Error() string {
	return fmt.Sprintf("custom pattern %q: %s", e.PatternName, e.Message)
}

// LoadCustomPatterns converts config.CustomPattern definitions to Pattern objects.
// It validates regex patterns and returns errors for invalid ones.
// Returns a slice of valid patterns and a slice of errors for invalid patterns.
func LoadCustomPatterns(patterns []config.CustomPattern) ([]*Pattern, []error) {
	var result []*Pattern
	var errors []error

	for _, cp := range patterns {
		pattern, err := loadSinglePattern(cp)
		if err != nil {
			errors = append(errors, err)
			continue
		}
		result = append(result, pattern)
	}

	return result, errors
}

// loadSinglePattern converts a single config.CustomPattern to a Pattern.
func loadSinglePattern(cp config.CustomPattern) (*Pattern, error) {
	// Validate name is provided
	if cp.Name == "" {
		return nil, &CustomPatternError{
			PatternName: "(empty)",
			Message:     "pattern name is required",
		}
	}

	// Validate that either Regex or Keywords is provided (not both, not neither)
	hasRegex := cp.Regex != ""
	hasKeywords := len(cp.Keywords) > 0

	if !hasRegex && !hasKeywords {
		return nil, &CustomPatternError{
			PatternName: cp.Name,
			Message:     "either regex or keywords must be provided",
		}
	}

	if hasRegex && hasKeywords {
		return nil, &CustomPatternError{
			PatternName: cp.Name,
			Message:     "regex and keywords are mutually exclusive, provide only one",
		}
	}

	// Validate regex if provided
	if hasRegex {
		if _, err := regexp.Compile(cp.Regex); err != nil {
			return nil, &CustomPatternError{
				PatternName: cp.Name,
				Message:     fmt.Sprintf("invalid regex pattern: %v", err),
			}
		}
	}

	// Build the pattern
	builder := NewPattern(cp.Name).
		WithCategory(mapCategory(cp.Category)).
		WithSeverity(mapSeverity(cp.Severity)).
		WithDescription(fmt.Sprintf("Custom pattern: %s", cp.Name))

	if hasRegex {
		builder = builder.WithRegex(cp.Regex)
	} else {
		builder = builder.WithKeywords(cp.Keywords...)
	}

	return builder.Build(), nil
}

// mapSeverity converts a string severity to the Severity type.
// Defaults to SeverityMedium for unrecognized values.
func mapSeverity(s string) Severity {
	switch strings.ToLower(s) {
	case "critical":
		return SeverityCritical
	case "high":
		return SeverityHigh
	case "medium":
		return SeverityMedium
	case "low":
		return SeverityLow
	default:
		return SeverityMedium
	}
}

// mapCategory converts a string category to the Category type.
// Defaults to CategoryCustom for unrecognized values.
func mapCategory(c string) Category {
	switch strings.ToLower(c) {
	case "cloud_credentials":
		return CategoryCloudCredentials
	case "private_key":
		return CategoryPrivateKey
	case "api_token":
		return CategoryAPIToken
	case "auth_token":
		return CategoryAuthToken
	case "sensitive_file":
		return CategorySensitiveFile
	case "database_credential":
		return CategoryDatabaseCred
	case "high_entropy":
		return CategoryHighEntropy
	case "credit_card":
		return CategoryCreditCard
	case "custom", "":
		return CategoryCustom
	default:
		return CategoryCustom
	}
}
