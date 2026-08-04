package checks

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
)

// Shadowing is a HARD check that flags cross-server tool impersonation and
// reference (FR — shadowing). Two distinct attack shapes:
//
//  1. Impersonation clone: a tool whose name AND near-duplicate description
//     both match another server's tool (one impersonating the other so an
//     agent calls the wrong one). A name collision ALONE is never flagged:
//     mcpproxy exists to unify many servers, every tool is namespaced
//     server:tool, and ordinary compound names (list_models, search_issues…)
//     legitimately collide across servers — retrieve_tools' ranking is what
//     disambiguates them, and no fixed "distinctive name" heuristic can
//     separate those from attacks (MCP-3520: ElevenLabs vs kaggle
//     list_models). The description clone is the evidence.
//  2. Cross-server reference: a tool whose description names a DISTINCTIVE
//     tool that lives ONLY on different servers (steering the agent's tool
//     selection). A name the tool's own server also exposes is ordinary
//     self-documentation, whoever else exposes it.
//
// The reference shape still requires the name to be distinctive: generic verbs
// ("search", "get", "list") appear in prose constantly. A tool referencing its
// OWN name is also ignored.
type Shadowing struct{}

// ID implements detect.Check.
func (*Shadowing) ID() string { return "shadowing.cross_server" }

// commonNames are generic tool names whose collision/reference across servers is
// ordinary and must never be treated as shadowing.
var commonNames = map[string]struct{}{
	"search": {}, "get": {}, "list": {}, "read": {}, "write": {}, "fetch": {},
	"query": {}, "run": {}, "exec": {}, "call": {}, "create": {}, "update": {},
	"delete": {}, "add": {}, "remove": {}, "find": {}, "open": {}, "close": {},
	"send": {}, "load": {}, "save": {}, "echo": {}, "ping": {}, "status": {},
	"help": {}, "info": {}, "scan": {}, "check": {}, "test": {},
}

// distinctiveName reports whether a tool name is specific enough that a
// cross-server collision/reference is suspicious rather than coincidental.
// Distinctive = reasonably long and not a bare common verb.
func distinctiveName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if len(n) < 6 {
		return false
	}
	if _, common := commonNames[n]; common {
		return false
	}
	return true
}

// Inspect implements detect.Check. Cross-tool reasoning uses the RegistryView
// indexes built once per scan.
func (c *Shadowing) Inspect(tool detect.ToolView, reg detect.RegistryView) []detect.Signal {
	var sigs []detect.Signal

	// 1. Impersonation clone: same name on another server AND a near-duplicate
	// description. The clone is the evidence — a bare name coincidence is the
	// proxy's normal operating condition, not a finding.
	for _, other := range reg.ToolsByName[tool.Name] {
		if other.Server != tool.Server && cloneDescriptions(tool.Description, other.Description) {
			sigs = append(sigs, detect.Signal{
				CheckID:    c.ID(),
				Tier:       detect.TierHard,
				ThreatType: detect.ThreatToolPoisoning,
				Confidence: 0.85,
				Evidence:   detect.CapEvidence(fmt.Sprintf("tool %q duplicates server %q's tool of the same name, description included", tool.Name, other.Server)),
				Detail:     fmt.Sprintf("Tool %q clones server %q's tool — same name and near-identical description — possible impersonation.", tool.Name, other.Server),
			})
			break // one clone signal is enough
		}
	}

	sigs = append(sigs, c.referenceSignals(tool, reg)...)
	return sigs
}

// cloneDescriptions reports whether two descriptions are near-duplicates after
// normalization — the impersonation-clone evidence. Deterministic token-set
// containment: cosmetic edits (case, punctuation, whitespace, word order) do
// not launder a copy, while genuinely different descriptions of a shared name
// stay far below the threshold.
//
// Accepted, deliberate limits (owner decision, MCP-3520):
//   - An attacker who writes a genuinely DIFFERENT description for a colliding
//     name is out of this check's scope — by name alone that case is
//     indistinguishable from two honest servers sharing a compound name
//     (list_models on every model host), which is the proxy's normal
//     condition. The defenses there are admission quarantine for new servers,
//     server:tool namespacing, and server provenance in retrieve_tools.
//   - Descriptions with fewer than three tokens carry too little information
//     to distinguish a clone from a coincidence ("Create" == "Create" says
//     nothing) and never match; empty descriptions likewise.
func cloneDescriptions(a, b string) bool {
	const minTokens = 3
	ta, tb := descTokens(a), descTokens(b)
	if len(ta) < minTokens || len(tb) < minTokens {
		return false
	}
	shared := 0
	for tok := range ta {
		if _, ok := tb[tok]; ok {
			shared++
		}
	}
	smaller, larger := len(ta), len(tb)
	if smaller > larger {
		smaller, larger = larger, smaller
	}
	// Both directions matter: containment of the smaller set catches a copy
	// with words bolted on, while the larger-set floor keeps a short generic
	// sentence from "matching" a long one that merely contains its words.
	return float64(shared) >= 0.85*float64(smaller) && float64(shared) >= 0.7*float64(larger)
}

// descTokens lowercases and splits a description into its letter/digit tokens.
// Unicode-aware on purpose: a Cyrillic or CJK description must tokenize to
// real words, not to an empty set that can never evidence a clone.
func descTokens(s string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, tok := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		out[tok] = struct{}{}
	}
	return out
}

// wordRe extracts identifier-like tokens (incl. snake_case / camelCase words)
// from a description for reference matching.
var wordRe = regexp.MustCompile(`[A-Za-z][A-Za-z0-9_]{5,}`)

// referenceSignals flags a description that names a distinctive tool living on a
// different server. A reference to the tool's own name is ignored.
func (c *Shadowing) referenceSignals(tool detect.ToolView, reg detect.RegistryView) []detect.Signal {
	tokens := wordRe.FindAllString(tool.Description, -1)
	seen := make(map[string]struct{})
	var sigs []detect.Signal
	for _, tok := range tokens {
		if tok == tool.Name {
			continue // self-reference
		}
		if _, dup := seen[tok]; dup {
			continue
		}
		owners, ok := reg.ToolsByName[tok]
		if !ok || !distinctiveName(tok) {
			continue
		}
		// Only flag when the referenced tool lives EXCLUSIVELY on different
		// servers. A name the tool's own server also exposes is ordinary
		// self-documentation ("call list_models first") — that another server
		// happens to expose the same name is the proxy's normal condition,
		// not steering. Accepted corner (owner decision, MCP-3520): a server
		// can silence this branch for itself by exposing a decoy tool under
		// the referenced name — but steering an agent toward a name the
		// server itself exposes reduces to the name-coincidence case above,
		// with the same defenses (admission quarantine, namespacing).
		onOtherServer, onOwnServer := false, false
		for _, o := range owners {
			if o.Server == tool.Server {
				onOwnServer = true
			} else {
				onOtherServer = true
			}
		}
		if !onOtherServer || onOwnServer {
			continue
		}
		seen[tok] = struct{}{}
		sigs = append(sigs, detect.Signal{
			CheckID:    c.ID(),
			Tier:       detect.TierHard,
			ThreatType: detect.ThreatToolPoisoning,
			Confidence: 0.85,
			Evidence:   detect.CapEvidence(fmt.Sprintf("description references cross-server tool %q", tok)),
			Detail:     fmt.Sprintf("Tool %q description steers the agent toward another server's tool %q.", tool.Name, tok),
		})
	}
	return sigs
}
