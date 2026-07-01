package checks

import (
	"fmt"
	"regexp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
)

// PhraseInjection is the curated HARD check (Spec 077 FR-004) that restores the
// approval-blocking posture the deleted legacy tpa_hidden_instructions /
// data_exfiltration substring rules provided — without their false positives.
//
// It fires ONLY on a small, high-confidence set of prompt-injection and
// data-exfiltration DIRECTIVES:
//
//   - instruction overrides ("ignore all previous instructions"),
//   - explicit secret-exfiltration ("send the credentials to …",
//     "exfiltrate ~/.ssh/id_rsa", "upload the .env file to …"),
//   - system-prompt / instruction exfiltration ("reveal your system prompt").
//
// Broader, lower-confidence phrasing stays in the SOFT directive.imperative
// check (review-only). Being hard, a hit here contributes to the dangerous
// verdict and auto-quarantine, so the patterns are deliberately narrow and every
// match is position-discounted: a phrase that is quoted or merely described
// ("detects prompts such as 'ignore previous instructions'") lands below the
// hard emit floor and is not blocked (FR-005, the core false-positive control).
//
// It runs over the engine's NORMALIZED text (lowercased, contraction-expanded,
// lightly-stemmed, format-runes stripped) so "don't disclose" / "do not tell"
// and "instructions" / "instruction" collapse to one matchable form.
type PhraseInjection struct{}

// ID implements detect.Check.
func (*PhraseInjection) ID() string { return "phrase.injection" }

// phraseHardMinConfidence is the per-check emit floor. A lone example-position
// match (base × exampleDiscount ≈ 0.9 × 0.25 = 0.225) lands below it and emits
// nothing; an instruction-position match clears it. This keeps "describes the
// phrase" tools from being hard-blocked (FR-005 MUST-NOT).
const phraseHardMinConfidence = 0.6

// phraseFamily is one curated regex family with its instruction-position base
// confidence and threat classification. Order is fixed for determinism.
type phraseFamily struct {
	re     *regexp.Regexp
	base   float64
	threat string
	what   string
}

// phraseFamilies are matched against NORMALIZED text. Patterns use lowercase,
// contraction-expanded, lightly-stemmed forms (e.g. "instruction" matches the
// stemmed "instructions"; "credential" matches "credentials"). Built once at
// package init. The exfiltration family requires an exfil verb AND a sensitive
// target within a short, same-sentence window, so a bare verb ("send an email")
// never fires — that narrowness is what makes the check safe at the hard tier.
var phraseFamilies = []phraseFamily{
	{ // Instruction override: "ignore all previous instructions", "disregard the above directions".
		re:     regexp.MustCompile(`\b(?:ignore|disregard|forget|override|discard|bypass) (?:all |the |any |these |your )*(?:previous|prior|above|earlier|preceding|initial|original|foregoing|all|these) (?:instruction|direction|command|prompt|rule|guideline|order)`),
		base:   0.9,
		threat: detect.ThreatPromptInjection,
		what:   "instruction-override directive",
	},
	{ // Secret exfiltration: an exfil verb + a sensitive target within the same sentence.
		re:     regexp.MustCompile(`\b(?:send|upload|post|transmit|exfiltrat\w*|leak|forward|copy|dump|steal|harvest|expos\w*|smuggle|beacon)\b[^.!?]{0,40}?(?:credential|api key|access token|auth token|secret|password|passphrase|private key|ssh key|\.env|env file|id_rsa|/etc/passwd|~/\.ssh|~/\.aws|/\.ssh/|/\.aws/)`),
		base:   0.88,
		threat: detect.ThreatExfiltration,
		what:   "secret-exfiltration directive",
	},
	{ // System-prompt / instruction exfiltration: "reveal your system prompt", "print these instructions".
		re:     regexp.MustCompile(`\b(?:reveal|expos\w*|print|output|send|leak|disclos\w*|repeat|show|dump) (?:your |the |me |us |all )*(?:system prompt|hidden instruction|these instruction|your instruction|initial prompt|secret instruction)`),
		base:   0.85,
		threat: detect.ThreatPromptInjection,
		what:   "system-prompt exfiltration directive",
	},
}

// Inspect implements detect.Check. It emits at most one signal per tool: the
// highest-confidence curated directive that clears phraseHardMinConfidence after
// position discounting.
func (c *PhraseInjection) Inspect(tool detect.ToolView, _ detect.RegistryView) []detect.Signal {
	text := tool.NormalizedText
	if text == "" {
		return nil
	}

	bestConf := 0.0
	bestMatch := ""
	bestWhat := ""
	bestThreat := ""
	for _, fam := range phraseFamilies {
		for _, loc := range fam.re.FindAllStringIndex(text, -1) {
			conf := fam.base * detect.ClassifyPosition(text, loc[0]).Discount()
			if conf > bestConf {
				bestConf = conf
				bestMatch = text[loc[0]:loc[1]]
				bestWhat = fam.what
				bestThreat = fam.threat
			}
		}
	}

	if bestConf < phraseHardMinConfidence {
		return nil
	}

	return []detect.Signal{{
		CheckID:    c.ID(),
		Tier:       detect.TierHard,
		ThreatType: bestThreat,
		Confidence: detect.ClampConfidence(bestConf),
		Evidence:   detect.CapEvidence(bestMatch),
		Detail:     fmt.Sprintf("Description contains a high-confidence %s (%q) — an instruction to the agent, not a tool description.", bestWhat, bestMatch),
	}}
}
