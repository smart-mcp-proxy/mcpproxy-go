// SPDX-License-Identifier: Apache-2.0
//
// The anti-refusal pattern families below are adapted from NVIDIA SkillSpector
// (src/skillspector/nodes/analyzers/static_patterns_anti_refusal.py, AR1–AR3,
// reviewed at commit 5cb9385ab0dffa974088ee21c4376c7e560b2a52).
// Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// Modifications Copyright (c) 2026 the mcpproxy-go authors.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not
// use this file except in compliance with the License. You may obtain a copy
// of the License at http://www.apache.org/licenses/LICENSE-2.0. Unless
// required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//
// Changes from upstream: regexes rewritten for the detect package's normalized
// text (lowercased, contraction-expanded, lightly stemmed), scanned per
// metadata field (description and every schema string) and position-classified
// with detect.ClassifyPosition instead of upstream's contextual-triage tags; a
// risk-concealment family was added; families that are also ordinary tool
// behaviour require an agent-directed cue; technical warning nouns are masked.

package checks

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
)

// AntiRefusal is a SOFT check (Spec 122 US1) for directives that try to
// neutralise the agent's refusal or safety behaviour rather than inject a
// task: refusal suppression ("never refuse", "always comply"), disclaimer and
// warning suppression ("no disclaimers", "do not moralize"), safety
// nullification ("you have no restrictions", "no content policy applies")
// and risk concealment ("suppress security warnings", "never warn the user
// about the risks"). Every match is a review warning; nothing here
// auto-quarantines.
type AntiRefusal struct{}

// ID implements detect.Check.
func (*AntiRefusal) ID() string { return "refusal.directive" }

// antiRefusalMinConfidence is the review floor (FR-013): a quoted or
// illustrated match (×0.25) always lands below it; a descriptive one (×0.5)
// survives only for the two strongest families.
const antiRefusalMinConfidence = 0.3

// Family bases (FR-013).
const (
	refusalSuppressionBase    = 0.6
	disclaimerSuppressionBase = 0.5
	safetyNullificationBase   = 0.65
	riskConcealmentBase       = 0.55
)

type antiRefusalFamily struct {
	re   *regexp.Regexp
	base float64
	what string
	// agentCue requires an agent-directed word in the match's sentence
	// (FR-011): the family's vocabulary is also ordinary tool behaviour
	// ("do not show warnings for deprecated fields").
	agentCue bool
	// strictCue (Sol r5) requires, for families whose verb is also server
	// behaviour ("never refuse connections", "always answer every request
	// with HTTP 200"), either a strict agent/user word in the sentence
	// OUTSIDE the match or a request-like object right after the verb.
	strictCue bool
}

// strictAgentSubjectRe is an agent addressed BEFORE the verb ("you must
// never refuse", "the assistant will always answer …"). A bare "your" or
// "user" elsewhere in the sentence is not a cue: "never refuse connections
// from your local network", "answer every request in the user locale" are
// ordinary metadata (Sol r7 P2).
var strictAgentSubjectRe = regexp.MustCompile(`\b(?:you|yourself|the (?:assistant|agent|model|ai|llm|chatbot)|as an? (?:assistant|agent|ai))\b`)

// strictHarmCueRe is a safety word anywhere in the sentence that makes the
// universal directive about the agent's refusal behaviour ("regardless" and
// "no matter" alone are not: "regardless of the processing result", Sol r6).
var strictHarmCueRe = regexp.MustCompile(`\b(?:harmful|unethical|illegal|dangerous|unsafe|inappropriate|restriction\w*|polic\w*|safety|guideline\w*|guidelin\w*|ethic\w*|jailbreak\w*|no matter (?:what|how))\b`)

// strictObjectRe is a request-like object within three words after the verb.
var strictObjectRe = regexp.MustCompile(`^\W*(?:(?:\w+\W+){0,2})?(?:request|task|instruction|prompt|quer\w*|question|command|order|demand|ask|anyth\w*|whatever)\b`)

// agentCueRe marks a sentence as addressed to the agent rather than
// describing tool output.
var agentCueRe = regexp.MustCompile(`\b(?:you|your|yourself|assistant|agent|model|llm|chatbot|answer\w*|respond\w*|repl(?:y|i\w*)|response|directly|lectur\w*|moraliz\w*|moralis\w*|regardless)\b`)

