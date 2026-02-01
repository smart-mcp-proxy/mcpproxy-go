package patterns

import (
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadCustomPatterns_ValidRegex(t *testing.T) {
	tests := []struct {
		name        string
		pattern     config.CustomPattern
		wantName    string
		testInput   string
		wantMatches bool
	}{
		{
			name: "simple regex pattern",
			pattern: config.CustomPattern{
				Name:     "internal_api_key",
				Regex:    `INTERNAL_[A-Z0-9]{16}`,
				Severity: "high",
				Category: "api_token",
			},
			wantName:    "internal_api_key",
			testInput:   "INTERNAL_ABCD1234EFGH5678",
			wantMatches: true,
		},
		{
			name: "email pattern",
			pattern: config.CustomPattern{
				Name:     "corporate_email",
				Regex:    `[a-zA-Z0-9._%+-]+@company\.com`,
				Severity: "medium",
				Category: "custom",
			},
			wantName:    "corporate_email",
			testInput:   "user@company.com",
			wantMatches: true,
		},
		{
			name: "ssn-like pattern",
			pattern: config.CustomPattern{
				Name:     "ssn_pattern",
				Regex:    `\d{3}-\d{2}-\d{4}`,
				Severity: "critical",
				Category: "custom",
			},
			wantName:    "ssn_pattern",
			testInput:   "The SSN is 123-45-6789 in the document",
			wantMatches: true,
		},
		{
			name: "pattern with no match",
			pattern: config.CustomPattern{
				Name:     "no_match_pattern",
				Regex:    `NOMATCH_[0-9]+`,
				Severity: "low",
			},
			wantName:    "no_match_pattern",
			testInput:   "This text has no matching content",
			wantMatches: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patterns, errors := LoadCustomPatterns([]config.CustomPattern{tt.pattern})

			require.Empty(t, errors, "expected no errors")
			require.Len(t, patterns, 1, "expected exactly one pattern")

			p := patterns[0]
			assert.Equal(t, tt.wantName, p.Name)

			matches := p.Match(tt.testInput)
			if tt.wantMatches {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.testInput)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.testInput)
			}
		})
	}
}

func TestLoadCustomPatterns_InvalidRegex(t *testing.T) {
	tests := []struct {
		name        string
		pattern     config.CustomPattern
		wantErrMsg  string
	}{
		{
			name: "unclosed bracket",
			pattern: config.CustomPattern{
				Name:     "bad_pattern",
				Regex:    `[a-z`,
				Severity: "high",
			},
			wantErrMsg: "invalid regex pattern",
		},
		{
			name: "invalid escape sequence",
			pattern: config.CustomPattern{
				Name:     "bad_escape",
				Regex:    `\k`,
				Severity: "medium",
			},
			wantErrMsg: "invalid regex pattern",
		},
		{
			name: "unclosed group",
			pattern: config.CustomPattern{
				Name:     "unclosed_group",
				Regex:    `(abc`,
				Severity: "low",
			},
			wantErrMsg: "invalid regex pattern",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patterns, errors := LoadCustomPatterns([]config.CustomPattern{tt.pattern})

			assert.Empty(t, patterns, "expected no valid patterns")
			require.Len(t, errors, 1, "expected exactly one error")

			err := errors[0]
			assert.Contains(t, err.Error(), tt.wantErrMsg)
			assert.Contains(t, err.Error(), tt.pattern.Name)
		})
	}
}

