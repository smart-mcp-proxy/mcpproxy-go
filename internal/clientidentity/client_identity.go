// Package clientidentity derives the safe, stable identity used for untrusted
// MCP clientInfo names across persisted presence and session rows.
package clientidentity

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	maxDisplayBytes      = 128
	plainKeyPrefix       = "v1:p:"
	transformedKeyPrefix = "v1:t:"
	transformedIDPrefix  = "~t:"
	escapedPlainIDPrefix = "~p:"
	digestBytes          = 12
)

// Identity is a safe key and label derived from one untrusted client name.
// RawNormalized is retained only during the immediate classification step and
// must never be returned to an API consumer.
type Identity struct {
	Key           string
	PublicID      string
	DisplayName   string
	RawNormalized string
}

// FromRaw derives a common identity for session names and initialize evidence.
// Key is versioned and namespace-separated: a printable name cannot reproduce
// the serialized key of a name changed by sanitization or truncation. PublicID
// keeps ordinary names readable while reserving a sentinel for transformed
// names; ordinary names beginning with that sentinel are escaped.
func FromRaw(raw string) Identity {
	normalized := NormalizeRaw(raw)
	if normalized == "" {
		return Identity{}
	}
	display := SanitizeDisplay(raw)
	if display == "" {
		display = "unknown client"
	}
	if normalized == display {
		return Identity{
			Key:           plainKeyPrefix + display,
			PublicID:      plainPublicID(display),
			DisplayName:   display,
			RawNormalized: normalized,
		}
	}
	digest := digestFor(normalized)
	return Identity{
		Key:           transformedKeyPrefix + display + ":" + digest,
		PublicID:      transformedIDPrefix + display + "-" + digest,
		DisplayName:   display,
		RawNormalized: normalized,
	}
}

// FromStoredKey reconstructs the safe display label from a persisted key. The
// key is already an internal safe representation, so its identity stays exact.
// Legacy unversioned keys remain readable but never use the old hash-suffix
// heuristic, which could merge a printable name with a transformed one.
func FromStoredKey(key string) Identity {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return Identity{}
	}
	if display, ok := storedPlainDisplay(key); ok {
		return Identity{Key: key, PublicID: plainPublicID(display), DisplayName: display, RawNormalized: display}
	}
	if display, digest, ok := storedTransformedDisplay(key); ok {
		return Identity{Key: key, PublicID: transformedIDPrefix + display + "-" + digest, DisplayName: display, RawNormalized: key}
	}
	display := SanitizeDisplay(key)
	if display == "" {
		return Identity{}
	}
	return Identity{Key: key, PublicID: plainPublicID(display), DisplayName: display, RawNormalized: display}
}

func digestFor(normalized string) string {
	digest := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(digest[:digestBytes])
}

func plainPublicID(display string) string {
	if strings.HasPrefix(display, "~") {
		return escapedPlainIDPrefix + display
	}
	return display
}

func storedPlainDisplay(key string) (string, bool) {
	if !strings.HasPrefix(key, plainKeyPrefix) {
		return "", false
	}
	display := SanitizeDisplay(strings.TrimPrefix(key, plainKeyPrefix))
	return display, display != ""
}

func storedTransformedDisplay(key string) (string, string, bool) {
	if !strings.HasPrefix(key, transformedKeyPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(key, transformedKeyPrefix)
	separator := strings.LastIndexByte(rest, ':')
	if separator <= 0 {
		return "", "", false
	}
	display := SanitizeDisplay(rest[:separator])
	digest := rest[separator+1:]
	if display == "" || len(digest) != digestBytes*2 {
		return "", "", false
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", "", false
	}
	return display, digest, true
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
