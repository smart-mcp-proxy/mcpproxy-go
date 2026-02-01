package security

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestShannonEntropy(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		minExpect float64
		maxExpect float64
	}{
		{
			name:      "empty string",
			input:     "",
			minExpect: 0,
			maxExpect: 0,
		},
		{
			name:      "single character",
			input:     "a",
			minExpect: 0,
			maxExpect: 0,
		},
		{
			name:      "repeated character - very low entropy",
			input:     "aaaaaaaaaaaaaaaa",
			minExpect: 0,
			maxExpect: 0.1,
		},
		{
			name:      "two alternating characters",
			input:     "abababababababab",
			minExpect: 0.9,
			maxExpect: 1.1,
		},
		{
			name:      "lowercase alphabet - high entropy",
			input:     "abcdefghijklmnopqrstuvwxyz",
			minExpect: 4.5,
			maxExpect: 5.0,
		},
		{
			name:      "natural language - medium entropy",
			input:     "the quick brown fox jumps over the lazy dog",
			minExpect: 3.5,
			maxExpect: 4.5,
		},
		{
			name:      "hex string - high entropy",
			input:     "0123456789abcdef",
			minExpect: 3.9,
			maxExpect: 4.1,
		},
		{
			name:      "base64-like high entropy secret",
			input:     "aBcDeFgHiJkLmNoPqRsTuVwXyZ012345",
			minExpect: 4.8,
			maxExpect: 5.2,
		},
		{
			name:      "typical API key pattern",
			input:     "sk_test_Abc123Def456Ghi789Jkl0",
			minExpect: 4.0,
			maxExpect: 5.0,
		},
		{
			name:      "UUID - medium entropy",
			input:     "550e8400e29b41d4a716446655440000",
			minExpect: 3.0,
			maxExpect: 4.0,
		},
		{
			name:      "binary-like (0 and 1 only)",
			input:     "0110101010110101",
			minExpect: 0.9,
			maxExpect: 1.1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entropy := ShannonEntropy(tt.input)
			assert.GreaterOrEqual(t, entropy, tt.minExpect, "entropy should be >= %f, got %f", tt.minExpect, entropy)
			assert.LessOrEqual(t, entropy, tt.maxExpect, "entropy should be <= %f, got %f", tt.maxExpect, entropy)
		})
	}
}

func TestShannonEntropy_CharacterSets(t *testing.T) {
	// Test different character sets to understand entropy behavior
	t.Run("digits only", func(t *testing.T) {
		entropy := ShannonEntropy("0123456789")
		assert.Greater(t, entropy, 3.0) // log2(10) ≈ 3.32
	})

	t.Run("uppercase only", func(t *testing.T) {
		entropy := ShannonEntropy("ABCDEFGHIJKLMNOPQRSTUVWXYZ")
		assert.Greater(t, entropy, 4.5) // log2(26) ≈ 4.7
	})

	t.Run("mixed case and digits", func(t *testing.T) {
		entropy := ShannonEntropy("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789")
		assert.Greater(t, entropy, 5.5) // log2(62) ≈ 5.95
	})

	t.Run("base64 chars including + and /", func(t *testing.T) {
		entropy := ShannonEntropy("ABCDabcd0123+/==")
		assert.Greater(t, entropy, 3.5)
	})
}

