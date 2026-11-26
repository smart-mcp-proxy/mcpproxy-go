package audit

// redact.go implements the fixed-prefix credential masking used both
// per-field at build time (line.go) and as the defence-in-depth whole-line
// pass (SanitizeLine) documented in contracts/audit-line-events.md
// "Redaction (FR-015)". Deliberately excludes the generic high-entropy
// rule: it would mask every args_sha256/email_hash and break the schema
// after validation.

import (
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
)

// maxFieldLength is the length cap FR-015/FR-016 require for every
// per-field-sanitised caller/operator-controlled string (client.name,
// client.version, caller.token_name, profile, and server/tool on a refused
// dispatch). Without it an unbounded value — e.g. MCP `initialize`
// clientInfo, which is entirely caller-asserted — can exceed the audit
// sink's rotating-file writer record limit, causing a synchronous write
// failure that drops the required authz/tool_call line, or force
// unbounded audit-log disk growth (round-2 cross-review finding, PR-D).
// Applied in runes, after credential masking, so a masked value is never
// re-split mid-escape.
const maxFieldLength = 256

// truncateField caps s at maxFieldLength runes, appending a marker so a
// truncated value is distinguishable from one that legitimately ends at
// the boundary.
func truncateField(s string) string {
	r := []rune(s)
	if len(r) <= maxFieldLength {
		return s
	}
	return string(r[:maxFieldLength]) + "...(truncated)"
}

// credentialPatterns are all matched against the original string and their
// spans merged (applyMasking), so their order no longer decides what is
// masked.
// legacyPatterns are the Spec 107 fixed-prefix patterns, in their original
// order (most specific prefix first, e.g. sk-ant- before sk-).
var legacyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-ant-[A-Za-z0-9-]{10,}`),
	// OpenAI keys: legacy sk-{48}, and current sk-proj-/sk-svcacct-/sk-admin-
	// forms, which insert a hyphen-delimited segment before the random
	// suffix (round-3 cross-review finding, PR-D — the prior pattern only
	// matched the legacy form).
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`gh[poushr]_[A-Za-z0-9]{16,}`),
	regexp.MustCompile(`AKIA[0-9A-Za-z]{8,}`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`),
	regexp.MustCompile(`Bearer\s+[A-Za-z0-9\-_.~+/]+=*`),
}

// spec133Patterns are the shapes Spec 133 FR-016 added: the proxy's own
// tokens (agent token, client credential, browser-login access and refresh
// tokens; sixteen or more characters after the kind prefix, so a display
// token_prefix of 8–16 characters never matches) and GitHub fine-grained
// PATs.
var spec133Patterns = []*regexp.Regexp{
	regexp.MustCompile(`mcp_(agt|cli|oat|ort)_[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{22,}`),
}

// credentialPatterns is every pattern, legacy first.
var credentialPatterns = append(append([]*regexp.Regexp(nil), legacyPatterns...), spec133Patterns...)

func maskMatch(s string) string {
	if len(s) <= 8 {
		return "***"
	}
	return s[:4] + "***" + s[len(s)-2:]
}

// applyMasking runs every fixed-prefix pattern over s in order and reports
// whether any of them fired.
//
// Every pattern is matched against the ORIGINAL string and overlapping or
// touching matches are merged before anything is replaced (Spec 133 review):
// replacing pattern by pattern let one mask consume the prefix another
// pattern needed (a classic gh*_ token running into github_pat_, a proxy
// token running into a JWT), leaving the second secret's tail in the line.
func applyMasking(s string) (masked string, hit bool) {
	// FR-004: a string that matches none of the Spec 133 patterns is masked
	// exactly as before (pattern by pattern, legacy order), so a legacy line
	// stays byte-identical.
	newShape := false
	for _, re := range spec133Patterns {
		if re.MatchString(s) {
			newShape = true
			break
		}
	}
	if !newShape {
		return applyLegacyMasking(s)
	}
	return maskAllSpans(s)
}

// applyLegacyMasking is the Spec 107 pass: pattern by pattern, legacy order.
func applyLegacyMasking(s string) (masked string, hit bool) {
	{
		for _, re := range legacyPatterns {
			if re.MatchString(s) {
				hit = true
				s = re.ReplaceAllStringFunc(s, maskMatch)
			}
		}
		return s, hit
	}
}

