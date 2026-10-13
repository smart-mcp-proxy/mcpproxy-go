package server

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
)

// Issue #1146: the activity records for the two MUTATING built-in tools
// (upstream_servers, quarantine_security) used to carry only {operation, name}.
// Every payload field — the env vars added, the URL changed, the isolation
// image swapped — was dropped, so the audit trail could say that something was
// patched but never what. This file records the FULL argument set instead, with
// redaction, plus the resolved before/after config diff.
//
// Redaction policy, and why it is unconditional:
//
//	The activity store is a different durability class from the live
//	upstream_servers `list` projection. Rows are persisted in BBolt, exported by
//	`mcpproxy activity list`, and broadcast over SSE to every Web UI viewer, so
//	`config.RevealSecretHeaders` — an opt-in flag for a live read surface — is
//	deliberately NOT honoured here. Secrets are always masked.
//
//	The goal is redacted-but-PRESENT: the operator must see THAT `API_KEY`
//	changed, and which FIELD it was, without the value ever landing in the
//	store. ${keyring:…} / ${env:…} references pass through unchanged because
//	those are labels, not secrets.
//
// Why this file masks with oauth.AuditMaskValue and not oauth.MaskValue
// (issue #1146, review round 3):
//
//	MaskValue renders `••••<last2> (<N> chars)`. On the live read surfaces —
//	the tray, the Web UI server form — that is a deliberate trade: it lets an
//	operator recognise WHICH token is configured, and the write path
//	(oauth.UnmaskHeaders / UnmaskEnvValues) recognises the exact rendering when
//	a client echoes it back, so an edit of a neighbouring field cannot overwrite
//	the real secret with its own mask. Those surfaces keep it.
//
//	The activity store has neither property and a much longer life. Its rows
//	are persisted in BBolt, streamed over SSE, exported by `mcpproxy activity
//	list` and pasted into bug reports; nothing ever writes back through them.
//	There the exact byte length and the trailing two bytes are not an
//	affordance, they are a durable fingerprint — a handle for correlating one
//	credential across records, and a materially smaller search space for a
//	low-entropy one. So the audit surfaces use a fixed marker that carries
//	neither.
//
//	The value-shaped detector (security.MaskText) keeps its own four-rune
//	prefix — `AKIA…****`, `ghp_…****`. That prefix is the credential TYPE, not
//	its content: it is what tells the operator which vendor's key to rotate,
//	and it carries no length. It is deliberately left as is.
//
// The whole request is captured rather than an allowlist of known field names:
// an allowlist is exactly what rotted into this bug, and a future tool
// parameter must be logged by default rather than silently dropped.
const (
	// activityArgValueLimit caps each recorded string leaf so one mutation
	// carrying a large env map or arg list cannot bloat the activity store.
	// Mirrors activityErrorMessageLimit.
	activityArgValueLimit = 512

	// jsonParamSuffix marks the tool parameters whose value is a JSON *string*
	// (env_json, headers_json, isolation_json, oauth_json, args_json). They are
	// parsed before redaction so the record shows WHICH keys changed instead of
	// an opaque blob.
	jsonParamSuffix = "_json"

	// activityJSONNullMarker is how the literal `null` removal marker is
	// recorded once it has been parsed.
	activityJSONNullMarker = "null"

	// activityUnparsableJSONFormat is the fail-closed placeholder for a
	// `*_json` argument that does not parse. It carries the byte count so the
	// row still says something happened, and none of the bytes.
	activityUnparsableJSONFormat = "<unparsable json omitted: %d bytes>"
)

// activityArgsFromRequest builds the redacted activity-argument map from the
// raw MCP request. Returns nil for an empty request so callers keep emitting
// `null` arguments exactly as before.
func activityArgsFromRequest(request mcp.CallToolRequest) map[string]interface{} {
	// Drop MCPProxy's own `_auth_*` plumbing BEFORE anything else. Neither of
	// the two handlers this file serves calls injectAuthMetadata, so a key
	// under that prefix here can only have been sent by the CALLER — and
	// internal/runtime copies `_auth_user_id` / `_auth_user_email` straight
	// onto ActivityRecord.UserID/UserEmail. Capturing the whole request would
	// therefore hand an agent a forged identity stamp on the audit row for a
	// privileged mutation. The old {operation,name} allowlist dropped these by
	// accident; dropping them on purpose is what keeps "record everything"
	// safe.
	raw := security.StripInternalArgs(request.GetArguments())
	if len(raw) == 0 {
		return nil
	}

	args := make(map[string]interface{}, len(raw))
	for key, value := range raw {
		// The *_json parameters carry a serialized object; parse it so the
		// walker can mask per key.
		if s, ok := value.(string); ok && strings.HasSuffix(key, jsonParamSuffix) {
			args[key] = redactActivityJSONParam(strings.TrimSuffix(key, jsonParamSuffix), s)
			continue
		}
		args[key] = redactActivityValue(key, value)
	}
	return args
}

