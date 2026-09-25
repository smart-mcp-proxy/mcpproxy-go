package configimport

import (
	"sort"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

// ImportedField is one env var or header on an imported server preview row,
// classified for the FR-040 "second line" without exposing the value itself
// (see ImportedServer.EnvFields/HeaderFields).
type ImportedField struct {
	// Name is the env var or header name.
	Name string `json:"name"`
	// ValuePresent is true when the source config set a non-empty value.
	ValuePresent bool `json:"value_present"`
	// SecretLike is a name-based heuristic (research D13):
	// (?i)(token|secret|password|passwd|api[_-]?key|[_-]key$|auth|credential|
	// private[_-]?key). Delegates to oauth.IsSensitiveKeyName /
	// IsSensitiveHeaderName — the SAME heuristic the redaction and
	// reveal-secret-headers surfaces already use, so every door agrees on
	// what "looks like a secret" means.
	SecretLike bool `json:"secret_like"`
	// EmptyOrPlaceholder is true when the value is empty or an obvious
	// placeholder ("YOUR_API_KEY", "<TOKEN>", "changeme", "xxx", …) — the
	// case FR-040's "needs secret" tag exists to flag.
	EmptyOrPlaceholder bool `json:"empty_or_placeholder"`
}

// placeholderTokens lists common placeholder values (lower-cased, with
// surrounding <>{}$ and whitespace trimmed) that a hand-edited or
// copy-pasted config leaves behind instead of a real credential. Not
// exhaustive by design: a value that merely LOOKS unusual but isn't on this
// list is left alone rather than guessed at (a false "needs secret" is
// noise; a missed one just means the user finds out on first use, same as
// today).
var placeholderTokens = map[string]bool{
	"":                true,
	"changeme":        true,
	"change_me":       true,
	"change-me":       true,
	"your_api_key":    true,
	"your-api-key":    true,
	"your_token":      true,
	"your-token":      true,
	"your_key":        true,
	"xxx":             true,
	"xxxx":            true,
	"xxxxx":           true,
	"todo":            true,
	"replace_me":      true,
	"replace-me":      true,
	"insert_key_here": true,
	"insert-key-here": true,
	"fill_me_in":      true,
	"api_key_here":    true,
	"token_here":      true,
	"secret_here":     true,
	"placeholder":     true,
	"redacted":        true,
	"<redacted>":      true,
	"none":            true,
}

// isPlaceholder reports whether value is empty or an obvious placeholder.
func isPlaceholder(value string) bool {
	v := strings.ToLower(strings.TrimSpace(value))
	v = strings.Trim(v, "<>{}$")
	return placeholderTokens[v]
}

// describeFields builds the classified ImportedField list for an env or
// header map, sorted by name for deterministic output. isHeader selects the
// name heuristic: header names (e.g. "Authorization") and env var names
// (e.g. "API_KEY") follow different sensitive-name conventions. Returns nil
// for an empty map so JSON output omits the field entirely (omitempty).
func describeFields(values map[string]string, isHeader bool) []ImportedField {
	if len(values) == 0 {
		return nil
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)

	fields := make([]ImportedField, 0, len(names))
	for _, name := range names {
		value := values[name]
		secretLike := oauth.IsSensitiveKeyName(name)
		if isHeader {
			secretLike = oauth.IsSensitiveHeaderName(name)
		}
		fields = append(fields, ImportedField{
			Name:               name,
			ValuePresent:       value != "",
			SecretLike:         secretLike,
			EmptyOrPlaceholder: isPlaceholder(value),
		})
	}
	return fields
}

// needsSecretTag reports whether any field looks like a credential slot that
// is currently empty or a placeholder — the FR-040 "needs secret" tag.
// Requiring BOTH secretLike and emptyOrPlaceholder (rather than either alone)
// avoids flagging an ordinary, legitimately-empty non-secret variable.
func needsSecretTag(fields ...[]ImportedField) bool {
	for _, group := range fields {
		for _, f := range group {
			if f.SecretLike && f.EmptyOrPlaceholder {
				return true
			}
		}
	}
	return false
}

// summarizeServer builds the FR-040 second-line summary and its tags
// (local process|remote, needs secret, oauth) for an imported server.
// Command/args/url are redacted through oauth.LiveRedaction before being
// joined into Summary — the SAME rule every other display surface for this
// data applies (issue #1148's "one rule set" principle) — so Summary never
// carries a raw secret even when an argv value or URL query looks like one.
func summarizeServer(server *config.ServerConfig, envFields, headerFields []ImportedField) (summary string, tags []string) {
	// Mirror internal/transport.DetermineTransportType, the runtime's actual
	// transport selector: an explicit "stdio" is authoritative regardless of
	// a (possibly leftover) URL — a hand-edited entry that declares
	// "type": "stdio" but still carries a url field runs as stdio — and an
	// empty OR "auto" protocol with a command also resolves to stdio
	// (DetermineTransportType checks Command before URL and treats "auto"
	// identically to ""), independent of whether a URL is also present.
	// internal/config/config.go's own static validation is a different,
	// narrower rule (Protocol=="stdio" || (Protocol=="" && Command!=""), no
	// "auto" case, no URL condition either way) that governs config
	// acceptance, not transport selection — DetermineTransportType is what
	// this preview needs to agree with.
	isStdio := server.Protocol == "stdio" ||
		((server.Protocol == "" || server.Protocol == "auto") && server.Command != "")

	if isStdio {
		parts := append([]string{server.Command}, oauth.LiveRedaction.Argv(server.Args)...)
		summary = strings.TrimSpace(strings.Join(parts, " "))
		tags = []string{"local process"}
	} else {
		authType := "no auth"
		switch {
		case server.OAuth != nil:
			authType = "oauth"
		case len(server.Headers) > 0:
			authType = "header auth"
		}
		summary = strings.TrimSpace(oauth.LiveRedaction.URLValue(server.URL) + " (" + authType + ")")
		tags = []string{"remote"}
	}

	if server.OAuth != nil {
		tags = append(tags, "oauth")
	}
	if needsSecretTag(envFields, headerFields) {
		tags = append(tags, "needs secret")
	}
	return summary, tags
}