func TestFindHighEntropyStrings(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		threshold   float64
		maxMatches  int
		wantMatches int
		wantEmpty   bool
	}{
		{
			name:        "empty content",
			content:     "",
			threshold:   4.5,
			maxMatches:  10,
			wantMatches: 0,
			wantEmpty:   true,
		},
		{
			name:        "no high entropy strings",
			content:     "this is just normal text without secrets",
			threshold:   4.5,
			maxMatches:  10,
			wantMatches: 0,
			wantEmpty:   true,
		},
		{
			name:        "contains high entropy secret",
			content:     "api_key=aBcDeFgHiJkLmNoPqRsTuVwXyZ0123",
			threshold:   4.5,
			maxMatches:  10,
			wantMatches: 1,
			wantEmpty:   false,
		},
		{
			name:        "multiple secrets",
			content:     "key1=aBcDeFgHiJkLmNoPqRsTuVwXyZ0123 key2=xYzAbCdEfGhIjKlMnOpQrStUv9876",
			threshold:   4.5,
			maxMatches:  10,
			wantMatches: 2,
			wantEmpty:   false,
		},
		{
			name:        "respects max matches",
			content:     "k1=aBcDeFgHiJkLmNoPqRsTuVw k2=xYzAbCdEfGhIjKlMnOpQr k3=zZyYxWvUtSrQpOnMlKjI",
			threshold:   4.0,
			maxMatches:  2,
			wantMatches: 2,
			wantEmpty:   false,
		},
		{
			name:        "default threshold when zero",
			content:     "secret=aBcDeFgHiJkLmNoPqRsTuVwXyZ",
			threshold:   0, // Should use default 4.5
			maxMatches:  10,
			wantMatches: 1,
			wantEmpty:   false,
		},
		{
			name:        "default maxMatches when zero",
			content:     "secret=aBcDeFgHiJkLmNoPqRsTuVwXyZ",
			threshold:   4.5,
			maxMatches:  0, // Should use default 10
			wantMatches: 1,
			wantEmpty:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := FindHighEntropyStrings(tt.content, tt.threshold, tt.maxMatches)

			if tt.wantEmpty {
				assert.Empty(t, matches)
			} else {
				assert.Len(t, matches, tt.wantMatches)
			}
		})
	}
}

func TestFindHighEntropyStrings_MinLength(t *testing.T) {
	// The highEntropyCandidate regex requires 20+ chars
	t.Run("strings shorter than 20 chars not detected", func(t *testing.T) {
		content := "short=aBcDeF123" // Only 9 chars after =
		matches := FindHighEntropyStrings(content, 4.0, 10)
		assert.Empty(t, matches)
	})

	t.Run("strings 20+ chars detected", func(t *testing.T) {
		content := "long=aBcDeFgHiJkLmNoPqRsT" // 20 chars after =
		matches := FindHighEntropyStrings(content, 4.0, 10)
		assert.NotEmpty(t, matches)
	})
}

func TestIsHighEntropy(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		threshold float64
		want      bool
	}{
		{
			name:      "high entropy string above threshold",
			input:     "aBcDeFgHiJkLmNoPqRsTuVwXyZ",
			threshold: 4.0,
			want:      true,
		},
		{
			name:      "low entropy string below threshold",
			input:     "aaaaaaaaaa",
			threshold: 4.0,
			want:      false,
		},
		{
			name:      "medium entropy below threshold",
			input:     "aaaaaaaaaa",
			threshold: 1.0,
			want:      false, // entropy is 0, below threshold
		},
		{
			name:      "default threshold when zero",
			input:     "aBcDeFgHiJkLmNoPqRsTuVwXyZ012345",
			threshold: 0, // Should use default 4.5
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsHighEntropy(tt.input, tt.threshold)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestHighEntropyCandidate_Pattern(t *testing.T) {
	// Test the regex pattern directly
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "base64 string",
			input:     "SGVsbG8gV29ybGQhIFRoaXMgaXMgYSB0ZXN0",
			wantMatch: true,
		},
		{
			name:      "hex string",
			input:     "0123456789abcdef0123456789abcdef",
			wantMatch: true,
		},
		{
			name:      "alphanumeric with underscores",
			input:     "my_secret_key_12345678",
			wantMatch: true,
		},
		{
			name:      "alphanumeric with dashes",
			input:     "my-secret-key-12345678",
			wantMatch: true,
		},
		{
			name:      "short string (< 20 chars)",
			input:     "short",
			wantMatch: false,
		},
		{
			name:      "string with spaces",
			input:     "this has spaces in it",
			wantMatch: false, // spaces not in pattern
		},
		{
			name:      "URL-safe base64",
			input:     "base64_url_safe_string_123",
			wantMatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := highEntropyCandidate.FindAllString(tt.input, -1)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

func BenchmarkShannonEntropy(b *testing.B) {
	testString := strings.Repeat("abcdefghijklmnopqrstuvwxyz0123456789", 10)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ShannonEntropy(testString)
	}
}

func BenchmarkFindHighEntropyStrings(b *testing.B) {
	content := strings.Repeat("normal text with secret=aBcDeFgHiJkLmNoPqRsTuVwXyZ ", 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FindHighEntropyStrings(content, 4.5, 10)
	}
}
