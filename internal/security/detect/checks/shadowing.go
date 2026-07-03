package checks

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
)

// Shadowing is a HARD check that flags cross-server tool impersonation and
// reference (FR — shadowing). Two distinct attack shapes:
//
//  1. Name collision: a DISTINCTIVE tool name exposed by two different servers
//     (one impersonating the other so an agent calls the wrong one).
//  2. Cross-server reference: a tool whose description names a DISTINCTIVE tool
//     that lives on a different server (steering the agent's tool selection).
//
// To hold near-zero FP, both shapes require the name to be distinctive: generic
// verbs ("search", "get", "list") collide across servers all the time and are
// never flagged. A tool referencing its OWN name is also ignored.
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
	if !distinctiveName(tool.Name) {
		// Still allow this tool to reference OTHER distinctive tools, so only
		// the collision branch is gated on the tool's own name.
		return c.referenceSignals(tool, reg)
	}

	var sigs []detect.Signal

	// 1. Name collision across servers.
	for _, other := range reg.ToolsByName[tool.Name] {
		if other.Server != tool.Server {
			sigs = append(sigs, detect.Signal{
				CheckID:    c.ID(),
				Tier:       detect.TierHard,
				ThreatType: detect.ThreatToolPoisoning,
				Confidence: 0.85,
				Evidence:   detect.CapEvidence(fmt.Sprintf("tool %q also exposed by server %q", tool.Name, other.Server)),
				Detail:     fmt.Sprintf("Distinctive tool name %q collides with server %q — possible impersonation.", tool.Name, other.Server),
			})
			break // one collision signal is enough
		}
	}

	sigs = append(sigs, c.referenceSignals(tool, reg)...)
	return sigs
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
		// Only flag when the referenced tool lives on a DIFFERENT server.
		onOtherServer := false
		for _, o := range owners {
			if o.Server != tool.Server {
				onOtherServer = true
				break
			}
		}
		if !onOtherServer {
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