// maskAllSpans matches every pattern against the ORIGINAL string and masks
// the merged spans. admin_action labels always use it (a new event kind has
// no legacy bytes to keep); legacy kinds use it when a Spec 133 shape is
// present (applyMasking).
// credentialRunEnd returns the index of the first byte at or after i that no
// credential pattern can contain.
func credentialRunEnd(s string, i int) int {
	for ; i < len(s); i++ {
		c := s[i]
		ok := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
			c == '_' || c == '-' || c == '.' || c == '~' || c == '+' || c == '/' || c == '=' ||
			c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
		if !ok {
			return i
		}
	}
	return i
}

// maxOverlapRescans bounds the overlapping-match rescans per pattern.
const maxOverlapRescans = 32

func maskAllSpans(s string) (masked string, hit bool) {
	// Every pattern is matched against the ORIGINAL string, at
	// every start position (FindAll returns only non-overlapping matches, so
	// two back-to-back tokens of one shape would hide the second), and the
	// overlapping spans are merged before anything is replaced: replacing
	// pattern by pattern lets one mask consume the prefix another needs.
	var spans [][2]int
	for _, re := range credentialPatterns {
		// Restarting one byte after each match start finds overlapping
		// matches; it is bounded (maxOverlapRescans per pattern) so a string
		// packed with token prefixes cannot make masking quadratic. At the
		// bound the remainder of the string is masked whole.
		rescans := 0
		for off := 0; off < len(s); {
			m := re.FindStringIndex(s[off:])
			if m == nil {
				break
			}
			spans = append(spans, [2]int{off + m[0], off + m[1]})
			if rescans >= maxOverlapRescans {
				// Bound reached: mask from this match to the end of the
				// credential-shaped run (the first byte no pattern can
				// contain, e.g. a JSON quote), rather than resume mid-way and
				// leave a partly consumed token's tail visible. Masking stops
				// there, so the rest of a serialized record is untouched.
				end := credentialRunEnd(s, off+m[0])
				spans = append(spans, [2]int{off + m[0], end})
				// Keep scanning after the run, with a fresh bound, so a
				// later credential in the same string is still found.
				off, rescans = end, 0
				continue
			}
			rescans++
			off += m[0] + 1
		}
	}
	if len(spans) == 0 {
		return s, false
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })
	merged := spans[:1]
	for _, sp := range spans[1:] {
		last := &merged[len(merged)-1]
		if sp[0] <= last[1] {
			if sp[1] > last[1] {
				last[1] = sp[1]
			}
			continue
		}
		merged = append(merged, sp)
	}
	var b strings.Builder
	prev := 0
	for _, sp := range merged {
		b.WriteString(s[prev:sp[0]])
		b.WriteString(maskMatch(s[sp[0]:sp[1]]))
		prev = sp[1]
	}
	b.WriteString(s[prev:])
	return b.String(), true
}

// maskCredential applies the per-field pass to one caller/operator-controlled
// string (client.name, caller.token_name, profile, and server/tool on a
// refused dispatch): fixed-prefix credential masking, then the length cap
// (FR-015). A value with no credential-shaped substring and within the cap
// survives verbatim.
func maskCredential(s string) string {
	masked, hit := applyMasking(s)
	if hit {
		noteFieldMask()
	}
	return truncateField(masked)
}

// fieldMaskHits counts per-field mask hits across every event kind (Spec 133
// plan D-1): a credential-shaped label masked at build time. Distinct from the
// sink's SanitizerHits, which counts builder bugs. Process-wide.
var fieldMaskHits atomic.Uint64

func noteFieldMask() { fieldMaskHits.Add(1) }

// FieldMaskHits returns the process-wide count of per-field mask hits.
func FieldMaskHits() uint64 { return fieldMaskHits.Load() }

// buildFailures counts lines a builder rejected or that could not be
// serialized (Spec 133 plan D-1). Process-wide; incremented by the writers
// (NoteBuildFailure), surfaced by doctor.
var buildFailures atomic.Uint64

// NoteBuildFailure records one rejected or unserialisable audit line.
func NoteBuildFailure() { buildFailures.Add(1) }

// BuildFailures returns the process-wide count of rejected lines.
func BuildFailures() uint64 { return buildFailures.Load() }

// SanitizeLine is the defence-in-depth whole-line pass applied before a
// sink write: it must be the identity on well-formed builder output (every
// caller/operator-controlled field was already masked per field) and only
// fires on a builder bug. Returns the (possibly masked) line and whether
// any pattern hit.
func SanitizeLine(raw []byte) ([]byte, bool) {
	masked, hit := applyMasking(string(raw))
	return []byte(masked), hit
}
