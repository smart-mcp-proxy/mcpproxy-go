package preflight

import (
	"errors"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolannotations"
)

// Sentinel errors the GLUE returns for conditions the served surface must map
// to a specific HTTP status. Everything else that comes back from a preflight is
// an infrastructure read failure (FR-006) and answers 503.
var (
	// ErrUnknownProfile means the request named a profile that is not
	// configured. The served surface answers 400 (FR-010) — it is a caller
	// mistake, not proxy state, and inventing a verdict for it would put a
	// request bug into the reason taxonomy.
	ErrUnknownProfile = errors.New("preflight: unknown profile")
	// ErrRuntimeUnavailable means the process is too degraded to evaluate
	// honestly (no storage, no index, no live config). The served surface
	// answers 503 rather than emit reduced-fidelity verdicts (FR-006).
	ErrRuntimeUnavailable = errors.New("preflight: runtime unavailable")
)

// MaxWaitMS is the FR-012 cap on a request's wait budget. It lives here so the
// REST validator and the CLI flag check the same number: the CLI has to know it
// because it converts a duration to milliseconds, and a lossy conversion would
// otherwise smuggle an out-of-range wait past the daemon's own check as a
// rounded, in-range one.
const MaxWaitMS = 10000

// Params is one preflight request as the glue layer receives it: the caller's
// identity-derived inputs (tier, token scope, token profile pin) plus the
// request's own inputs (tool refs, profile, policy filters).
//
// It carries NAMES, not resolved scopes: resolving a profile slug to its server
// set requires the live config, which only the glue can read. That is also where
// an unknown profile becomes ErrUnknownProfile.
type Params struct {
	// Tools are the requested refs, already deduplicated by the caller, in
	// first-occurrence order.
	Tools []ToolRef
	// Tier selects the disclosure rules (FR-013). Defaults to TierOperator when
	// empty — the caller must set TierAgentToken explicitly, so a new call site
	// cannot accidentally get the more permissive disclosure.
	Tier Tier
	// Profile is the profile named in the request body ("" = unscoped operator
	// view).
	Profile string
	// TokenServers is the agent token's allowed_servers list (nil for operator
	// callers).
	TokenServers []string
	// TokenProfilePin is the profile an agent token is pinned to, propagated
	// through REST auth (Spec 057 / review finding 11). It can only narrow the
	// evaluation scope, never widen it.
	TokenProfilePin string
	// Filters are the caller's annotation policy filters (spec 094 semantics).
	Filters toolannotations.Filters
}

// Outcome is one evaluated preflight: the per-tool results in request order plus
// the set-level verdict derived from them.
type Outcome struct {
	Verdict Verdict
	Results []Result
}
