package server

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

// Issue #1148: `quarantine_security list_quarantined` json.Marshal'd the raw
// []*config.ServerConfig straight out of storage, so every credential the
// struct carries — env values, header values, oauth.client_secret, URL query
// credentials and argv tokens — travelled out of the MCP surface in the clear,
// to any caller, and was then recorded verbatim into the activity store.
//
// The durable fix is not "mask the five fields the report named". It is to stop
// MCP-facing payloads being built from the config struct at all, and build them
// from a REDACTED VIEW instead, so the NEXT field added to ServerConfig is
// masked by default rather than leaked by default.
//
// The view is deliberately NOT a hand-written key list. An allowlist is exactly
// what rotted into #1146 (the activity record that dropped every payload field)
// and what makes this class of bug recur: it encodes today's field set and
// silently omits tomorrow's. Instead the config is marshalled through its own
// MarshalJSON and the resulting generic map is walked by the shared redaction
// walker from mcp_activity_args.go, whose rules are keyed on field NAMES and,
// for every leaf that survives that, on the VALUE's own shape.
//
// WARNING: the view must stay a map. config.ServerConfig declares
// MarshalJSON/UnmarshalJSON, and Go promotes those to any struct that embeds
// it — an embedding wrapper silently drops its own fields on encode (see the
// warning on config.ServerConfig.UnmarshalJSON).

// redactedServerView renders one server config as a generic JSON map with every
// secret-bearing leaf masked under the given policy.
//
// Pass liveRedaction for an interactive MCP read surface (keeps the
// `••••<last2> (<N> chars)` rendering the patch-path unmaskers recognise) and
// auditRedaction for anything persisted. Returns nil for a nil config.
func redactedServerView(sc *config.ServerConfig, r redactionPolicy) map[string]interface{} {
	if sc == nil {
		return nil
	}

	normalized, ok := normalizeForRedaction(sc).(map[string]interface{})
	if !ok {
		// ServerConfig always marshals to an object; a failure here can only
		// mean the encoder broke. Fail CLOSED — an empty view loses
		// information, a raw struct loses secrets.
		return map[string]interface{}{"name": sc.Name}
	}

	redacted, ok := redactValueWith("", normalized, r).(map[string]interface{})
	if !ok {
		return map[string]interface{}{"name": sc.Name}
	}
	return redacted
}

// redactedServerViews renders a slice of server configs, skipping nils.
func redactedServerViews(servers []*config.ServerConfig, r redactionPolicy) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(servers))
	for _, sc := range servers {
		if sc == nil {
			continue
		}
		out = append(out, redactedServerView(sc, r))
	}
	return out
}

// scrubUpstreamText scrubs free-form text that originated OUTSIDE mcpproxy's
// own structured fields — an upstream connection error, a line tailed from a
// server log, a child process's stdout.
//
// Issue #1148 (c2/c3): these strings routinely embed the upstream URL with its
// query credentials (`?token=…`), and a child MCP server is free to print its
// own API key. There is no enclosing key to judge them by, so both scrubbers
// run: oauth.RedactSensitiveData for `<param>=<value>` shapes and bearer
// tokens, then the value-shaped detector for vendor-formatted credentials.
//
// mcp.go's `upstream_servers list` already did exactly this for `last_error`;
// routing every such site through one helper is what stops the four call sites
// drifting apart again.
//
// It does NOT truncate. Round 2 finding 3: this used to apply the 512-byte
// ACTIVITY-STORE cap, which is a property of a persisted row and not of a live
// read — so every `tail_log` line, `connection_status.last_error` and
// `connection_message` was silently cut at 512 bytes. tail_log is the primary
// debugging surface and a long line is precisely what an operator opens it for.
// The cap now lives on scrubUpstreamTextForAudit, where it belongs.
func scrubUpstreamText(s string) string {
	if s == "" {
		return s
	}
	return maskDetectedSecrets(oauth.RedactSensitiveData(s))
}

// scrubUpstreamTextForAudit is scrubUpstreamText for a string that will be
// PERSISTED — an activity row's error message or a non-JSON response body. It
// adds the activity store's size cap so one enormous upstream error cannot
// bloat BBolt, which is the only thing the two paths need to differ on.
func scrubUpstreamTextForAudit(s string) string {
	return auditRedaction.capString(scrubUpstreamText(s))
}

