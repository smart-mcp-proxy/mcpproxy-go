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
// hard emit floor and is NOT auto-blocked (FR-005, the core false-positive
// control). Such a matched-but-discounted phrase is not discarded either — it is
// downgraded to a SOFT review signal (never-fully-suppress, Codex round-3), so a
// real injection can never disappear behind a framing cue.
//
// It runs over the engine's NORMALIZED text (lowercased, contraction-expanded,
// lightly-stemmed, format-runes stripped) so "don't disclose" / "do not tell"
// and "instructions" / "instruction" collapse to one matchable form.
type PhraseInjection struct{}

// ID implements detect.Check.
func (*PhraseInjection) ID() string { return "phrase.injection" }

// phraseHardMinConfidence is the HARD emit floor: at/above it a match
// auto-quarantine-gates. An instruction-position match clears it; a discounted
// (descriptive/example) one does not, so "describes the phrase" tools are never
// hard-blocked (FR-005 MUST-NOT, the core false-positive control).
const phraseHardMinConfidence = 0.6

// phraseSoftFloor is the SOFT review floor. Because this check is built for
// recall, a phrase that MATCHED a curated family but was position-discounted
// below the hard floor is NOT dropped — it is re-floored to phraseSoftFloor and
// emitted at the SOFT tier (review-only, never auto-quarantine). This is the
// "never fully suppress a matched injection" invariant (Spec 077 US1, Codex
// round-3): it closes the whole recurring silent-bypass class, where a fresh
// framing cue each review round ("prompt:", "Example output format.", quotes)
// pushed a real injection below every floor and made it vanish. Worst case a
// matched injection now surfaces for review; it can never be invisible. The
// accepted cost is soft-tier noise on scanners that legitimately quote an
// injection string — soft is review-only, so it never blocks them.
const phraseSoftFloor = 0.3

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
	{ // Bare instruction override WITHOUT an explicit directive-noun: "ignore the
		// above and …", "disregard all previous, then …", "forget everything above; …".
		// The noun-anchored family above misses these (it requires
		// instruction/direction/… after above/previous), but the legacy tpa rules
		// caught them (Spec 077 US1, Codex round-3 finding #2). A trailing continuation
		// (comma/semicolon/colon or and/then/instead/now) is REQUIRED so a benign
		// "ignore the above warning" — where a noun follows the override word — never
		// matches; only the override-then-new-directive shape does. HARD because a
		// bare override immediately handing off to a follow-on instruction is
		// essentially never benign in a tool description.
		re:     regexp.MustCompile(`\b(?:ignore|disregard|forget) (?:all |the |any |these |everything )*(?:previous|prior|above|preceding|earlier|foregoing)\b\s*(?:,|;|:|\band\b|\bthen\b|\binstead\b|\bnow\b)`),
		base:   0.85,
		threat: detect.ThreatPromptInjection,
		what:   "bare instruction-override directive",
	},
	{ // Guardrail override: "ignore your guidelines", "disregard these instructions",
		// "bypass your restrictions". Restores the legacy tpa "ignore your
		// guidelines" posture (Spec 077 FR-004). Requires a possessive/demonstrative
		// ("your"/"these") so it fires only on an imperative to disregard the agent's
		// OWN directives — inherently injection, essentially never benign.
		re:     regexp.MustCompile(`\b(?:ignore|disregard|bypass|override|violat\w*|abandon|circumvent) (?:all )?(?:your|these) (?:safety |content |system |ethical |usage )?(?:guidelin\w*|instruction|rule|policy|restriction|constraint|directive|protocol|guardrail)\w*`),
		base:   0.85,
		threat: detect.ThreatPromptInjection,
		what:   "guardrail-override directive",
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
// highest-confidence curated directive found after position discounting.
//
// Tiering (Spec 077 US1, Codex round-3 "never fully suppress"):
//   - conf ≥ phraseHardMinConfidence (instruction-position) → HARD (auto-quarantine).
//   - a family MATCHED but was discounted below the hard floor
//     (descriptive/example/quoted) → SOFT, re-floored to phraseSoftFloor so the
//     match surfaces for review instead of vanishing. A matched injection is
//     therefore never silently dropped, no matter what framing cue precedes it.
//   - nothing matched → no signal.
func (c *PhraseInjection) Inspect(tool detect.ToolView, _ detect.RegistryView) []detect.Signal {
	text := tool.NormalizedText
	if text == "" {
		return nil
	}

	matched := false
	bestConf := 0.0
	bestMatch := ""
	bestWhat := ""
	bestThreat := ""
	for _, fam := range phraseFamilies {
		for _, loc := range fam.re.FindAllStringIndex(text, -1) {
			conf := fam.base * detect.ClassifyPosition(text, loc[0]).Discount()
			if !matched || conf > bestConf {
				matched = true
				bestConf = conf
				bestMatch = text[loc[0]:loc[1]]
				bestWhat = fam.what
				bestThreat = fam.threat
			}
		}
	}

	if !matched {
		return nil
	}

	// Instruction-position clears the hard floor and auto-quarantine-gates. A
	// matched-but-discounted phrase is NOT dropped: downgrade to SOFT and re-floor
	// to the review floor so it always surfaces (never-fully-suppress invariant).
	tier := detect.TierHard
	conf := bestConf
	detail := fmt.Sprintf("Description contains a high-confidence %s (%q) — an instruction to the agent, not a tool description.", bestWhat, bestMatch)
	if bestConf < phraseHardMinConfidence {
		tier = detect.TierSoft
		if conf < phraseSoftFloor {
			conf = phraseSoftFloor
		}
		detail = fmt.Sprintf("Description references a %s (%q) in a quoted/described position — surfaced for review, not auto-blocked.", bestWhat, bestMatch)
	}

	return []detect.Signal{{
		CheckID:    c.ID(),
		Tier:       tier,
		ThreatType: bestThreat,
		Confidence: detect.ClampConfidence(conf),
		Evidence:   detect.CapEvidence(bestMatch),
		Detail:     detail,
	}}
}