// technicalWarning blanks warning nouns that belong to tooling, not to the
// agent's safety behaviour, so "suppresses compiler warnings" and "do not show
// warnings for deprecated fields" never match (FR-011). Same-length masking
// keeps offsets valid for position classification. Normalized forms: "warning"
// stems to "warn", "warnings" to "warning".
var technicalWarning = regexp.MustCompile(`\b(?:(?:health[- ]?check|http\w*|api|ping|heartbeat|probe|dns|tcp|grpc|rest|graphql|websocket|options|preflight|cors|liveness|readiness|pull|merge|pull-request|feature|change|support|service|network|rpc|json-rpc|batch|read|write|get|post|put|delete|retry|idempotent|cache|cached|webhook|callback|oauth|token|auth\w*|login|signup|password[- ]reset|checkout|payment|refund|booking|search) (?:request|query|quer|message|messag|command)\w*|(?:compil\w*|lint\w*|deprecat\w*|console|build|runtime|type|syntax|eslint|gcc|clang|tsc|pylint|dependenc\w*|npm|yarn|cargo|pip|vet|ssl|tls|certificat\w*|browser|shell|bash|import|unused|parser|log|debug|test|coverage|low[- ]?battery|weather|storm|flood|traffic|health|allerg\w*|dosag\w*|interaction) (?:warning|warn|alert|caveat)\w*|(?:warning|warn|alert|caveat)\w* (?:for|about|from|in|on) (?:deprecat\w*|unused|compil\w*|the compiler|the linter|the build|ssl|tls|certificat\w*|syntax|type|lint\w*|import\w*|dependenc\w*))\b`)

