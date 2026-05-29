package security

import (
	"regexp"
	"sort"
	"strings"
)

// SpotlightUntrusted wraps untrusted upstream tool output in named guillemet
// fences so downstream LLMs can visually distinguish proxied content from
// trusted instructions (Spec 054 Track B, FR-B1/FR-B2).
//
// Any guillemet characters already present in text are HTML-escaped before
// wrapping so the content cannot spoof or break out of the delimiter (FR-B2).
//
// An empty text is a no-op and returns "".
func SpotlightUntrusted(text, server, tool string) string {
	if text == "" {
		return ""
	}

	// Escape delimiter characters in the body to prevent breakout.
	escaped := strings.ReplaceAll(text, "«", "&laquo;")
	escaped = strings.ReplaceAll(escaped, "»", "&raquo;")

	var b strings.Builder
	b.WriteString("«untrusted:")
	b.WriteString(server)
	b.WriteString("/")
	b.WriteString(tool)
	b.WriteString("»\n")
	b.WriteString(escaped)
	b.WriteString("\n«/untrusted:")
	b.WriteString(server)
	b.WriteString("/")
	b.WriteString(tool)
	b.WriteString("»")
	return b.String()
}

// Control-sequence stripping regexes.
var (
	// CSI sequences: ESC [ ... <final byte>
	ansiCSIRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	// OSC sequences: ESC ] ... (BEL or ST)
	ansiOSCRe = regexp.MustCompile(`\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)
)

// StripControlSequences removes hostile/invisible control sequences from text
// on a per-class basis. Only classes whose map value is true are applied.
//
// Supported class keys:
//   - "ansi":       ANSI CSI and OSC escape sequences
//   - "c0c1":       C0 (U+0000–U+001F, except \t \n \r) and C1 (U+0080–U+009F)
//   - "zero_width": U+200B, U+200C, U+200D, U+2060, U+FEFF
//   - "bidi":       U+202A–U+202E and U+2066–U+2069
//
// The returned stripped slice contains the (sorted) names of the classes that
// actually removed at least one character, for audit logging.
func StripControlSequences(text string, classes map[string]bool) (out string, stripped []string) {
	out = text
	strippedSet := make(map[string]bool)

	if classes["ansi"] {
		before := out
		out = ansiCSIRe.ReplaceAllString(out, "")
		out = ansiOSCRe.ReplaceAllString(out, "")
		if out != before {
			strippedSet["ansi"] = true
		}
	}

	if classes["c0c1"] {
		before := out
		out = stripRunes(out, func(r rune) bool {
			if r == '\t' || r == '\n' || r == '\r' {
				return false
			}
			if r <= 0x1F {
				return true
			}
			if r >= 0x80 && r <= 0x9F {
				return true
			}
			return false
		})
		if out != before {
			strippedSet["c0c1"] = true
		}
	}

	if classes["zero_width"] {
		before := out
		out = stripRunes(out, func(r rune) bool {
			switch r {
			case 0x200B, 0x200C, 0x200D, 0x2060, 0xFEFF:
				return true
			}
			return false
		})
		if out != before {
			strippedSet["zero_width"] = true
		}
	}

	if classes["bidi"] {
		before := out
		out = stripRunes(out, func(r rune) bool {
			if r >= 0x202A && r <= 0x202E {
				return true
			}
			if r >= 0x2066 && r <= 0x2069 {
				return true
			}
			return false
		})
		if out != before {
			strippedSet["bidi"] = true
		}
	}

	for name := range strippedSet {
		stripped = append(stripped, name)
	}
	sort.Strings(stripped)
	return out, stripped
}

// stripRunes removes every rune for which remove returns true.
func stripRunes(s string, remove func(rune) bool) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if remove(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
