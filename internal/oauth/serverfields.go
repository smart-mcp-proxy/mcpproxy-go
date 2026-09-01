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
	if server.LastError != "" {
		server.LastError = MaskDetectedSecrets(RedactSensitiveData(server.LastError))
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
// it appears on the wire, keyed by JSON field name. Both config.ServerConfig
// (the MCP door + the config file) and contracts.Server (the REST/SSE door) are
// covered; the two share most names.
//
// TestServerFieldMaskDecisions_CoverEveryServerField reflects over BOTH structs
// and fails when a field appears here without a decision — so adding a field to
// either struct forces the author to answer the question rather than inheriting
// "leaked by default" or "corrupted by default".
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

	// Docker/sandbox overrides. `extra_args` is free text an operator can put a
	// `-e API_KEY=…` into, so the read doors mask it through the same leaf rule
	// as any other config string; nothing binds a mask back to an element of a
	// caller-replaced slice, so an echo is refused by the residual net.
	"isolation": MaskDecisionRefuse,

	// Server edition only (//go:build server); the personal edition carries an
	// empty stub. Brokered credentials are masked by the leaf rules on read and
	// have no key-bound revert, so an echo is refused.
	"auth_broker": MaskDecisionRefuse,

	// --- contracts.Server-only projections (read-only; never accepted on a write) ---
	"connected":            MaskDecisionNotSecret,
	"connecting":           MaskDecisionNotSecret,
	"status":               MaskDecisionNotSecret,
	"last_error":           MaskDecisionNotSecret, // scrubbed free-form text; nothing writes back through it
	"connected_at":         MaskDecisionNotSecret,
	"last_reconnect_at":    MaskDecisionNotSecret,
	"reconnect_count":      MaskDecisionNotSecret,
	"tool_count":           MaskDecisionNotSecret,
	"isolation_defaults":   MaskDecisionNotSecret,
	"isolation_effective":  MaskDecisionNotSecret,
	"authenticated":        MaskDecisionNotSecret,
	"oauth_status":         MaskDecisionNotSecret,
	"token_expires_at":     MaskDecisionNotSecret,
	"tool_list_token_size": MaskDecisionNotSecret,
	"should_retry":         MaskDecisionNotSecret,
	"retry_count":          MaskDecisionNotSecret,
	"last_retry_time":      MaskDecisionNotSecret,
	"user_logged_out":      MaskDecisionNotSecret,
	"health":               MaskDecisionNotSecret, // health.detail is scrubbed free-form text
	"quarantine":           MaskDecisionNotSecret,
	"security_scan":        MaskDecisionNotSecret,
	"diagnostic":           MaskDecisionNotSecret, // diagnostic.cause is scrubbed free-form text
	"error_code":           MaskDecisionNotSecret,
}
