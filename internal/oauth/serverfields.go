package oauth

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// RedactServerSecretFields masks the secret-bearing fields of one
// contracts.Server in place, using the LIVE policy — the SAME
// oauth.Redaction rule set the MCP `upstream_servers list` /
// `quarantine_security list_quarantined` payloads are built from.
//
// Issue #1148, round 4 finding 3 put the FIELD LIST here. Round 6 finding 2
// put the RULES here too: this function used to apply name-rule-only redaction
// (RedactEnvValues / RedactStringHeaders / RedactURLQueryParams) while the MCP
// door applied the name rule PLUS the value-shaped detector, so a credential
// under a benign env/header name — or under an unrecognised URL query
// parameter — was masked on one door and published in the clear on the other.
// Sharing a field list but not the rules is the same half-done shape this issue
// keeps producing; both now come from LiveRedaction.
//
// There are THREE doors onto this struct — `GET /api/v1/servers` and its
// single-server children, the `/events` SSE `servers.changed` payload, and the
// tray/Web UI reading either. Parity between them is load-bearing beyond the
// leak: the Web UI's mergeServers treats each payload as authoritative, so a
// masked-vs-plaintext mismatch between the list response and the SSE delivery
// would flicker on every event.
//
// Callers apply the `reveal_secret_headers` opt-out themselves; by the time a
// server reaches here it is being redacted.
//
// The write-path answer for every field is recorded ONCE, in
// ServerFieldMaskDecisions below, and enforced by the write doors through
// UnmaskLiveEnvValues / UnmaskLiveHeaders / UnmaskLiveURL / UnmaskLiveOAuth
// plus the CheckServerWriteMasks residual net.
func RedactServerSecretFields(server *contracts.Server) {
	if server == nil {
		return
	}
	if len(server.Headers) > 0 {
		headers := make(map[string]string, len(server.Headers))
		for k, v := range server.Headers {
			headers[k] = LiveRedaction.HeaderValue(k, v)
		}
		server.Headers = headers
	}
	if len(server.Env) > 0 {
		env := make(map[string]string, len(server.Env))
		for k, v := range server.Env {
			env[k] = LiveRedaction.EnvValue(k, v)
		}
		server.Env = env
	}
	if server.URL != "" {
		server.URL = LiveRedaction.URLValue(server.URL)
	}
	if len(server.Args) > 0 {
		server.Args = LiveRedaction.Argv(server.Args)
	}
	if server.OAuth != nil {
		// Copy the struct rather than writing through the pointer: the
		// contracts.Server may share its OAuth block with the caller's config.
		masked := *server.OAuth
		// auth_url / token_url are DISCOVERED endpoints, not secrets, and they
		// exist only on this projection (config.OAuthConfig has no such
		// fields). They take the URL rule rather than the name rule on
		// purpose: `auth_url` contains the substring `AUTH`, so judging it by
		// its NAME would blank a public endpoint the operator needs to read,
		// while the URL rule still masks a credential carried in its query
		// string.
		masked.AuthURL = LiveRedaction.URLValue(masked.AuthURL)
		masked.TokenURL = LiveRedaction.URLValue(masked.TokenURL)
		// client_id takes the same leaf rule the MCP door applies to
		// config.OAuthConfig.client_id — readable unless the value itself is
		// credential-shaped.
		masked.ClientID = LiveRedaction.Leaf("client_id", masked.ClientID)
		if len(masked.ExtraParams) > 0 {
			params := make(map[string]string, len(masked.ExtraParams))
			for k, v := range masked.ExtraParams {
				params[k] = LiveRedaction.Leaf(k, v)
			}
			masked.ExtraParams = params
		}
		// Round 6 finding 3: the MCP door masks `oauth.scopes` (and REFUSES an
		// echo of that mask) because a scope slot is free text an agent can
		// paste a credential into. Two doors, one field, one answer.
		if len(masked.Scopes) > 0 {
			scopes := make([]string, len(masked.Scopes))
			for i, v := range masked.Scopes {
				scopes[i] = LiveRedaction.Leaf("scopes", v)
			}
			masked.Scopes = scopes
		}
		server.OAuth = &masked
	}
	// The isolation blocks are REDACTED THROUGH THE SHARED WALK rather than
	// field by field (issue #1148, round 7 finding 2).
	//
	// This function used to skip them entirely, so `isolation.extra_args` —
	// free text an operator can put `-e API_KEY=<token>` into — was masked on
	// the MCP door and published IN THE CLEAR on `GET /api/v1/servers` and on
	// every `/events` servers.changed payload. The decision table asserted in
	// prose that it was covered; it was not.
	//
	// Walking rather than enumerating is the durable half: a field added to
	// contracts.IsolationConfig / IsolationDefaults is masked because the walk
	// reaches it. `isolation_defaults` is included because its contents are
	// resolved from the SAME operator-supplied global config — a credential in
	// the global `docker_isolation.extra_args` lands there verbatim.
	//
	// A walk that cannot round-trip fails CLOSED: the block is dropped rather
	// than published half-redacted.
	if server.Isolation != nil {
		iso := *server.Isolation
		if LiveRedaction.RedactNested("isolation", &iso) {
			server.Isolation = &iso
		} else {
			server.Isolation = nil
		}
	}
	if server.IsolationDefaults != nil {
		defaults := *server.IsolationDefaults
		if LiveRedaction.RedactNested("isolation_defaults", &defaults) {
			server.IsolationDefaults = &defaults
		} else {
			server.IsolationDefaults = nil
		}
	}
	if server.LastError != "" {
		server.LastError = MaskDetectedSecrets(RedactSensitiveData(server.LastError))
	}
	// GH #1145's retry_stopped_reason is free-form UPSTREAM text, not the fixed
	// catalog string its name suggests: diagnostics.PermanentFailureReason falls
	// through to the raw error whenever the terminal code has no catalog
	// message, and that raw error is ci.LastError.Error() — which
	// core.enrichTransportClosedError folds the CHILD PROCESS'S captured stderr
	// into, and which routinely carries the upstream URL with its query
	// credentials. So it is scrubbed in parity with LastError / Health.Detail /
	// Diagnostic.Cause rather than trusted for its name.
	if server.RetryStoppedReason != "" {
		server.RetryStoppedReason = MaskDetectedSecrets(RedactSensitiveData(server.RetryStoppedReason))
	}
	if server.Health != nil && server.Health.Detail != "" {
		health := *server.Health
		health.Detail = MaskDetectedSecrets(RedactSensitiveData(health.Detail))
		server.Health = &health
	}
	// Spec 044 diagnostic — its Cause echoes the raw connect error, which
	// carries the full upstream URL (query secrets and all); scrub it in parity
	// with LastError / Health.Detail.
	if server.Diagnostic != nil && server.Diagnostic.Cause != "" {
		diag := *server.Diagnostic
		diag.Cause = MaskDetectedSecrets(RedactSensitiveData(diag.Cause))
		server.Diagnostic = &diag
	}
}

