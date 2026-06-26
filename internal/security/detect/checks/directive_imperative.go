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
	{ // Hidden-instruction / role-injection tags: <IMPORTANT>, <system>, …
		re:   regexp.MustCompile(`<\s*(important|system|secret|critical|admin|instruction|developer|assistant)\b`),
		base: 0.7,
		what: "hidden-instruction tag",
	},
	{ // Instruction override: "ignore previous instructions", "disregard all prior directions".
		re:   regexp.MustCompile(`\b(ignore|disregard|forget|override|bypass) (?:the |all |any |these )?(?:previous|prior|above|earlier|preceding|all|these|any) (?:instruction|direction|command|prompt|rule|guideline)\w*`),
		base: 0.65,
		what: "instruction-override directive",
	},
	{ // Secrecy imperative: "do not tell the user", "must not reveal".
		re:   regexp.MustCompile(`\b(?:do not|must not|never) (?:tell|inform|reveal|disclos\w*|mention|notify|warn|expose)\b`),
		base: 0.6,
		what: "secrecy imperative",
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
