package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	return auditRedaction.CapString(scrubUpstreamText(s))
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
// Returns nil for nil so JSON callers keep emitting `null`. The rules live in
// oauth.Redaction.Argv so the MCP and REST doors share one implementation.
func redactedArgs(args []string, r redactionPolicy) []string {
	masked := r.Argv(args)
	for i, m := range masked {
		masked[i] = r.CapString(m)
	}
	return masked
}

// Issue #1148, round 3: argv masks are NEVER reverted. They are REFUSED.
//
// The write path used to restore a masked argv token from the stored vector
// (`unmaskArgv`), first by VALUE (round 2 finding 1) and then bound to the
// index plus the preceding flag (round 3 finding 1). Both leaked, and the
// second one leaked for a reason no tightening of the argv-internal context can
// remove:
//
//   - An argv token has NO KEY. env values bind to the variable name, headers
//     to the header name, a URL secret to its scheme+host — each is a piece of
//     context the caller cannot restate without also restating what the secret
//     is FOR. An argv slot has only its index and its neighbours.
//   - The caller supplies the WHOLE vector *and* `command` in the same patch.
//     So every candidate binding is caller-controlled: an index is chosen, a
//     preceding flag is copied verbatim, and even a byte-identical argv means
//     nothing once `command` moves from `mcp-foo` to `curl`. There is no
//     surrounding context that proves the slot still means what it meant when
//     the value was masked.
//   - The index-plus-flag binding also missed every mask the VALUE-shaped
//     detector produced (`ghp_…****`) and every mask at index 0, because
//     nothing but the index bound those at all:
//         stored   = ["mcp-foo", "run", "ghp_…"]
//         incoming = ["--silent", "--data", "ghp_…****"]
//     restored the live token into an attacker-chosen command line.
//
// So the revert is gone. A client that echoes a mask back is told to resend the
// real value instead, which is the one answer that is safe in both directions:
// the secret never moves (no relocation), and the mask is never written over
// the credential either (no read-modify-write corruption — the #1142/#1146
// failure the revert was added to prevent).
//
// Refusing rather than silently keeping the stored vector is deliberate:
// `args_json` REPLACES the vector, so silently ignoring it would make the write
// look applied when it was not.

// containsArgvMaskMarker / checkArgvMaskEcho are thin bindings onto the shared
// implementation in internal/oauth (oauth.MaskMarkers,
// oauth.ContainsMaskMarker, oauth.CheckArgvMaskEcho), which is where they have
// to live: `GET /api/v1/servers` masks argv too, and internal/httpapi cannot
// import this package (issue #1148, round 4 finding 3).
func containsArgvMaskMarker(token string) bool {
	return oauth.ContainsMaskMarker(token)
}

// checkArgvMaskEcho reports an error when an incoming argv vector still carries
// a mask this proxy rendered. `args_json` is the MCP surface's spelling of the
// parameter; REST passes `args`.
func checkArgvMaskEcho(incoming, stored []string) error {
	return oauth.CheckArgvMaskEcho("args_json", incoming, stored)
}

// The unmaskLive* helpers below are thin bindings onto the shared write rules
// in internal/oauth (serverwrite.go), which is where they have to live: REST
// `PATCH /api/v1/servers/{name}` needs exactly the same reverts, and
// internal/httpapi cannot import this package (issue #1148, rounds 4 and 6).

// unmaskLiveEnvValues reverts env values a client echoed back exactly as the
// LIVE VIEW rendered them, before oauth.UnmaskEnvValues gets the map.
func unmaskLiveEnvValues(incoming, stored map[string]string) map[string]string {
	return oauth.UnmaskLiveEnvValues(incoming, stored)
}

// unmaskLiveHeaders is unmaskLiveEnvValues for header maps.
func unmaskLiveHeaders(incoming, stored map[string]string) map[string]string {
	return oauth.UnmaskLiveHeaders(incoming, stored)
}

// unmaskLiveURL reverts a masked URL on the write path, and REFUSES the write
// when a mask it cannot bind survives.
func unmaskLiveURL(incoming, stored string) (string, error) {
	return oauth.UnmaskLiveURL(incoming, stored)
}

// unmaskLiveOAuth reverts masked oauth fields echoed back by a client, and
// REFUSES the write when a mask it cannot bind survives. `oauth_json` is the
// MCP surface's spelling of the parameter.
func unmaskLiveOAuth(incoming, stored *config.OAuthConfig) error {
	if err := oauth.UnmaskLiveOAuth(incoming, stored); err != nil {
		return errors.New("oauth_json" + strings.TrimPrefix(err.Error(), "oauth"))
	}
	return nil
}

// checkServerWriteMasks is the fail-closed residual net every write door runs
// after its key-bound reverts: anything still carrying a mask this proxy
// rendered is refused rather than persisted over a live credential. It is what
// makes a field ADDED to config.ServerConfig later fail closed.
func checkServerWriteMasks(what string, v interface{}) error {
	return oauth.CheckServerWriteMasks(what, v)
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

// isolationValues renders one server's per-server isolation override as a
// generic map with every secret-bearing leaf masked.
//
// Issue #1148, round 7 finding 3. `upstream_servers list` built its
// `docker_isolation.server_isolation` block by reading the config struct field
// by field, so `extra_args` — free text an operator puts `-e API_KEY=<token>`
// into — was republished verbatim in the SAME response that masked env, url and
// args. Sourcing the values from the shared walk instead means a field added to
// config.IsolationConfig is masked because the walk reaches it.
//
// `reveal` is the caller's authenticated `reveal_secret_headers` opt-in; it is
// honoured here exactly as it is for env/headers/url, so an operator who asked
// for raw values still gets them and nobody else does.
//
// A walk that cannot round-trip fails CLOSED: an empty map publishes nothing.
func isolationValues(iso *config.IsolationConfig, reveal bool) map[string]interface{} {
	if iso == nil {
		return map[string]interface{}{}
	}
	normalized, ok := normalizeForRedaction(iso).(map[string]interface{})
	if !ok {
		return map[string]interface{}{}
	}
	if reveal {
		return normalized
	}
	redacted, ok := redactValueWith("isolation", normalized, liveRedaction).(map[string]interface{})
	if !ok {
		return map[string]interface{}{}
	}
	return redacted
}

// globalIsolationView renders the GLOBAL docker_isolation block for the
// `docker_status` section of `upstream_servers list`, masked under the same
// rule and the same reveal gate.
//
// It used to be the raw *config.DockerIsolationConfig, so the global
// `extra_args` — one place an operator configures a registry credential for
// every isolated server at once — travelled out of this surface in the clear.
// Returns nil for nil so the payload keeps emitting `null` as before.
func globalIsolationView(global *config.DockerIsolationConfig, reveal bool) interface{} {
	if global == nil {
		return nil
	}
	normalized := normalizeForRedaction(global)
	if reveal {
		return normalized
	}
	return redactValueWith("docker_isolation", normalized, liveRedaction)
}
