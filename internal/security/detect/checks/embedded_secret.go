package checks

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/patterns"
)

// EmbeddedSecret is a SOFT check (FR-009, US2) that flags a live credential
// hardcoded into a tool's description or schema — an AWS key, a private key, a
// database password, a Luhn-valid card, etc. It wraps the shared
// internal/security/patterns matchers and carries each match's per-match
// confidence (Spec 076 T015): a validated card / live cloud key is high, a
// documented placeholder (AKIA…EXAMPLE) collapses to near-zero and is dropped.
//
// It also restores the two secret categories the deleted legacy
// security.NewDetector(nil) path covered but the pattern matchers do not
// (Spec 077): a sensitive file-path reference (~/.ssh/id_rsa, ~/.aws/credentials,
// .env, /etc/passwd, *.pem, …) and a high-entropy blob that looks like an opaque
// secret. Both stay self-contained and offline (stdlib regexp + math only), so
// the detect package keeps its no-network / no-filesystem guarantee.
//
// It scans RAW text (not the engine's normalized text): secrets are
// case-sensitive and exact, and normalization would lowercase prefixes (AKIA…)
// and fold the very bytes the matchers key on.
//
// Being soft, a hit raises a finding for review and never auto-quarantines —
// an embedded secret may be a careless example as easily as a planted one.
type EmbeddedSecret struct{}

// ID implements detect.Check.
func (*EmbeddedSecret) ID() string { return "secret.embedded" }

// secretMinConfidence drops below-floor matches (documented examples collapse to
// patterns.confidenceExample). Keeps placeholders from being flagged (FR-012).
const secretMinConfidence = 0.3

// sensitiveFileConfidence / highEntropyConfidence are the fixed confidences for
// the two restored categories. Both clear secretMinConfidence but sit below the
// validated-credential matches, so a real key still wins the single-signal slot.
const (
	sensitiveFileConfidence = 0.5
	highEntropyConfidence   = 0.4
)

// entropyMinLen / entropyThreshold gate the high-entropy scan. The length floor
// (24, slightly above the legacy detector's 20) plus a 4.5-bit Shannon threshold
// keep ordinary identifiers and documented example keys (e.g. the 20-char
// AKIA…EXAMPLE) below the bar while still catching opaque 32/40-char secrets.
const (
	entropyMinLen    = 24
	entropyThreshold = 4.5
)

// builtinSecretPatterns is the fixed-order set of credential matchers reused
// from the sensitive-data detector. Order is deterministic so ties resolve
// stably.
func builtinSecretPatterns() []*patterns.Pattern {
	var all []*patterns.Pattern
	all = append(all, patterns.GetCloudPatterns()...)
	all = append(all, patterns.GetKeyPatterns()...)
	all = append(all, patterns.GetTokenPatterns()...)
	all = append(all, patterns.GetDatabasePatterns()...)
	all = append(all, patterns.GetCreditCardPatterns()...)
	return all
}

