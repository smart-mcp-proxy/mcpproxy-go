package logs

import (
	"regexp"
	"strings"
	"sync"

	"go.uber.org/zap/zapcore"
)

// SecretSanitizer wraps a zapcore.Core to sanitize sensitive values from logs
type SecretSanitizer struct {
	zapcore.Core
	patterns      []*secretPattern
	resolvedCache sync.Map // Cache of resolved secret values to mask
}

// secretPattern defines a pattern for detecting and masking secrets
type secretPattern struct {
	name    string
	regex   *regexp.Regexp
	maskFunc func(string) string
}

// NewSecretSanitizer creates a new sanitizing core that wraps the provided core
func NewSecretSanitizer(core zapcore.Core) *SecretSanitizer {
	s := &SecretSanitizer{
		Core:     core,
		patterns: make([]*secretPattern, 0),
	}

	// Register common secret patterns
	s.registerDefaultPatterns()

	return s
}

// registerDefaultPatterns registers patterns for common secret formats
func (s *SecretSanitizer) registerDefaultPatterns() {
	// GitHub tokens (ghp_, gho_, ghu_, ghs_, ghr_)
	s.patterns = append(s.patterns, &secretPattern{
		name:  "github_token",
		regex: regexp.MustCompile(`\b(gh[poushr]_[A-Za-z0-9]{36,255})\b`),
		maskFunc: func(token string) string {
			if len(token) <= 7 {
				return "****"
			}
			return token[:7] + "***" + token[len(token)-2:]
		},
	})

	// OpenAI API keys (sk-...)
	s.patterns = append(s.patterns, &secretPattern{
		name:  "openai_key",
		regex: regexp.MustCompile(`\b(sk-[A-Za-z0-9]{20,})\b`),
		maskFunc: func(key string) string {
			if len(key) <= 5 {
				return "****"
			}
			return key[:5] + "***" + key[len(key)-2:]
		},
	})

	// Anthropic API keys (sk-ant-...)
	s.patterns = append(s.patterns, &secretPattern{
		name:  "anthropic_key",
		regex: regexp.MustCompile(`\b(sk-ant-[A-Za-z0-9\-]{30,})\b`),
		maskFunc: func(key string) string {
			if len(key) <= 10 {
				return "****"
			}
			return key[:10] + "***" + key[len(key)-2:]
		},
	})

	// Generic Bearer tokens
	s.patterns = append(s.patterns, &secretPattern{
		name:  "bearer_token",
		regex: regexp.MustCompile(`\b(Bearer\s+[A-Za-z0-9\-\._~\+\/]+=*)\b`),
		maskFunc: func(token string) string {
			parts := strings.SplitN(token, " ", 2)
			if len(parts) != 2 {
				return "Bearer ****"
			}
			if len(parts[1]) <= 4 {
				return "Bearer ****"
			}
			return "Bearer " + parts[1][:4] + "***" + parts[1][len(parts[1])-2:]
		},
	})

	// AWS keys (AKIA...)
	s.patterns = append(s.patterns, &secretPattern{
		name:  "aws_key",
		regex: regexp.MustCompile(`\b(AKIA[0-9A-Z]{16})\b`),
		maskFunc: func(key string) string {
			return key[:8] + "***" + key[len(key)-2:]
		},
	})

	// Generic high-entropy strings (likely tokens/passwords)
	// Only mask if they appear in suspicious contexts (after = : or in quotes)
	s.patterns = append(s.patterns, &secretPattern{
		name:  "high_entropy",
		regex: regexp.MustCompile(`(["\']|[=:][\s]*)(["'])?([A-Za-z0-9+/]{32,}={0,2})(["'])?`),
		maskFunc: func(match string) string {
			// Extract the actual value
			re := regexp.MustCompile(`(["\']|[=:][\s]*)(["'])?([A-Za-z0-9+/]{32,}={0,2})(["'])?`)
			parts := re.FindStringSubmatch(match)
			if len(parts) < 4 {
				return match
			}
			prefix := parts[1]
			openQuote := parts[2]
			value := parts[3]
			closeQuote := parts[4]

			// Only mask if it has high entropy
			if hasHighEntropy(value) {
				masked := maskValue(value)
				return prefix + openQuote + masked + closeQuote
			}
			return match
		},
	})

	// JWT tokens (eyJ...)
	s.patterns = append(s.patterns, &secretPattern{
		name:  "jwt",
		regex: regexp.MustCompile(`\b(eyJ[A-Za-z0-9\-_]+\.eyJ[A-Za-z0-9\-_]+\.[A-Za-z0-9\-_]+)\b`),
		maskFunc: func(jwt string) string {
			parts := strings.Split(jwt, ".")
			if len(parts) != 3 {
				return "****"
			}
			// Show first part (header) but mask payload and signature
			return parts[0] + ".***." + parts[2][len(parts[2])-4:]
		},
	})
}