// scrubbedConnectionStatus returns a copy of a managed client's
// GetConnectionStatus() map with every string leaf scrubbed.
//
// Round 2 finding 2: `inspect_quarantined` — the sibling operation of the
// `list_quarantined` path #1148 fixed, one screen away in the same file —
// rendered this map straight into its response, and the map carries
// `last_error`, which routinely echoes the upstream URL with its query
// credentials. The copy matters: the map belongs to the caller's diagnostic
// path, which must keep seeing the real error.
func scrubbedConnectionStatus(status map[string]interface{}) map[string]interface{} {
	if status == nil {
		return nil
	}
	out := make(map[string]interface{}, len(status))
	for k, v := range status {
		if s, ok := v.(string); ok {
			out[k] = scrubUpstreamText(s)
			continue
		}
		out[k] = v
	}
	return out
}

// scrubUpstreamLines scrubs a batch of log lines.
func scrubUpstreamLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = scrubUpstreamText(line)
	}
	return out
}

// redactBuiltinResponseForActivity renders the response text of a management
// built-in for the ACTIVITY STORE.
//
// The activity store is a different durability class from the live tool
// response: rows are persisted in BBolt, streamed over SSE, exported by
// `mcpproxy activity list` and pasted into bug reports. So even a response that
// is already masked for the caller is re-rendered here under the audit policy,
// which carries neither the secret's length nor its trailing bytes.
//
// One net at the emit site covers every current AND future operation of the
// tool — including the ones whose payloads this change does not touch
// individually — which is the property the per-handler fixes cannot give.
// Non-JSON responses (plain-text results, error strings) fall back to the
// free-form scrubber.
func redactBuiltinResponseForActivity(responseText string) interface{} {
	if responseText == "" {
		return responseText
	}
	var parsed interface{}
	if err := json.Unmarshal([]byte(responseText), &parsed); err != nil || parsed == nil {
		return scrubUpstreamTextForAudit(responseText)
	}
	return redactValueWith("", parsed, auditRedaction)
}

// viewField reads one key out of a redacted view, falling back to the raw
// config value when the key is absent.
//
// Every string/slice/map field of config.ServerConfig is tagged `omitempty`, so
// an empty command or a nil args slice simply does not appear in the view. The
// fallback keeps hand-written projections (upstream_servers list) rendering
// `""` and `null` exactly as they did before the view existed — an empty value
// carries no secret, so nothing is lost by passing it through.
func viewField(view map[string]interface{}, key string, fallback interface{}) interface{} {
	if v, ok := view[key]; ok {
		return v
	}
	return fallback
}

// viewString reads a string leaf out of a redacted view, falling back to the
// raw value when the key was omitted (empty) or is not a string.
func viewString(view map[string]interface{}, key, fallback string) string {
	if v, ok := view[key].(string); ok {
		return v
	}
	return fallback
}

// redactedArgs masks a command-line argument vector under the given policy.
// Returns nil for nil so JSON callers keep emitting `null`.
func redactedArgs(args []string, r redactionPolicy) []string {
	if args == nil {
		return nil
	}
	items := make([]interface{}, len(args))
	for i, a := range args {
		items[i] = a
	}
	masked := redactArgvWith(items, r)
	out := make([]string, len(masked))
	for i, v := range masked {
		s, ok := v.(string)
		if !ok {
			// redactArgvWith returns a string for every string input, which is
			// all this helper ever supplies. Fail closed anyway.
			return redactedArgsFallback(args, r)
		}
		out[i] = s
	}
	return out
}

// redactedArgsFallback masks every element wholesale. Unreachable in practice;
// it exists so redactedArgs can never fall back to the RAW vector.
func redactedArgsFallback(args []string, r redactionPolicy) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = r.mask(a)
	}
	return out
}

