// Package patterns provides sensitive data detection patterns for various credential types.
package patterns

import (
	"regexp"
	"strings"
)

// Severity levels for detected patterns
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
)

// Category of pattern
type Category string

const (
	CategoryCloudCredentials Category = "cloud_credentials"
	CategoryPrivateKey       Category = "private_key"
	CategoryAPIToken         Category = "api_token"
	CategoryAuthToken        Category = "auth_token"
	CategorySensitiveFile    Category = "sensitive_file"
	CategoryDatabaseCred     Category = "database_credential"
	CategoryHighEntropy      Category = "high_entropy"
	CategoryCreditCard       Category = "credit_card"
	CategoryCustom           Category = "custom"
)

// Pattern represents a sensitive data detection pattern
type Pattern struct {
	Name          string
	Category      Category
	Severity      Severity
	Description   string
	regex         *regexp.Regexp
	keywords      []string
	validator     func(match string) bool
	normalizer    func(match string) string // Normalizes match before known example lookup
	knownExamples map[string]bool
	confidence    float64 // explicit per-pattern confidence; 0 = derive from Severity
}

// confidenceExample is the confidence assigned to a documented example/placeholder
// value (e.g. an AWS doc key like AKIA…EXAMPLE). Near-zero so a placeholder is
// never treated as a live secret (Spec 076, FR-012 false-positive control).
const confidenceExample = 0.1

// severityConfidence maps a pattern severity to a default 0.0–1.0 confidence.
// Validated/high-distinctiveness matchers (critical) sit at the top; broad,
// entropy-only matchers (low) sit at the bottom. Monotonic by design.
func severityConfidence(s Severity) float64 {
	switch s {
	case SeverityCritical:
		return 0.9
	case SeverityHigh:
		return 0.7
	case SeverityMedium:
		return 0.4
	case SeverityLow:
		return 0.2
	default:
		return 0.4
	}
}

// ConfidenceFor returns the 0.0–1.0 confidence that a given match is a genuine
// sensitive value (Spec 076 T015). A documented example collapses to
// confidenceExample; an explicit WithConfidence wins over the severity default;
// otherwise the severity-derived default applies. This is purely additive — it
// does not change Match/IsValid/IsKnownExample behavior or any existing caller.
func (p *Pattern) ConfidenceFor(match string) float64 {
	if p.IsKnownExample(match) {
		return confidenceExample
	}
	if p.confidence > 0 {
		return p.confidence
	}
	return severityConfidence(p.Severity)
}

// Match finds all matches in the given content
// If a validator is set, only matches that pass validation are returned
func (p *Pattern) Match(content string) []string {
	var matches []string

	if p.regex != nil {
		matches = p.regex.FindAllString(content, -1)
	} else if len(p.keywords) > 0 {
		contentLower := strings.ToLower(content)
		for _, kw := range p.keywords {
			if strings.Contains(contentLower, strings.ToLower(kw)) {
				matches = append(matches, kw)
			}
		}
	}

	// Apply validator if present to filter matches
	if p.validator != nil && len(matches) > 0 {
		var valid []string
		for _, m := range matches {
			if p.validator(m) {
				valid = append(valid, m)
			}
		}
		return valid
	}

	return matches
}

// IsValid validates a match using the pattern's validator
func (p *Pattern) IsValid(match string) bool {
	if p.validator == nil {
		return true
	}
	return p.validator(match)
}

// IsKnownExample checks if a match is a known test/example value
func (p *Pattern) IsKnownExample(match string) bool {
	if p.knownExamples == nil {
		return false
	}
	// Apply normalizer if present (e.g., for credit cards: strip separators)
	key := match
	if p.normalizer != nil {
		key = p.normalizer(match)
	}
	return p.knownExamples[key]
}

// PatternBuilder provides a fluent API for building patterns
type PatternBuilder struct {
	pattern *Pattern
}

// NewPattern creates a new pattern builder
func NewPattern(name string) *PatternBuilder {
	return &PatternBuilder{
		pattern: &Pattern{
			Name:          name,
			Category:      CategoryCustom,
			Severity:      SeverityMedium,
			knownExamples: make(map[string]bool),
		},
	}
}

// WithRegex sets the regex pattern
func (b *PatternBuilder) WithRegex(pattern string) *PatternBuilder {
	b.pattern.regex = regexp.MustCompile(pattern)
	return b
}

// WithKeywords sets the keywords for matching
func (b *PatternBuilder) WithKeywords(keywords ...string) *PatternBuilder {
	b.pattern.keywords = keywords
	return b
}

// WithCategory sets the pattern category
func (b *PatternBuilder) WithCategory(category Category) *PatternBuilder {
	b.pattern.Category = category
	return b
}

// WithSeverity sets the pattern severity
func (b *PatternBuilder) WithSeverity(severity Severity) *PatternBuilder {
	b.pattern.Severity = severity
	return b
}

// WithDescription sets the pattern description
func (b *PatternBuilder) WithDescription(description string) *PatternBuilder {
	b.pattern.Description = description
	return b
}

// WithValidator sets a custom validator function
func (b *PatternBuilder) WithValidator(validator func(string) bool) *PatternBuilder {
	b.pattern.validator = validator
	return b
}

// WithKnownExamples sets known example values (like AWS example keys)
func (b *PatternBuilder) WithKnownExamples(examples ...string) *PatternBuilder {
	for _, ex := range examples {
		b.pattern.knownExamples[ex] = true
	}
	return b
}

// WithNormalizer sets a function to normalize matches before known example lookup
func (b *PatternBuilder) WithNormalizer(normalizer func(string) string) *PatternBuilder {
	b.pattern.normalizer = normalizer
	return b
}

// WithConfidence sets an explicit per-pattern confidence (0.0–1.0), overriding
// the severity-derived default. Used to mark validated matchers (e.g. Luhn cards)
// as high and broad/generic matchers as low (Spec 076 T015).
func (b *PatternBuilder) WithConfidence(c float64) *PatternBuilder {
	b.pattern.confidence = c
	return b
}

// Build creates the Pattern
func (b *PatternBuilder) Build() *Pattern {
	return b.pattern
}
