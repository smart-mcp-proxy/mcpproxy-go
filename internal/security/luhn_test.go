package security

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLuhnValid(t *testing.T) {
	tests := []struct {
		name   string
		number string
		want   bool
	}{
		// Valid test card numbers
		{
			name:   "Visa test card",
			number: "4111111111111111",
			want:   true,
		},
		{
			name:   "Stripe Visa test card",
			number: "4242424242424242",
			want:   true,
		},
		{
			name:   "Mastercard test card",
			number: "5555555555554444",
			want:   true,
		},
		{
			name:   "American Express test card",
			number: "378282246310005",
			want:   true,
		},
		{
			name:   "Discover test card",
			number: "6011111111111117",
			want:   true,
		},
		{
			name:   "JCB test card",
			number: "3566002020360505",
			want:   true,
		},

		// Invalid numbers
		{
			name:   "invalid checksum",
			number: "4111111111111112",
			want:   false,
		},
		{
			name:   "too short (12 digits)",
			number: "411111111111",
			want:   false,
		},
		{
			name:   "too long (20 digits)",
			number: "41111111111111111111",
			want:   false,
		},
		{
			name:   "empty string",
			number: "",
			want:   false,
		},
		{
			name:   "all zeros",
			number: "0000000000000000",
			want:   true, // Mathematically valid Luhn
		},
		{
			name:   "random invalid number",
			number: "1234567890123456",
			want:   false,
		},

		// Numbers with separators (should still work)
		{
			name:   "Visa with spaces",
			number: "4111 1111 1111 1111",
			want:   true,
		},
		{
			name:   "Visa with dashes",
			number: "4111-1111-1111-1111",
			want:   true,
		},
		{
			name:   "Visa with mixed separators",
			number: "4111-1111 1111-1111",
			want:   true,
		},
		{
			name:   "Amex with spaces",
			number: "3782 822463 10005",
			want:   true,
		},

		// Edge cases
		{
			name:   "13 digits (valid length)",
			number: "4222222222222",
			want:   true, // Old Visa format
		},
		{
			name:   "19 digits (max length)",
			number: "6304000000000000000",
			want:   true, // Maestro extended format
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := LuhnValid(tt.number)
			assert.Equal(t, tt.want, result, "LuhnValid(%q)", tt.number)
		})
	}
}

func TestNormalizeCardNumber(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   string
	}{
		{
			name:   "already normalized",
			input:  "4111111111111111",
			want:   "4111111111111111",
		},
		{
			name:   "spaces",
			input:  "4111 1111 1111 1111",
			want:   "4111111111111111",
		},
		{
			name:   "dashes",
			input:  "4111-1111-1111-1111",
			want:   "4111111111111111",
		},
		{
			name:   "mixed separators",
			input:  "4111-1111 1111.1111",
			want:   "4111111111111111",
		},
		{
			name:   "leading/trailing spaces",
			input:  "  4111111111111111  ",
			want:   "4111111111111111",
		},
		{
			name:   "empty string",
			input:  "",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeCardNumber(tt.input)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestExtractCardNumbers(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		wantCount  int
		wantValues []string
	}{
		{
			name:       "single valid card",
			text:       "Card number is 4111111111111111",
			wantCount:  1,
			wantValues: []string{"4111111111111111"},
		},
		{
			name:       "multiple valid cards",
			text:       "Visa: 4111111111111111, MC: 5555555555554444",
			wantCount:  2,
			wantValues: []string{"4111111111111111", "5555555555554444"},
		},
		{
			name:       "card with spaces",
			text:       "Card: 4111 1111 1111 1111",
			wantCount:  1,
			wantValues: []string{"4111 1111 1111 1111"},
		},
		{
			name:       "invalid card (bad checksum)",
			text:       "Invalid: 4111111111111112",
			wantCount:  0,
			wantValues: nil,
		},
		{
			name:       "no cards",
			text:       "No credit cards here",
			wantCount:  0,
			wantValues: nil,
		},
		{
			name:       "duplicate cards only counted once",
			text:       "Card 4111111111111111 and again 4111111111111111",
			wantCount:  1,
			wantValues: []string{"4111111111111111"},
		},
		{
			name:       "mixed valid and invalid",
			text:       "Valid: 4111111111111111, Invalid: 1234567890123456",
			wantCount:  1,
			wantValues: []string{"4111111111111111"},
		},
		{
			name:       "empty string",
			text:       "",
			wantCount:  0,
			wantValues: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractCardNumbers(tt.text)
			assert.Len(t, result, tt.wantCount)
			if tt.wantValues != nil {
				for _, expected := range tt.wantValues {
					found := false
					for _, actual := range result {
						if NormalizeCardNumber(actual) == NormalizeCardNumber(expected) {
							found = true
							break
						}
					}
					assert.True(t, found, "expected to find %s in results", expected)
				}
			}
		})
	}
}