func TestLoadCustomPatterns_Keywords(t *testing.T) {
	tests := []struct {
		name        string
		pattern     config.CustomPattern
		testInput   string
		wantMatches bool
		matchCount  int
	}{
		{
			name: "single keyword match",
			pattern: config.CustomPattern{
				Name:     "confidential_marker",
				Keywords: []string{"CONFIDENTIAL"},
				Severity: "high",
			},
			testInput:   "This document is CONFIDENTIAL",
			wantMatches: true,
			matchCount:  1,
		},
		{
			name: "multiple keywords match",
			pattern: config.CustomPattern{
				Name:     "secret_markers",
				Keywords: []string{"SECRET", "CLASSIFIED", "TOP-SECRET"},
				Severity: "critical",
			},
			testInput:   "This is a SECRET document that is also CLASSIFIED",
			wantMatches: true,
			matchCount:  2,
		},
		{
			name: "case insensitive keyword match",
			pattern: config.CustomPattern{
				Name:     "password_marker",
				Keywords: []string{"PASSWORD"},
				Severity: "high",
			},
			testInput:   "The password is stored here",
			wantMatches: true,
			matchCount:  1,
		},
		{
			name: "mixed case input matching",
			pattern: config.CustomPattern{
				Name:     "api_key_marker",
				Keywords: []string{"api_key", "apikey"},
				Severity: "high",
			},
			testInput:   "Set your API_KEY in the config and also your APIKEY",
			wantMatches: true,
			matchCount:  2,
		},
		{
			name: "no keyword match",
			pattern: config.CustomPattern{
				Name:     "no_match_keywords",
				Keywords: []string{"NOMATCH1", "NOMATCH2"},
				Severity: "low",
			},
			testInput:   "This text has no matching keywords",
			wantMatches: false,
			matchCount:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patterns, errors := LoadCustomPatterns([]config.CustomPattern{tt.pattern})

			require.Empty(t, errors, "expected no errors")
			require.Len(t, patterns, 1, "expected exactly one pattern")

			p := patterns[0]
			matches := p.Match(tt.testInput)

			if tt.wantMatches {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.testInput)
				assert.Len(t, matches, tt.matchCount, "expected %d matches", tt.matchCount)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.testInput)
			}
		})
	}
}

func TestLoadCustomPatterns_SeverityMapping(t *testing.T) {
	tests := []struct {
		name         string
		severity     string
		wantSeverity Severity
	}{
		{
			name:         "critical severity",
			severity:     "critical",
			wantSeverity: SeverityCritical,
		},
		{
			name:         "high severity",
			severity:     "high",
			wantSeverity: SeverityHigh,
		},
		{
			name:         "medium severity",
			severity:     "medium",
			wantSeverity: SeverityMedium,
		},
		{
			name:         "low severity",
			severity:     "low",
			wantSeverity: SeverityLow,
		},
		{
			name:         "uppercase severity",
			severity:     "CRITICAL",
			wantSeverity: SeverityCritical,
		},
		{
			name:         "mixed case severity",
			severity:     "High",
			wantSeverity: SeverityHigh,
		},
		{
			name:         "unknown severity defaults to medium",
			severity:     "unknown",
			wantSeverity: SeverityMedium,
		},
		{
			name:         "empty severity defaults to medium",
			severity:     "",
			wantSeverity: SeverityMedium,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patterns, errors := LoadCustomPatterns([]config.CustomPattern{
				{
					Name:     "test_pattern",
					Keywords: []string{"test"},
					Severity: tt.severity,
				},
			})

			require.Empty(t, errors, "expected no errors")
			require.Len(t, patterns, 1, "expected exactly one pattern")

			assert.Equal(t, tt.wantSeverity, patterns[0].Severity)
		})
	}
}

func TestLoadCustomPatterns_CategoryMapping(t *testing.T) {
	tests := []struct {
		name         string
		category     string
		wantCategory Category
	}{
		{
			name:         "cloud_credentials category",
			category:     "cloud_credentials",
			wantCategory: CategoryCloudCredentials,
		},
		{
			name:         "private_key category",
			category:     "private_key",
			wantCategory: CategoryPrivateKey,
		},
		{
			name:         "api_token category",
			category:     "api_token",
			wantCategory: CategoryAPIToken,
		},
		{
			name:         "auth_token category",
			category:     "auth_token",
			wantCategory: CategoryAuthToken,
		},
		{
			name:         "sensitive_file category",
			category:     "sensitive_file",
			wantCategory: CategorySensitiveFile,
		},
		{
			name:         "database_credential category",
			category:     "database_credential",
			wantCategory: CategoryDatabaseCred,
		},
		{
			name:         "high_entropy category",
			category:     "high_entropy",
			wantCategory: CategoryHighEntropy,
		},
		{
			name:         "credit_card category",
			category:     "credit_card",
			wantCategory: CategoryCreditCard,
		},
		{
			name:         "custom category",
			category:     "custom",
			wantCategory: CategoryCustom,
		},
		{
			name:         "uppercase category",
			category:     "API_TOKEN",
			wantCategory: CategoryAPIToken,
		},
		{
			name:         "empty category defaults to custom",
			category:     "",
			wantCategory: CategoryCustom,
		},
		{
			name:         "unknown category defaults to custom",
			category:     "unknown_category",
			wantCategory: CategoryCustom,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patterns, errors := LoadCustomPatterns([]config.CustomPattern{
				{
					Name:     "test_pattern",
					Keywords: []string{"test"},
					Severity: "medium",
					Category: tt.category,
				},
			})

			require.Empty(t, errors, "expected no errors")
			require.Len(t, patterns, 1, "expected exactly one pattern")

			assert.Equal(t, tt.wantCategory, patterns[0].Category)
		})
	}
}

