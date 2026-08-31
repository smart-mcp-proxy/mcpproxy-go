package server

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
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
//	changed, and recognise which credential it was, without the value ever
//	landing in the store. oauth.MaskValue renders `••••<last2> (<N> chars)`,
//	matching the tray / Web UI convention, and passes ${keyring:…} / ${env:…}
//	references through unchanged because those are labels, not secrets.
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
)

// activityArgsFromRequest builds the redacted activity-argument map from the
// raw MCP request. Returns nil for an empty request so callers keep emitting
// `null` arguments exactly as before.
func activityArgsFromRequest(request mcp.CallToolRequest) map[string]interface{} {
	raw := request.GetArguments()
	if len(raw) == 0 {
		return nil
	}

	args := make(map[string]interface{}, len(raw))
	for key, value := range raw {
		// The *_json parameters carry a serialized object; parse it so the
		// walker can mask per key. On failure (including the literal "null"
		// removal marker) fall back to scrubbing the raw string — a rejected
		// mutation is still audit-relevant, so never drop it.
		if s, ok := value.(string); ok && strings.HasSuffix(key, jsonParamSuffix) {
			var parsed interface{}
			if err := json.Unmarshal([]byte(s), &parsed); err == nil && parsed != nil {
				args[key] = redactActivityValue(strings.TrimSuffix(key, jsonParamSuffix), parsed)
				continue
			}
		}
		args[key] = redactActivityValue(key, value)
	}
	return args
}

// activityTargetServer resolves the server a mutation targets, so the activity
// row renders a Server column and `activity list --server <name>` matches it.
// runtime.EmitActivityInternalToolCall omits target_server when empty, which is
// why every site used to render "-" (issue #1146).
func activityTargetServer(request mcp.CallToolRequest) string {
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
	if v == nil {
		return nil
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return v
	}
	dec := json.NewDecoder(bytes.NewReader(encoded))
	dec.UseNumber()
	var out interface{}
	if err := dec.Decode(&out); err != nil {
		return v
	}
	return out
}

// redactActivityValue walks a generic JSON value, masking leaves according to
// the key that encloses them. `key` is the enclosing field name (for a slice,
// the name of the slice itself).
func redactActivityValue(key string, v interface{}) interface{} {
	switch typed := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		isHeaders := isActivityHeadersKey(key)
		for k, val := range typed {
			if isHeaders {
				if s, ok := val.(string); ok {
					// HTTP header semantics: mask by header name.
					out[k] = capActivityValue(oauth.RedactStringHeaders(map[string]string{k: s})[k])
					continue
				}
			}
			out[k] = redactActivityValue(k, val)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for i, val := range typed {
			out[i] = redactActivityValue(key, val)
		}
		return out
	case string:
		return redactActivityString(key, typed)
	default:
		// Bools, json.Number and nil carry no secrets and stay verbatim.
		return v
	}
}

// redactActivityString masks one string leaf. The default policy is the env-var
// rule (oauth.RedactEnvValues over a single pair), which is the package's single
// source of truth for "does this key name look like it holds a secret": it masks
// sensitive-looking keys with MaskValue, passes ${keyring:…}/${env:…} references
// through, URL-redacts connection strings, and runs RedactSensitiveData over
// everything else as defence in depth. Over-masking a harmless field is the safe
// direction; ordinary configuration (protocol, command, LOG_LEVEL, an image
// named python:3.12) stays readable.
func redactActivityString(key, s string) string {
	switch {
	case isActivityHeadersKey(key):
		return capActivityValue(oauth.RedactStringHeaders(map[string]string{key: s})[key])
	case strings.EqualFold(key, "url"):
		return capActivityValue(oauth.RedactURLQueryParams(s))
	default:
		return capActivityValue(oauth.RedactEnvValues(map[string]string{key: s})[key])
	}
}

// isActivityHeadersKey reports whether a map should be masked with HTTP-header
// semantics. Covers both the `headers` config field and the `headers_json` tool
// parameter (the suffix is trimmed before the walk).
func isActivityHeadersKey(key string) bool {
	return strings.EqualFold(key, "headers")
}

func capActivityValue(s string) string {
	if len(s) <= activityArgValueLimit {
		return s
	}
	return s[:safeTruncateBytes(s, activityArgValueLimit)] + activityErrorMessageEllipsis
}
