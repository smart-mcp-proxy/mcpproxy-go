package checks

import (
	"fmt"
	"regexp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
)

// DirectiveImperative is a SOFT check (FR-009, US2) that flags prompt-injection
// directives smuggled into a tool's description: hidden-instruction tags
// (<IMPORTANT>…), secrecy imperatives ("do not tell the user"), instruction
// overrides ("ignore previous instructions"), and tool-preamble injections
// ("before using this tool, first …").
//
// It runs over the engine's NORMALIZED text (lowercased, contraction-expanded,
// format-rune-stripped) so "don't disclose" and "do not tell" collapse to one
// matchable form. Each hit is position-classified: a phrase quoted or
// illustrated ("detects prompts such as 'ignore previous instructions'") is
// example-position and discounted below the emit threshold, so legitimate
// security tooling that merely *describes* these phrases is not flagged. This is
// the core false-positive control for US2 (per detect.ClassifyPosition).
//
// Being soft, it raises a finding for human review but never auto-quarantines.
type DirectiveImperative struct{}

// ID implements detect.Check.
func (*DirectiveImperative) ID() string { return "directive.imperative" }

// directiveMinConfidence is the per-check emit floor. A lone example-position
// match (base × exampleDiscount, ≈0.55×0.25) lands below it and emits nothing;
// an instruction-position match clears it. Keeps "describes the phrase" tools
// from being flagged (FR-009 MUST-NOT).
const directiveMinConfidence = 0.3

// directiveFamily is one regex family with its base (instruction-position)
// confidence. Order is fixed for determinism.
type directiveFamily struct {
	re   *regexp.Regexp
	base float64
	what string
}