func TestLoadCustomPatterns_Validation(t *testing.T) {
	tests := []struct {
		name       string
		pattern    config.CustomPattern
		wantErrMsg string
	}{
		{
			name: "missing name",
			pattern: config.CustomPattern{
				Regex:    `test`,
				Severity: "high",
			},
			wantErrMsg: "pattern name is required",
		},
		{
			name: "neither regex nor keywords",
			pattern: config.CustomPattern{
				Name:     "empty_pattern",
				Severity: "high",
			},
			wantErrMsg: "either regex or keywords must be provided",
		},
		{
			name: "both regex and keywords",
			pattern: config.CustomPattern{
				Name:     "both_pattern",
				Regex:    `test`,
				Keywords: []string{"test"},
				Severity: "high",
			},
			wantErrMsg: "regex and keywords are mutually exclusive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patterns, errors := LoadCustomPatterns([]config.CustomPattern{tt.pattern})

			assert.Empty(t, patterns, "expected no valid patterns")
			require.Len(t, errors, 1, "expected exactly one error")

			assert.Contains(t, errors[0].Error(), tt.wantErrMsg)
		})
	}
}

func TestLoadCustomPatterns_MultiplePatterns(t *testing.T) {
	customPatterns := []config.CustomPattern{
		{
			Name:     "valid_regex",
			Regex:    `TEST_[0-9]+`,
			Severity: "high",
			Category: "api_token",
		},
		{
			Name:     "invalid_regex",
			Regex:    `[invalid`,
			Severity: "medium",
		},
		{
			Name:     "valid_keywords",
			Keywords: []string{"secret", "password"},
			Severity: "critical",
			Category: "auth_token",
		},
		{
			Name:     "missing_pattern",
			Severity: "low",
		},
	}

	patterns, errors := LoadCustomPatterns(customPatterns)

	// Should have 2 valid patterns
	assert.Len(t, patterns, 2, "expected 2 valid patterns")

	// Should have 2 errors
	assert.Len(t, errors, 2, "expected 2 errors")

	// Verify the valid patterns
	patternNames := make(map[string]bool)
	for _, p := range patterns {
		patternNames[p.Name] = true
	}
	assert.True(t, patternNames["valid_regex"], "expected valid_regex pattern")
	assert.True(t, patternNames["valid_keywords"], "expected valid_keywords pattern")

	// Verify error messages contain the pattern names
	foundInvalidRegex := false
	foundMissingPattern := false
	for _, err := range errors {
		msg := err.Error()
		if strings.Contains(msg, "invalid_regex") {
			foundInvalidRegex = true
		}
		if strings.Contains(msg, "missing_pattern") {
			foundMissingPattern = true
		}
	}
	assert.True(t, foundInvalidRegex, "expected error for invalid_regex")
	assert.True(t, foundMissingPattern, "expected error for missing_pattern")
}

func TestLoadCustomPatterns_EmptySlice(t *testing.T) {
	patterns, errors := LoadCustomPatterns([]config.CustomPattern{})

	assert.Empty(t, patterns, "expected no patterns")
	assert.Empty(t, errors, "expected no errors")
}

func TestLoadCustomPatterns_NilSlice(t *testing.T) {
	patterns, errors := LoadCustomPatterns(nil)

	assert.Empty(t, patterns, "expected no patterns")
	assert.Empty(t, errors, "expected no errors")
}

func TestCustomPatternError_Error(t *testing.T) {
	err := &CustomPatternError{
		PatternName: "test_pattern",
		Message:     "test error message",
	}

	assert.Equal(t, `custom pattern "test_pattern": test error message`, err.Error())
}

func TestLoadCustomPatterns_DescriptionGeneration(t *testing.T) {
	patterns, errors := LoadCustomPatterns([]config.CustomPattern{
		{
			Name:     "my_custom_pattern",
			Keywords: []string{"test"},
			Severity: "low",
		},
	})

	require.Empty(t, errors)
	require.Len(t, patterns, 1)

	// Description should contain the pattern name
	assert.Contains(t, patterns[0].Description, "my_custom_pattern")
	assert.Contains(t, patterns[0].Description, "Custom pattern")
}