// RegisterResolvedSecret registers a secret value that was resolved from keyring/env
// so it can be masked in logs
func (s *SecretSanitizer) RegisterResolvedSecret(value string) {
	if value == "" || len(value) < 4 {
		return
	}
	s.resolvedCache.Store(value, true)
}

// UnregisterResolvedSecret removes a secret from the mask cache
func (s *SecretSanitizer) UnregisterResolvedSecret(value string) {
	s.resolvedCache.Delete(value)
}

// sanitizeString applies all registered patterns to mask secrets
func (s *SecretSanitizer) sanitizeString(str string) string {
	result := str

	// First, mask any explicitly registered resolved secrets
	s.resolvedCache.Range(func(key, value interface{}) bool {
		secretValue, ok := key.(string)
		if !ok || secretValue == "" {
			return true
		}

		// Only mask if the secret is substantial enough
		if len(secretValue) >= 8 {
			masked := maskValue(secretValue)
			result = strings.ReplaceAll(result, secretValue, masked)
		}
		return true
	})

	// Then apply pattern-based masking
	for _, pattern := range s.patterns {
		result = pattern.regex.ReplaceAllStringFunc(result, pattern.maskFunc)
	}

	return result
}

// Write sanitizes the entry before writing
func (s *SecretSanitizer) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	// Sanitize entry message
	entry.Message = s.sanitizeString(entry.Message)

	// Sanitize fields
	sanitizedFields := make([]zapcore.Field, len(fields))
	for i, field := range fields {
		sanitizedFields[i] = s.sanitizeField(field)
	}

	return s.Core.Write(entry, sanitizedFields)
}

// sanitizeField sanitizes a zap field
func (s *SecretSanitizer) sanitizeField(field zapcore.Field) zapcore.Field {
	switch field.Type {
	case zapcore.StringType:
		field.String = s.sanitizeString(field.String)
	case zapcore.ByteStringType:
		// Convert to string, sanitize, convert back
		original := string(field.Interface.([]byte))
		sanitized := s.sanitizeString(original)
		field.Interface = []byte(sanitized)
	case zapcore.ReflectType:
		// For complex types, we sanitize the string representation
		// This is a best-effort approach
		if stringer, ok := field.Interface.(interface{ String() string }); ok {
			original := stringer.String()
			sanitized := s.sanitizeString(original)
			if original != sanitized {
				// Replace with sanitized string
				field = zapcore.Field{
					Key:    field.Key,
					Type:   zapcore.StringType,
					String: sanitized,
				}
			}
		}
	}
	return field
}

// With creates a sanitizing child core
func (s *SecretSanitizer) With(fields []zapcore.Field) zapcore.Core {
	sanitizedFields := make([]zapcore.Field, len(fields))
	for i, field := range fields {
		sanitizedFields[i] = s.sanitizeField(field)
	}
	return &SecretSanitizer{
		Core:          s.Core.With(sanitizedFields),
		patterns:      s.patterns,
		resolvedCache: s.resolvedCache,
	}
}

// Check delegates to the wrapped core
func (s *SecretSanitizer) Check(entry zapcore.Entry, checkedEntry *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if s.Enabled(entry.Level) {
		return checkedEntry.AddCore(entry, s)
	}
	return checkedEntry
}

// Helper functions

// maskValue masks a secret value showing first 3 and last 2 characters
func maskValue(value string) string {
	if len(value) <= 5 {
		return "****"
	}
	if len(value) <= 8 {
		return value[:2] + "****"
	}
	return value[:3] + "***" + value[len(value)-2:]
}

// hasHighEntropy checks if a string has high entropy (likely a secret)
func hasHighEntropy(s string) bool {
	if len(s) < 16 {
		return false
	}

	// Count unique characters
	charCount := make(map[rune]int)
	for _, char := range s {
		charCount[char]++
	}

	// If most characters are unique, it has high entropy
	uniqueRatio := float64(len(charCount)) / float64(len(s))

	// Also check for variety of character types
	hasUpper := false
	hasLower := false
	hasDigit := false
	hasSpecial := false

	for _, char := range s {
		switch {
		case char >= 'A' && char <= 'Z':
			hasUpper = true
		case char >= 'a' && char <= 'z':
			hasLower = true
		case char >= '0' && char <= '9':
			hasDigit = true
		default:
			hasSpecial = true
		}
	}

	varietyScore := 0
	if hasUpper {
		varietyScore++
	}
	if hasLower {
		varietyScore++
	}
	if hasDigit {
		varietyScore++
	}
	if hasSpecial {
		varietyScore++
	}

	// High entropy if unique ratio > 0.6 and has at least 3 types of characters
	return uniqueRatio > 0.6 && varietyScore >= 3
}