// antiRefusalFamilies are matched against NORMALIZED text. Order is fixed
// for determinism.
var antiRefusalFamilies = []antiRefusalFamily{
	// AR1 — refusal suppression.
	{regexp.MustCompile(`\b(?:never|do not|must not|cannot|will not) (?:ever )?refus\w*\b`), refusalSuppressionBase, "refusal-suppression directive", false, true},
	{regexp.MustCompile(`\b(?:never|do not|must not) (?:ever )?declin\w* (?:a |an |any |the |all |to |user |users |their |such )?(?:request|task|instruction|prompt|quer\w*|question|command|order|user\w*|to (?:answer|help|respond|comply|assist))\b`), refusalSuppressionBase, "refusal-suppression directive", false, false},
	{regexp.MustCompile(`\byou (?:must|will|should|can|shall) ?(?:not|never) (?:refus\w*|declin\w*|say no)\b`), refusalSuppressionBase, "refusal-suppression directive", false, false},
	{regexp.MustCompile(`\byou (?:cannot|can not|are (?:not able|unable)) (?:to )?(?:refus\w*|declin\w*|say no)\b`), refusalSuppressionBase, "refusal-suppression directive", false, false},
	{regexp.MustCompile(`\bnever say (?:that )?(?:you )?(?:cannot|will not|no)\b`), refusalSuppressionBase, "refusal-suppression directive", false, false},
	// "always comply/obey" needs the agent as subject or a request/user as
	// object: "always comply with HTTP caching semantics" is a protocol rule.
	{regexp.MustCompile(`\b(?:(?:you|the (?:agent|assistant|model)) (?:must|should|will|shall) alway\w* (?:comply|compli\w*|obey)\b|alway\w* (?:comply|compli\w*|obey)(?: with| to)? (?:the |every |all |any |each |their |whatever |what )?(?:user|request|instruction|command|order|demand|whatever|what|me|us|them|human)\w*)`), refusalSuppressionBase, "refusal-suppression directive", false, false},
	{regexp.MustCompile(`\balway\w* (?:answer|respond|repl\w*)(?: to)? (?:(?:every|all|any|each)(?: single| incoming| user| user's| human| customer)? (?:request|question|prompt|quer\w*|command|instruction|demand|ask|order|message|messag)\w*|whatever|whatsoever|the user)\b`), refusalSuppressionBase - 0.05, "refusal-suppression directive", false, true},
	{regexp.MustCompile(`\b(?:(?:you|the (?:agent|assistant|model)) (?:must|should|will|shall) alway\w* agree|alway\w* agree (?:with|to) (?:the |every |all |any |each )?(?:user|request|question|prompt|quer\w*|command)\w*)\b`), refusalSuppressionBase, "refusal-suppression directive", false, false},
	// The object must be a request from someone, not a protocol message
	// ("answer every health-check request with HTTP 200", Sol r3 P2);
	// technical request nouns are masked below.
	{regexp.MustCompile(`\b(?:you )?must (?:alway\w* )?(?:answer|comply with|respond to|fulfill|fulfil|complete|execute|perform|obey|honou?r) (?:every|all|any|each)(?: single| incoming| user| user's| human| customer)? (?:request|question|prompt|quer\w*|command|instruction|demand|ask|order|message|messag)\w*`), refusalSuppressionBase, "refusal-suppression directive", false, true},
	{regexp.MustCompile(`\bno request is (?:off[- ]?limit\w*|forbidden|too|ever)\b`), refusalSuppressionBase, "refusal-suppression directive", false, false},
	{regexp.MustCompile(`\bregardless of (?:any |your |the |its )?(?:restriction|polic\w*|guideline|guidelin|rule|rul|safety|content|ethic|limit)\w*\b`), refusalSuppressionBase, "refusal-suppression directive", false, false},
	// AR2 — disclaimer / caveat / warning suppression.
	{regexp.MustCompile(`\b(?:do not|never|no|must not) (?:(?:include|add|give|provide|use|append|show|output|print|emit|display|attach|insert) )?(?:any |a |the |unnecessary |extra )?(?:(?:warning|warn)\w*,? (?:or|and) )?(?:disclaimer|caveat)\w*\b`), disclaimerSuppressionBase, "disclaimer-suppression directive", false, false},
	{regexp.MustCompile(`\b(?:do not|never|must not) (?:include|add|give|provide|show|output|print|emit|display|attach|insert) (?:any |a |the |unnecessary |extra )?(?:warning|warn)\w*\b`), disclaimerSuppressionBase, "disclaimer-suppression directive", true, false},
	{regexp.MustCompile(`\bwithout (?:any |unnecessary |extra )?(?:warning|disclaimer\w*|caveat\w*|moraliz\w*|moralis\w*|judg\w*|lectur\w*)\b`), disclaimerSuppressionBase, "disclaimer-suppression directive", true, false},
	{regexp.MustCompile(`\b(?:do not|never|must not) (?:lecture|lectur|moraliz\w*|moralis\w*|preach|judge|judg|sermoniz\w*)\b`), disclaimerSuppressionBase + 0.05, "disclaimer-suppression directive", false, false},
	{regexp.MustCompile(`\b(?:skip|omit|leave out|drop|strip|remove) (?:the |any |all |your )?(?:disclaimer\w*|warning|warn|ethic\w*|moraliz\w*|moralis\w*|safety (?:note|warning|warn|caveat|preamble)\w*)\b`), disclaimerSuppressionBase, "disclaimer-suppression directive", true, false},
	{regexp.MustCompile(`\b(?:no|without) (?:ethical|moral) (?:judg\w*|consideration\w*|commentary|objection\w*|qualm\w*)\b`), disclaimerSuppressionBase, "disclaimer-suppression directive", false, false},
	{regexp.MustCompile(`\b(?:do not|never) (?:apologize|apologis\w*|say sorry)\b`), disclaimerSuppressionBase - 0.1, "disclaimer-suppression directive", false, false},
	// AR3 — safety / policy nullification.
	{regexp.MustCompile(`\byou (?:have|has|possess) no (?:restriction|limitation|filter|rule|rul|guideline|guidelin|boundari|limit|constraint|safeguard|guardrail)\w*\b`), safetyNullificationBase, "safety-nullification directive", false, false},
	{regexp.MustCompile(`\b(?:you are|act as|pretend (?:to be|you are|you'?re)|behave as|roleplay as)\b[^.!?]{0,30}\b(?:unrestricted|unfilter\w*|uncensor\w*|no filter\w*|without (?:any )?(?:restriction|limit)\w*)\b`), safetyNullificationBase, "safety-nullification directive", false, false},
	{regexp.MustCompile(`\b(?:ignore|disregard|forget|bypass|override|suspend|drop) (?:all |any )?(?:your (?:content |safety |usage |ethical )?(?:polic\w*|guidelin\w*|guideline\w*|rule|rul\w*|restriction\w*|filter\w*|safeguard\w*|guardrail\w*|constraint\w*|training)|(?:content |safety |ethical )(?:polic\w*|guidelin\w*|guideline\w*|restriction\w*|filter\w*|guardrail\w*|constraint\w*)|safety)\b`), safetyNullificationBase, "safety-nullification directive", false, false},
	{regexp.MustCompile(`\bnot bound by (?:any |the |your )?(?:rule|rul|polic\w*|guideline|guidelin|restriction|constraint|openai|anthropic|google|law|term)\w*\b`), safetyNullificationBase, "safety-nullification directive", false, false},
	{regexp.MustCompile(`\bdo anyth\w* now\b`), safetyNullificationBase, "safety-nullification directive", false, false},
	// "developer/debug mode enabled" is deliberately absent: the existing
	// directive.imperative jailbreak-trigger family already surfaces
	// "developer mode" for review, and application modes ("starts the browser
	// with developer mode enabled") are ordinary metadata (Sol r2 P2).
	// Generic application filters/rules ("no filters apply when the query
	// is empty") are not safety policy: a safety qualifier, or the agent as
	// the thing not bound, is required (Sol r4 P2).
	{regexp.MustCompile(`\bno (?:content |safety |usage |ethical |moderation |ai |model )(?:polic\w*|filter\w*|restriction\w*|guideline\w*|guidelin\w*|rule|rul\w*|limit\w*) (?:appl\w*|exist\w*|bind\w*|constrain\w*)\b|\bno (?:polic\w*|restriction\w*|rule|rul\w*|filter\w*|limit\w*) (?:appl\w*|exist\w*|bind\w*) (?:to you|to the (?:assistant|agent|model)|anymore|any more|whatsoever)\b`), safetyNullificationBase, "safety-nullification directive", false, false},
	{regexp.MustCompile(`\b(?:free from|without|free of) (?:any |all )?(?:safety (?:guardrail|constraint|safeguard|restriction|filter)\w*|guardrail\w*|safeguard\w*|ethical (?:constraint|restriction|limit)\w*|content (?:polic\w*|filter\w*|restriction\w*))\b`), safetyNullificationBase, "safety-nullification directive", false, false},
	{regexp.MustCompile(`\b(?:your (?:safety |content |usage |ethical )?|(?:the |all |any )?(?:safety |content |usage |ethical |moderation ))(?:polic\w*|guideline\w*|guidelin\w*|restriction\w*|guardrail\w*|safeguard\w*|rule|rul\w*|filter\w*) (?:no longer appl\w*|do not appl\w*|are (?:lift|suspend|disabl|remov|void|null)\w*|is (?:lift|suspend|disabl|remov|void|null)\w*|have been (?:lift|suspend|disabl|remov)\w*)\b|\b(?:the |all |any )?(?:guardrail\w*|safeguard\w*) (?:no longer appl\w*|do not appl\w*|are (?:lift|suspend|disabl|remov|void|null)\w*|have been (?:lift|suspend|disabl|remov)\w*)\b`), safetyNullificationBase, "safety-nullification directive", false, false},
	// Risk concealment.
	{regexp.MustCompile(`\b(?:suppress|hide|conceal|silenc|silence|disabl\w*|mute|swallow|drop|omit|remov\w*|strip|bury|downplay)\w* (?:all |any |the |every )?(?:security|safety|risk|danger|privacy) (?:warning|warn|alert|notice|caveat|notification|flag|concern|message)\w*\b`), riskConcealmentBase, "risk-concealment directive", false, false},
	{regexp.MustCompile(`\b(?:do not|never|must not|should not) (?:warn|alert|inform|tell|notify|remind|caution) (?:the )?(?:user|human|operator|caller|person|customer)\w* (?:about|of|regarding|on) (?:any |the |potential |possible |these |this |such )?(?:risk|danger|hazard|security|safety|side effect|consequence|threat|vulnerabilit|harm|exposure|privacy)\w*`), riskConcealmentBase, "risk-concealment directive", false, false},
	{regexp.MustCompile(`\b(?:do not|never|must not|should not) (?:mention|flag|report|disclose|disclos|surfac\w*|highlight|point out|bring up|raise) (?:any |the |potential |possible |these |this |such )?(?:risk|danger|hazard|security (?:issue|concern|risk|flaw|vulnerabilit|problem|implication)|safety (?:issue|concern|risk|problem|implication)|vulnerabilit|side effect|harm)\w*\b`), riskConcealmentBase, "risk-concealment directive", false, false},
}

