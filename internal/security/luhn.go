package security

import (
	"regexp"
	"strings"
)

// nonDigit matches any non-digit character for stripping from card numbers
var nonDigit = regexp.MustCompile(`\D`)

// LuhnValid validates a credit card number using the Luhn algorithm.
// The Luhn algorithm is used to validate credit card numbers and other identification numbers.
//
// It accepts card numbers with various separators (spaces, dashes) which are stripped before validation.
// Valid card numbers are typically 13-19 digits.
func LuhnValid(number string) bool {
	// Remove all non-digit characters (handles spaces, dashes, etc.)
	digits := nonDigit.ReplaceAllString(number, "")

	// Credit cards are typically 13-19 digits
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}

	// Luhn algorithm implementation
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

// NormalizeCardNumber removes all non-digit characters from a card number
func NormalizeCardNumber(number string) string {
	return nonDigit.ReplaceAllString(number, "")
}

// ExtractCardNumbers finds potential card numbers in text and validates them
// Returns only Luhn-valid card numbers
func ExtractCardNumbers(text string) []string {
	// Pattern matches 13-19 digits with optional separators
	cardPattern := regexp.MustCompile(`\b(?:\d[ \-]*?){13,19}\b`)
	candidates := cardPattern.FindAllString(text, -1)

	var validCards []string
	seen := make(map[string]bool)

	for _, candidate := range candidates {
		normalized := NormalizeCardNumber(candidate)
		if !seen[normalized] && LuhnValid(normalized) {
			seen[normalized] = true
			validCards = append(validCards, candidate)
		}
	}

	return validCards
}

// KnownTestCards contains well-known test card numbers used in development
var KnownTestCards = map[string]string{
	"4111111111111111": "visa_test",        // Visa test card
	"4242424242424242": "stripe_visa_test", // Stripe Visa test
	"5555555555554444": "mastercard_test",  // Mastercard test
	"378282246310005":  "amex_test",        // Amex test
	"6011111111111117": "discover_test",    // Discover test
	"3566002020360505": "jcb_test",         // JCB test
}

// IsTestCard checks if a card number is a known test card
func IsTestCard(number string) bool {
	normalized := NormalizeCardNumber(number)
	_, isTest := KnownTestCards[normalized]
	return isTest
}

// GetCardType returns the card type based on the number prefix
func GetCardType(number string) string {
	normalized := NormalizeCardNumber(number)
	if len(normalized) < 1 {
		return "unknown"
	}

	// Check prefixes
	switch {
	case strings.HasPrefix(normalized, "4"):
		return "visa"
	case strings.HasPrefix(normalized, "51") || strings.HasPrefix(normalized, "52") ||
		strings.HasPrefix(normalized, "53") || strings.HasPrefix(normalized, "54") ||
		strings.HasPrefix(normalized, "55"):
		return "mastercard"
	case strings.HasPrefix(normalized, "34") || strings.HasPrefix(normalized, "37"):
		return "amex"
	case strings.HasPrefix(normalized, "6011") || strings.HasPrefix(normalized, "65"):
		return "discover"
	case strings.HasPrefix(normalized, "35"):
		return "jcb"
	case strings.HasPrefix(normalized, "30") || strings.HasPrefix(normalized, "36") ||
		strings.HasPrefix(normalized, "38"):
		return "diners"
	default:
		return "unknown"
	}
}
