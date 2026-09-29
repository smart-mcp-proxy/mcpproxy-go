// Package clientidentity derives the safe, stable identity used for untrusted
// MCP clientInfo names across persisted presence and session rows.
package clientidentity

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const maxDisplayBytes = 128

// Identity is a safe key and label derived from one untrusted client name.
// RawNormalized is retained only during the immediate classification step and
// must never be returned to an API consumer.
type Identity struct {
	Key           string
	DisplayName   string
	RawNormalized string
}

// FromRaw derives a common identity for session names and initialize evidence.
// Ordinary names keep their familiar identifier (for example, "zed"). Names
// changed by sanitization or truncation receive a short hash suffix so they
// cannot collide with a supported alias or another long unknown name.
func FromRaw(raw string) Identity {
	normalized := NormalizeRaw(raw)
	if normalized == "" {
		return Identity{}
	}
	display := SanitizeDisplay(raw)
	if display == "" {
		display = "unknown client"
	}
	key := display
	if normalized != display {
		digest := sha256.Sum256([]byte(normalized))
		key += "-" + hex.EncodeToString(digest[:12])
	}
	return Identity{Key: key, DisplayName: display, RawNormalized: normalized}
}

// FromStoredKey reconstructs the safe display label from a persisted key. The
// key is already an internal safe representation, so its identity stays exact.
func FromStoredKey(key string) Identity {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return Identity{}
	}
	display := key
	if dash := strings.LastIndexByte(key, '-'); dash > 0 && len(key)-dash == 25 {
		if _, err := hex.DecodeString(key[dash+1:]); err == nil {
			display = key[:dash]
		}
	}
	return Identity{Key: key, DisplayName: display, RawNormalized: key}
}

// NormalizeRaw is for exact case-insensitive alias classification only. It
// intentionally preserves controls, so an escape-prefixed client cannot become
// a supported alias merely because its display label removes the escape.
func NormalizeRaw(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// SanitizeDisplay keeps rendered names safe and bounded.
func SanitizeDisplay(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		if r < 0x20 || r == 0x7f {
			continue
		}
		encoded := string(r)
		if b.Len()+len(encoded) > maxDisplayBytes {
			break
		}
		b.WriteString(encoded)
	}
	return strings.TrimSpace(b.String())
}
