package server

import (
	"fmt"
	"sort"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
)

// Output sanitisation for skill text (FR-024).
//
// The policy that blocks, redacts or strips upstream tool output applies to
// skill text too, and it is applied to the WHOLE file before the file is cut
// into parts. Applied to each part on its own, a secret that straddles two
// parts would pass in two harmless halves. Offsets and sizes in an answer are
// therefore positions in the text as it is served: equal to the approved copy
// unless the policy changed it.

const (
	// A served text is kept so that the parts of one long read do not each
	// pay for sanitising the whole file again.
	skillServedCacheEntries = 8
	skillServedCacheBytes   = 32 << 20
)

// skillServed is one file as the policy lets it out.
type skillServed struct {
	text    []byte // nil: the approved bytes, unchanged
	blocked bool
	reason  string // why it is blocked
	action  string // "redact" or "strip" when the policy changed the text
	summary string
	used    uint64
}

// skillOutputPolicy is the live output sanitisation policy, as skills use it.
type skillOutputPolicy struct {
	cfg       *config.OutputSanitisationConfig
	mutates   bool // block, redact or strip is on
	spotlight bool
	key       string // identifies the mutating part of the policy
}

func (p *MCPProxyServer) skillPolicy() skillOutputPolicy {
	var cfg *config.OutputSanitisationConfig
	if live := p.currentConfig(); live != nil {
		cfg = live.OutputSanitisation
	}
	if cfg == nil {
		cfg = config.DefaultOutputSanitisationConfig()
	}
	pol := skillOutputPolicy{
		cfg:       cfg,
		mutates:   cfg.IsBlock() || cfg.IsRedact() || cfg.IsStripEnabled(),
		spotlight: cfg.IsSpotlightEnabled(),
	}
	if pol.mutates {
		var classes []string
		for c, on := range cfg.EnabledStripClasses() {
			if on {
				classes = append(classes, c)
			}
		}
		sort.Strings(classes)
		pol.key = fmt.Sprintf("b%t r%t s%t %s m%d d%p", cfg.IsBlock(), cfg.IsRedact(), cfg.IsStripEnabled(),
			strings.Join(classes, ","), cfg.MaxRedactions, p.sanitisationDetector)
	}
	return pol
}

// decide is the policy's decision for untrusted text.
func (pol skillOutputPolicy) decide(critical bool) osDecision {
	return evaluateOutputSanitisation(pol.cfg, contracts.ContentTrustUntrusted, critical)
}

// servedSkillText returns the file as the policy lets it out, computed on the
// whole file and remembered per (server, digest, policy).
func (p *MCPProxyServer) servedSkillText(pol skillOutputPolicy, serverName, digest string, data []byte) skillServed {
	if !pol.mutates {
		return skillServed{}
	}
	if digest == "" { // nothing to remember it under
		return p.sanitiseWholeSkillText(pol, string(data))
	}
	key := serverName + "\x00" + digest + "\x00" + pol.key
	p.skillServedMu.Lock()
	p.skillServedTick++
	if e, ok := p.skillServedCache[key]; ok {
		e.used = p.skillServedTick
		out := *e
		p.skillServedMu.Unlock()
		return out
	}
	p.skillServedMu.Unlock()

	e := p.sanitiseWholeSkillText(pol, string(data))

	if len(e.text) <= skillServedCacheBytes {
		p.skillServedMu.Lock()
		if p.skillServedCache == nil {
			p.skillServedCache = map[string]*skillServed{}
		}
		stored := e
		stored.used = p.skillServedTick
		p.skillServedCache[key] = &stored
		for {
			total, oldest := 0, ""
			for k, c := range p.skillServedCache {
				total += len(c.text)
				if k != key && (oldest == "" || c.used < p.skillServedCache[oldest].used) {
					oldest = k
				}
			}
			if oldest == "" || (len(p.skillServedCache) <= skillServedCacheEntries && total <= skillServedCacheBytes) {
				break
			}
			delete(p.skillServedCache, oldest)
		}
		p.skillServedMu.Unlock()
	}
	return e
}

func (p *MCPProxyServer) sanitiseWholeSkillText(pol skillOutputPolicy, text string) skillServed {
	critical := pol.cfg.IsBlock() && p.sanitisationDetector != nil && hasCriticalDetection(p.sanitisationDetector, text)
	d := pol.decide(critical)
	if d.block {
		return skillServed{blocked: true, reason: d.reason}
	}
	if !d.redact && !d.strip {
		return skillServed{}
	}
	cats, classes := map[string]struct{}{}, map[string]struct{}{}
	out, n := p.sanitiseTextValue(text, d, pol.cfg.EnabledStripClasses(), cats, classes)
	var e skillServed
	if out != text {
		e.text = []byte(out)
	}
	if n > 0 || len(classes) > 0 {
		e.action, e.summary = summariseSanitisation(n, cats, classes)
	}
	return e
}

// skillHeaderPolicy applies the policy to the upstream-written words of a
// header (skill name, file names): they reach the model like the text does.
// blocked reports a critical secret among them under a blocking policy; clean
// redacts and strips one of them under the other policies. The lines mcpproxy
// writes itself (version, byte range) are never passed through it: a content
// hash must not be mistaken for a secret and cut out of the continuation.
func (p *MCPProxyServer) skillHeaderPolicy(pol skillOutputPolicy, words []string) (clean func(string) string, blocked bool, reason string) {
	same := func(s string) string { return s }
	if !pol.mutates {
		return same, false, ""
	}
	if pol.cfg.IsBlock() {
		if p.sanitisationDetector != nil && hasCriticalDetection(p.sanitisationDetector, strings.Join(words, "\n")) {
			return same, true, pol.decide(true).reason
		}
		return same, false, ""
	}
	d := pol.decide(false)
	if !d.redact && !d.strip {
		return same, false, ""
	}
	strip := pol.cfg.EnabledStripClasses()
	return func(s string) string {
		out, _ := p.sanitiseTextValue(s, d, strip, map[string]struct{}{}, map[string]struct{}{})
		return out
	}, false, ""
}

// spotlightLabel is an upstream name made safe for the spotlight fence: one
// bounded line without the fence's own delimiters.
func spotlightLabel(s string, max int) string {
	return strings.NewReplacer("«", "", "»", "").Replace(oneLine(s, max))
}

// spotlightSkillPart wraps one part in the untrusted-content fence when the
// policy asks for it. It is a frame around the part, not a change of the text.
func spotlightSkillPart(pol skillOutputPolicy, part, serverName, skillName string) string {
	if !pol.spotlight || part == "" {
		return part
	}
	return security.SpotlightUntrusted(part, spotlightLabel(serverName, skillServerMax), spotlightLabel(skillName, skillNameMax))
}