func TestIsTestCard(t *testing.T) {
	tests := []struct {
		name   string
		number string
		want   bool
	}{
		{
			name:   "Visa test card",
			number: "4111111111111111",
			want:   true,
		},
		{
			name:   "Stripe Visa test card",
			number: "4242424242424242",
			want:   true,
		},
		{
			name:   "Mastercard test card",
			number: "5555555555554444",
			want:   true,
		},
		{
			name:   "Amex test card",
			number: "378282246310005",
			want:   true,
		},
		{
			name:   "Discover test card",
			number: "6011111111111117",
			want:   true,
		},
		{
			name:   "JCB test card",
			number: "3566002020360505",
			want:   true,
		},
		{
			name:   "test card with spaces",
			number: "4111 1111 1111 1111",
			want:   true,
		},
		{
			name:   "test card with dashes",
			number: "4111-1111-1111-1111",
			want:   true,
		},
		{
			name:   "not a test card",
			number: "4012888888881881",
			want:   false, // Valid Luhn but not a known test card
		},
		{
			name:   "empty string",
			number: "",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsTestCard(tt.number)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestGetCardType(t *testing.T) {
	tests := []struct {
		name   string
		number string
		want   string
	}{
		// Visa
		{
			name:   "Visa standard",
			number: "4111111111111111",
			want:   "visa",
		},
		{
			name:   "Visa with 4 prefix",
			number: "4242424242424242",
			want:   "visa",
		},

		// Mastercard
		{
			name:   "Mastercard 51 prefix",
			number: "5105105105105100",
			want:   "mastercard",
		},
		{
			name:   "Mastercard 52 prefix",
			number: "5200828282828210",
			want:   "mastercard",
		},
		{
			name:   "Mastercard 53 prefix",
			number: "5300000000000005",
			want:   "mastercard",
		},
		{
			name:   "Mastercard 54 prefix",
			number: "5400000000000007",
			want:   "mastercard",
		},
		{
			name:   "Mastercard 55 prefix",
			number: "5555555555554444",
			want:   "mastercard",
		},

		// American Express
		{
			name:   "Amex 34 prefix",
			number: "340000000000009",
			want:   "amex",
		},
		{
			name:   "Amex 37 prefix",
			number: "378282246310005",
			want:   "amex",
		},

		// Discover
		{
			name:   "Discover 6011 prefix",
			number: "6011111111111117",
			want:   "discover",
		},
		{
			name:   "Discover 65 prefix",
			number: "6500000000000002",
			want:   "discover",
		},

		// JCB
		{
			name:   "JCB 35 prefix",
			number: "3566002020360505",
			want:   "jcb",
		},

		// Diners Club
		{
			name:   "Diners 30 prefix",
			number: "30569309025904",
			want:   "diners",
		},
		{
			name:   "Diners 36 prefix",
			number: "36000000000008",
			want:   "diners",
		},
		{
			name:   "Diners 38 prefix",
			number: "38000000000006",
			want:   "diners",
		},

		// Unknown
		{
			name:   "unknown prefix",
			number: "9999999999999999",
			want:   "unknown",
		},
		{
			name:   "empty string",
			number: "",
			want:   "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetCardType(tt.number)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestKnownTestCards_AllValid(t *testing.T) {
	// Verify all known test cards pass Luhn validation
	for cardNumber, cardType := range KnownTestCards {
		t.Run(cardType, func(t *testing.T) {
			assert.True(t, LuhnValid(cardNumber), "test card %s (%s) should be Luhn valid", cardNumber, cardType)
		})
	}
}

func BenchmarkLuhnValid(b *testing.B) {
	number := "4111111111111111"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		LuhnValid(number)
	}
}

func BenchmarkExtractCardNumbers(b *testing.B) {
	text := strings.Repeat("Card: 4111111111111111, MC: 5555555555554444 ", 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ExtractCardNumbers(text)
	}
}
