package security

import (
	"regexp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/patterns"
)

// Pattern defines a pattern for detecting sensitive data
type Pattern struct {
	// Name is the unique identifier for this pattern (e.g., "aws_access_key")
	Name string

	// Description is human-readable explanation
	Description string

	// Regex is the compiled pattern to match
	Regex *regexp.Regexp

	// Keywords are exact strings to match (alternative to Regex)
	Keywords []string

	// Category groups related patterns
	Category Category

	// Severity indicates the risk level
	Severity Severity

	// Validate is an optional function for additional validation (e.g., Luhn)
	Validate func(match string) bool

	// KnownExamples are test/example values to flag as is_likely_example
	KnownExamples []string

	// delegate is an optional patterns.Pattern to delegate Match/IsKnownExample to
	delegate *patterns.Pattern
}

// Match checks if the content matches this pattern and returns all matches
func (p *Pattern) Match(content string) []string {
	// Delegate to patterns.Pattern if set (already filters through validator)
	if p.delegate != nil {
		return p.delegate.Match(content)
	}

	if p.Regex != nil {
		return p.Regex.FindAllString(content, -1)
	}

	// Keyword matching
	var matches []string
	for _, keyword := range p.Keywords {
		if containsWord(content, keyword) {
			matches = append(matches, keyword)
		}
	}
	return matches
}

// IsValid checks if a match passes additional validation (e.g., Luhn for credit cards)
func (p *Pattern) IsValid(match string) bool {
	if p.Validate == nil {
		return true
	}
	return p.Validate(match)
}

// IsKnownExample checks if a match is a known test/example value
func (p *Pattern) IsKnownExample(match string) bool {
	// Delegate to patterns.Pattern if set (handles normalization)
	if p.delegate != nil {
		return p.delegate.IsKnownExample(match)
	}

	for _, example := range p.KnownExamples {
		if match == example {
			return true
		}
	}
	return false
}

// containsWord checks if content contains the word (case-insensitive substring)
func containsWord(content, word string) bool {
	// Simple case-sensitive containment for now
	// Could be enhanced with word boundary detection
	return len(word) > 0 && len(content) >= len(word) &&
		(content == word ||
		 len(content) > len(word) &&
		 (content[:len(word)] == word ||
		  content[len(content)-len(word):] == word ||
		  containsSubstring(content, word)))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// PatternBuilder provides a fluent API for building patterns
type PatternBuilder struct {
	pattern Pattern
}

// NewPattern creates a new pattern builder
func NewPattern(name string) *PatternBuilder {
	return &PatternBuilder{
		pattern: Pattern{
			Name: name,
		},
	}
}

// WithDescription sets the description
func (b *PatternBuilder) WithDescription(desc string) *PatternBuilder {
	b.pattern.Description = desc
	return b
}

// WithRegex sets the regex pattern
func (b *PatternBuilder) WithRegex(pattern string) *PatternBuilder {
	b.pattern.Regex = regexp.MustCompile(pattern)
	return b
}

// WithKeywords sets the keywords to match
func (b *PatternBuilder) WithKeywords(keywords ...string) *PatternBuilder {
	b.pattern.Keywords = keywords
	return b
}

// WithCategory sets the category
func (b *PatternBuilder) WithCategory(cat Category) *PatternBuilder {
	b.pattern.Category = cat
	return b
}

// WithSeverity sets the severity
func (b *PatternBuilder) WithSeverity(sev Severity) *PatternBuilder {
	b.pattern.Severity = sev
	return b
}

// WithValidator sets the validation function
func (b *PatternBuilder) WithValidator(fn func(string) bool) *PatternBuilder {
	b.pattern.Validate = fn
	return b
}

// WithExamples sets known example values
func (b *PatternBuilder) WithExamples(examples ...string) *PatternBuilder {
	b.pattern.KnownExamples = examples
	return b
}

// Build returns the constructed pattern
func (b *PatternBuilder) Build() *Pattern {
	return &b.pattern
}

// FilePathPattern defines a sensitive file path pattern
type FilePathPattern struct {
	// Name identifies the pattern
	Name string

	// Category for grouping (e.g., "ssh", "cloud", "env")
	Category string

	// Severity for this path type
	Severity Severity

	// Patterns are glob-style patterns to match
	// Supports: * (any chars), ? (single char), ** (recursive)
	Patterns []string

	// Platform specifies the OS: "all", "linux", "darwin", "windows"
	Platform string
}
