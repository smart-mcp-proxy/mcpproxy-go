package patterns

import (
	"regexp"
	"strings"
)

// nonDigit matches any non-digit character
var nonDigit = regexp.MustCompile(`\D`)

// GetCreditCardPatterns returns credit card detection patterns
func GetCreditCardPatterns() []*Pattern {
	return []*Pattern{
		creditCardPattern(),
	}
}

// normalizeCreditCard removes all non-digit characters from a credit card number
func normalizeCreditCard(s string) string {
	return nonDigit.ReplaceAllString(s, "")
}

// creditCardPattern detects credit card numbers with Luhn validation
func creditCardPattern() *Pattern {
	// Known test card numbers (stored normalized - digits only)
	knownTestCards := []string{
		"4111111111111111",  // Visa test
		"4242424242424242",  // Stripe Visa test
		"5555555555554444",  // Mastercard test
		"378282246310005",   // Amex test
		"6011111111111117",  // Discover test
		"3566002020360505",  // JCB test
	}

	builder := NewPattern("credit_card").
		WithRegex(`\b(?:\d[ .\-]*?){13,19}\b`).
		WithCategory(CategoryCreditCard).
		WithSeverity(SeverityCritical).
		WithDescription("Credit card number").
		WithValidator(validateCreditCard).
		WithNormalizer(normalizeCreditCard).
		WithKnownExamples(knownTestCards...)

	return builder.Build()
}

// validateCreditCard validates a potential credit card number
func validateCreditCard(candidate string) bool {
	// Normalize: remove all non-digits
	digits := nonDigit.ReplaceAllString(candidate, "")

	// Check length (13-19 digits for credit cards)
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}

	// Check valid card prefix
	if !hasValidCardPrefix(digits) {
		return false
	}

	// Check Luhn algorithm
	return luhnValid(digits)
}

// hasValidCardPrefix checks if the card number starts with a valid prefix
func hasValidCardPrefix(digits string) bool {
	if len(digits) < 1 {
		return false
	}

	// Visa: starts with 4
	if strings.HasPrefix(digits, "4") {
		return true
	}

	// Mastercard: starts with 51-55 or 2221-2720
	if len(digits) >= 2 {
		prefix2 := digits[:2]
		if prefix2 >= "51" && prefix2 <= "55" {
			return true
		}
		if len(digits) >= 4 {
			prefix4 := digits[:4]
			if prefix4 >= "2221" && prefix4 <= "2720" {
				return true
			}
		}
	}

	// American Express: starts with 34 or 37
	if strings.HasPrefix(digits, "34") || strings.HasPrefix(digits, "37") {
		return true
	}

	// Discover: starts with 6011, 644-649, or 65
	if strings.HasPrefix(digits, "6011") || strings.HasPrefix(digits, "65") {
		return true
	}
	if len(digits) >= 3 {
		prefix3 := digits[:3]
		if prefix3 >= "644" && prefix3 <= "649" {
			return true
		}
	}

	// JCB: starts with 35
	if strings.HasPrefix(digits, "35") {
		return true
	}

	// Diners Club: starts with 30, 36, 38, 39
	if strings.HasPrefix(digits, "30") || strings.HasPrefix(digits, "36") ||
		strings.HasPrefix(digits, "38") || strings.HasPrefix(digits, "39") {
		return true
	}

	return false
}

// luhnValid implements the Luhn algorithm for credit card validation
func luhnValid(digits string) bool {
	sum := 0
	alt := false
	for i := len(digits) - 1; i >= 0; i-- {
		n := int(digits[i] - '0')
		if alt {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		alt = !alt
	}
	return sum%10 == 0
}