// redactActivityJSONParam records one `*_json` tool parameter.
//
// Parsing is what makes per-key masking possible, so a payload that does not
// parse fails CLOSED (issue #1146, review round 3). The previous fallback —
// heuristic string scrubbing of the raw text — is regex-shaped and keyed on
// `<name>=<value>` / `<name>: <value>`, so a payload that is merely malformed
// JSON slips straight past it: `{"API_KEY" "sk-live-…"}` has no separator to
// match and was stored verbatim. The mutation is rejected by the handler in
// that case, but the activity row outlives the rejection.
//
// The row still records THAT the parameter was sent, and its size, because a
// rejected mutation is audit-relevant; only the bytes we could not understand
// are withheld.
func redactActivityJSONParam(key, raw string) interface{} {
	var parsed interface{}
	err := json.Unmarshal([]byte(raw), &parsed)
	switch {
	case err == nil && parsed != nil:
		return redactActivityValue(key, parsed)
	case err == nil:
		// The literal `null` removal marker: valid JSON, carries nothing, and
		// its presence is the audit signal ("this field was cleared").
		return activityJSONNullMarker
	default:
		return fmt.Sprintf(activityUnparsableJSONFormat, len(raw))
	}
}

// activityAttributedOps are the operations of the two management built-ins that
// are handed the request — the only ones that can have acted on the caller's
// `name`. An operation absent from this set (an inventory read, an unknown
// string, a request that never named an operation at all) dispatched nothing
// against `name`, so its activity row stays unattributed rather than letting an
// agent stamp activity onto a server it never touched.
//
// The set is an ALLOWLIST so an unknown operation defaults to safe, and
// TestActivityAttributedOps_MatchTheOperationSwitches pins it to the two switch
// statements in BOTH directions: a new mutating operation that forgets to
// register here fails the test (the issue-#1146 "-" Server column would be
// back), and so does an inventory operation that wrongly appears.
var activityAttributedOps = map[string]bool{
	// upstream_servers
	operationAdd:        true,
	operationRemove:     true,
	"update":            true,
	"patch":             true,
	"tail_log":          true,
	"enable":            true,
	"disable":           true,
	"restart":           true,
	"refresh":           true,
	"add_from_registry": true,

	// quarantine_security
	"inspect_quarantined": true,
	"quarantine_server":   true,
	"quarantine":          true,
	"inspect_tools":       true,
	"scan_server":         true,
	"get_scan_report":     true,
	"approve_tool":        true,
	"approve_all_tools":   true,
	"block_tool":          true,
	"block_all_tools":     true,
	"enable_tool":         true,
	"disable_tool":        true,
	"inspect_prompts":     true,
	"approve_prompt":      true,
	"approve_all_prompts": true,
}

// activityTargetServer resolves the server a mutation targets, so the activity
// row renders a Server column and `activity list --server <name>` matches it.
// runtime.EmitActivityInternalToolCall omits target_server when empty, which is
// why every site used to render "-" (issue #1146).
//
// `name` is agent-supplied and never validated here, so it is only trusted for
// an operation that actually acts on it: an unresolved operation and the
// inventory reads stay unattributed. Without that gate an agent could attribute
// its management chatter — and, through /api/v1/activity/summary, per-server
// totals — to any server it liked by calling `list` with a `name`.
func activityTargetServer(request mcp.CallToolRequest, operation string) string {
	if !activityAttributedOps[operation] {
		return ""
	}
	return request.GetString("name", "")
}

// serverNameFromRegistryResult lifts the persisted server name out of the
// add_from_registry success payload. That operation resolves the name from the
// registry entry, so it is knowable only after the fact. Returns "" for error
// payloads and for anything that is not the expected shape.
func serverNameFromRegistryResult(responseText string) string {
	if responseText == "" {
		return ""
	}
	var payload struct {
		Success bool `json:"success"`
		Server  struct {
			Name string `json:"name"`
		} `json:"server"`
	}
	if err := json.Unmarshal([]byte(responseText), &payload); err != nil {
		return ""
	}
	if !payload.Success {
		return ""
	}
	return payload.Server.Name
}

// redactedConfigDiff renders a *config.ConfigDiff with every field PATH
// verbatim — that is the audit signal — and every before/after VALUE masked.
//
// config.FieldChange carries raw From/To values, and MergeServerConfig
// populates Modified["env"], ["headers"] and ["oauth"] with the whole
// before/after maps in the clear (including OAuthConfig.ClientSecret). This one
// rendering is shared by the tool response, the audit log line and the activity
// record so the three can never drift. Returns nil for a nil or empty diff.
func redactedConfigDiff(diff *config.ConfigDiff) map[string]interface{} {
	if diff == nil || diff.IsEmpty() {
		return nil
	}

	modified := make(map[string]interface{}, len(diff.Modified))
	for field, change := range diff.Modified {
		modified[field] = map[string]interface{}{
			"path": change.Path,
			"from": redactActivityValue(field, normalizeForRedaction(change.From)),
			"to":   redactActivityValue(field, normalizeForRedaction(change.To)),
		}
	}

	out := map[string]interface{}{
		"modified": modified,
		"removed":  diff.Removed,
	}
	if len(diff.Added) > 0 {
		out["added"] = diff.Added
	}
	return out
}

