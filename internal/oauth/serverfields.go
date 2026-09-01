package oauth

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// RedactServerSecretFields masks the secret-bearing fields of one
// contracts.Server in place, using the LIVE rendering
// (`••••<last2> (<N> chars)`; ${keyring:…} / ${env:…} references pass through).
//
// Issue #1148, round 4 finding 3. There are THREE doors that serve this struct
// — `GET /api/v1/servers` and its single-server children, the `/events` SSE
// `servers.changed` payload, and the tray/Web UI reading either — and until now
// the REST handler (internal/httpapi) and the event bus (internal/runtime) each
// carried their OWN copy of the field list. Both copies covered headers, env,
// the URL and the error strings; neither covered `args` or
// `oauth.extra_params`, so a credential passed as `--api-key sk-…` and a signed
// RFC 8707 resource indicator went out in the clear on both.
//
// Parity between those doors is load-bearing beyond the leak: the Web UI's
// mergeServers treats each payload as authoritative, so a masked-vs-plaintext
// mismatch between the list response and the SSE delivery would flicker on
// every event. One function is what makes the parity structural instead of two
// lists that have to be remembered together.
//
// Callers apply the `reveal_secret_headers` opt-out themselves; by the time a
// server reaches here it is being redacted.
//
// The write-path contract per masked field:
//
//   - headers / env — reverted from an echoed mask, bound to the map key
//     (UnmaskHeaders / UnmaskEnvValues).
//   - url — reverted per query parameter, bound to the stored scheme and
//     host:port (UnmaskURL / UnmaskLiveURLParams).
//   - args — REFUSED, never reverted: an argv slot carries no key to bind a
//     stored secret to, and the `args` field replaces the whole vector. See
//     CheckArgvMaskEcho, which POST and PATCH /api/v1/servers both call.
//   - oauth — read-only over REST (AddServerRequest carries no oauth field),
//     so no REST write can echo this mask back.
//   - last_error / health.detail / diagnostic.cause — free-form text nothing
//     writes back through.
func RedactServerSecretFields(server *contracts.Server) {
	if server == nil {
		return
	}
	if len(server.Headers) > 0 {
		server.Headers = RedactStringHeaders(server.Headers)
	}
	if len(server.Env) > 0 {
		server.Env = RedactEnvValues(server.Env)
	}
	if server.URL != "" {
		server.URL = RedactURLQueryParams(server.URL)
	}
	if len(server.Args) > 0 {
		server.Args = LiveRedaction.Argv(server.Args)
	}
	if server.OAuth != nil && len(server.OAuth.ExtraParams) > 0 {
		params := make(map[string]string, len(server.OAuth.ExtraParams))
		for k, v := range server.OAuth.ExtraParams {
			params[k] = LiveRedaction.Leaf(k, v)
		}
		// Copy the struct rather than writing through the pointer: the
		// contracts.Server may share its OAuth block with the caller's config.
		masked := *server.OAuth
		masked.ExtraParams = params
		server.OAuth = &masked
	}
	if server.LastError != "" {
		server.LastError = RedactSensitiveData(server.LastError)
	}
	if server.Health != nil && server.Health.Detail != "" {
		server.Health.Detail = RedactSensitiveData(server.Health.Detail)
	}
	// Spec 044 diagnostic — its Cause echoes the raw connect error, which
	// carries the full upstream URL (query secrets and all); scrub it in parity
	// with LastError / Health.Detail.
	if server.Diagnostic != nil && server.Diagnostic.Cause != "" {
		server.Diagnostic.Cause = RedactSensitiveData(server.Diagnostic.Cause)
	}
}