// MaskDecision is what a write door does when a client echoes back the mask a
// read door rendered for a field.
//
// Issue #1148 spent five review rounds on one shape: a rule applied at one door
// and not its sibling. The cure is that the answer for a field is written down
// ONCE, here, and every door reads it from the same place — so a field cannot
// be masked on read with no matching answer on write (which corrupts the stored
// credential), nor left in the clear on one door because the other one already
// handles it.
type MaskDecision string

const (
	// MaskDecisionRevertByKey — the read mask is reverted on write, bound to a
	// KEY the caller cannot restate without also restating what the secret is
	// for: a map key, a query-parameter name plus the stored scheme+host, a
	// named struct field. Anything the binding cannot reach is refused.
	MaskDecisionRevertByKey MaskDecision = "revert-by-key"

	// MaskDecisionRefuse — the read mask is NEVER reverted; a write carrying it
	// is rejected and the caller resends the real value. This is the answer for
	// every field whose secret has no key to bind to (an argv slot has only its
	// index and its neighbours, and the caller supplies the whole vector *and*
	// `command` in the same request) and, by default, for every field nobody
	// has yet given a binding — which is what makes a new field fail CLOSED.
	MaskDecisionRefuse MaskDecision = "refuse"

	// MaskDecisionNotSecret — the field carries no credential, is never masked
	// on any read door, and so can never be echoed back as a mask. The residual
	// net still refuses one if it appears.
	MaskDecisionNotSecret MaskDecision = "not-secret"
)

