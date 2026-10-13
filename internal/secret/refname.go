package secret

import (
	"fmt"
	"regexp"
	"strings"
)

// refNameMaxLen is the keyring entry name length cap (FR-065). Existing
// convention from ServerDetail.vue's pre-109 suggestSecretName.
const refNameMaxLen = 64

// refNameInvalidChars matches every run of characters outside [a-z0-9-] once
// the input has been lower-cased, so it collapses "My Server!" the same way
// for env and header kinds.
var refNameInvalidChars = regexp.MustCompile(`[^a-z0-9-]+`)

// refNameRepeatHyphen collapses a run of hyphens left behind once an invalid
// run sits next to a literal '-' already in the input (e.g. "Server!-env").
var refNameRepeatHyphen = regexp.MustCompile(`-{2,}`)

// RefName computes the OS-keyring entry name for one field on one server
// (FR-065): "<server>-<kind>-<key>", lower-cased, with every run of
// characters outside [a-z0-9-] collapsed to a single '-', leading/trailing
// '-' trimmed, and the result capped at 64 characters. kind is normally "env"
// or "header" — keeping it as a distinct path component (rather than folding
// it into key) is what keeps an env var and a header of the same name from
// colliding on one keyring entry.
//
// taken reports whether a candidate name is already present in the keyring
// (GET /secrets); when the computed name collides, RefName appends -2, -3, …
// until it finds a free one, so an add never silently overwrites an existing
// secret (D28). taken must be safe to call repeatedly and cheap (typically a
// map/set built once from a single GET /secrets response).
func RefName(server, kind, key string, taken func(string) bool) string {
	base := normalizeRefComponent(server + "-" + kind + "-" + key)
	if taken == nil {
		return base
	}

	candidate := base
	for n := 2; taken(candidate); n++ {
		suffix := fmt.Sprintf("-%d", n)
		trimmed := base
		if maxBase := refNameMaxLen - len(suffix); len(trimmed) > maxBase {
			trimmed = trimmed[:maxBase]
		}
		candidate = trimmed + suffix
	}
	return candidate
}

// normalizeRefComponent lower-cases s, collapses every run outside
// [a-z0-9-] to a single '-', trims leading/trailing '-', and caps the length.
func normalizeRefComponent(s string) string {
	lower := strings.ToLower(s)
	collapsed := refNameInvalidChars.ReplaceAllString(lower, "-")
	collapsed = refNameRepeatHyphen.ReplaceAllString(collapsed, "-")
	trimmed := strings.Trim(collapsed, "-")
	if len(trimmed) > refNameMaxLen {
		trimmed = strings.Trim(trimmed[:refNameMaxLen], "-")
	}
	return trimmed
}
