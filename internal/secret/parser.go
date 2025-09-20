package secret

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

var (
	// secretRefRegex matches ${type:name} patterns
	secretRefRegex = regexp.MustCompile(`\$\{([^:}]+):([^}]+)\}`)
)

// ParseSecretRef parses a string that may contain secret references
func ParseSecretRef(input string) (*SecretRef, error) {
	matches := secretRefRegex.FindStringSubmatch(input)
	if len(matches) != 3 {
		return nil, fmt.Errorf("invalid secret reference format: %s", input)
	}

	return &SecretRef{
		Type:     strings.TrimSpace(matches[1]),
		Name:     strings.TrimSpace(matches[2]),
		Original: input,
	}, nil
}

// IsSecretRef returns true if the string looks like a secret reference
func IsSecretRef(input string) bool {
	return secretRefRegex.MatchString(input)
}

// FindSecretRefs finds all secret references in a string
func FindSecretRefs(input string) []*SecretRef {
	matches := secretRefRegex.FindAllStringSubmatch(input, -1)
	refs := make([]*SecretRef, 0, len(matches))

	for _, match := range matches {
		if len(match) == 3 {
			refs = append(refs, &SecretRef{
				Type:     strings.TrimSpace(match[1]),
				Name:     strings.TrimSpace(match[2]),
				Original: match[0],
			})
		}
	}

	return refs
}

// ExpandSecretRefs replaces all secret references in a string with resolved values
func (r *Resolver) ExpandSecretRefs(ctx context.Context, input string) (string, error) {
	if !IsSecretRef(input) {
		return input, nil
	}

	result := input
	refs := FindSecretRefs(input)

	for _, ref := range refs {
		value, err := r.Resolve(ctx, *ref)
		if err != nil {
			return "", fmt.Errorf("failed to resolve secret %s: %w", ref.Original, err)
		}
		result = strings.ReplaceAll(result, ref.Original, value)
	}

	return result, nil
}

// MaskSecretValue masks a secret value for safe display
func MaskSecretValue(value string) string {
	if len(value) <= 4 {
		return "****"
	}
	if len(value) <= 8 {
		return value[:2] + "****"
	}
	return value[:3] + "****" + value[len(value)-2:]
}

// DetectPotentialSecret analyzes a string to determine if it might be a secret
func DetectPotentialSecret(value string, fieldName string) (bool, float64) {
	if value == "" {
		return false, 0.0
	}

	confidence := 0.0

	// Field name indicators
	fieldLower := strings.ToLower(fieldName)
	secretKeywords := []string{
		"password", "passwd", "pass", "secret", "key", "token",
		"auth", "credential", "cred", "api_key", "apikey",
		"client_secret", "private", "priv",
	}

	for _, keyword := range secretKeywords {
		if strings.Contains(fieldLower, keyword) {
			confidence += 0.4
			break
		}
	}

	// Value characteristics
	if len(value) >= 16 {
		confidence += 0.2
	}
	if len(value) >= 32 {
		confidence += 0.1
	}

	// Contains base64-like characters
	if regexp.MustCompile(`^[A-Za-z0-9+/]+=*$`).MatchString(value) && len(value) >= 16 {
		confidence += 0.3
	}

	// Contains hex-like characters
	if regexp.MustCompile(`^[a-fA-F0-9]+$`).MatchString(value) && len(value) >= 16 {
		confidence += 0.2
	}

	// High entropy (simple check)
	if hasHighEntropy(value) {
		confidence += 0.2
	}

	// Avoid false positives for common non-secrets
	if isCommonNonSecret(value) {
		confidence *= 0.1
	}

	return confidence >= 0.5, confidence
}

// hasHighEntropy performs a simple entropy check
func hasHighEntropy(s string) bool {
	if len(s) < 8 {
		return false
	}

	charCount := make(map[rune]int)
	for _, char := range s {
		charCount[char]++
	}

	// If most characters are unique, it has high entropy
	uniqueChars := len(charCount)
	return float64(uniqueChars)/float64(len(s)) > 0.6
}

// isCommonNonSecret returns true for common values that are not secrets
func isCommonNonSecret(value string) bool {
	commonValues := []string{
		"localhost", "127.0.0.1", "example.com", "test", "admin",
		"user", "guest", "demo", "true", "false", "enabled", "disabled",
		"http://", "https://", "file://", "/tmp/", "/var/", "/usr/",
	}

	valueLower := strings.ToLower(value)
	for _, common := range commonValues {
		if strings.Contains(valueLower, common) {
			return true
		}
	}

	return false
}
