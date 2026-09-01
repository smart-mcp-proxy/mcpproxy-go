package oauth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Issue #1148, round 6. The WRITE half of the rule set lives here, next to the
// read half in redactview.go, and every write door — MCP `upstream_servers
// add|update|patch`, REST `POST`/`PATCH /api/v1/servers` — calls exactly these
// functions.
//
// It has to be one place for the same reason the read half does: internal/httpapi
// sits BELOW internal/server in the import graph, so a mirror written in
// internal/server is unreachable from REST. Round 5 left the live-rendering
// mirrors (unmaskLive*) up in internal/server while round 6 moved the live
// RENDERING onto the REST read door — which, without this move, would have
// turned REST rows from "published in the clear" into "silently corrupted",
// the one trade this branch must never make.
//
// The shape every door follows:
//
//  1. Revert what can be BOUND TO A KEY — a map key, a query-parameter name
//     plus the stored scheme+host, a named struct field.
//  2. REFUSE whatever still carries a mask this proxy rendered
//     (CheckServerWriteMasks). That covers the fields with no binding (argv,
//     scopes, isolation.extra_args) AND every field added to the config struct
//     later, so a new field fails CLOSED instead of persisting its own mask
//     over a live credential.
//
// ServerFieldMaskDecisions in serverfields.go records which fields are in (1)
// and which fall to (2), and a reflection test fails when a new field appears
// without an answer.

// UnmaskLiveEnvValues reverts env values a client echoed back exactly as a LIVE
// read door rendered them.
//
// It exists alongside UnmaskEnvValues because the live rendering runs the
// value-shaped detector over leaves the NAME rule left alone (`env: {BENIGN:
// ghp_…}`) — a rendering UnmaskEnvValues cannot recognise, since it compares
// against maskedEnvValue. Without this mirror, masking that leaf would trade a
// disclosure for the read-modify-write corruption of #1142/#1146.
//
// Binding is by KEY, exactly as UnmaskEnvValues binds, so a value can only ever
// be restored to the variable it was read from.
func UnmaskLiveEnvValues(incoming, stored map[string]string) map[string]string {
	return unmaskLiveMapValues(incoming, stored, LiveRedaction.EnvValue)
}

// UnmaskLiveHeaders is UnmaskLiveEnvValues for header maps.
func UnmaskLiveHeaders(incoming, stored map[string]string) map[string]string {
	return unmaskLiveMapValues(incoming, stored, LiveRedaction.HeaderValue)
}