// directiveFamilies are matched against NORMALIZED text. Patterns therefore use
// lowercase, contraction-expanded, lightly-stemmed forms (e.g. "instruction"
// matches the stemmed "instructions"). Built once at init.
var directiveFamilies = []directiveFamily{
	{ // Hidden-instruction / role-injection tags: <IMPORTANT>, <system>, <hidden>,
		// <system_prompt>, … "hidden" restores the legacy tpa <hidden> marker (Spec 077
		// US1, Codex round-2 finding C). The optional (?:[_-]\w+)* suffix lets a
		// compound tag name match — <system_prompt> / <developer-note> — which a bare
		// `\b` after the keyword misses because "_" is a word char (Codex round-3
		// finding #3). It does NOT loosen to prefixes: "<systematic>" still fails (no
		// separator), so the keyword must be a whole tag-name token or the head of an
		// underscore/hyphen-joined one.
		re:   regexp.MustCompile(`<\s*(?:important|system|secret|critical|admin|instruction|developer|assistant|hidden)(?:[_-]\w+)*\b`),
		base: 0.7,
		what: "hidden-instruction tag",
	},
	{ // Instruction override: "ignore previous instructions", "disregard all prior directions".
		re:   regexp.MustCompile(`\b(ignore|disregard|forget|override|bypass) (?:the |all |any |these )?(?:previous|prior|above|earlier|preceding|all|these|any) (?:instruction|direction|command|prompt|rule|guideline)\w*`),
		base: 0.65,
		what: "instruction-override directive",
	},
	{ // Injected new-instruction preamble (legacy tpa restore, Spec 077 US1 Codex
		// round-3 finding #2): "new instructions:", "updated directions:",
		// "additional instructions:". The colon anchor keeps it to the smuggled-header
		// shape — a benign "follow the new instructions carefully" (no colon) does not
		// match. SOFT: benignly phrasable ("returns the new instructions: …"), so
		// review-only. (Normalization leaves the trailing "instructions:" token
		// unstemmed because the colon blocks the plural-strip, so \w* absorbs the "s".)
		re:   regexp.MustCompile(`\b(?:new|updated|revised|additional|further|latest|real|actual|hidden|secret) (?:instruction|direction|command|rule|order)\w*\s*:`),
		base: 0.5,
		what: "injected instruction preamble",
	},
	{ // Secrecy imperative: "do not tell the user", "must not reveal".
		re:   regexp.MustCompile(`\b(?:do not|must not|never) (?:tell|inform|reveal|disclos\w*|mention|notify|warn|expose)\b`),
		base: 0.6,
		what: "secrecy imperative",
	},
	{ // Covert-secrecy directive (legacy tpa restore, Spec 077 US1 Codex round-2
		// finding C): coercion to act behind the user's back — "without telling the
		// user", "without informing the user", "hide this from …", "keep this
		// hidden/secret". Matched on NORMALIZED text (stemmed: "telling"→"tell").
		// Benignly phrasable in rare cases, so SOFT (review, never auto-quarantine).
		re:   regexp.MustCompile(`\b(?:without (?:tell|inform|notify|alert|warn)\w* (?:the )?(?:user|caller|human|operator|client)|(?:hide|conceal) (?:this|it|that)(?: \w+)? from|keep (?:this|it|that)(?: \w+)? (?:hidden|secret|conceal\w*|private|quiet|confidential))\b`),
		base: 0.6,
		what: "covert-secrecy directive",
	},
	{ // Exfiltration imperative: "reveal your system prompt", "send the api key".
		re:   regexp.MustCompile(`\b(?:reveal|expose|print|output|send|leak|disclos\w*) (?:your |the |all )?(?:system prompt|api key\w*|secret\w*|password\w*|credential\w*|private key)\b`),
		base: 0.6,
		what: "exfiltration imperative",
	},
	{ // Tool-preamble injection: "before using this tool, first …".
		re:   regexp.MustCompile(`\bbefore (?:you )?(?:us\w*|call\w*|invok\w*|run\w*|execut\w*|access\w*) (?:this )?tool\b`),
		base: 0.55,
		what: "tool-preamble injection",
	},
	{ // Tool-priority coercion (legacy tpa): "always call this tool first",
		// "before using any other tool". Broader than tool-preamble and benignly
		// phrasable ("call this tool first to authenticate"), so it lives in the
		// SOFT tier (review, never auto-quarantine). "always" stems to "alway".
		re:   regexp.MustCompile(`\b(?:(?:alway\w* )?(?:call|us\w*|invok\w*|run\w*) this tool (?:first|before)|before (?:us\w*|call\w*|invok\w*|run\w*) any other tool)\b`),
		base: 0.5,
		what: "tool-priority coercion",
	},
	{ // Jailbreak / mode-override triggers (legacy tpa): "developer mode",
		// "jailbreak", "ignore your guidelines". Kept SOFT because "developer mode"
		// has legitimate uses; a genuine guardrail-override is separately caught at
		// the HARD tier by phrase.injection.
		re:   regexp.MustCompile(`\b(?:developer mode|jailbreak\w*|dan mode|do anything now|unrestricted mode|ignore your guidelin\w*)\b`),
		base: 0.5,
		what: "jailbreak trigger",
	},
	{ // Data-forwarding to an external sink (legacy tpa): "upload the file to an
		// external endpoint", "post the contents to a remote server". Requires an
		// external/remote-flavoured target so benign "uploads the file to the
		// storage bucket" does not match. SOFT: it informs review without blocking.
		re:   regexp.MustCompile(`\b(?:upload|post|send|transmit|forward|exfiltrat\w*|leak|dump) (?:the |all |your )?(?:file|content|data|output|result|conversation|log|record|payload)\w* to (?:an? |the |our )?(?:external|remote|third.?party|attacker|adversar\w*|off.?site|another server|http|ftp)`),
		base: 0.5,
		what: "external data-forwarding directive",
	},
}

// Inspect implements detect.Check. It emits at most one signal per tool: the
// highest-confidence directive match that clears directiveMinConfidence after
// position discounting.
func (c *DirectiveImperative) Inspect(tool detect.ToolView, _ detect.RegistryView) []detect.Signal {
	text := tool.NormalizedText
	if text == "" {
		return nil
	}

	bestConf := 0.0
	bestMatch := ""
	bestWhat := ""
	for _, fam := range directiveFamilies {
		for _, loc := range fam.re.FindAllStringIndex(text, -1) {
			conf := fam.base * detect.ClassifyPosition(text, loc[0]).Discount()
			if conf > bestConf {
				bestConf = conf
				bestMatch = text[loc[0]:loc[1]]
				bestWhat = fam.what
			}
		}
	}

	if bestConf < directiveMinConfidence {
		return nil
	}

	return []detect.Signal{{
		CheckID:    c.ID(),
		Tier:       detect.TierSoft,
		ThreatType: detect.ThreatPromptInjection,
		Confidence: detect.ClampConfidence(bestConf),
		Evidence:   detect.CapEvidence(bestMatch),
		Detail:     fmt.Sprintf("Description contains a %s (%q) — embedded instructions to the agent, not a tool description.", bestWhat, bestMatch),
	}}
}
