package security

import (
	"math"
	"regexp"
)

// highEntropyCandidate matches strings that might be high-entropy secrets
// Matches: base64-like strings, hex strings, or alphanumeric strings 20+ chars
var highEntropyCandidate = regexp.MustCompile(`[a-zA-Z0-9+/=_\-]{20,}`)

// ShannonEntropy calculates the Shannon entropy of a string.
// Higher entropy (> 4.5) indicates more randomness, suggesting a potential secret.
//
// Entropy ranges:
// - < 3.0: Low entropy (natural language, repeated chars)
// - 3.0-4.0: Medium entropy (encoded data)
// - 4.0-4.5: High entropy (possibly a secret)
// - > 4.5: Very high entropy (likely a random secret)
func ShannonEntropy(s string) float64 {
	if len(s) == 0 {
		return 0
	}

	// Count character frequencies
	freq := make(map[rune]int)
	for _, r := range s {
		freq[r]++
	}

	// Calculate entropy: H(X) = -Σ p(x) * log2(p(x))
	var entropy float64
	length := float64(len(s))
	for _, count := range freq {
		p := float64(count) / length
		entropy -= p * math.Log2(p)
	}
	return entropy
}

// FindHighEntropyStrings finds strings with entropy above the threshold
func FindHighEntropyStrings(content string, threshold float64, maxMatches int) []string {
	if threshold <= 0 {
		threshold = 4.5 // Default threshold
	}
	if maxMatches <= 0 {
		maxMatches = 10 // Default max matches
	}

	matches := highEntropyCandidate.FindAllString(content, maxMatches*2)
	var highEntropyMatches []string

	for _, match := range matches {
		if len(highEntropyMatches) >= maxMatches {
			break
		}

		entropy := ShannonEntropy(match)
		if entropy > threshold {
			highEntropyMatches = append(highEntropyMatches, match)
		}
	}

	return highEntropyMatches
}

// IsHighEntropy checks if a string has entropy above the threshold
func IsHighEntropy(s string, threshold float64) bool {
	if threshold <= 0 {
		threshold = 4.5
	}
	return ShannonEntropy(s) > threshold
}