func unmaskLiveMapValues(incoming, stored map[string]string, rendered func(k, v string) string) map[string]string {
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

// UnmaskLiveURL reverts a masked URL on the write path, and REFUSES the write
// when a mask it cannot bind survives.
//
// Three steps, widest binding first:
//
//  1. The whole URL echoed back byte for byte — revert to the stored URL.
//  2. A genuine edit — UnmaskURL restores the userinfo password and the
//     SENSITIVE query params under its own authority safeguard, then
//     UnmaskLiveURLParams restores, PER PARAMETER, anything the value-shaped
//     detector masked under a parameter name no matcher recognises.
//  3. Whatever still carries a mask marker is refused.
//
// Round 4 finding 2 is why (2) needs the per-parameter pass and (3) exists at
// all: for `https://host/old?opaque=ghp_…` the LIVE detector masks the value,
// but changing `/old` to `/new` defeats the whole-URL comparison in (1) and
// UnmaskURL ignores the unrecognised `opaque` parameter — so the mask was
// written through as the credential. The per-parameter revert is bound to the
// parameter name and the authority and to nothing else, so it survives an
// unrelated edit; the refusal covers everything that binding cannot reach (a
// URL moved to another host, a mask in the path or the fragment, a parameter
// whose stored counterpart is gone).
func UnmaskLiveURL(incoming, stored string) (string, error) {
	if incoming == "" {
		return incoming, nil
	}
	if stored != "" {
		if incoming == LiveRedaction.URLValue(stored) {
			return stored, nil
		}
		incoming = UnmaskLiveURLParams(UnmaskURL(incoming, stored), stored)
	}
	if ContainsMaskMarker(incoming) {
		return "", errors.New("url is a redaction placeholder, not a URL: " +
			"credentials inside the URL are masked on read, and this one cannot be bound back to the stored " +
			"value (the scheme/host changed, or the credential does not sit in a query parameter). " +
			"Resend the real URL, or omit url to leave the stored one unchanged")
	}
	return incoming, nil
}

// UnmaskLiveOAuth reverts masked oauth fields echoed back by a client, and
// REFUSES the write when a mask it cannot bind survives.
//
// The oauth block is REPLACED wholesale by a write, and the live view masks
// every leaf of it that looks like a credential — not just client_secret.
// Round 2 finding 6 fixed client_secret; round 4 finding 1 caught the rest:
// `extra_params` routinely holds an RFC 8707 resource indicator with a signed
// URL, which the URL rule masks, and any leaf can be masked by the value-shaped
// detector. A read-modify-write therefore persisted the MASK STRING over those
// values.
//
//   - client_secret, client_id, redirect_uri — reverted, each bound to its own
//     field name, using the same rendering the read path produced.
//   - extra_params — reverted per PARAMETER NAME, exactly as env vars and
//     headers are reverted per key.
//   - scopes, and any field added to config.OAuthConfig in future — REFUSED. A
//     scope's only context is its position in a caller-supplied slice, which is
//     the same non-binding an argv token has (see CheckArgvMaskEcho), and a
//     future field has no revert at all. The residual check walks the whole
//     block, so a new field fails CLOSED instead of silently corrupting.
func UnmaskLiveOAuth(incoming, stored *config.OAuthConfig) error {
	if incoming == nil {
		return nil
	}
	if stored != nil {
		incoming.ClientSecret = UnmaskLiveField("client_secret", incoming.ClientSecret, stored.ClientSecret)
		incoming.ClientID = UnmaskLiveField("client_id", incoming.ClientID, stored.ClientID)
		incoming.RedirectURI = UnmaskLiveField("redirect_uri", incoming.RedirectURI, stored.RedirectURI)
		incoming.ExtraParams = unmaskLiveMapValues(incoming.ExtraParams, stored.ExtraParams, LiveRedaction.Leaf)
	}
	if path, ok := FindMaskMarker("oauth", NormalizeForRedaction(incoming)); ok {
		return fmt.Errorf("oauth%s is a redaction placeholder, not a value: "+
			"it carries no key this proxy can bind the stored secret to, so it is never restored on write. "+
			"Resend the real value, or omit the oauth block to leave the stored one unchanged",
			strings.TrimPrefix(path, "oauth"))
	}
	return nil
}

// UnmaskLiveField reverts one scalar field echoed back exactly as a live read
// door rendered it, bound to the field NAME the rendering was keyed on.
func UnmaskLiveField(key, incoming, stored string) string {
	if incoming == "" || stored == "" {
		return incoming
	}
	if incoming == LiveRedaction.Leaf(key, stored) {
		return stored
	}
	return incoming
}

// CheckServerWriteMasks is the fail-closed net on EVERY server write door.
//
// After the key-bound reverts above have restored what they can bind, anything
// still carrying a mask this proxy rendered is refused. That is what makes the
// scheme total rather than a list of fields somebody remembered: a field with
// no revert (args, oauth.scopes, isolation.extra_args), a mask relocated to a
// field it was never read from, and — the case five review rounds kept
// producing — a field ADDED to config.ServerConfig later, all fail CLOSED
// instead of persisting the mask over a live credential.
//
// `what` names the thing being written, for the operator-facing message.
func CheckServerWriteMasks(what string, v interface{}) error {
	path, ok := FindMaskMarker("", NormalizeForRedaction(v))
	if !ok {
		return nil
	}
	return fmt.Errorf("%s%s is a redaction placeholder, not a value: "+
		"secrets are masked on read and this mask carries no key this proxy can bind the stored value to, "+
		"so it is never restored on write. Resend the real value, or omit the field to leave the stored one unchanged",
		what, strings.TrimPrefix(path, "."))
}

// FindMaskMarker walks a generic JSON value and returns the dotted path of the
// first string leaf still carrying a mask this proxy rendered.
//
// Walking the whole value rather than checking a hand-written field list is the
// point: it is what makes a field ADDED to the struct later fail closed instead
// of quietly round-tripping its own mask into the config.
func FindMaskMarker(path string, v interface{}) (string, bool) {
	switch typed := v.(type) {
	case map[string]interface{}:
		for k, val := range typed {
			if p, ok := FindMaskMarker(path+"."+k, val); ok {
				return p, true
			}
		}
	case []interface{}:
		for i, val := range typed {
			if p, ok := FindMaskMarker(fmt.Sprintf("%s[%d]", path, i), val); ok {
				return p, true
			}
		}
	case string:
		if ContainsMaskMarker(typed) {
			return path, true
		}
	}
	return "", false
}

// NormalizeForRedaction turns an arbitrary typed config value into generic JSON
// (maps / slices / json.Number / string / bool) so the walkers above and the
// redaction walker in internal/server can inspect it key by key. json.Number
// keeps integers exact instead of degrading them to float64 and rendering as
// 1.048576e+06. On failure the value is returned unchanged; the walkers' default
// branch then passes it through.
func NormalizeForRedaction(v interface{}) interface{} {
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
