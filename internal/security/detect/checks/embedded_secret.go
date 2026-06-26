package checks

import (
	"fmt"
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

// Inspect implements detect.Check. It emits at most one signal per tool: the
// highest-confidence embedded secret found in the raw description + schema.
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

	bestConf := 0.0
	bestCategory := ""
	bestMatch := ""
	for _, p := range builtinSecretPatterns() {
		for _, m := range p.Match(raw) { // Match already filters through the validator
			if m == "" || p.IsKnownExample(m) {
				continue // documented placeholder — not a live secret
			}
			if conf := p.ConfidenceFor(m); conf > bestConf {
				bestConf = conf
				bestCategory = string(p.Category)
				bestMatch = m
			}
		}
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
