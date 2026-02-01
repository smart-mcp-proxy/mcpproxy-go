package patterns

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Test Visa card detection
func TestVisaCardPattern(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantMatch   bool
		wantExample bool
	}{
		{
			name:        "Visa test card",
			input:       "4111111111111111",
			wantMatch:   true,
			wantExample: true,
		},
		{
			name:        "Stripe Visa test card",
			input:       "4242424242424242",
			wantMatch:   true,
			wantExample: true,
		},
		{
			name:        "Visa with spaces",
			input:       "4111 1111 1111 1111",
			wantMatch:   true,
			wantExample: true,
		},
		{
			name:        "Visa with dashes",
			input:       "4111-1111-1111-1111",
			wantMatch:   true,
			wantExample: true,
		},
		{
			name:        "Visa in text",
			input:       "Card number: 4111111111111111 is used for testing",
			wantMatch:   true,
			wantExample: true,
		},
		{
			name:        "Invalid Visa (bad checksum)",
			input:       "4111111111111112",
			wantMatch:   false,
			wantExample: false,
		},
	}

	patterns := GetCreditCardPatterns()
	ccPattern := findPatternByName(patterns, "credit_card")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if ccPattern == nil {
				t.Skip("Credit card pattern not implemented yet")
				return
			}
			matches := ccPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
				if len(matches) > 0 && tt.wantExample {
					assert.True(t, ccPattern.IsKnownExample(matches[0]), "expected to be known example")
				}
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test Mastercard detection
func TestMastercardPattern(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantMatch   bool
		wantExample bool
	}{
		{
			name:        "Mastercard test card",
			input:       "5555555555554444",
			wantMatch:   true,
			wantExample: true,
		},
		{
			name:        "Mastercard with spaces",
			input:       "5555 5555 5555 4444",
			wantMatch:   true,
			wantExample: true,
		},
		{
			name:        "Invalid Mastercard prefix",
			input:       "5000000000000000",
			wantMatch:   false,
			wantExample: false,
		},
	}

	patterns := GetCreditCardPatterns()
	ccPattern := findPatternByName(patterns, "credit_card")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if ccPattern == nil {
				t.Skip("Credit card pattern not implemented yet")
				return
			}
			matches := ccPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test American Express detection
func TestAmexPattern(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantMatch   bool
		wantExample bool
	}{
		{
			name:        "Amex test card",
			input:       "378282246310005",
			wantMatch:   true,
			wantExample: true,
		},
		{
			name:        "Amex with spaces",
			input:       "3782 822463 10005",
			wantMatch:   true,
			wantExample: true,
		},
	}

	patterns := GetCreditCardPatterns()
	ccPattern := findPatternByName(patterns, "credit_card")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if ccPattern == nil {
				t.Skip("Credit card pattern not implemented yet")
				return
			}
			matches := ccPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test Discover card detection
func TestDiscoverPattern(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantMatch   bool
		wantExample bool
	}{
		{
			name:        "Discover test card",
			input:       "6011111111111117",
			wantMatch:   true,
			wantExample: true,
		},
	}

	patterns := GetCreditCardPatterns()
	ccPattern := findPatternByName(patterns, "credit_card")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if ccPattern == nil {
				t.Skip("Credit card pattern not implemented yet")
				return
			}
			matches := ccPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test JCB card detection
func TestJCBPattern(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantMatch   bool
		wantExample bool
	}{
		{
			name:        "JCB test card",
			input:       "3566002020360505",
			wantMatch:   true,
			wantExample: true,
		},
	}

	patterns := GetCreditCardPatterns()
	ccPattern := findPatternByName(patterns, "credit_card")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if ccPattern == nil {
				t.Skip("Credit card pattern not implemented yet")
				return
			}
			matches := ccPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test various separators
func TestCreditCardSeparators(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "no separator",
			input:     "4111111111111111",
			wantMatch: true,
		},
		{
			name:      "space separator",
			input:     "4111 1111 1111 1111",
			wantMatch: true,
		},
		{
			name:      "dash separator",
			input:     "4111-1111-1111-1111",
			wantMatch: true,
		},
		{
			name:      "dot separator",
			input:     "4111.1111.1111.1111",
			wantMatch: true,
		},
		{
			name:      "mixed separators",
			input:     "4111-1111 1111.1111",
			wantMatch: true,
		},
	}

	patterns := GetCreditCardPatterns()
	ccPattern := findPatternByName(patterns, "credit_card")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if ccPattern == nil {
				t.Skip("Credit card pattern not implemented yet")
				return
			}
			matches := ccPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test invalid card numbers
func TestInvalidCreditCards(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "too short",
			input:     "411111111111",
			wantMatch: false,
		},
		{
			name:      "too long",
			input:     "41111111111111111111",
			wantMatch: false,
		},
		{
			name:      "all zeros",
			input:     "0000000000000000",
			wantMatch: false, // While Luhn valid, not a real card
		},
		{
			name:      "random invalid",
			input:     "1234567890123456",
			wantMatch: false,
		},
		{
			name:      "letters mixed in",
			input:     "4111-1111-abcd-1111",
			wantMatch: false,
		},
	}

	patterns := GetCreditCardPatterns()
	ccPattern := findPatternByName(patterns, "credit_card")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if ccPattern == nil {
				t.Skip("Credit card pattern not implemented yet")
				return
			}
			matches := ccPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}
