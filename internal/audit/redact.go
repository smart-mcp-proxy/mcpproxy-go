package audit

// redact.go implements the fixed-prefix credential masking used both
// per-field at build time (line.go) and as the defence-in-depth whole-line
// pass (SanitizeLine) documented in contracts/audit-line-events.md
// "Redaction (FR-015)". Deliberately excludes the generic high-entropy
// rule: it would mask every args_sha256/email_hash and break the schema
// after validation.

import "regexp"

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

// credentialPatterns are evaluated in order (most specific prefix first,
// e.g. sk-ant- before sk-) so a longer, more specific match is consumed
// before a shorter pattern could also match a prefix of it.
var credentialPatterns = []*regexp.Regexp{
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

func maskMatch(s string) string {
	if len(s) <= 8 {
		return "***"
	}
	return s[:4] + "***" + s[len(s)-2:]
}

// applyMasking runs every fixed-prefix pattern over s in order and reports
// whether any of them fired.
func applyMasking(s string) (masked string, hit bool) {
	for _, re := range credentialPatterns {
		if re.MatchString(s) {
			hit = true
			s = re.ReplaceAllStringFunc(s, maskMatch)
		}
	}
	return s, hit
}

// maskCredential applies the per-field pass to one caller/operator-controlled
// string (client.name, caller.token_name, profile, and server/tool on a
// refused dispatch): fixed-prefix credential masking, then the length cap
// (FR-015). A value with no credential-shaped substring and within the cap
// survives verbatim.
func maskCredential(s string) string {
	masked, _ := applyMasking(s)
	return truncateField(masked)
}

// SanitizeLine is the defence-in-depth whole-line pass applied before a
// sink write: it must be the identity on well-formed builder output (every
// caller/operator-controlled field was already masked per field) and only
// fires on a builder bug. Returns the (possibly masked) line and whether
// any pattern hit.
func SanitizeLine(raw []byte) ([]byte, bool) {
	masked, hit := applyMasking(string(raw))
	return []byte(masked), hit
}
