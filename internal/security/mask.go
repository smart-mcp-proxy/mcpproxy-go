package security

import (
	"strings"
)

// maskedSuffix is appended after the retained prefix of every masked value.
// The ellipsis marks the elision and the asterisks make it obvious, at a
// glance and in a screenshot, that nothing behind it is real.
const maskedSuffix = "…****"

// maskedWhole is the mask for values too short to keep any prefix from.
const maskedWhole = "****"

// maskPrefixRunes is how much of a detected value survives masking. Four runes
// is enough to recognise WHICH credential was flagged (`AKIA…`, `ghp_…`,
// `sk-a…`) without being enough to use, and matches the prefix length the
// detector's own patterns key on.
const maskPrefixRunes = 4

// MaskValue renders a detected secret as a recognisable but unusable preview:
// the first four runes, then an elision. `AKIAIOSFODNN7EXAMPLE` becomes
// `AKIA…****`.
//
// Values of four runes or fewer keep no prefix at all — a four-character secret
// is entirely prefix, so showing it would defeat the mask.
func MaskValue(value string) string {
	if value == "" {
		return ""
	}

	runes := []rune(value)
	if len(runes) <= maskPrefixRunes {
		return maskedWhole
	}
	return string(runes[:maskPrefixRunes]) + maskedSuffix
}

