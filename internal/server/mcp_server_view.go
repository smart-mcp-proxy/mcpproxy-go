package server

import (
	"encoding/json"

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
func scrubUpstreamText(s string) string {
	if s == "" {
		return s
	}
	return auditRedaction.capString(maskDetectedSecrets(oauth.RedactSensitiveData(s)))
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
		return scrubUpstreamText(responseText)
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
// same function the read path used, so the two cannot drift. Matching is by
// VALUE rather than by position, because `args_json` may legitimately reorder
// or resize the vector.
//
// Two deliberate refusals:
//   - a masked rendering produced by two DIFFERENT stored tokens is ambiguous;
//     reverting it would be a guess, so it is left as the client sent it;
//   - a token whose masked rendering equals itself (an ordinary argument the
//     read path did not touch) is not a mask at all and is never reverted.
func unmaskArgv(incoming, stored []string) []string {
	if incoming == nil || len(stored) == 0 {
		return incoming
	}

	maskedStored := redactedArgs(stored, liveRedaction)
	revert := make(map[string]string, len(stored))
	ambiguous := make(map[string]bool, len(stored))
	for i, masked := range maskedStored {
		if masked == stored[i] {
			continue // not a mask
		}
		if existing, seen := revert[masked]; seen && existing != stored[i] {
			ambiguous[masked] = true
			continue
		}
		revert[masked] = stored[i]
	}

	out := make([]string, len(incoming))
	for i, token := range incoming {
		if original, ok := revert[token]; ok && !ambiguous[token] {
			out[i] = original
			continue
		}
		out[i] = token
	}
	return out
}