// unmaskArgv reverts argv tokens that a client echoed back still masked
// (issue #1148).
//
// `args_json` REPLACES the vector entirely and internal/oauth owns no unmask
// contract for argv, so masking argv on the read path without this would let a
// read-modify-write client persist the mask over the real credential — the
// exact failure mode oauth.UnmaskEnvValues / UnmaskHeaders exist to prevent for
// env and headers.
//
// The mapping is built from the STORED vector through redactedArgs, i.e. the
// same function the read path used, so the two cannot drift.
//
// The revert is BOUND to the slot the mask came from — round 2 finding 1. The
// first cut matched by VALUE anywhere in the vector, which made this far worse
// than the disclosure it was added to prevent:
//
//   - RELOCATION. `args_json` replaces the whole vector and the caller also
//     controls `command`, so `{"command":"sh","args_json":"[\"-c\",\"<mask>\"]"}`
//     had mcpproxy substitute the real credential into an attacker-chosen
//     command line.
//   - RE-DISCLOSURE, which is the sharper one. A credential masked because of
//     the FLAG in front of it (`--api-key <secret>`) has no credential shape of
//     its own. Move it into a bare positional slot and the read path no longer
//     recognises it, so the very next `upstream_servers list` hands it back in
//     the clear — a masked-read → relocate-write → read-back chain that undoes
//     all of #1148, over an endpoint that is unauthenticated by default.
//
// So a stored token is restored ONLY to the index it was masked at, and only
// when the token that BOUND the mask is unchanged:
//
//   - a value masked because of the preceding flag requires that same flag
//     still to precede it (`--api-key` cannot become `--exfil-to`);
//   - an inline `--flag=<mask>` carries its own flag inside the token, so token
//     equality already binds it;
//   - a detector-recognised token (`ghp_…`) is masked by its own shape, so it
//     is still masked wherever it ends up and re-disclosure is impossible.
//
// This is the same shape as the safeguards the sibling contracts already carry:
// oauth.UnmaskEnvValues / UnmaskHeaders bind to the map KEY, and UnmaskURL
// refuses to move a stored secret onto a different scheme/host. A vector that
// was reordered or resized simply does not round-trip its masks; the caller
// resends the real values, which is the safe direction.
func unmaskArgv(incoming, stored []string) []string {
	if incoming == nil || len(stored) == 0 {
		return incoming
	}

	maskedStored := redactedArgs(stored, liveRedaction)
	flagBound := argvFlagBoundMasks(stored)

	out := make([]string, len(incoming))
	for i, token := range incoming {
		out[i] = token
		if i >= len(stored) || maskedStored[i] == stored[i] || token != maskedStored[i] {
			// Out of range, not a mask at all, or a genuine edit.
			continue
		}
		// argvFlagBoundMasks never marks index 0, so i >= 1 here.
		if flagBound[i] && incoming[i-1] != stored[i-1] {
			// The flag that made this value a secret is gone or different;
			// restoring here would move the credential to a flag the caller
			// chose. Leave the mask literal — the write then carries a value
			// that is obviously not a credential rather than the real one.
			continue
		}
		out[i] = stored[i]
	}
	return out
}

// argvFlagBoundMasks reports, per index of the stored vector, whether that
// token would be masked BECAUSE OF the flag in front of it rather than because
// of its own shape. Mirrors the `maskNext` rule in redactArgvWith.
//
// It deliberately over-reports rather than under-reports: after a masked value
// is consumed, redactArgvWith resets the pairing, so a run of sensitive-looking
// flags marks one index here that the masker did not pair. An extra index only
// makes unmaskArgv stricter (it also demands the preceding token be unchanged),
// which is the safe direction; a missed index would drop a binding.
func argvFlagBoundMasks(stored []string) []bool {
	bound := make([]bool, len(stored))
	for i := 1; i < len(stored); i++ {
		prev := stored[i-1]
		bound[i] = isArgvFlag(prev) && oauth.IsSensitiveKeyName(argvFlagKey(prev))
	}
	return bound
}

// unmaskLiveEnvValues reverts env values a client echoed back exactly as the
// LIVE VIEW rendered them, before oauth.UnmaskEnvValues gets the map.
//
// Round 2 finding 7 let the live policy's value-shaped detector fire on a leaf
// the name rule left untouched (`env: {BENIGN: ghp_…}`), which is a rendering
// oauth.UnmaskEnvValues cannot recognise — it compares against oauth.
// maskedEnvValue. Without this mirror, masking that leaf would trade a
// disclosure for the read-modify-write corruption of #1142/#1146.
//
// Binding is by KEY, exactly as oauth's own unmaskers bind, so a value can only
// ever be restored to the variable it was read from.
func unmaskLiveEnvValues(incoming, stored map[string]string) map[string]string {
	return unmaskLiveMap(incoming, stored, func(k, v string) string {
		return redactEnvValueWith(k, v, liveRedaction)
	})
}

