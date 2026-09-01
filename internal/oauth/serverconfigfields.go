package oauth

import (
	"encoding/json"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Issue #1148: the *config.ServerConfig analogue of RedactServerSecretFields.
//
// RedactServerSecretFields covers contracts.Server — the REST/SSE projection.
// But three doors hand out the CONFIG STRUCT ITSELF: the MCP
// `upstream_servers list` / `quarantine_security list_quarantined` payloads,
// and (server edition) `GET|POST /api/v1/user/servers…`, whose ServerResponse
// EMBEDS *config.ServerConfig. The MCP door grew its own copy of this logic in
// internal/server; the server-edition door had none at all, so every
// authenticated user of a deployment was handed the ADMIN-configured shared
// upstreams' `headers` (Authorization, X-API-Key), `env`, URL query
// credentials, `oauth.client_secret` and `auth_broker.client_secret` in the
// clear.
//
// So the rule lives here, once, beside the leaf rules and the walk — below
// internal/server, internal/httpapi and internal/serveredition in the import
// graph, which is the only place all three doors can reach.
//
// It is deliberately a WALK, not a field list. An allowlist encodes today's
// field set and silently leaks tomorrow's; that is the shape this issue has
// produced at every door. The config is marshalled through its own MarshalJSON
// and the resulting generic map is walked by the shared rules, so a field added
// to ServerConfig — or to a build-tagged block like `auth_broker`, whose fields
// this package cannot even name in the personal edition — is masked because the
// walk reaches it.
//
// The write-path answer for every field is recorded in
// ServerFieldMaskDecisions, and TestServerFieldMaskDecisions_CoverEveryNestedLeaf
// walks config.ServerConfig to enforce that every text-carrying leaf has one.

// RedactedServerConfigView renders one server config as a generic JSON map with
// every secret-bearing leaf masked under the given policy.
//
// Pass LiveRedaction for an interactive read surface (it keeps MaskValue's
// `••••<last2> (<N> chars)` rendering, which the write-path unmaskers
// recognise) and AuditRedaction for anything persisted. Returns nil for a nil
// config.
//
// The view must stay a MAP for a JSON-rendering caller: config.ServerConfig
// declares MarshalJSON/UnmarshalJSON and Go promotes those to any struct that
// embeds it, so an embedding wrapper silently drops its own fields on encode
// (see the warning on config.ServerConfig.UnmarshalJSON).
func RedactedServerConfigView(sc *config.ServerConfig, r Redaction) map[string]interface{} {
	if sc == nil {
		return nil
	}

	normalized, ok := NormalizeForRedaction(sc).(map[string]interface{})
	if !ok {
		// ServerConfig always marshals to an object; a failure here can only
		// mean the encoder broke. Fail CLOSED — an empty view loses
		// information, a raw struct loses secrets.
		return map[string]interface{}{"name": sc.Name}
	}

	redacted, ok := r.Value("", normalized).(map[string]interface{})
	if !ok {
		return map[string]interface{}{"name": sc.Name}
	}
	return redacted
}

// RedactedServerConfigViews renders a slice of server configs, skipping nils.
func RedactedServerConfigViews(servers []*config.ServerConfig, r Redaction) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(servers))
	for _, sc := range servers {
		if sc == nil {
			continue
		}
		out = append(out, RedactedServerConfigView(sc, r))
	}
	return out
}

// RedactServerConfigSecrets returns a masked COPY of sc under the LIVE policy,
// for a door that must hand back a *config.ServerConfig rather than a map (the
// server edition's ServerResponse embeds one).
//
// The input is never mutated and the result shares no map, slice or pointer
// with it: the masked view is decoded into a FRESH struct. That matters because
// the caller's config is typically the LIVE one — `h.sharedServers` is the
// admin's running configuration, and writing a mask through it would be the
// #1142/#1146 corruption with a whole deployment's upstreams as the blast
// radius.
//
// It FAILS CLOSED. If the masked view cannot be decoded back into a
// ServerConfig, the result carries the server's name and nothing else: a
// caller that loses fields is a bug report, a caller that publishes an
// unredacted credential is this issue.
//
// Returns nil for a nil config.
func RedactServerConfigSecrets(sc *config.ServerConfig) *config.ServerConfig {
	if sc == nil {
		return nil
	}
	view := RedactedServerConfigView(sc, LiveRedaction)
	encoded, err := json.Marshal(view)
	if err != nil {
		return &config.ServerConfig{Name: sc.Name}
	}
	masked := &config.ServerConfig{}
	if err := json.Unmarshal(encoded, masked); err != nil {
		return &config.ServerConfig{Name: sc.Name}
	}
	return masked
}