// ServerFieldMaskDecisions records the decision for every field of a server as
// it appears on the wire, keyed by its DOTTED WIRE PATH. Both
// config.ServerConfig (the MCP door + the config file) and contracts.Server
// (the REST/SSE door) are covered; the two share most names.
//
// The keys are paths, not top-level field names, because round 7 finding 4
// found the guard itself failing open: it reflected over the TOP LEVEL only,
// and all three of that round's leaks — `isolation.extra_args` published in the
// clear on two doors and its mask accepted on a third — lived one level down,
// under a single `isolation` row that claimed in prose to cover them.
//
// TestServerFieldMaskDecisions_CoverEveryNestedLeaf walks BOTH structs
// recursively (into nested structs, slices and maps) and fails when any leaf
// that can carry TEXT — and therefore a credential — appears without a
// decision. Whether a leaf can carry text is derived from its Go type, not
// asserted here: a bool, an int or a time.Time structurally cannot hold a
// token, so it needs no row. Everything else does, which is what stops a new
// nested field inheriting "leaked by default" or "corrupted by default".
var ServerFieldMaskDecisions = map[string]MaskDecision{
	// --- masked on read, reverted on write, bound to a key ---
	"url":     MaskDecisionRevertByKey, // UnmaskURL + UnmaskLiveURLParams, bound to the parameter name and the stored scheme+host:port
	"env":     MaskDecisionRevertByKey, // UnmaskEnvValues / UnmaskLiveEnvValues, bound to the variable name
	"headers": MaskDecisionRevertByKey, // UnmaskHeaders / UnmaskLiveHeaders, bound to the header name
	"oauth":   MaskDecisionRevertByKey, // UnmaskLiveOAuth: client_secret/client_id/redirect_uri/extra_params bound to their own names; scopes and every future leaf REFUSED

	// --- masked on read, never reverted ---
	"args": MaskDecisionRefuse, // CheckArgvMaskEcho: an argv slot carries no key

	// --- not secret-bearing ---
	"id":                         MaskDecisionNotSecret,
	"name":                       MaskDecisionNotSecret,
	"protocol":                   MaskDecisionNotSecret,
	"command":                    MaskDecisionNotSecret,
	"working_dir":                MaskDecisionNotSecret,
	"enabled":                    MaskDecisionNotSecret,
	"quarantined":                MaskDecisionNotSecret,
	"skip_quarantine":            MaskDecisionNotSecret,
	"auto_approve_tool_changes":  MaskDecisionNotSecret,
	"trust_mode":                 MaskDecisionNotSecret,
	"shared":                     MaskDecisionNotSecret,
	"created":                    MaskDecisionNotSecret,
	"updated":                    MaskDecisionNotSecret,
	"reconnect_on_use":           MaskDecisionNotSecret,
	"expose_prompts":             MaskDecisionNotSecret,
	"launcher_wait_timeout":      MaskDecisionNotSecret,
	"health_check_interval":      MaskDecisionNotSecret,
	"tool_discovery_interval":    MaskDecisionNotSecret,
	"init_timeout":               MaskDecisionNotSecret,
	"max_concurrent_requests":    MaskDecisionNotSecret,
	"queue_size":                 MaskDecisionNotSecret,
	"queue_timeout":              MaskDecisionNotSecret,
	"toon_output":                MaskDecisionNotSecret,
	"enabled_tools":              MaskDecisionNotSecret,
	"disabled_tools":             MaskDecisionNotSecret,
	"source_registry_id":         MaskDecisionNotSecret,
	"source_registry_provenance": MaskDecisionNotSecret,

	// Docker/sandbox overrides, leaf by leaf. `extra_args` is free text an
	// operator can put a `-e API_KEY=…` into, and the rest are free-form
	// strings a credential can be pasted into just as easily; the read doors
	// mask them through the same leaf rule as any other config string, and
	// nothing binds a mask back to an element of a caller-replaced block, so an
	// echo is refused by the residual net.
	//
	// Round 7 finding 3: this row used to stand alone and was read as covering
	// the subtree, while `upstream_servers list` republished
	// `docker_isolation.server_isolation.extra_args` (and the whole global
	// `docker_status.isolation_config`) in the clear in the same payload.
	"isolation":               MaskDecisionRefuse,
	"isolation.mode":          MaskDecisionRefuse,
	"isolation.mode_override": MaskDecisionRefuse,
	"isolation.image":         MaskDecisionRefuse,
	"isolation.network_mode":  MaskDecisionRefuse,
	"isolation.extra_args":    MaskDecisionRefuse,
	"isolation.working_dir":   MaskDecisionRefuse,
	"isolation.memory_limit":  MaskDecisionRefuse,
	"isolation.cpu_limit":     MaskDecisionRefuse,
	"isolation.timeout":       MaskDecisionRefuse,
	"isolation.log_driver":    MaskDecisionRefuse,
	"isolation.log_max_size":  MaskDecisionRefuse,
	"isolation.log_max_files": MaskDecisionRefuse,

	// oauth, leaf by leaf. UnmaskLiveOAuth binds a revert to the field NAME for
	// the four leaves that have one; everything else — including any leaf added
	// later — is refused by the residual net that closes UnmaskLiveOAuth.
	"oauth.client_secret": MaskDecisionRevertByKey,
	"oauth.client_id":     MaskDecisionRevertByKey,
	"oauth.redirect_uri":  MaskDecisionRevertByKey,
	"oauth.extra_params":  MaskDecisionRevertByKey, // bound per parameter name
	"oauth.scopes":        MaskDecisionRefuse,      // a scope slot has no key, exactly as an argv slot has none
	// auth_url / token_url are DISCOVERED endpoints that exist only on the
	// contracts projection; nothing writes back through them, and a mask
	// arriving in one is refused rather than bound.
	"oauth.auth_url":  MaskDecisionRefuse,
	"oauth.token_url": MaskDecisionRefuse,

	// Server edition only (//go:build server); the personal edition carries an
	// empty stub. Brokered credentials are masked by the leaf rules on read and
	// have no key-bound revert, so an echo is refused.
	"auth_broker": MaskDecisionRefuse,

	// --- contracts.Server-only projections (read-only; never accepted on a write) ---
	"connected":         MaskDecisionNotSecret,
	"connecting":        MaskDecisionNotSecret,
	"status":            MaskDecisionNotSecret,
	"last_error":        MaskDecisionNotSecret, // scrubbed free-form text; nothing writes back through it
	"connected_at":      MaskDecisionNotSecret,
	"last_reconnect_at": MaskDecisionNotSecret,
	"reconnect_count":   MaskDecisionNotSecret,
	// GH #1145 parked-server state. `retry_stopped_code` is a stable MCPX_*
	// catalog code this proxy produced. `retry_stopped_reason` falls back to the
	// RAW upstream error when the code has no catalog message, so it is scrubbed
	// on read exactly like last_error; nothing writes back through either.
	"retry_stopped_code":   MaskDecisionNotSecret,
	"retry_stopped_reason": MaskDecisionNotSecret, // scrubbed free-form text
	"tool_count":           MaskDecisionNotSecret,
	// isolation_defaults is READ-ONLY output, but round 7 finding 4 re-judged
	// it: it is resolved from the operator-supplied GLOBAL docker_isolation
	// block, so a credential in the global `extra_args` lands in it verbatim.
	// It is masked on read like any other isolation block, and an echo is
	// refused — nothing binds a mask back into a derived projection.
	"isolation_defaults":              MaskDecisionRefuse,
	"isolation_defaults.runtime_type": MaskDecisionRefuse,
	"isolation_defaults.image":        MaskDecisionRefuse,
	"isolation_defaults.network_mode": MaskDecisionRefuse,
	"isolation_defaults.extra_args":   MaskDecisionRefuse,
	"isolation_defaults.working_dir":  MaskDecisionRefuse,

	// isolation_effective is derived state: an enum mode, the resolved global
	// mode and the name of the deciding rule. None of the three can carry an
	// operator-supplied value.
	"isolation_effective":             MaskDecisionNotSecret,
	"isolation_effective.mode":        MaskDecisionNotSecret,
	"isolation_effective.global_mode": MaskDecisionNotSecret,
	"isolation_effective.source":      MaskDecisionNotSecret,
	"authenticated":                   MaskDecisionNotSecret,
	"oauth_status":                    MaskDecisionNotSecret,
	"token_expires_at":                MaskDecisionNotSecret,
	"tool_list_token_size":            MaskDecisionNotSecret,
	"should_retry":                    MaskDecisionNotSecret,
	"retry_count":                     MaskDecisionNotSecret,
	"last_retry_time":                 MaskDecisionNotSecret,
	"user_logged_out":                 MaskDecisionNotSecret,
	"quarantine":                      MaskDecisionNotSecret,
	"error_code":                      MaskDecisionNotSecret,

	// Health: computed status plus operator-facing prose. `detail` is the only
	// leaf that echoes upstream text, and RedactServerSecretFields scrubs it;
	// nothing writes back through any of them.
	"health":             MaskDecisionNotSecret,
	"health.level":       MaskDecisionNotSecret,
	"health.admin_state": MaskDecisionNotSecret,
	"health.summary":     MaskDecisionNotSecret,
	"health.detail":      MaskDecisionNotSecret, // scrubbed free-form text
	"health.action":      MaskDecisionNotSecret,

	// Spec 044 diagnostic: a classified failure. `cause` echoes the raw connect
	// error and is scrubbed; the rest are codes and fixed prose. Read-only.
	"diagnostic":                       MaskDecisionNotSecret,
	"diagnostic.code":                  MaskDecisionNotSecret,
	"diagnostic.severity":              MaskDecisionNotSecret,
	"diagnostic.cause":                 MaskDecisionNotSecret, // scrubbed free-form text
	"diagnostic.user_message":          MaskDecisionNotSecret,
	"diagnostic.docs_url":              MaskDecisionNotSecret,
	"diagnostic.fix_steps[].type":      MaskDecisionNotSecret,
	"diagnostic.fix_steps[].label":     MaskDecisionNotSecret,
	"diagnostic.fix_steps[].command":   MaskDecisionNotSecret,
	"diagnostic.fix_steps[].url":       MaskDecisionNotSecret,
	"diagnostic.fix_steps[].fixer_key": MaskDecisionNotSecret,

	// Scan results: statuses and scanner identifiers this proxy produced.
	// Read-only; no operator value reaches them.
	"security_scan":                                MaskDecisionNotSecret,
	"security_scan.status":                         MaskDecisionNotSecret,
	"security_scan.deep_scan.skipped_scanners":     MaskDecisionNotSecret,
	"security_scan.deep_scan.scanners_failed[].id": MaskDecisionNotSecret,
	// `reason` is a scanner's own error text; it is produced by this proxy's
	// scanner layer, never round-tripped, and the residual net still refuses a
	// mask arriving in it.
	"security_scan.deep_scan.scanners_failed[].reason": MaskDecisionNotSecret,
}

func init() {
	// Edition-gated fields (//go:build server) are recorded in their own file
	// so the personal build does not carry decisions for paths that do not
	// exist in it — a stale row reads as a rule being enforced when it is not.
	for path, decision := range editionServerFieldMaskDecisions {
		ServerFieldMaskDecisions[path] = decision
	}
}