// sensitiveFilePatterns is the curated, fixed-order set of sensitive file-path
// references restored from the legacy sensitive_file detector. Matched
// case-insensitively against raw text; order is deterministic so ties resolve
// stably.
var sensitiveFilePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:~|%userprofile%|/home/[^/\s]+|/root)?[/\\]?\.ssh[/\\](?:id_(?:rsa|dsa|ecdsa|ed25519)|[^/\\\s]*_key)`),
	regexp.MustCompile(`(?i)(?:~|%userprofile%|/home/[^/\s]+|/root)?[/\\]?\.aws[/\\](?:credentials|config)`),
	regexp.MustCompile(`(?i)\.config[/\\]gcloud[/\\](?:application_default_credentials\.json|credentials\.db)`),
	regexp.MustCompile(`(?i)(?:~|/home/[^/\s]+|/root)?[/\\]?\.kube[/\\]config`),
	regexp.MustCompile(`(?i)/etc/(?:passwd|shadow|sudoers)`),
	regexp.MustCompile(`(?i)(?:^|[\s"'` + "`" + `/\\])\.env(?:\.[a-z]+)?(?:$|[\s"'` + "`" + `])`),
	regexp.MustCompile(`(?i)[\w./\\-]+\.(?:pem|pfx|p12|kdbx|pgpass)\b`),
	regexp.MustCompile(`(?i)(?:\.git-credentials|\.npmrc|\.netrc)\b`),
}

// entropyCandidate matches contiguous runs that could be an opaque secret token.
var entropyCandidate = regexp.MustCompile(`[A-Za-z0-9+/=_\-]{` + fmt.Sprint(entropyMinLen) + `,}`)

// Inspect implements detect.Check. It emits at most one signal per tool: the
// highest-confidence embedded secret found in the raw description + schema,
// across credential matchers, sensitive file paths, and high-entropy blobs.
func (c *EmbeddedSecret) Inspect(tool detect.ToolView, _ detect.RegistryView) []detect.Signal {
	var b strings.Builder
	b.WriteString(tool.Description)
	if len(tool.InputSchema) > 0 {
		b.WriteByte(' ')
		b.Write(tool.InputSchema)
	}
	if len(tool.OutputSchema) > 0 {
		b.WriteByte(' ')
		b.Write(tool.OutputSchema)
	}
	raw := b.String()
	if raw == "" {
		return nil
	}

	patternList := builtinSecretPatterns()

	bestConf := 0.0
	bestCategory := ""
	bestMatch := ""
	consider := func(conf float64, category, match string) {
		if match != "" && conf > bestConf {
			bestConf = conf
			bestCategory = category
			bestMatch = match
		}
	}

	// 1. Validated credential matchers (cloud/key/token/database/card).
	for _, p := range patternList {
		for _, m := range p.Match(raw) { // Match already filters through the validator
			if m == "" || p.IsKnownExample(m) {
				continue // documented placeholder — not a live secret
			}
			consider(p.ConfidenceFor(m), string(p.Category), m)
		}
	}

	// 2. Sensitive file-path references (restored legacy sensitive_file coverage).
	for _, re := range sensitiveFilePatterns {
		if m := strings.TrimSpace(re.FindString(raw)); m != "" {
			consider(sensitiveFileConfidence, "sensitive_file", m)
		}
	}

	// 3. High-entropy blobs (restored legacy high_entropy coverage). Skip any
	// candidate a credential matcher already recognises as a documented example,
	// so placeholders never re-enter through the entropy path.
	for _, m := range entropyCandidate.FindAllString(raw, -1) {
		if shannonEntropy(m) < entropyThreshold || isKnownExampleAny(patternList, m) {
			continue
		}
		consider(highEntropyConfidence, "high_entropy", m)
	}

	if bestConf < secretMinConfidence {
		return nil
	}

	return []detect.Signal{{
		CheckID:    c.ID(),
		Tier:       detect.TierSoft,
		ThreatType: detect.ThreatToolPoisoning,
		Confidence: detect.ClampConfidence(bestConf),
		Evidence:   detect.CapEvidence(maskSecret(bestMatch)),
		Detail:     fmt.Sprintf("Description embeds a likely %s — a credential should never be hardcoded into tool metadata.", bestCategory),
	}}
}

// isKnownExampleAny reports whether any credential pattern recognises match as a
// documented example/placeholder.
func isKnownExampleAny(patternList []*patterns.Pattern, match string) bool {
	for _, p := range patternList {
		if p.IsKnownExample(match) {
			return true
		}
	}
	return false
}

// shannonEntropy returns the Shannon entropy (bits/byte) of s. Deterministic and
// offline — a self-contained copy so detect keeps its no-import guarantee.
func shannonEntropy(s string) float64 {
	if s == "" {
		return 0
	}
	var freq [256]float64
	for i := 0; i < len(s); i++ {
		freq[s[i]]++
	}
	n := float64(len(s))
	entropy := 0.0
	for _, count := range freq {
		if count == 0 {
			continue
		}
		p := count / n
		entropy -= p * math.Log2(p)
	}
	return entropy
}

// maskSecret returns a render-safe, minimally-disclosing form of a matched
// secret: a short visible prefix followed by a fixed-length mask. The full
// secret is never echoed into a finding/report.
func maskSecret(s string) string {
	const prefix = 4
	r := []rune(s)
	if len(r) <= prefix {
		return strings.Repeat("*", len(r))
	}
	masked := len(r) - prefix
	if masked > 12 {
		masked = 12
	}
	return string(r[:prefix]) + strings.Repeat("*", masked)
}
