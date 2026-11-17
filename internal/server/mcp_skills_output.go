package server

import (
	"fmt"
	"sort"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
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

// skillWordWithheld stands in for an upstream-written word a blocking policy
// does not let out.
const skillWordWithheld = "[withheld by the output sanitisation policy]"

// skillWordCleaner applies the policy to one upstream-written word of an
// answer: a skill name, a description, a file name. These reach the model
// like the text does. Under a blocking policy a word with a critical secret is
// replaced whole; under the other policies it is redacted and stripped. The
// lines mcpproxy writes itself (version, byte range) are never passed through
// it: a content hash must not be mistaken for a secret and cut out of the
// continuation.
func (p *MCPProxyServer) skillWordCleaner(pol skillOutputPolicy) func(string) string {
	same := func(s string) string { return s }
	if !pol.mutates {
		return same
	}
	if pol.cfg.IsBlock() {
		if p.sanitisationDetector == nil {
			return same
		}
		return func(s string) string {
			if hasCriticalDetection(p.sanitisationDetector, s) {
				return skillWordWithheld
			}
			return s
		}
	}
	d := pol.decide(false)
	if !d.redact && !d.strip {
		return same
	}
	strip := pol.cfg.EnabledStripClasses()
	return func(s string) string {
		out, _ := p.sanitiseTextValue(s, d, strip, map[string]struct{}{}, map[string]struct{}{})
		return out
	}
}

// skillAddressable reports whether a skill can be named to an agent. A skill's
// id is its path, which the upstream chose; a path the output policy would
// change cannot be shown without showing what the policy withholds, and cannot
// be shown changed either, because then it names nothing. Such a skill is left
// out of every listing.
func skillAddressable(clean func(string) string, sk *runtime.AvailableSkill) bool {
	return clean(sk.Path) == sk.Path
}

// CleanSkillForAgents returns a copy of sk whose upstream-written words (name,
// description, file paths) have passed the output policy, or nil for a skill
// that cannot be named under the policy (skillAddressable). It is what a
// scoped REST caller is shown, so that door and the agent tools agree.
func (p *MCPProxyServer) CleanSkillForAgents(sk *runtime.AvailableSkill) *runtime.AvailableSkill {
	clean := p.skillWordCleaner(p.skillPolicy())
	if !skillAddressable(clean, sk) {
		return nil
	}
	out := *sk
	out.Name = clean(sk.Name)
	out.Description = clean(sk.Description)
	out.Files = append([]storage.SkillFile(nil), sk.Files...)
	for i := range out.Files {
		out.Files[i].RelPath = clean(out.Files[i].RelPath)
	}
	return &out
}

// SkillTextForAgents returns one approved file as the output policy lets it
// out, for a door other than the agent tools (a scoped REST read). The second
// result is a short note for the activity log when the policy changed the
// text.
func (p *MCPProxyServer) SkillTextForAgents(serverName, digest string, data []byte) (text []byte, action, summary string, err error) {
	served := p.servedSkillText(p.skillPolicy(), serverName, digest, data)
	if served.blocked {
		return nil, "blocked", served.reason, runtime.ErrSkillOutputBlocked
	}
	if served.text != nil {
		data = served.text
	}
	return data, served.action, served.summary, nil
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