// normalizeForRedaction turns an arbitrary typed config value into generic JSON
// (maps / slices / json.Number / string / bool) so the walker below can inspect
// it key by key. json.Number keeps integers exact instead of degrading them to
// float64 and rendering as 1.048576e+06. On failure the value is returned
// unchanged; the walker's default branch then passes it through.
func normalizeForRedaction(v interface{}) interface{} {
	return oauth.NormalizeForRedaction(v)
}

// redactionPolicy is the SHARED redaction rule set (oauth.Redaction — which key
// names are sensitive, which values look like credentials, how argv pairs up,
// and how the generic walk descends into a nested payload) as applied at this
// package's doors.
//
// It is an ALIAS, not a wrapper. Issue #1148 round 7 findings 2 and 3 were both
// a door that could not reach the WALK because the walk lived here: the leaf
// rules were already shared, but `RedactServerSecretFields` (REST + SSE) and
// the hand-built isolation projections in `upstream_servers list` each
// re-derived the descent field by field, and each missed
// `isolation.extra_args`. The walk now lives in internal/oauth beside the
// rules — below both internal/server and internal/httpapi in the import graph —
// so every door runs the same code. The one thing that used to be local, the
// activity store's per-leaf size cap, is expressed as oauth.Redaction.Limit.
type redactionPolicy = oauth.Redaction

// auditRedaction is the policy for anything PERSISTED or EXPORTED: the fixed
// `••••` marker (no length, no trailing bytes — see the file header) and the
// activity-store size cap.
var auditRedaction = oauth.AuditRedaction.WithLimit(activityArgValueLimit)

// liveRedaction is the policy for the LIVE MCP read surfaces (upstream_servers
// list, quarantine_security list_quarantined).
//
// It keeps oauth.MaskValue's `••••<last2> (<N> chars)` rendering, and that is
// load-bearing rather than cosmetic: the patch/update path calls
// oauth.UnmaskURL / UnmaskEnvValues / UnmaskHeaders, which recognise exactly
// that rendering when a client echoes a value back. A read-modify-write agent
// that round-tripped an `••••` marker instead would OVERWRITE the real secret
// with the mask string.
//
// It does not cap: a truncated URL or arg is no longer equal to mask(stored),
// so the unmasker would write the truncation through.
var liveRedaction = oauth.LiveRedaction

// redactActivityValue walks a generic JSON value under the audit policy.
func redactActivityValue(key string, v interface{}) interface{} {
	return redactValueWith(key, v, auditRedaction)
}

// redactValueWith walks a generic JSON value, masking leaves according to the
// key that encloses them. `key` is the enclosing field name (for a slice, the
// name of the slice itself).
func redactValueWith(key string, v interface{}, r redactionPolicy) interface{} {
	return r.Value(key, v)
}

// redactStringWith masks one string leaf under the shared leaf rule, then
// applies the policy's size cap. `key` is the enclosing field name ("" for a
// leaf with no key, such as a positional argv token).
func redactStringWith(key, s string, r redactionPolicy) string {
	return r.CapString(r.Leaf(key, s))
}

// redactEnvValueWith masks one environment-variable leaf, reproducing
// oauth.maskedEnvValue exactly so oauth.UnmaskEnvValues can recognise the
// rendering if a client echoes it back on the write path.
func redactEnvValueWith(name, value string, r redactionPolicy) string {
	return r.CapString(r.EnvValue(name, value))
}

// redactHeaderValueWith masks one HTTP header leaf, judged by the header NAME
// first and then by the value's own shape.
func redactHeaderValueWith(name, value string, r redactionPolicy) string {
	return r.CapString(r.HeaderValue(name, value))
}

// redactActivityConfigValues redacts a flat before/after value map for a
// runtime config_change activity row.
//
// Issue #1146, review round 3: `upstream_servers add` emits TWO activity rows —
// the internal-tool row built by activityArgsFromRequest, and a separate
// config_change row built straight from the resolved request values. The second
// one was unredacted, so `add` with `https://host/mcp?token=…` masked the
// credential in one row and published it in the other.
func redactActivityConfigValues(values map[string]interface{}) map[string]interface{} {
	if values == nil {
		return nil
	}
	out := make(map[string]interface{}, len(values))
	for key, value := range values {
		out[key] = redactActivityValue(key, normalizeForRedaction(value))
	}
	return out
}

// maskDetectedSecrets masks any credential the value-shaped detector
// recognises, leaving text it does not recognise untouched.
func maskDetectedSecrets(s string) string {
	return oauth.MaskDetectedSecrets(s)
}
