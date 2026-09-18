package runtime

import "github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

// The restart-gated field set: exactly the fields DetectConfigChanges refuses
// to hot-apply, because each one is baked into something built once at startup
// — the bound listener, the http.ServeMux pattern /mcp is registered on, the
// open BBolt database, the auth middleware, the TLS handler chain and the
// http.Server deadlines.
//
// pinRestartGated returns a copy of `desired` with every one of those fields
// taken from `live`, i.e. the configuration the process CAN adopt right now.
// Everything else in `desired` — every hot-reloadable field — is carried
// through, which is the whole point: before this existed, an apply that touched
// one restart-gated field threw away the hot half of the same write, so
// switching to Direct and turning on deferred schemas in one breath left
// deferred schemas off until a restart nobody was told to perform.
//
// The returned config is a SHALLOW copy: it shares Servers, Registries and the
// nested maps with `desired`, which both configs describe identically — the
// pinning only ever touches scalar/pointer fields at the top level. Callers must
// not mutate those shared structures in place, the same contract GetConfig
// already carries.
//
// Keep this list in lockstep with the restart clauses of DetectConfigChanges: a
// field gated there but missing here would be adopted in memory while the API
// reports it as pending, which is the drift the pending/served split exists to
// prevent.
func pinRestartGated(live, desired *config.Config) *config.Config {
	if live == nil || desired == nil {
		return desired
	}
	pinned := *desired
	pinned.Listen = live.Listen
	pinned.RoutingMode = live.RoutingMode
	pinned.DataDir = live.DataDir
	pinned.APIKey = live.APIKey
	pinned.TLS = live.TLS
	pinned.HTTPReadTimeout = live.HTTPReadTimeout
	pinned.HTTPWriteTimeout = live.HTTPWriteTimeout
	pinned.HTTPIdleTimeout = live.HTTPIdleTimeout
	// The JS runtime pool is sized once at server construction and never
	// resized in-process (config_hotreload.go says so where it gates the
	// field). It is the one restart-gated clause that does NOT early-return in
	// the detector, which is exactly why it is easy to miss here — and missing
	// it would let the API report a pool size that is not in effect.
	pinned.CodeExecutionPoolSize = live.CodeExecutionPoolSize
	// audit_log's sink is bound once at construction (config_hotreload.go's
	// detector clause, :470-482) and never rebuilt in-process — restart-
	// pinned exactly like the HTTP/listener fields above (round-3
	// cross-review finding, PR-D: this clause was missing entirely, so a
	// mixed apply that also touched a hot field adopted the new audit_log
	// into the live config while the API still reported the apply as
	// pending a restart, leaving Runtime.Config() readers disagreeing with
	// the sink actually still writing).
	pinned.AuditLog = live.AuditLog
	// server_edition's restart-pinned subset (enabled, oauth.*, public_url,
	// session_cookie_secure, session_ttl, bearer_token_ttl,
	// credential_encryption_key — Spec 107 FR-039 part 2) is bound at login
	// handler / session store / credential store construction, exactly like
	// the fields above; admin_emails stays hot (#1169). Cross-review round 1,
	// chunk 3 P1: this block was missing entirely, so e.g. disabling
	// server_edition.enabled took effect in the live config immediately —
	// restoring anonymous /mcp access — while DetectConfigChanges still
	// reported the apply as requiring a restart.
	pinned.ServerEdition = config.MergeServerEditionRestartGated(live.ServerEdition, desired.ServerEdition)
	return &pinned
}

// mergeChangedFields appends the second list to the first, skipping duplicates,
// so a mixed apply reports both halves without repeating a field.
func mergeChangedFields(base, extra []string) []string {
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, list := range [][]string{base, extra} {
		for _, f := range list {
			if _, dup := seen[f]; dup {
				continue
			}
			seen[f] = struct{}{}
			out = append(out, f)
		}
	}
	return out
}