// unmaskLiveHeaders is unmaskLiveEnvValues for header maps.
func unmaskLiveHeaders(incoming, stored map[string]string) map[string]string {
	return unmaskLiveMap(incoming, stored, func(k, v string) string {
		return redactHeaderValueWith(k, v, liveRedaction)
	})
}

func unmaskLiveMap(incoming, stored map[string]string, rendered func(k, v string) string) map[string]string {
	if incoming == nil || len(stored) == 0 {
		return incoming
	}
	out := make(map[string]string, len(incoming))
	for k, v := range incoming {
		if sv, ok := stored[k]; ok && v == rendered(k, sv) {
			out[k] = sv
			continue
		}
		out[k] = v
	}
	return out
}

// unmaskLiveURL reverts a URL echoed back exactly as the live view rendered it.
// A genuinely edited URL falls through to oauth.UnmaskURL, which restores the
// individual masked components under its own authority safeguard.
func unmaskLiveURL(incoming, stored string) string {
	if incoming == "" || stored == "" {
		return incoming
	}
	if incoming == redactStringWith("url", stored, liveRedaction) {
		return stored
	}
	return oauth.UnmaskURL(incoming, stored)
}

// unmaskLiveOAuth reverts a masked oauth.client_secret echoed back by a client.
//
// Round 2 finding 6: #1148 started masking client_secret on the MCP read
// surface (`quarantine_security list_quarantined` renders the WHOLE config)
// without giving the write path anything to recognise the mask by, so a
// read-modify-write through `oauth_json` — which REPLACES the oauth block —
// persisted `••••42 (17 chars)` as the client secret. Read and write now agree
// on the same rendering, bound to the field it came from.
//
// The REST API is not affected in either direction: contracts.OAuthConfig
// carries no client_secret, so no REST response ever renders one.
func unmaskLiveOAuth(incoming, stored *config.OAuthConfig) {
	if incoming == nil || stored == nil || stored.ClientSecret == "" {
		return
	}
	if incoming.ClientSecret == redactStringWith("client_secret", stored.ClientSecret, liveRedaction) {
		incoming.ClientSecret = stored.ClientSecret
	}
}

// inspectConnectionTimeoutError renders `inspect_quarantined`'s
// connection-timeout diagnostic (issue #1148, round 2 finding 2).
//
// The status map is scrubbed rather than dropped: which state the client is in,
// how many retries it has burned and what the (redacted) error was is the whole
// value of this message to an operator deciding whether a quarantined server is
// merely broken or actively hostile.
func inspectConnectionTimeoutError(serverName string, timeout time.Duration, status map[string]interface{}) string {
	return fmt.Sprintf(
		"Quarantined server '%s' failed to connect within %v timeout. Connection status: %v. "+
			"This may indicate the server process is not running, there's a network issue, "+
			"or the server is unstable (see issue #105).",
		serverName, timeout, scrubbedConnectionStatus(status))
}

// inspectConnectionFailedAnalysis renders `inspect_quarantined`'s
// tool-retrieval-failed payload with the connection status and the raw upstream
// error scrubbed (issue #1148, round 2 finding 2).
func inspectConnectionFailedAnalysis(serverName string, status map[string]interface{}, err error) []map[string]interface{} {
	info := scrubbedConnectionStatus(status)
	if info == nil {
		info = map[string]interface{}{}
	}
	detail := ""
	if err != nil {
		detail = scrubUpstreamText(err.Error())
	}
	info["connection_error"] = detail

	return []map[string]interface{}{{
		"server_name": serverName,
		"status":      "QUARANTINED_CONNECTION_FAILED",
		"message": fmt.Sprintf(
			"Server '%s' is quarantined and connection failed during tool retrieval. "+
				"This may indicate the server process crashed or disconnected.", serverName),
		"connection_info": info,
		"error_details":   detail,
		"next_steps":      "The server connection failed. Check server process status, logs, and configuration. Server may need to be restarted.",
		"security_note":   "Connection failure prevents tool analysis. Server must be stable and connected for security inspection.",
	}}
}
