package server

import (
	"context"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
)

// truncatorForProfile returns the truncator for a call served under the
// profile named name in idx (issue #2027): a truncator carrying the profile's
// own tool_response_limit when it sets one (0 = no limit), else the live
// global truncator. A name idx has no profile for (none resolved, a deleted
// pin, a publication gap) keeps the global limit, so a missing profile never
// widens what a caller receives.
//
// Like currentTruncator it must be called at use time: the global truncator is
// swapped on a tool_response_limit hot reload (#861).
func (p *MCPProxyServer) truncatorForProfile(idx *profileIndex, name string) *truncate.Truncator {
	global := p.currentTruncator()
	if idx == nil || name == "" {
		return global
	}
	pc := idx.lookup(name)
	if pc == nil || pc.ToolResponseLimit == nil {
		return global
	}
	return truncate.NewTruncator(*pc.ToolResponseLimit)
}

// truncatorForRequest is truncatorForProfile for a handler that makes no
// profile decision of its own: it resolves the request's effective profile
// without recording it on the session.
func (p *MCPProxyServer) truncatorForRequest(ctx context.Context) *truncate.Truncator {
	idx := p.profileIndexCurrent(ctx)
	return p.truncatorForProfile(idx, p.resolveProfileV3(ctx, idx).Name)
}

// responseLimitForProfile is the direct-mode counterpart: that path bounds
// text by a bare character count rather than a Truncator.
func responseLimitForProfile(idx *profileIndex, name string, global int) int {
	if idx == nil || name == "" {
		return global
	}
	if pc := idx.lookup(name); pc != nil {
		return pc.EffectiveToolResponseLimit(global)
	}
	return global
}

// structuredWithholdLimit is the size above which a structured result is taken
// out of structuredContent and delivered as a text block
// (withholdOversizeStructured), for a call whose effective limit is
// profileLimit while the global limit is globalLimit.
//
// Normally that is the effective limit itself. Output sanitisation reads text
// blocks only, though, and under the global limit an oversize structured
// result reached it as text. A profile whose limit is wider (or 0, no limit)
// must not take that away: while sanitisation can block or rewrite output
// (sanitising), the global limit still decides when the result becomes text,
// and the profile's limit decides only how much of that text is delivered.
func structuredWithholdLimit(profileLimit, globalLimit int, sanitising bool) int {
	if !sanitising || globalLimit <= 0 {
		return profileLimit
	}
	if profileLimit == 0 || profileLimit > globalLimit {
		return globalLimit
	}
	return profileLimit
}

// outputSanitisationRewrites reports whether output sanitisation is set to
// block, redact or strip, the modes that act on a response before it is
// forwarded.
func (p *MCPProxyServer) outputSanitisationRewrites() bool {
	if p.config == nil {
		return false
	}
	cfg := p.config.OutputSanitisation
	return cfg.IsBlock() || cfg.IsRedact() || cfg.IsStripEnabled()
}