// MaskText replaces every sensitive value the detector recognises inside text
// with its MaskValue preview, returning the masked text and whether anything
// was replaced.
//
// It differs from Redact in two deliberate ways:
//
//   - The replacement keeps a short prefix instead of collapsing to
//     `[REDACTED:<category>]`, so an operator reviewing an activity record can
//     still tell WHICH key leaked (and therefore which one to rotate) without
//     the record itself carrying the credential.
//   - Known example values (`AKIAIOSFODNN7EXAMPLE` and friends) are masked too.
//     Redact leaves them alone because a redacted example is a useless
//     placeholder; here the opposite holds — the drawer that renders this text
//     ends up in screenshots and screen-shares, and "it only looked like a
//     secret" is not a judgement the rendering surface gets to make.
//
// Sensitive FILE PATHS are deliberately not masked: the path is what makes the
// finding actionable ("this call read ~/.aws/credentials") and the path itself
// is not the secret.
//
// Unlike Scan, masking is NOT capped at MaxDetectionsPerScan. That cap bounds
// how many findings are worth reporting; here a cap would be a disclosure
// budget — the 51st secret in a payload would be served in cleartext. Work
// stays bounded instead by the fixed pattern set (each applied once, over
// de-duplicated matches) and by the payload sizes the activity log already
// caps.
//
// Masking also ignores the `enabled` flag: a record that carries a detection
// verdict was flagged while detection was on, and turning the feature off later
// must not retroactively serve its credentials. Per-category switches are still
// honoured, so a category the operator excluded is neither flagged nor masked.
func (d *Detector) MaskText(text string) (masked string, changed bool) {
	if text == "" {
		return text, false
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	masked = text

	allPatterns := append(d.patterns, d.customPatterns...) //nolint:gocritic // intentional copy: two slices scanned as one
	for _, pattern := range allPatterns {
		if !d.config.IsCategoryEnabled(string(pattern.Category)) {
			continue
		}

		seen := make(map[string]struct{})
		var pairs []string
		for _, match := range pattern.Match(masked) {
			if match == "" || !pattern.IsValid(match) {
				continue
			}
			if _, done := seen[match]; done {
				continue
			}
			seen[match] = struct{}{}

			// The private-key patterns match only the PEM/PGP BEGIN line, so
			// masking the match alone would leave the key body in the clear.
			if pattern.Category == CategoryPrivateKey {
				masked = maskKeyBlocks(masked, match)
				continue
			}

			preview := MaskValue(match)
			if preview == match {
				continue
			}
			pairs = append(pairs, match, preview)
		}
		masked = replaceAllPairs(masked, pairs)
	}

	// High-entropy strings have no pattern to key on, but the detector reports
	// them as findings, so a flagged record must not render them either. The
	// limit is a work bound well above what any reviewable payload carries —
	// not a disclosure budget.
	if d.config.IsCategoryEnabled("high_entropy") {
		var pairs []string
		seen := make(map[string]struct{})
		for _, match := range FindHighEntropyStrings(masked, d.config.GetEntropyThreshold(), maxEntropyMasks) {
			preview := MaskValue(match)
			if preview == match {
				continue
			}
			if _, done := seen[match]; done {
				continue
			}
			seen[match] = struct{}{}
			pairs = append(pairs, match, preview)
		}
		masked = replaceAllPairs(masked, pairs)
	}

	return masked, masked != text
}

// replaceAllPairs applies every (value, preview) pair in ONE pass.
//
// Per-value strings.ReplaceAll would re-scan the whole payload once per secret
// found, which is quadratic on a payload stuffed with them — and since the
// replacement cap was removed precisely so no secret is skipped, that is the
// pathological case to keep cheap. strings.Replacer walks the text once.
func replaceAllPairs(text string, pairs []string) string {
	if len(pairs) == 0 {
		return text
	}
	return strings.NewReplacer(pairs...).Replace(text)
}

// maxEntropyMasks bounds the high-entropy sweep. Scan reports at most five such
// findings; masking has to cover far more than it reports, because every
// unmasked one is a value served in the clear.
const maxEntropyMasks = 500

// maskKeyBlocks replaces whole PEM/PGP blocks introduced by the given BEGIN
// header — header, body and footer — with a single masked marker.
//
// The private-key patterns match the header line only ("-----BEGIN RSA PRIVATE
// KEY-----"), so masking the match would replace the least secret part of the
// payload and leave the key itself readable. Everything from the header to the
// matching END line (or, if the payload was truncated before it, to the end of
// the text) goes.
func maskKeyBlocks(text, header string) string {
	const endMarker = "-----END"

	var b strings.Builder
	rest := text
	for {
		start := strings.Index(rest, header)
		if start < 0 {
			b.WriteString(rest)
			return b.String()
		}

		b.WriteString(rest[:start])
		b.WriteString(MaskValue(header))

		after := rest[start+len(header):]
		endIdx := strings.Index(after, endMarker)
		if endIdx < 0 {
			// Unterminated block (truncated payload): drop the remainder
			// rather than serve a partial key.
			return b.String()
		}
		// Skip the END line too, so its own dashes do not survive alone.
		tail := after[endIdx:]
		if nl := strings.IndexByte(tail, '\n'); nl >= 0 {
			rest = tail[nl:]
		} else {
			return b.String()
		}
	}
}

// MaskArguments returns a deep copy of a tool call's arguments with every
// string leaf run through MaskText. The input map belongs to the storage layer
// and is never modified.
func (d *Detector) MaskArguments(args map[string]interface{}) map[string]interface{} {
	if len(args) == 0 {
		return args
	}
	masked, _ := d.maskAny(args)
	out, _ := masked.(map[string]interface{})
	return out
}

// MaskJSON returns a copy of an arbitrary decoded-JSON value with every string
// leaf run through MaskText. Use it for payload fields typed as `any` (a tool
// call's response, say); MaskArguments is the map-shaped convenience wrapper.
func (d *Detector) MaskJSON(value interface{}) interface{} {
	masked, _ := d.maskAny(value)
	return masked
}

// maskAny walks an arbitrary decoded-JSON value, masking string leaves. It
// returns a copy whenever anything below it changed, and the original value
// otherwise, so untouched subtrees are not needlessly re-allocated.
func (d *Detector) maskAny(value interface{}) (interface{}, bool) {
	switch v := value.(type) {
	case string:
		masked, changed := d.MaskText(v)
		return masked, changed

	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		changed := false
		for key, item := range v {
			maskedItem, itemChanged := d.maskAny(item)
			out[key] = maskedItem
			changed = changed || itemChanged
		}
		if !changed {
			return v, false
		}
		return out, true

	case []interface{}:
		out := make([]interface{}, len(v))
		changed := false
		for i, item := range v {
			maskedItem, itemChanged := d.maskAny(item)
			out[i] = maskedItem
			changed = changed || itemChanged
		}
		if !changed {
			return v, false
		}
		return out, true

	default:
		return value, false
	}
}

// internalArgPrefix marks arguments MCPProxy injects into a call for its own
// use (server-edition auth identity, today `_auth_user_id` / `_auth_user_email`
// / `_auth_auth_type`). They are plumbing, never something the caller sent, and
// they name the operator — so they do not belong in a payload view.
const internalArgPrefix = "_auth_"

// StripInternalArgs returns a copy of a tool call's arguments without the
// internal `_auth_*` keys. Returns the input untouched when there are none, so
// the common case allocates nothing.
func StripInternalArgs(args map[string]interface{}) map[string]interface{} {
	if len(args) == 0 {
		return args
	}

	found := false
	for key := range args {
		if strings.HasPrefix(key, internalArgPrefix) {
			found = true
			break
		}
	}
	if !found {
		return args
	}

	out := make(map[string]interface{}, len(args))
	for key, value := range args {
		if strings.HasPrefix(key, internalArgPrefix) {
			continue
		}
		out[key] = value
	}
	return out
}