// Inspect implements detect.Check. It scans every field (description and each
// schema string) on its own, in two views (plain normalized and de-obfuscated
// phrase text) so folding can only add matches, and emits at most one signal:
// the highest-confidence match after position discounting, ties broken by
// field order then offset (FR-003).
func (c *AntiRefusal) Inspect(tool detect.ToolView, _ detect.RegistryView) []detect.Signal {
	fields, _ := detect.FieldStrings(tool)
	bestConf := 0.0
	bestMatch, bestWhat, bestPath := "", "", ""
	for _, f := range fields {
		if strings.TrimSpace(f.Text) == "" {
			continue
		}
		plain := detect.Normalize(f.Text)
		views := []string{plain}
		if phrase := detect.NormalizeForPhrase(f.Text); phrase != plain {
			views = append(views, phrase)
		}
		for _, t := range views {
			masked := technicalWarning.ReplaceAllStringFunc(t, func(m string) string { return strings.Repeat("#", len(m)) })
			// A semicolon separates independent clauses ("Return JSON only;
			// do not include warnings"): the framing of the first clause
			// must not discount the directive in the second. Same length,
			// so offsets stay valid.
			posText := strings.ReplaceAll(t, ";", ".")
			for _, fam := range antiRefusalFamilies {
				if fam.base <= bestConf {
					continue // cannot beat the best match found so far
				}
				for _, loc := range fam.re.FindAllStringIndex(masked, -1) {
					if fam.agentCue && !sentenceHasAgentCue(t, loc[0], loc[1]) {
						continue
					}
					// masked, not t: a technical object blanked for the family
					// must not requalify the match ("never refuse an HTTP
					// request", Sol r8 P2).
					if fam.strictCue && !strictCueSatisfied(masked, loc[0], loc[1]) {
						continue
					}
					conf := fam.base * detect.ClassifyPosition(posText, loc[0]).Discount()
					if conf < antiRefusalMinConfidence {
						continue
					}
					if conf > bestConf {
						bestConf = conf
						bestMatch = t[loc[0]:loc[1]]
						bestWhat = fam.what
						bestPath = f.Path
					}
					if conf >= fam.base {
						break // an undiscounted match: later ones of this family cannot score higher
					}
				}
			}
		}
	}
	if bestConf < antiRefusalMinConfidence {
		return nil
	}
	where := ""
	if bestPath != string(detect.SpanFieldDescription) {
		where = fmt.Sprintf(" [field %s]", bestPath)
	}
	return []detect.Signal{{
		CheckID:    c.ID(),
		Tier:       detect.TierSoft,
		ThreatType: detect.ThreatPromptInjection,
		Confidence: detect.ClampConfidence(bestConf),
		Evidence:   detect.CapEvidence(bestMatch),
		Detail:     fmt.Sprintf("Description contains a %s (%q) — an attempt to suppress the agent's refusal or safety behaviour, surfaced for review.%s", bestWhat, bestMatch, where),
	}}
}

// sentenceHasAgentCue reports whether the sentence holding text[start:end]
// carries a word that addresses the agent (FR-011).
func sentenceHasAgentCue(text string, start, end int) bool {
	lo := 0
	if i := strings.LastIndexAny(text[:start], ".!?;"); i >= 0 {
		lo = i + 1
	}
	hi := len(text)
	if i := strings.IndexAny(text[end:], ".!?;"); i >= 0 {
		hi = end + i
	}
	return agentCueRe.MatchString(text[lo:hi])
}

// strictCueSatisfied reports a strict agent/user word in the sentence outside
// the match, or a request-like object right after it.
func strictCueSatisfied(text string, start, end int) bool {
	lo := 0
	if i := strings.LastIndexAny(text[:start], ".!?;"); i >= 0 {
		lo = i + 1
	}
	hi := len(text)
	if i := strings.IndexAny(text[end:], ".!?;"); i >= 0 {
		hi = end + i
	}
	if strictObjectRe.MatchString(text[end:hi]) {
		return true
	}
	return strictAgentSubjectRe.MatchString(text[lo:start]) || strictHarmCueRe.MatchString(text[lo:start]) || strictHarmCueRe.MatchString(text[end:hi])
}
